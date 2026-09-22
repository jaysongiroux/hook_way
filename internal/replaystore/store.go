// Package replaystore persists a Postgres-backed record of accepted inbound
// webhook events (the "replay store") so operators can inspect and re-deliver
// past webhooks. This file defines the schema, the write path (Capture,
// RecordOutcome), the read path (List, Get, Count) and retention purge
// (PurgeExpired, PurgeOverCount), and the Store lifecycle (New, Ping, Close).
// Capture wiring, the HTTP API, and a scheduled cleanup loop are later tasks
// and are intentionally absent here.
package replaystore

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jaysongiroux/hook_way/internal/config"
	"github.com/jaysongiroux/hook_way/internal/queue"
)

// Event is one captured inbound webhook, ready to insert into replay_events.
// Envelope is the full queue.Message the destinations would have received --
// stored verbatim as the jsonb "envelope" column, the source of truth for a
// later replay.
type Event struct {
	ID             string
	Endpoint       string
	IdempotencyKey string
	RequestID      string
	TraceID        string
	Status         string
	ReceivedAt     time.Time
	Envelope       queue.Message
}

// Delivery is one destination's outcome for a captured event, as recorded by
// RecordOutcome. Outcome must be one of OutcomeDelivered/OutcomeFailed --
// RecordOutcome validates this and rejects anything else (see
// validateOutcome) rather than silently persisting a typo'd value.
type Delivery struct {
	DestinationID string
	Target        string
	Outcome       string
}

// OutcomeDelivered and OutcomeFailed are the only valid values for
// Delivery.Outcome. Anything else is rejected by RecordOutcome before it
// writes anything (see validateOutcome) -- an unrecognized outcome string
// would otherwise persist verbatim and be counted as neither delivered nor
// failed by rollupStatus, silently mis-classifying the event's status.
const (
	OutcomeDelivered = "delivered"
	OutcomeFailed    = "failed"
)

// Store is the replay store's Postgres-backed persistence layer: schema
// bootstrap plus the Capture/RecordOutcome write path. ops is the pgOps
// (Exec/Query/QueryRow/Ping/Close) built by newClient; eventsTable and
// deliveriesTable are pre-sanitized, schema-qualified identifiers so every
// query built from them is injection-safe by construction (see
// qualifiedTableNames below).
type Store struct {
	ops    pgOps
	logger *slog.Logger

	schema            string
	eventsTable       string
	deliveriesTable   string
	insertEventSQL    string
	recordOutcomeSQL  string
	getEventSQL       string
	deliveriesReadSQL string
	purgeExpiredSQL   string
	purgeOverCountSQL string
}

// New connects to cfg.Postgres (newClient itself Pings once to fail fast on
// a bad connection) and ensures the replay_events/replay_deliveries schema
// exists (CREATE TABLE/INDEX IF NOT EXISTS -- safe to run on every startup).
func New(ctx context.Context, cfg config.ReplayConfig, logger *slog.Logger) (*Store, error) {
	if logger == nil {
		logger = slog.Default()
	}

	ops, err := newClient(ctx, cfg.Postgres)
	if err != nil {
		return nil, fmt.Errorf("replaystore: connect: %w", err)
	}

	schema := cfg.Postgres.Schema
	if schema == "" {
		schema = "public"
	}
	eventsTable, deliveriesTable := qualifiedTableNames(schema)

	s := &Store{
		ops:               ops,
		logger:            logger,
		schema:            schema,
		eventsTable:       eventsTable,
		deliveriesTable:   deliveriesTable,
		insertEventSQL:    buildInsertEventSQL(eventsTable),
		recordOutcomeSQL:  buildRecordOutcomeSQL(eventsTable, deliveriesTable),
		getEventSQL:       buildGetEventSQL(eventsTable),
		deliveriesReadSQL: buildDeliveriesReadSQL(deliveriesTable),
		purgeExpiredSQL:   buildPurgeExpiredSQL(eventsTable),
		purgeOverCountSQL: buildPurgeOverCountSQL(eventsTable),
	}

	for _, stmt := range schemaDDL(eventsTable, deliveriesTable) {
		if _, err := ops.Exec(ctx, stmt); err != nil {
			ops.Close()
			return nil, fmt.Errorf("replaystore: ensure schema: %w", err)
		}
	}

	logger.Info("replaystore: schema ensured", slog.String("schema", schema))
	return s, nil
}

// qualifiedTableNames returns the sanitized "schema"."table" expressions for
// both replay tables.
//
// Schema-qualification choice: table-name prefixing via pgx.Identifier{...}.
// Sanitize(), not a session-level `SET search_path`. This mirrors the
// existing precedent in internal/queue/postgres.go (NewPostgresPublisher
// builds tableExpr the same way) and has two advantages over search_path
// here: (1) pgxpool multiplexes queries across pooled connections, and a
// search_path set on one connection would not reliably apply to whichever
// connection later serves a given query without also setting it per
// connection (e.g. via AfterConnect) or per statement; explicit qualification
// has no such connection-affinity requirement. (2) it keeps every SQL
// statement in this package self-contained and greppable. The schema
// defaults to "public" (matching config.convertPostgresConfig's default), and
// is always applied via pgx.Identifier{schema, table}.Sanitize() regardless
// of whether it is literally "public", keeping one code path.
func qualifiedTableNames(schema string) (eventsTable, deliveriesTable string) {
	eventsTable = pgx.Identifier{schema, "replay_events"}.Sanitize()
	deliveriesTable = pgx.Identifier{schema, "replay_deliveries"}.Sanitize()
	return eventsTable, deliveriesTable
}

// schemaDDL returns the ordered CREATE TABLE/INDEX IF NOT EXISTS statements
// for the replay store, qualified against the given (already-sanitized)
// table expressions. Index names are sanitized separately: indexes live in
// the schema of their table automatically, so they need no schema prefix of
// their own.
func schemaDDL(eventsTable, deliveriesTable string) []string {
	endpointIdx := pgx.Identifier{"replay_events_endpoint_received_at"}.Sanitize()
	statusIdx := pgx.Identifier{"replay_events_status_received_at"}.Sanitize()
	destIdx := pgx.Identifier{"replay_deliveries_dest_outcome"}.Sanitize()

	return []string{
		`CREATE TABLE IF NOT EXISTS ` + eventsTable + ` (
    id uuid PRIMARY KEY,
    received_at timestamptz NOT NULL,
    endpoint text NOT NULL,
    idempotency_key text,
    request_id text,
    trace_id text,
    status text NOT NULL DEFAULT 'pending',
    envelope jsonb NOT NULL,
    inserted_at timestamptz NOT NULL DEFAULT now()
)`,
		`CREATE INDEX IF NOT EXISTS ` + endpointIdx + ` ON ` + eventsTable + ` (endpoint, received_at)`,
		`CREATE INDEX IF NOT EXISTS ` + statusIdx + ` ON ` + eventsTable + ` (status, received_at)`,
		// The outcome CHECK is defense in depth for fresh tables only:
		// CREATE TABLE IF NOT EXISTS will not add this constraint to a
		// replay_deliveries table that already exists from before the
		// constraint was introduced. The Go-level guard in validateOutcome
		// (called from RecordOutcome) is the primary defense and applies
		// regardless of when the table was created.
		`CREATE TABLE IF NOT EXISTS ` + deliveriesTable + ` (
    event_id uuid NOT NULL REFERENCES ` + eventsTable + `(id) ON DELETE CASCADE,
    destination_id text NOT NULL,
    target text NOT NULL,
    outcome text NOT NULL CHECK (outcome IN ('delivered', 'failed')),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (event_id, destination_id, target)
)`,
		`CREATE INDEX IF NOT EXISTS ` + destIdx + ` ON ` + deliveriesTable + ` (destination_id, outcome)`,
	}
}

// buildInsertEventSQL builds the parameterized Capture INSERT against the
// given events table expression. Status is always the literal 'pending' --
// every captured event starts pending regardless of Event.Status (that field
// exists for the read path to report the current rolled-up status, not for
// Capture to set it).
func buildInsertEventSQL(eventsTable string) string {
	return `INSERT INTO ` + eventsTable + ` (id, received_at, endpoint, idempotency_key, request_id, trace_id, status, envelope)
VALUES ($1, $2, $3, $4, $5, $6, 'pending', $7::jsonb) ON CONFLICT (id) DO NOTHING`
}

// buildRecordOutcomeSQL builds the parameterized RecordOutcome statement.
//
// Transaction-correctness choice: this is deliberately ONE statement (a
// data-modifying CTE performing the delivery upserts, followed by the status
// UPDATE) rather than an explicit BEGIN/COMMIT wrapping two statements. The
// pgOps interface this package defines (see client.go) intentionally exposes
// only Exec/Query/QueryRow/Ping/Close -- no Begin -- per the brief. A single
// Postgres statement is inherently atomic (all rows upsert and the rollup
// status update commit together, or neither does on error), which is the
// correctness property "in a transaction" is after, without widening the
// interface. $1=event id, $2/$3/$4=parallel arrays of destination_id/target/
// outcome (unnested into rows), $5=the rollup status computed by the caller.
func buildRecordOutcomeSQL(eventsTable, deliveriesTable string) string {
	return `WITH input AS (
    SELECT * FROM unnest($2::text[], $3::text[], $4::text[]) AS t(destination_id, target, outcome)
),
ins AS (
    INSERT INTO ` + deliveriesTable + ` (event_id, destination_id, target, outcome)
    SELECT $1, destination_id, target, outcome FROM input
    ON CONFLICT (event_id, destination_id, target) DO UPDATE SET outcome = EXCLUDED.outcome, updated_at = now()
    RETURNING 1
)
UPDATE ` + eventsTable + ` SET status = $5 WHERE id = $1`
}

// persistedEnvelope is the jsonb shape stored in replay_events.envelope.
// queue.Message keeps RawBody/OrigHeaders as json:"-" so they are never
// published to destinations (OrigHeaders in particular carries unredacted
// vendor signatures). Capture still needs those fields to reconstruct a
// proxy-mode webhook POST, so they are stored alongside the public envelope
// under orig_headers, and RawBody is always persisted via raw_body_base64.
type persistedEnvelope struct {
	queue.Message
	OrigHeaders http.Header `json:"orig_headers,omitempty"`
}

// marshalEnvelope JSON-encodes the captured queue.Message for binding to the
// envelope jsonb column. Extracted as its own function so the encoding can be
// pinned down by a unit test without a database (TestCaptureMarshalsEnvelope).
func marshalEnvelope(msg queue.Message) (string, error) {
	if msg.RawBodyBase64 == nil && len(msg.RawBody) > 0 {
		encoded := base64.StdEncoding.EncodeToString(msg.RawBody)
		msg.RawBodyBase64 = &encoded
	}
	b, err := json.Marshal(persistedEnvelope{Message: msg, OrigHeaders: msg.OrigHeaders})
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// unmarshalEnvelope reverses marshalEnvelope, restoring RawBody from
// raw_body_base64 and OrigHeaders from orig_headers so a later replay can
// reconstruct a proxy-mode webhook POST.
func unmarshalEnvelope(raw []byte) (queue.Message, error) {
	var p persistedEnvelope
	if err := json.Unmarshal(raw, &p); err != nil {
		return queue.Message{}, err
	}
	msg := p.Message
	msg.OrigHeaders = p.OrigHeaders
	if msg.RawBodyBase64 != nil && len(msg.RawBody) == 0 {
		decoded, err := base64.StdEncoding.DecodeString(*msg.RawBodyBase64)
		if err != nil {
			return queue.Message{}, err
		}
		msg.RawBody = decoded
	}
	return msg, nil
}

// nullableString returns nil (SQL NULL) for an empty string, otherwise the
// string itself. idempotency_key/request_id/trace_id are optional columns;
// binding "" would store an empty string instead of SQL NULL.
func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// Capture inserts e as one replay_events row with status 'pending'. It is
// idempotent per event id: ON CONFLICT (id) DO NOTHING means capturing the
// same e.ID twice (e.g. this same request flow calling Capture again) leaves
// exactly one row. That dedup is scoped to a single id, NOT across retries in
// general -- a provider's retried delivery, or a skip_failed partial-failure
// retry, mints a fresh env.ID for the new attempt (see ingest), so it is
// captured as a second, distinct row rather than deduped against the first.
func (s *Store) Capture(ctx context.Context, e Event) error {
	envelope, err := marshalEnvelope(e.Envelope)
	if err != nil {
		return fmt.Errorf("replaystore: marshal envelope for event %q: %w", e.ID, err)
	}
	_, err = s.ops.Exec(ctx, s.insertEventSQL,
		e.ID, e.ReceivedAt, e.Endpoint,
		nullableString(e.IdempotencyKey), nullableString(e.RequestID), nullableString(e.TraceID),
		envelope,
	)
	if err != nil {
		return fmt.Errorf("replaystore: capture event %q: %w", e.ID, err)
	}
	return nil
}

// validateOutcome rejects any Delivery.Outcome that is not exactly
// OutcomeDelivered or OutcomeFailed. This is the primary defense against a
// typo'd or otherwise unrecognized outcome (e.g. "Delivered", "skipped")
// silently persisting into replay_deliveries.outcome and being counted as
// neither delivered nor failed by rollupStatus (see the doc comment on
// OutcomeDelivered/OutcomeFailed). The CHECK constraint added to the
// replay_deliveries DDL (schemaDDL) is defense in depth for fresh tables
// only; this Go-level check is what protects any table already created
// before that constraint existed, since CREATE TABLE IF NOT EXISTS will not
// retrofit it.
func validateOutcome(outcome string) error {
	switch outcome {
	case OutcomeDelivered, OutcomeFailed:
		return nil
	default:
		return fmt.Errorf("replaystore: invalid delivery outcome %q (want %s|%s)", outcome, OutcomeDelivered, OutcomeFailed)
	}
}

// RecordOutcome upserts one replay_deliveries row per delivery (keyed on
// event_id, destination_id, target -- a redelivery to the same destination
// and target updates the existing row's outcome rather than duplicating it),
// then rolls the event's status up from the delivery outcomes via
// rollupStatus, all as a single atomic statement (see buildRecordOutcomeSQL).
//
// Every Delivery.Outcome is validated (validateOutcome) before any SQL is
// built or executed: on the first invalid outcome, RecordOutcome returns an
// error and the statement is never issued, so nothing is written -- not even
// the valid deliveries earlier in the slice.
func (s *Store) RecordOutcome(ctx context.Context, id string, deliveries []Delivery) error {
	destIDs := make([]string, len(deliveries))
	targets := make([]string, len(deliveries))
	outcomes := make([]string, len(deliveries))
	delivered, failed := 0, 0
	for i, d := range deliveries {
		if err := validateOutcome(d.Outcome); err != nil {
			return err
		}
		destIDs[i] = d.DestinationID
		targets[i] = d.Target
		outcomes[i] = d.Outcome
		switch d.Outcome {
		case OutcomeDelivered:
			delivered++
		case OutcomeFailed:
			failed++
		}
	}
	rollup := rollupStatus(len(deliveries), delivered, failed)

	_, err := s.ops.Exec(ctx, s.recordOutcomeSQL, id, destIDs, targets, outcomes, rollup)
	if err != nil {
		return fmt.Errorf("replaystore: record outcome for event %q: %w", id, err)
	}
	return nil
}

// rollupStatus derives a replay_events.status value from delivery counts:
// all delivered -> "delivered"; all failed -> "failed"; a mix -> "partial";
// no deliveries yet -> "pending". Pure so it is directly unit-testable.
func rollupStatus(total, delivered, failed int) string {
	switch {
	case total == 0:
		return "pending"
	case delivered == total:
		return "delivered"
	case failed == total:
		return "failed"
	default:
		return "partial"
	}
}

// ---------------------------------------------------------------------------
// Read path: List, Get, Count.
// ---------------------------------------------------------------------------

// Filter narrows List/Count to a subset of replay_events, optionally joined
// against replay_deliveries when DestinationID (and/or Target) is set. Zero
// values mean "no constraint" for every field except Limit, which is clamped
// by clampLimit (<=0 -> defaultListLimit, >maxListLimit -> maxListLimit).
type Filter struct {
	Endpoint      string
	DestinationID string // when set, join replay_deliveries
	Target        string
	Status        string // pending|delivered|failed|partial
	Since         time.Time
	Until         time.Time
	Limit         int
	Cursor        string // opaque; encodes (received_at, id) of the last row seen
}

// EventSummary is the flat (envelope-free) projection List returns -- enough
// to render a list UI or drive a dry-run bulk replay without paying to
// deserialize every event's full jsonb envelope.
type EventSummary struct {
	ID             string
	Endpoint       string
	Status         string
	IdempotencyKey string
	ReceivedAt     time.Time
}

const (
	defaultListLimit = 100
	maxListLimit     = 500
)

// clampLimit normalizes a caller-supplied Filter.Limit: non-positive means
// "use the default page size"; anything over the max is capped rather than
// rejected, so a caller passing an unbounded-sounding limit still gets a
// bounded query.
func clampLimit(limit int) int {
	switch {
	case limit <= 0:
		return defaultListLimit
	case limit > maxListLimit:
		return maxListLimit
	default:
		return limit
	}
}

// encodeCursor packs (receivedAt, id) -- the last row of a page, in the same
// (received_at DESC, id DESC) order List sorts by -- into an opaque token
// callers pass back as Filter.Cursor to fetch the next page. The wire format
// (nanoseconds since epoch, a literal '|', then the id) is deliberately
// unparsed by callers; decodeCursor is the only intended reader.
func encodeCursor(receivedAt time.Time, id string) string {
	raw := fmt.Sprintf("%d|%s", receivedAt.UnixNano(), id)
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// decodeCursor reverses encodeCursor, returning an error (never panicking) on
// malformed input -- a cursor is client-supplied and must be treated as
// untrusted input, not a value that can only ever be well-formed.
func decodeCursor(cursor string) (time.Time, string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return time.Time{}, "", fmt.Errorf("replaystore: malformed cursor: %w", err)
	}
	nanosPart, id, found := strings.Cut(string(raw), "|")
	if !found || id == "" {
		return time.Time{}, "", fmt.Errorf("replaystore: malformed cursor: missing id")
	}
	nanos, err := strconv.ParseInt(nanosPart, 10, 64)
	if err != nil {
		return time.Time{}, "", fmt.Errorf("replaystore: malformed cursor timestamp: %w", err)
	}
	return time.Unix(0, nanos).UTC(), id, nil
}

// buildFilterPredicates builds the WHERE-clause fragments and bound args
// shared by buildListQuery and buildCountQuery, so the two never drift. Every
// predicate is parameterized ($n) -- the only interpolated strings are the
// already-sanitized deliveriesTable identifier and column/operator literals,
// never a data value. When f.DestinationID is set, join replay_deliveries so
// callers can also filter by d.target; the caller (buildListQuery/
// buildCountQuery) is responsible for de-duplicating the resulting one-event-
// to-many-deliveries fan-out (DISTINCT / count(DISTINCT e.id)), since a
// destination filter can otherwise return the same event once per matching
// delivery row.
//
// includeCursor controls whether the keyset predicate is added: Count ignores
// Cursor (and Limit) per the brief, so it passes false.
func buildFilterPredicates(f Filter, deliveriesTable string, includeCursor bool) (join string, wheres []string, args []any) {
	if f.DestinationID != "" {
		join = " JOIN " + deliveriesTable + " d ON d.event_id = e.id"
		args = append(args, f.DestinationID)
		wheres = append(wheres, fmt.Sprintf("d.destination_id = $%d", len(args)))
		if f.Target != "" {
			args = append(args, f.Target)
			wheres = append(wheres, fmt.Sprintf("d.target = $%d", len(args)))
		}
	}
	if f.Endpoint != "" {
		args = append(args, f.Endpoint)
		wheres = append(wheres, fmt.Sprintf("e.endpoint = $%d", len(args)))
	}
	if f.Status != "" {
		args = append(args, f.Status)
		wheres = append(wheres, fmt.Sprintf("e.status = $%d", len(args)))
	}
	if !f.Since.IsZero() {
		args = append(args, f.Since)
		wheres = append(wheres, fmt.Sprintf("e.received_at >= $%d", len(args)))
	}
	if !f.Until.IsZero() {
		args = append(args, f.Until)
		wheres = append(wheres, fmt.Sprintf("e.received_at < $%d", len(args)))
	}
	if includeCursor && f.Cursor != "" {
		if ts, id, err := decodeCursor(f.Cursor); err == nil {
			args = append(args, ts, id)
			wheres = append(wheres, fmt.Sprintf("(e.received_at, e.id) < ($%d, $%d)", len(args)-1, len(args)))
		}
	}
	return join, wheres, args
}

// buildListQuery is the pure query builder behind List: given a Filter and a
// schema name, it returns the fully parameterized SQL and its positional args
// with no I/O, so it is unit-testable without a database. Ordering is always
// received_at DESC, id DESC, matching the keyset predicate built into
// buildFilterPredicates -- consistent ordering is what makes the cursor
// gapless (no skipped or repeated rows across pages). Only the flat summary
// columns are selected (never envelope), and DISTINCT is added whenever the
// destination join is present so a multi-delivery event yields one row.
func buildListQuery(f Filter, schema string) (string, []any) {
	eventsTable, deliveriesTable := qualifiedTableNames(schema)
	join, wheres, args := buildFilterPredicates(f, deliveriesTable, true)

	var b strings.Builder
	b.WriteString("SELECT ")
	if f.DestinationID != "" {
		b.WriteString("DISTINCT ")
	}
	b.WriteString("e.id, e.endpoint, e.status, e.idempotency_key, e.received_at FROM ")
	b.WriteString(eventsTable)
	b.WriteString(" e")
	b.WriteString(join)
	if len(wheres) > 0 {
		b.WriteString(" WHERE ")
		b.WriteString(strings.Join(wheres, " AND "))
	}
	b.WriteString(" ORDER BY e.received_at DESC, e.id DESC")

	args = append(args, clampLimit(f.Limit))
	_, _ = fmt.Fprintf(&b, " LIMIT $%d", len(args))

	return b.String(), args
}

// buildCountQuery mirrors buildListQuery's predicates (via
// buildFilterPredicates) for Count: same WHERE/JOIN, no ORDER BY/LIMIT/
// cursor, and count(DISTINCT e.id) instead of count(*) whenever the
// destination join could otherwise multiply-count an event with several
// matching deliveries.
func buildCountQuery(f Filter, schema string) (string, []any) {
	eventsTable, deliveriesTable := qualifiedTableNames(schema)
	join, wheres, args := buildFilterPredicates(f, deliveriesTable, false)

	countExpr := "*"
	if f.DestinationID != "" {
		countExpr = "DISTINCT e.id"
	}

	var b strings.Builder
	b.WriteString("SELECT count(")
	b.WriteString(countExpr)
	b.WriteString(") FROM ")
	b.WriteString(eventsTable)
	b.WriteString(" e")
	b.WriteString(join)
	if len(wheres) > 0 {
		b.WriteString(" WHERE ")
		b.WriteString(strings.Join(wheres, " AND "))
	}
	return b.String(), args
}

// buildGetEventSQL builds the parameterized single-row SELECT Get uses,
// including envelope (List never selects this column; Get is the one path
// that needs the full jsonb body).
func buildGetEventSQL(eventsTable string) string {
	return `SELECT id, received_at, endpoint, idempotency_key, request_id, trace_id, status, envelope
FROM ` + eventsTable + ` WHERE id = $1`
}

// buildDeliveriesReadSQL builds the parameterized per-event deliveries SELECT
// Deliveries uses. Ordered by (destination_id, target) for a stable,
// deterministic replay fan-out order.
func buildDeliveriesReadSQL(deliveriesTable string) string {
	return `SELECT destination_id, target, outcome
FROM ` + deliveriesTable + ` WHERE event_id = $1 ORDER BY destination_id, target`
}

// List returns a page of EventSummary matching f, newest first
// (received_at DESC, id DESC), plus an opaque nextCursor for fetching the
// next page. nextCursor is "" once the caller has reached the last page
// (fewer rows came back than the effective limit).
func (s *Store) List(ctx context.Context, f Filter) ([]EventSummary, string, error) {
	if f.Cursor != "" {
		if _, _, err := decodeCursor(f.Cursor); err != nil {
			return nil, "", fmt.Errorf("replaystore: list: %w", err)
		}
	}

	limit := clampLimit(f.Limit)
	sql, args := buildListQuery(f, s.schema)

	rows, err := s.ops.Query(ctx, sql, args...)
	if err != nil {
		return nil, "", fmt.Errorf("replaystore: list: %w", err)
	}
	defer rows.Close()

	var summaries []EventSummary
	for rows.Next() {
		var e EventSummary
		var idempotencyKey *string
		if err := rows.Scan(&e.ID, &e.Endpoint, &e.Status, &idempotencyKey, &e.ReceivedAt); err != nil {
			return nil, "", fmt.Errorf("replaystore: list: scan row: %w", err)
		}
		if idempotencyKey != nil {
			e.IdempotencyKey = *idempotencyKey
		}
		summaries = append(summaries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("replaystore: list: %w", err)
	}

	nextCursor := ""
	if len(summaries) == limit {
		last := summaries[len(summaries)-1]
		nextCursor = encodeCursor(last.ReceivedAt, last.ID)
	}
	return summaries, nextCursor, nil
}

// Get fetches one event by id, including its full envelope. A missing row is
// reported as (Event{}, false, nil) -- not an error -- so callers can
// distinguish "not found" from a real query failure.
func (s *Store) Get(ctx context.Context, id string) (Event, bool, error) {
	var e Event
	var idempotencyKey, requestID, traceID *string
	var envelopeRaw []byte

	row := s.ops.QueryRow(ctx, s.getEventSQL, id)
	err := row.Scan(&e.ID, &e.ReceivedAt, &e.Endpoint, &idempotencyKey, &requestID, &traceID, &e.Status, &envelopeRaw)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Event{}, false, nil
		}
		return Event{}, false, fmt.Errorf("replaystore: get event %q: %w", id, err)
	}

	if idempotencyKey != nil {
		e.IdempotencyKey = *idempotencyKey
	}
	if requestID != nil {
		e.RequestID = *requestID
	}
	if traceID != nil {
		e.TraceID = *traceID
	}
	msg, err := unmarshalEnvelope(envelopeRaw)
	if err != nil {
		return Event{}, false, fmt.Errorf("replaystore: unmarshal envelope for event %q: %w", id, err)
	}
	e.Envelope = msg

	return e, true, nil
}

// Deliveries returns the recorded replay_deliveries rows for one event, in a
// stable (destination_id, target) order. This is the SOURCE OF TRUTH for a
// replay's destination list: the stored Envelope.DestinationQueueID/Queue are
// comma-joined aggregate labels (e.g. "billing,activity"), not real
// per-destination ids, so replay MUST fan out over these recorded rows instead.
// An unknown (or never-delivered) event id returns an empty slice and nil
// error, not an error -- callers treat "no deliveries" as a distinct case.
func (s *Store) Deliveries(ctx context.Context, eventID string) ([]Delivery, error) {
	rows, err := s.ops.Query(ctx, s.deliveriesReadSQL, eventID)
	if err != nil {
		return nil, fmt.Errorf("replaystore: deliveries for event %q: %w", eventID, err)
	}
	defer rows.Close()

	var deliveries []Delivery
	for rows.Next() {
		var d Delivery
		if err := rows.Scan(&d.DestinationID, &d.Target, &d.Outcome); err != nil {
			return nil, fmt.Errorf("replaystore: deliveries for event %q: scan row: %w", eventID, err)
		}
		deliveries = append(deliveries, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("replaystore: deliveries for event %q: %w", eventID, err)
	}
	return deliveries, nil
}

// Count reports how many replay_events rows match f (ignoring Limit/Cursor),
// for dry-run bulk replay ("this would touch N events") without paging
// through every row.
func (s *Store) Count(ctx context.Context, f Filter) (int64, error) {
	sql, args := buildCountQuery(f, s.schema)
	var count int64
	if err := s.ops.QueryRow(ctx, sql, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("replaystore: count: %w", err)
	}
	return count, nil
}

// ---------------------------------------------------------------------------
// Retention purge: PurgeExpired, PurgeOverCount.
// ---------------------------------------------------------------------------

// buildPurgeExpiredSQL builds the parameterized DELETE PurgeExpired issues.
// Deleting the replay_events row cascades to replay_deliveries via the
// ON DELETE CASCADE foreign key in schemaDDL, so no separate deliveries
// DELETE is needed.
func buildPurgeExpiredSQL(eventsTable string) string {
	return `DELETE FROM ` + eventsTable + ` WHERE received_at < $1`
}

// buildPurgeOverCountSQL builds the parameterized DELETE PurgeOverCount
// issues: keep the `keep` newest rows (by the same received_at DESC, id DESC
// order List uses) and delete everything older via OFFSET $1 against that
// same ordering.
func buildPurgeOverCountSQL(eventsTable string) string {
	return `DELETE FROM ` + eventsTable + ` WHERE id IN (
    SELECT id FROM ` + eventsTable + ` ORDER BY received_at DESC, id DESC OFFSET $1
)`
}

// PurgeExpired deletes every replay_events row (and, via cascade, its
// replay_deliveries rows) with received_at older than olderThan, returning
// the number of events deleted.
func (s *Store) PurgeExpired(ctx context.Context, olderThan time.Time) (int64, error) {
	tag, err := s.ops.Exec(ctx, s.purgeExpiredSQL, olderThan)
	if err != nil {
		return 0, fmt.Errorf("replaystore: purge expired: %w", err)
	}
	return tag.RowsAffected(), nil
}

// PurgeOverCount deletes the oldest replay_events rows beyond the newest
// `keep`, returning the number of events deleted. keep<=0 is treated as a
// no-op (returns 0, deletes nothing) rather than as "keep zero rows" --
// OFFSET 0 would otherwise delete every row, which is never what a caller
// passing a zero-value/unset keep intends.
func (s *Store) PurgeOverCount(ctx context.Context, keep int64) (int64, error) {
	if keep <= 0 {
		return 0, nil
	}
	tag, err := s.ops.Exec(ctx, s.purgeOverCountSQL, keep)
	if err != nil {
		return 0, fmt.Errorf("replaystore: purge over count: %w", err)
	}
	return tag.RowsAffected(), nil
}

// Ping checks liveness of the underlying connection pool.
func (s *Store) Ping(ctx context.Context) error {
	return s.ops.Ping(ctx)
}

// Close releases the underlying connection pool.
func (s *Store) Close() error {
	s.ops.Close()
	return nil
}
