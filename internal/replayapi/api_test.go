package replayapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jaysongiroux/hook_way/internal/metrics"
	"github.com/jaysongiroux/hook_way/internal/queue"
	"github.com/jaysongiroux/hook_way/internal/replaystore"
)

// ---------------------------------------------------------------------------
// reconstruct unit tests (pure).
// ---------------------------------------------------------------------------

func sampleEvent() replaystore.Event {
	raw := "cmF3Ym9keQ=="
	return replaystore.Event{
		ID:             "11111111-2222-3333-4444-555555555555",
		Endpoint:       "orders_webhook",
		IdempotencyKey: "orig-idem-key",
		ReceivedAt:     time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC),
		Envelope: queue.Message{
			SchemaVersion:      "1.0",
			ID:                 "11111111-2222-3333-4444-555555555555",
			IdempotencyKey:     "orig-idem-key",
			RequestID:          "req-1",
			TraceID:            "trace-1",
			Traceparent:        "00-abcdef-01",
			Endpoint:           "orders_webhook",
			DestinationQueueID: "billing,activity", // AGGREGATE LABEL, not a real id
			Queue:              "billing_q,activity_q",
			ReceivedAt:         time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC),
			Method:             "POST",
			Path:               "/orders",
			Headers:            http.Header{"X-Test": {"1"}},
			Query:              map[string]any{"a": "b"},
			Body:               map[string]any{"hello": "world"},
			RawBodyBase64:      &raw,
			SourceIP:           "203.0.113.7",
			Hops:               2,
		},
	}
}

func TestReconstructNewIDAndReplayOf(t *testing.T) {
	e := sampleEvent()
	msg := reconstruct(e, false)

	if msg.ID == e.ID {
		t.Fatalf("reconstruct kept the original ID %q; want a fresh id", msg.ID)
	}
	if _, err := uuid.Parse(msg.ID); err != nil {
		t.Fatalf("reconstruct ID %q is not a valid uuid: %v", msg.ID, err)
	}
	if msg.ReplayOf != e.ID {
		t.Fatalf("ReplayOf = %q, want %q", msg.ReplayOf, e.ID)
	}
	if msg.Hops != 0 {
		t.Fatalf("Hops = %d, want 0", msg.Hops)
	}
}

func TestReconstructReprocessFalsePreservesIdempotencyKey(t *testing.T) {
	e := sampleEvent()
	msg := reconstruct(e, false)
	if msg.IdempotencyKey != e.Envelope.IdempotencyKey {
		t.Fatalf("IdempotencyKey = %q, want preserved %q", msg.IdempotencyKey, e.Envelope.IdempotencyKey)
	}
}

func TestReconstructReprocessTrueFreshIdempotencyKey(t *testing.T) {
	e := sampleEvent()
	msg := reconstruct(e, true)
	if msg.IdempotencyKey == "" {
		t.Fatal("reprocess=true produced an empty IdempotencyKey; want a fresh non-empty key")
	}
	if msg.IdempotencyKey == e.Envelope.IdempotencyKey {
		t.Fatalf("reprocess=true kept the original IdempotencyKey %q; want a fresh one", msg.IdempotencyKey)
	}
}

func TestReconstructHydratesRawBodyAndPreservesOrigHeaders(t *testing.T) {
	e := sampleEvent()
	e.Envelope.OrigHeaders = http.Header{"Stripe-Signature": {"t=1,v1=abc"}}
	msg := reconstruct(e, false)
	if string(msg.RawBody) != "rawbody" {
		t.Fatalf("RawBody = %q, want decoded raw_body_base64", msg.RawBody)
	}
	if msg.OrigHeaders.Get("Stripe-Signature") != "t=1,v1=abc" {
		t.Fatalf("OrigHeaders = %#v", msg.OrigHeaders)
	}
}

func TestReconstructPreservesEnvelopeFields(t *testing.T) {
	e := sampleEvent()
	msg := reconstruct(e, false)

	if msg.SchemaVersion != e.Envelope.SchemaVersion {
		t.Errorf("SchemaVersion = %q, want %q", msg.SchemaVersion, e.Envelope.SchemaVersion)
	}
	if msg.Endpoint != e.Envelope.Endpoint {
		t.Errorf("Endpoint = %q, want %q", msg.Endpoint, e.Envelope.Endpoint)
	}
	if msg.Method != e.Envelope.Method || msg.Path != e.Envelope.Path {
		t.Errorf("Method/Path = %q/%q, want %q/%q", msg.Method, msg.Path, e.Envelope.Method, e.Envelope.Path)
	}
	if !msg.ReceivedAt.Equal(e.Envelope.ReceivedAt) {
		t.Errorf("ReceivedAt = %v, want original %v", msg.ReceivedAt, e.Envelope.ReceivedAt)
	}
	if msg.Traceparent != e.Envelope.Traceparent {
		t.Errorf("Traceparent = %q, want %q", msg.Traceparent, e.Envelope.Traceparent)
	}
	if msg.RequestID != e.Envelope.RequestID || msg.TraceID != e.Envelope.TraceID {
		t.Errorf("RequestID/TraceID not preserved")
	}
	if msg.SourceIP != e.Envelope.SourceIP {
		t.Errorf("SourceIP = %q, want %q", msg.SourceIP, e.Envelope.SourceIP)
	}
	if fmt.Sprint(msg.Body) != fmt.Sprint(e.Envelope.Body) {
		t.Errorf("Body = %v, want %v", msg.Body, e.Envelope.Body)
	}
	if fmt.Sprint(msg.Headers) != fmt.Sprint(e.Envelope.Headers) {
		t.Errorf("Headers = %v, want %v", msg.Headers, e.Envelope.Headers)
	}
	if msg.RawBodyBase64 == nil || *msg.RawBodyBase64 != *e.Envelope.RawBodyBase64 {
		t.Errorf("RawBodyBase64 not preserved")
	}
}

// ---------------------------------------------------------------------------
// Fakes.
// ---------------------------------------------------------------------------

type fakeStore struct {
	listFn       func(ctx context.Context, f replaystore.Filter) ([]replaystore.EventSummary, string, error)
	getFn        func(ctx context.Context, id string) (replaystore.Event, bool, error)
	countFn      func(ctx context.Context, f replaystore.Filter) (int64, error)
	deliveriesFn func(ctx context.Context, id string) ([]replaystore.Delivery, error)
}

func (s *fakeStore) List(ctx context.Context, f replaystore.Filter) ([]replaystore.EventSummary, string, error) {
	return s.listFn(ctx, f)
}
func (s *fakeStore) Get(ctx context.Context, id string) (replaystore.Event, bool, error) {
	return s.getFn(ctx, id)
}
func (s *fakeStore) Count(ctx context.Context, f replaystore.Filter) (int64, error) {
	return s.countFn(ctx, f)
}
func (s *fakeStore) Deliveries(ctx context.Context, id string) ([]replaystore.Delivery, error) {
	return s.deliveriesFn(ctx, id)
}

type publishCall struct {
	destID string
	queue  string
	msg    queue.Message
}

type fakePublisher struct {
	mu     sync.Mutex
	calls  []publishCall
	failOn func(destID, target string) error
}

func (p *fakePublisher) Publish(_ context.Context, destID, queue string, msg queue.Message) error {
	p.mu.Lock()
	p.calls = append(p.calls, publishCall{destID: destID, queue: queue, msg: msg})
	p.mu.Unlock()
	if p.failOn != nil {
		return p.failOn(destID, queue)
	}
	return nil
}
func (p *fakePublisher) Ready(context.Context) error { return nil }
func (p *fakePublisher) Close() error                { return nil }

func (p *fakePublisher) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.calls)
}

const testKey = "secret"

func newTestHandler(store StoreReader, pub queue.Publisher) http.Handler {
	return Handler(testKey, store, pub, nil, metrics.New(), nil)
}

func newTestHandlerWithUnsupported(store StoreReader, pub queue.Publisher, unsupported map[string]string) http.Handler {
	return Handler(testKey, store, pub, unsupported, metrics.New(), nil)
}

func doReq(t *testing.T, h http.Handler, method, path, key string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body != nil {
		b, _ := json.Marshal(body)
		r = httptest.NewRequestWithContext(context.Background(), method, path, bytes.NewReader(b))
	} else {
		r = httptest.NewRequestWithContext(context.Background(), method, path, nil)
	}
	if key != "" {
		r.Header.Set("Authorization", "Bearer "+key)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// ---------------------------------------------------------------------------
// Handler tests.
// ---------------------------------------------------------------------------

func TestListRequiresAPIKey(t *testing.T) {
	store := &fakeStore{
		listFn: func(context.Context, replaystore.Filter) ([]replaystore.EventSummary, string, error) {
			return nil, "", nil
		},
	}
	h := newTestHandler(store, &fakePublisher{})

	if w := doReq(t, h, http.MethodGet, "/replay/events", "", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("no key: status = %d, want 401", w.Code)
	}
	if w := doReq(t, h, http.MethodGet, "/replay/events", "wrong", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("wrong key: status = %d, want 401", w.Code)
	}
	if w := doReq(t, h, http.MethodGet, "/replay/events", testKey, nil); w.Code != http.StatusOK {
		t.Fatalf("correct key: status = %d, want 200", w.Code)
	}
}

func TestListReturnsSummariesAndCursor(t *testing.T) {
	store := &fakeStore{
		listFn: func(_ context.Context, f replaystore.Filter) ([]replaystore.EventSummary, string, error) {
			if f.Endpoint != "orders_webhook" {
				t.Errorf("filter endpoint = %q, want orders_webhook", f.Endpoint)
			}
			return []replaystore.EventSummary{
				{ID: "a", Endpoint: "orders_webhook", Status: "delivered", ReceivedAt: time.Unix(1, 0).UTC()},
			}, "next-cur", nil
		},
	}
	h := newTestHandler(store, &fakePublisher{})
	w := doReq(t, h, http.MethodGet, "/replay/events?endpoint=orders_webhook", testKey, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
	var resp struct {
		Events []struct {
			ID string `json:"id"`
		} `json:"events"`
		NextCursor string `json:"next_cursor"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Events) != 1 || resp.Events[0].ID != "a" {
		t.Fatalf("events = %+v, want one event id=a", resp.Events)
	}
	if resp.NextCursor != "next-cur" {
		t.Fatalf("next_cursor = %q, want next-cur", resp.NextCursor)
	}
}

func TestListBadSinceReturns400(t *testing.T) {
	store := &fakeStore{listFn: func(context.Context, replaystore.Filter) ([]replaystore.EventSummary, string, error) {
		t.Fatal("List should not be called on a bad since")
		return nil, "", nil
	}}
	h := newTestHandler(store, &fakePublisher{})
	w := doReq(t, h, http.MethodGet, "/replay/events?since=not-a-time", testKey, nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestGetUnknownReturns404(t *testing.T) {
	store := &fakeStore{
		getFn: func(context.Context, string) (replaystore.Event, bool, error) {
			return replaystore.Event{}, false, nil
		},
	}
	h := newTestHandler(store, &fakePublisher{})
	w := doReq(t, h, http.MethodGet, "/replay/events/unknown-id", testKey, nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

// deliveriesStore returns a store whose Get yields e and Deliveries yields ds.
func deliveriesStore(e replaystore.Event, ds []replaystore.Delivery) *fakeStore {
	return &fakeStore{
		getFn: func(_ context.Context, id string) (replaystore.Event, bool, error) {
			if id == e.ID {
				return e, true, nil
			}
			return replaystore.Event{}, false, nil
		},
		deliveriesFn: func(context.Context, string) ([]replaystore.Delivery, error) {
			return ds, nil
		},
	}
}

func TestSingleReplayRepublishesToRecordedDeliveries(t *testing.T) {
	e := sampleEvent()
	// The recorded deliveries are the REAL destinations -- deliberately
	// different from the envelope's comma-joined DestinationQueueID label.
	ds := []replaystore.Delivery{
		{DestinationID: "billing", Target: "billing_q", Outcome: "delivered"},
		{DestinationID: "activity", Target: "activity_q", Outcome: "failed"},
	}
	pub := &fakePublisher{}
	h := newTestHandler(deliveriesStore(e, ds), pub)

	w := doReq(t, h, http.MethodPost, "/replay/events/"+e.ID+"/replay", testKey, map[string]any{"reprocess": false})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
	if pub.callCount() != 2 {
		t.Fatalf("publish calls = %d, want 2", pub.callCount())
	}
	// Assert we used the recorded deliveries, NOT the envelope's aggregate label.
	got := map[string]string{}
	var newID string
	for _, c := range pub.calls {
		got[c.destID] = c.queue
		if c.destID == "billing,activity" {
			t.Fatalf("published to the envelope's aggregate label %q; must use recorded deliveries", c.destID)
		}
		if c.msg.ID == e.ID {
			t.Fatalf("published msg kept the original event ID; want a fresh id")
		}
		if c.msg.ReplayOf != e.ID {
			t.Fatalf("published msg ReplayOf = %q, want %q", c.msg.ReplayOf, e.ID)
		}
		if c.msg.IdempotencyKey != e.Envelope.IdempotencyKey {
			t.Fatalf("reprocess=false: IdempotencyKey = %q, want preserved %q", c.msg.IdempotencyKey, e.Envelope.IdempotencyKey)
		}
		if c.msg.DestinationQueueID != c.destID {
			t.Fatalf("msg.DestinationQueueID = %q, want per-delivery %q", c.msg.DestinationQueueID, c.destID)
		}
		newID = c.msg.ID
	}
	if got["billing"] != "billing_q" || got["activity"] != "activity_q" {
		t.Fatalf("published to %+v, want billing->billing_q and activity->activity_q", got)
	}

	var resp struct {
		ReplayedEventID string `json:"replayed_event_id"`
		OriginalEventID string `json:"original_event_id"`
		Destinations    []struct {
			Destination string `json:"destination"`
			Status      string `json:"status"`
		} `json:"destinations"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.ReplayedEventID != newID || resp.OriginalEventID != e.ID {
		t.Fatalf("resp ids = %q/%q, want %q/%q", resp.ReplayedEventID, resp.OriginalEventID, newID, e.ID)
	}
	if len(resp.Destinations) != 2 {
		t.Fatalf("resp destinations = %d, want 2", len(resp.Destinations))
	}
}

func TestSingleReplayReprocessTrueFreshKey(t *testing.T) {
	e := sampleEvent()
	ds := []replaystore.Delivery{{DestinationID: "billing", Target: "billing_q", Outcome: "delivered"}}
	pub := &fakePublisher{}
	h := newTestHandler(deliveriesStore(e, ds), pub)

	w := doReq(t, h, http.MethodPost, "/replay/events/"+e.ID+"/replay", testKey, map[string]any{"reprocess": true})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if pub.callCount() != 1 {
		t.Fatalf("publish calls = %d, want 1", pub.callCount())
	}
	got := pub.calls[0].msg.IdempotencyKey
	if got == "" || got == e.Envelope.IdempotencyKey {
		t.Fatalf("reprocess=true IdempotencyKey = %q, want fresh non-empty", got)
	}
}

func TestSingleReplayNoDeliveries422(t *testing.T) {
	e := sampleEvent()
	pub := &fakePublisher{}
	h := newTestHandler(deliveriesStore(e, nil), pub)

	w := doReq(t, h, http.MethodPost, "/replay/events/"+e.ID+"/replay", testKey, nil)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (body: %s)", w.Code, w.Body.String())
	}
	if pub.callCount() != 0 {
		t.Fatalf("publish calls = %d, want 0", pub.callCount())
	}
}

func TestSingleReplayUnknownReturns404(t *testing.T) {
	store := &fakeStore{
		getFn: func(context.Context, string) (replaystore.Event, bool, error) {
			return replaystore.Event{}, false, nil
		},
	}
	h := newTestHandler(store, &fakePublisher{})
	w := doReq(t, h, http.MethodPost, "/replay/events/nope/replay", testKey, nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestSingleReplayPerDestinationFailure(t *testing.T) {
	e := sampleEvent()
	ds := []replaystore.Delivery{
		{DestinationID: "billing", Target: "billing_q", Outcome: "delivered"},
		{DestinationID: "activity", Target: "activity_q", Outcome: "delivered"},
	}
	pub := &fakePublisher{failOn: func(destID, _ string) error {
		if destID == "activity" {
			return fmt.Errorf("boom")
		}
		return nil
	}}
	h := newTestHandler(deliveriesStore(e, ds), pub)

	w := doReq(t, h, http.MethodPost, "/replay/events/"+e.ID+"/replay", testKey, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 even with a per-destination failure", w.Code)
	}
	var resp struct {
		Destinations []struct {
			Destination string `json:"destination"`
			Status      string `json:"status"`
			Error       string `json:"error"`
		} `json:"destinations"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	byDest := map[string]string{}
	errByDest := map[string]string{}
	for _, d := range resp.Destinations {
		byDest[d.Destination] = d.Status
		errByDest[d.Destination] = d.Error
	}
	if byDest["billing"] != "ok" {
		t.Fatalf("billing status = %q, want ok", byDest["billing"])
	}
	if byDest["activity"] != "failed" || errByDest["activity"] == "" {
		t.Fatalf("activity status/error = %q/%q, want failed with error", byDest["activity"], errByDest["activity"])
	}
}

// ---------------------------------------------------------------------------
// Unsupported-destination (e.g. proxy-mode webhook) skip-on-replay tests.
// ---------------------------------------------------------------------------

func TestSingleReplaySkipsUnsupportedDestinationWithReason(t *testing.T) {
	e := sampleEvent()
	ds := []replaystore.Delivery{
		{DestinationID: "billing", Target: "billing_q", Outcome: "delivered"},
		{DestinationID: "proxy_dest", Target: "proxy_q", Outcome: "delivered"},
	}
	pub := &fakePublisher{}
	const reason = "proxy-mode webhook replay is unsupported: the original raw request body/headers are not persisted"
	h := newTestHandlerWithUnsupported(deliveriesStore(e, ds), pub, map[string]string{"proxy_dest": reason})

	w := doReq(t, h, http.MethodPost, "/replay/events/"+e.ID+"/replay", testKey, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
	// The publisher must NEVER be called for the unsupported destination.
	if pub.callCount() != 1 {
		t.Fatalf("publish calls = %d, want 1 (only the non-proxy destination)", pub.callCount())
	}
	if pub.calls[0].destID != "billing" {
		t.Fatalf("published destination = %q, want billing", pub.calls[0].destID)
	}

	var resp struct {
		Destinations []struct {
			Destination string `json:"destination"`
			Status      string `json:"status"`
			Reason      string `json:"reason"`
		} `json:"destinations"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	byDest := map[string]struct {
		status string
		reason string
	}{}
	for _, d := range resp.Destinations {
		byDest[d.Destination] = struct {
			status string
			reason string
		}{d.Status, d.Reason}
	}
	if byDest["billing"].status != "ok" {
		t.Fatalf("billing status = %+v, want ok", byDest["billing"])
	}
	if byDest["proxy_dest"].status != "skipped" || byDest["proxy_dest"].reason != reason {
		t.Fatalf("proxy_dest result = %+v, want status=skipped reason=%q", byDest["proxy_dest"], reason)
	}
}

func TestSingleReplayAllDestinationsUnsupportedNoPublish(t *testing.T) {
	e := sampleEvent()
	ds := []replaystore.Delivery{
		{DestinationID: "proxy_dest", Target: "proxy_q", Outcome: "delivered"},
	}
	pub := &fakePublisher{}
	const reason = "proxy-mode webhook replay is unsupported"
	h := newTestHandlerWithUnsupported(deliveriesStore(e, ds), pub, map[string]string{"proxy_dest": reason})

	w := doReq(t, h, http.MethodPost, "/replay/events/"+e.ID+"/replay", testKey, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
	if pub.callCount() != 0 {
		t.Fatalf("publish calls = %d, want 0 -- nothing may be delivered when every destination is unsupported", pub.callCount())
	}

	var resp struct {
		ReplayedEventID string `json:"replayed_event_id"`
		Destinations    []struct {
			Destination string `json:"destination"`
			Status      string `json:"status"`
			Reason      string `json:"reason"`
		} `json:"destinations"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Destinations) != 1 || resp.Destinations[0].Status != "skipped" || resp.Destinations[0].Reason != reason {
		t.Fatalf("destinations = %+v, want one skipped proxy_dest with reason %q", resp.Destinations, reason)
	}
}

func TestBulkDryRunCountsWithoutPublishing(t *testing.T) {
	store := &fakeStore{
		countFn: func(_ context.Context, f replaystore.Filter) (int64, error) {
			if f.Endpoint != "orders_webhook" {
				t.Errorf("count filter endpoint = %q", f.Endpoint)
			}
			return 42, nil
		},
		listFn: func(context.Context, replaystore.Filter) ([]replaystore.EventSummary, string, error) {
			t.Fatal("dry_run must not call List")
			return nil, "", nil
		},
	}
	pub := &fakePublisher{}
	h := newTestHandler(store, pub)

	w := doReq(t, h, http.MethodPost, "/replay/replay", testKey, map[string]any{"endpoint": "orders_webhook", "dry_run": true})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp struct {
		Matched int64 `json:"matched"`
		DryRun  bool  `json:"dry_run"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Matched != 42 || !resp.DryRun {
		t.Fatalf("resp = %+v, want matched=42 dry_run=true", resp)
	}
	if pub.callCount() != 0 {
		t.Fatalf("dry_run published %d messages, want 0", pub.callCount())
	}
}

func TestBulkReplayHonorsMaxCap(t *testing.T) {
	e := sampleEvent()
	ds := []replaystore.Delivery{{DestinationID: "billing", Target: "billing_q", Outcome: "delivered"}}
	store := &fakeStore{
		listFn: func(_ context.Context, f replaystore.Filter) ([]replaystore.EventSummary, string, error) {
			// Honor the requested limit; signal "more exist" via a cursor.
			n := f.Limit
			out := make([]replaystore.EventSummary, n)
			for i := range out {
				out[i] = replaystore.EventSummary{ID: fmt.Sprintf("evt-%d", i)}
			}
			return out, "more", nil
		},
		getFn: func(_ context.Context, id string) (replaystore.Event, bool, error) {
			ev := e
			ev.ID = id
			return ev, true, nil
		},
		deliveriesFn: func(context.Context, string) ([]replaystore.Delivery, error) {
			return ds, nil
		},
	}
	pub := &fakePublisher{}
	h := newTestHandler(store, pub)

	w := doReq(t, h, http.MethodPost, "/replay/replay", testKey, map[string]any{"max": 2})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp struct {
		Replayed int  `json:"replayed"`
		Capped   bool `json:"capped"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Replayed != 2 {
		t.Fatalf("replayed = %d, want 2 (capped at max)", resp.Replayed)
	}
	if !resp.Capped {
		t.Fatalf("capped = false, want true")
	}
	if pub.callCount() != 2 {
		t.Fatalf("publish calls = %d, want 2", pub.callCount())
	}
}

func TestBulkReplayPerEventResults(t *testing.T) {
	e := sampleEvent()
	ds := []replaystore.Delivery{
		{DestinationID: "billing", Target: "billing_q", Outcome: "delivered"},
		{DestinationID: "activity", Target: "activity_q", Outcome: "delivered"},
	}
	store := &fakeStore{
		listFn: func(context.Context, replaystore.Filter) ([]replaystore.EventSummary, string, error) {
			return []replaystore.EventSummary{{ID: "evt-1"}, {ID: "evt-2"}}, "", nil
		},
		getFn: func(_ context.Context, id string) (replaystore.Event, bool, error) {
			ev := e
			ev.ID = id
			return ev, true, nil
		},
		deliveriesFn: func(context.Context, string) ([]replaystore.Delivery, error) {
			return ds, nil
		},
	}
	// Fail "activity" for every event.
	pub := &fakePublisher{failOn: func(destID, _ string) error {
		if destID == "activity" {
			return fmt.Errorf("boom")
		}
		return nil
	}}
	h := newTestHandler(store, pub)

	w := doReq(t, h, http.MethodPost, "/replay/replay", testKey, map[string]any{})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp struct {
		Replayed int  `json:"replayed"`
		Capped   bool `json:"capped"`
		Results  []struct {
			OriginalEventID string `json:"original_event_id"`
			ReplayedEventID string `json:"replayed_event_id"`
			Destinations    []struct {
				Destination string `json:"destination"`
				Status      string `json:"status"`
			} `json:"destinations"`
		} `json:"results"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Replayed != 2 {
		t.Fatalf("replayed = %d, want 2", resp.Replayed)
	}
	if len(resp.Results) != 2 {
		t.Fatalf("results = %d, want 2", len(resp.Results))
	}
	for _, r := range resp.Results {
		if r.ReplayedEventID == "" || r.ReplayedEventID == r.OriginalEventID {
			t.Fatalf("result %s: replayed id = %q, want fresh", r.OriginalEventID, r.ReplayedEventID)
		}
		byDest := map[string]string{}
		for _, d := range r.Destinations {
			byDest[d.Destination] = d.Status
		}
		if byDest["billing"] != "ok" || byDest["activity"] != "failed" {
			t.Fatalf("result %s destinations = %+v, want billing=ok activity=failed", r.OriginalEventID, byDest)
		}
	}
}

func TestReconstructPreservesVariables(t *testing.T) {
	ev := replaystore.Event{ID: "evt-1", Envelope: queue.Message{ID: "evt-1", Variables: map[string]string{"envName": "tenant-a"}}}
	msg := reconstruct(ev, false)
	if msg.Variables["envName"] != "tenant-a" {
		t.Fatalf("variables must survive reconstruction: %#v", msg.Variables)
	}
	if msg.ReplayOf != "evt-1" || msg.ID == "evt-1" {
		t.Fatalf("replay identity: id=%q replay_of=%q", msg.ID, msg.ReplayOf)
	}
}
