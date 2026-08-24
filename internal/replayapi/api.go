// Package replayapi serves the protected replay API: an operator lists and
// inspects captured webhook events (the replay store, see internal/replaystore)
// and re-delivers them to their original destinations, singly or in filtered
// bulk. It is a standalone HTTP server guarded by an API key, mirroring
// internal/editor's shape.
//
// The one subtle correctness rule this package enforces: a replay's destination
// list comes from the recorded replay_deliveries rows (Store.Deliveries), NEVER
// from the stored Envelope.DestinationQueueID/Queue -- those are comma-joined
// aggregate labels (e.g. "billing,activity"), not real per-destination ids, so
// publishing against them would route to a nonexistent destination.
//
// The optional unsupported map lets the caller skip destinations that cannot
// be safely replayed. Proxy-mode webhook destinations are replayable: the
// store persists orig_headers and raw_body_base64 for reconstructing the
// original POST.
package replayapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jasongiroux/hookway/internal/apikey"
	"github.com/jasongiroux/hookway/internal/metrics"
	"github.com/jasongiroux/hookway/internal/queue"
	"github.com/jasongiroux/hookway/internal/replaystore"
)

// maxBodyBytes caps replay request bodies. These are small JSON control
// documents ({reprocess, dry_run, max, filter}), so a modest ceiling is ample.
const maxBodyBytes = 1 << 20 // 1 MiB

// defaultBulkMax is the safe cap applied to a bulk replay when the caller
// passes max<=0 (or omits it): at most this many events are replayed in one
// request, so an unbounded-sounding bulk call can never fan out without limit.
// Note: the underlying store additionally clamps List's limit to its own
// maximum page size (replaystore.maxListLimit, 500), so a max above that is
// effectively capped there.
const defaultBulkMax = 100

// StoreReader is the read surface replayapi needs from the replay store. Get
// returns the full event (with envelope) for reconstruction; Deliveries returns
// the recorded per-destination rows that are the SOURCE OF TRUTH for where a
// replay fans out (see the package doc). List/Count drive listing and dry-run.
type StoreReader interface {
	List(ctx context.Context, f replaystore.Filter) ([]replaystore.EventSummary, string, error)
	Get(ctx context.Context, id string) (replaystore.Event, bool, error)
	Count(ctx context.Context, f replaystore.Filter) (int64, error)
	Deliveries(ctx context.Context, eventID string) ([]replaystore.Delivery, error)
}

type server struct {
	store  StoreReader
	pub    queue.Publisher
	// unsupported maps a destination id to a human-readable reason it must
	// never be published to on replay (today: proxy-mode webhook
	// destinations, whose RawBody/OrigHeaders are not persisted -- see
	// replayOne). A nil/empty map means every recorded destination is
	// replayable, which is the common case.
	unsupported map[string]string
	m           *metrics.Metrics
	logger      *slog.Logger
}

// Handler returns the routed replay API handler, guarded by apiKey when
// non-empty (apikey.Middleware disables auth for an empty key). logger may be
// nil (falls back to slog.Default); m may be nil (metric increments are then
// skipped). unsupported (may be nil) maps a destination id to the reason a
// replay to it must be skipped instead of published -- see replayOne.
func Handler(apiKey string, store StoreReader, pub queue.Publisher, unsupported map[string]string, m *metrics.Metrics, logger *slog.Logger) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	s := &server{store: store, pub: pub, unsupported: unsupported, m: m, logger: logger}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /replay/events", s.handleList)
	mux.HandleFunc("GET /replay/events/{id}", s.handleGet)
	mux.HandleFunc("POST /replay/events/{id}/replay", s.handleSingleReplay)
	mux.HandleFunc("POST /replay/replay", s.handleBulkReplay)
	return apikey.Middleware(apiKey, mux)
}

// NewServer returns an http.Server bound to addr serving the replay API. Timeout
// values mirror internal/editor's NewServer. unsupported (may be nil) is
// forwarded to Handler -- see its doc comment.
func NewServer(addr, apiKey string, store StoreReader, pub queue.Publisher, unsupported map[string]string, m *metrics.Metrics, logger *slog.Logger) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           Handler(apiKey, store, pub, unsupported, m, logger),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
}

// ---------------------------------------------------------------------------
// reconstruct: build the replayed message from a captured event. PURE.
// ---------------------------------------------------------------------------

// reconstruct derives the queue.Message to (re-)publish from a captured event.
// It starts from the stored envelope and changes only the fields that must
// differ on a replay:
//   - ID: a fresh uuidv7 (matches how ingest mints ids), so the replay is a
//     distinct message and never collides with the original.
//   - ReplayOf: the original event id, so downstream can correlate.
//   - Hops: reset to 0 -- a replay is a fresh delivery to destinations, not a
//     re-ingest, so it starts the loop counter over.
//   - IdempotencyKey: preserved when reprocess is false (dedup-safe: consumers
//     that dedup on this key will skip an already-handled event); replaced with
//     a fresh uuid when reprocess is true (so consumers re-process it).
//
// Everything else (Body, RawBody, RawBodyBase64, Headers, OrigHeaders, Query, Endpoint, Method, Path,
// RequestID, TraceID, Traceparent, SourceIP, Auth, SchemaVersion, and crucially
// ReceivedAt -- which stays the ORIGINAL receipt time, because this is event X
// re-delivered, not a newly received request) is copied unchanged.
// DestinationQueueID/Queue are intentionally left as-is here; the caller
// overwrites them per recorded delivery before publishing.
// RawBody is hydrated from RawBodyBase64 when the in-memory bytes are empty
// so a proxy-mode webhook replay can reconstruct the original POST.
func reconstruct(e replaystore.Event, reprocess bool) queue.Message {
	msg := e.Envelope
	if len(msg.RawBody) == 0 && msg.RawBodyBase64 != nil {
		if raw, err := base64.StdEncoding.DecodeString(*msg.RawBodyBase64); err == nil {
			msg.RawBody = raw
		}
	}
	msg.ID = uuid.Must(uuid.NewV7()).String()
	msg.ReplayOf = e.ID
	msg.Hops = 0
	if reprocess {
		msg.IdempotencyKey = uuid.NewString()
	}
	return msg
}

// ---------------------------------------------------------------------------
// Response DTOs.
// ---------------------------------------------------------------------------

type summaryDTO struct {
	ID             string    `json:"id"`
	Endpoint       string    `json:"endpoint"`
	Status         string    `json:"status"`
	IdempotencyKey string    `json:"idempotency_key,omitempty"`
	ReceivedAt     time.Time `json:"received_at"`
}

type eventDTO struct {
	ID             string        `json:"id"`
	Endpoint       string        `json:"endpoint"`
	Status         string        `json:"status"`
	IdempotencyKey string        `json:"idempotency_key,omitempty"`
	ReceivedAt     time.Time     `json:"received_at"`
	Envelope       queue.Message `json:"envelope"`
}

type destResult struct {
	Destination string `json:"destination"`
	Target      string `json:"target"`
	Status      string `json:"status"` // "ok" | "failed" | "skipped"
	Error       string `json:"error,omitempty"`
	// Reason explains a "skipped" status (e.g. proxy-mode webhook replay is
	// unsupported); empty for "ok"/"failed".
	Reason string `json:"reason,omitempty"`
}

// eventReplayResult is the per-event outcome, shared by the single and bulk
// responses. Skipped is set (with a reason) only in bulk when an event had no
// recorded deliveries; ReplayedEventID/Destinations are then omitted.
type eventReplayResult struct {
	OriginalEventID string       `json:"original_event_id"`
	ReplayedEventID string       `json:"replayed_event_id,omitempty"`
	Destinations    []destResult `json:"destinations,omitempty"`
	Skipped         string       `json:"skipped,omitempty"`
}

// ---------------------------------------------------------------------------
// Replay core: one event, fanned out over its recorded deliveries.
// ---------------------------------------------------------------------------

var (
	errEventNotFound = errors.New("replayapi: event not found")
	errNoDeliveries  = errors.New("replayapi: event has no recorded deliveries")
)

// replayOne re-delivers a single captured event to every destination recorded
// in replay_deliveries. It reconstructs the message ONCE (one fresh id fanned
// out to all destinations, mirroring the original ingest) and publishes a copy
// per destination with that destination's real (destination_id, target). A
// per-destination Publish failure is recorded in the result and does not abort
// the others. mode ("single"|"bulk") selects the metric label; the metric is
// incremented once per event with at least one destination actually
// published (an event whose destinations are ALL unsupported never
// increments it, since nothing was triggered).
//
// A destination present in s.unsupported is NEVER published to here.
// Instead it is recorded with status "skipped" and the configured reason,
// exactly like a failed publish does not abort the others.
func (s *server) replayOne(ctx context.Context, id string, reprocess bool, mode string) (eventReplayResult, error) {
	event, found, err := s.store.Get(ctx, id)
	if err != nil {
		return eventReplayResult{}, err
	}
	if !found {
		return eventReplayResult{}, errEventNotFound
	}

	deliveries, err := s.store.Deliveries(ctx, id)
	if err != nil {
		return eventReplayResult{}, err
	}
	deliveries = dedupDeliveries(deliveries)
	if len(deliveries) == 0 {
		return eventReplayResult{}, errNoDeliveries
	}

	msg := reconstruct(event, reprocess)
	res := eventReplayResult{OriginalEventID: id, ReplayedEventID: msg.ID}
	published := false
	for _, d := range deliveries {
		if reason, skip := s.unsupported[d.DestinationID]; skip {
			res.Destinations = append(res.Destinations, destResult{
				Destination: d.DestinationID, Target: d.Target, Status: "skipped", Reason: reason,
			})
			continue
		}
		m := msg
		m.DestinationQueueID = d.DestinationID
		m.Queue = d.Target
		dr := destResult{Destination: d.DestinationID, Target: d.Target, Status: "ok"}
		if err := s.pub.Publish(ctx, d.DestinationID, d.Target, m); err != nil {
			dr.Status = "failed"
			dr.Error = err.Error()
		}
		res.Destinations = append(res.Destinations, dr)
		published = true
	}
	if s.m != nil && published {
		s.m.ReplayTriggered(mode)
	}
	return res, nil
}

// dedupDeliveries removes duplicate (destination_id, target) rows while
// preserving order, so a replay never publishes twice to the same destination
// even if the recorded rows contain a duplicate.
func dedupDeliveries(in []replaystore.Delivery) []replaystore.Delivery {
	seen := make(map[[2]string]struct{}, len(in))
	out := in[:0:0]
	for _, d := range in {
		key := [2]string{d.DestinationID, d.Target}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, d)
	}
	return out
}

// ---------------------------------------------------------------------------
// Handlers.
// ---------------------------------------------------------------------------

func (s *server) handleList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := replaystore.Filter{
		Endpoint:      q.Get("endpoint"),
		DestinationID: q.Get("destination"),
		Target:        q.Get("target"),
		Status:        q.Get("status"),
		Cursor:        q.Get("cursor"),
	}
	if v := q.Get("since"); v != "" {
		ts, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid since (want RFC3339)")
			return
		}
		f.Since = ts
	}
	if v := q.Get("until"); v != "" {
		ts, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid until (want RFC3339)")
			return
		}
		f.Until = ts
	}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid limit")
			return
		}
		f.Limit = n
	}

	summaries, nextCursor, err := s.store.List(r.Context(), f)
	if err != nil {
		s.logger.Error("replayapi: list failed", slog.Any("error", err))
		writeError(w, http.StatusInternalServerError, "list failed")
		return
	}
	events := make([]summaryDTO, 0, len(summaries))
	for _, sum := range summaries {
		events = append(events, summaryDTO(sum))
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events, "next_cursor": nextCursor})
}

func (s *server) handleGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	event, found, err := s.store.Get(r.Context(), id)
	if err != nil {
		s.logger.Error("replayapi: get failed", slog.String("id", id), slog.Any("error", err))
		writeError(w, http.StatusInternalServerError, "get failed")
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	writeJSON(w, http.StatusOK, eventDTO{
		ID:             event.ID,
		Endpoint:       event.Endpoint,
		Status:         event.Status,
		IdempotencyKey: event.IdempotencyKey,
		ReceivedAt:     event.ReceivedAt,
		Envelope:       event.Envelope,
	})
}

func (s *server) handleSingleReplay(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Reprocess bool `json:"reprocess"`
	}
	if err := decodeOptionalJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	res, err := s.replayOne(r.Context(), id, body.Reprocess, "single")
	switch {
	case errors.Is(err, errEventNotFound):
		writeError(w, http.StatusNotFound, "not found")
	case errors.Is(err, errNoDeliveries):
		writeError(w, http.StatusUnprocessableEntity, "event has no recorded deliveries to replay to")
	case err != nil:
		s.logger.Error("replayapi: single replay failed", slog.String("id", id), slog.Any("error", err))
		writeError(w, http.StatusInternalServerError, "replay failed")
	default:
		writeJSON(w, http.StatusOK, map[string]any{
			"replayed_event_id": res.ReplayedEventID,
			"original_event_id": res.OriginalEventID,
			"destinations":      res.Destinations,
		})
	}
}

func (s *server) handleBulkReplay(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Endpoint    string `json:"endpoint"`
		Destination string `json:"destination"`
		Target      string `json:"target"`
		Status      string `json:"status"`
		Since       string `json:"since"`
		Until       string `json:"until"`
		Reprocess   bool   `json:"reprocess"`
		DryRun      bool   `json:"dry_run"`
		Max         int    `json:"max"`
	}
	if err := decodeOptionalJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	f := replaystore.Filter{
		Endpoint:      body.Endpoint,
		DestinationID: body.Destination,
		Target:        body.Target,
		Status:        body.Status,
	}
	if body.Since != "" {
		ts, err := time.Parse(time.RFC3339, body.Since)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid since (want RFC3339)")
			return
		}
		f.Since = ts
	}
	if body.Until != "" {
		ts, err := time.Parse(time.RFC3339, body.Until)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid until (want RFC3339)")
			return
		}
		f.Until = ts
	}

	ctx := r.Context()
	if body.DryRun {
		n, err := s.store.Count(ctx, f)
		if err != nil {
			s.logger.Error("replayapi: bulk dry-run count failed", slog.Any("error", err))
			writeError(w, http.StatusInternalServerError, "count failed")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"matched": n, "dry_run": true})
		return
	}

	max := body.Max
	if max <= 0 {
		max = defaultBulkMax
	}
	f.Limit = max

	summaries, nextCursor, err := s.store.List(ctx, f)
	if err != nil {
		s.logger.Error("replayapi: bulk list failed", slog.Any("error", err))
		writeError(w, http.StatusInternalServerError, "list failed")
		return
	}
	// capped: the filter matched more events than this request replayed. A
	// non-empty next cursor means the store had at least one more page.
	capped := nextCursor != ""
	if len(summaries) > max {
		summaries = summaries[:max]
		capped = true
	}

	replayed := 0
	results := make([]eventReplayResult, 0, len(summaries))
	for _, sum := range summaries {
		res, err := s.replayOne(ctx, sum.ID, body.Reprocess, "bulk")
		switch {
		case errors.Is(err, errEventNotFound):
			// Vanished (e.g. purged) between List and Get -- skip silently.
			continue
		case errors.Is(err, errNoDeliveries):
			results = append(results, eventReplayResult{OriginalEventID: sum.ID, Skipped: "no recorded deliveries"})
		case err != nil:
			s.logger.Error("replayapi: bulk replay of event failed", slog.String("id", sum.ID), slog.Any("error", err))
			results = append(results, eventReplayResult{OriginalEventID: sum.ID, Skipped: err.Error()})
		default:
			results = append(results, res)
			replayed++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"replayed": replayed, "capped": capped, "results": results})
}

// ---------------------------------------------------------------------------
// Small HTTP helpers.
// ---------------------------------------------------------------------------

// decodeOptionalJSON decodes r's (size-capped) body into v. An empty body is
// treated as "{}" -- these endpoints all have sensible zero-value defaults
// (reprocess=false, dry_run=false, empty filter), so a bodyless POST is valid.
func decodeOptionalJSON(r *http.Request, v any) error {
	defer func() { _ = r.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes))
	if err != nil {
		return err
	}
	if len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, v)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
