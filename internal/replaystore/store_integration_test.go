package replaystore

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jaysongiroux/hook_way/internal/config"
	"github.com/jaysongiroux/hook_way/internal/queue"
	"github.com/jaysongiroux/hook_way/internal/testenv"

	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// startPostgres boots a Postgres container and returns a connection URL. It
// self-skips when Docker is unavailable, mirroring
// internal/queue/postgres_integration_test.go's startPostgres.
func startPostgres(t *testing.T) string {
	t.Helper()
	testenv.SkipIfShort(t)
	ctx := context.Background()
	container, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("hookway"),
		tcpostgres.WithUsername("hookway"),
		tcpostgres.WithPassword("hookway"),
		tcpostgres.BasicWaitStrategies(),
	)
	testenv.RequireContainer(t, err, "postgres")
	t.Cleanup(func() { _ = container.Terminate(ctx) })
	url, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("get postgres connection string: %v", err)
	}
	return url
}

func newTestReplayStore(t *testing.T, url string) *Store {
	t.Helper()
	ctx := context.Background()
	store, err := New(ctx, config.ReplayConfig{
		Enabled:  true,
		Postgres: config.PostgresConfig{URL: url, Schema: "public"},
	}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

// TestReplayStoreCaptureAndGetRoundTrip captures one event and reads it back with a
// raw SQL SELECT (Get() itself is Task 3), asserting status is "pending" and
// the envelope jsonb round-trips to an equal queue.Message.
func TestReplayStoreCaptureAndGetRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	url := startPostgres(t)
	ctx := context.Background()
	store := newTestReplayStore(t, url)

	msg := queue.Message{
		SchemaVersion:      "1.0",
		ID:                 "evt-1111-2222-3333-444444444444",
		Endpoint:           "orders_webhook",
		DestinationQueueID: "warehouse",
		Queue:              "orders",
		ReceivedAt:         time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC),
		Headers:            map[string][]string{"X-Test": {"1"}},
		Body:               map[string]any{"hello": "world"},
	}
	event := Event{
		ID:             "11111111-2222-3333-4444-555555555555",
		Endpoint:       "orders_webhook",
		IdempotencyKey: "idem-1",
		RequestID:      "req-1",
		TraceID:        "trace-1",
		ReceivedAt:     msg.ReceivedAt,
		Envelope:       msg,
	}
	if err := store.Capture(ctx, event); err != nil {
		t.Fatalf("Capture: %v", err)
	}

	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("pgx.Connect: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	var count int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM public.replay_events WHERE id = $1`, event.ID).Scan(&count); err != nil {
		t.Fatalf("count query: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 row, got %d", count)
	}

	var endpoint, status string
	var idempotencyKey, requestID, traceID *string
	var envelopeRaw []byte
	if err := conn.QueryRow(ctx,
		`SELECT endpoint, status, idempotency_key, request_id, trace_id, envelope FROM public.replay_events WHERE id = $1`, event.ID).
		Scan(&endpoint, &status, &idempotencyKey, &requestID, &traceID, &envelopeRaw); err != nil {
		t.Fatalf("row query: %v", err)
	}
	if endpoint != "orders_webhook" {
		t.Fatalf("endpoint = %q", endpoint)
	}
	if status != "pending" {
		t.Fatalf("status = %q, want pending", status)
	}
	if idempotencyKey == nil || *idempotencyKey != "idem-1" {
		t.Fatalf("idempotency_key = %v", idempotencyKey)
	}
	if requestID == nil || *requestID != "req-1" {
		t.Fatalf("request_id = %v", requestID)
	}
	if traceID == nil || *traceID != "trace-1" {
		t.Fatalf("trace_id = %v", traceID)
	}

	var gotMsg queue.Message
	if err := json.Unmarshal(envelopeRaw, &gotMsg); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	if gotMsg.ID != msg.ID || gotMsg.Endpoint != msg.Endpoint || gotMsg.Queue != msg.Queue {
		t.Fatalf("envelope did not round-trip: got=%#v want=%#v", gotMsg, msg)
	}
	if !gotMsg.ReceivedAt.Equal(msg.ReceivedAt) {
		t.Fatalf("envelope.ReceivedAt = %v, want %v", gotMsg.ReceivedAt, msg.ReceivedAt)
	}
	if body, ok := gotMsg.Body.(map[string]any); !ok || body["hello"] != "world" {
		t.Fatalf("envelope.Body did not round-trip: %#v", gotMsg.Body)
	}
}

// TestReplayStoreCaptureIdempotent captures the same event id twice and asserts
// exactly one row remains (ON CONFLICT (id) DO NOTHING).
func TestReplayStoreCaptureIdempotent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	url := startPostgres(t)
	ctx := context.Background()
	store := newTestReplayStore(t, url)

	event := Event{
		ID:         "22222222-3333-4444-5555-666666666666",
		Endpoint:   "orders_webhook",
		ReceivedAt: time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC),
		Envelope:   queue.Message{ID: "evt-2", Endpoint: "orders_webhook"},
	}
	if err := store.Capture(ctx, event); err != nil {
		t.Fatalf("Capture (1st): %v", err)
	}
	// A second capture with the same id but different fields must still be a
	// no-op: the first row's contents are preserved, not overwritten.
	dup := event
	dup.Endpoint = "should_not_apply"
	if err := store.Capture(ctx, dup); err != nil {
		t.Fatalf("Capture (2nd): %v", err)
	}

	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("pgx.Connect: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	var count int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM public.replay_events WHERE id = $1`, event.ID).Scan(&count); err != nil {
		t.Fatalf("count query: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 row after duplicate capture, got %d", count)
	}

	var endpoint string
	if err := conn.QueryRow(ctx, `SELECT endpoint FROM public.replay_events WHERE id = $1`, event.ID).Scan(&endpoint); err != nil {
		t.Fatalf("endpoint query: %v", err)
	}
	if endpoint != "orders_webhook" {
		t.Fatalf("endpoint = %q, want original value preserved (ON CONFLICT DO NOTHING)", endpoint)
	}
}

// TestReplayStoreRecordOutcomeRollsUpStatus captures one event, then records
// deliveries in three scenarios (all delivered, mixed, all failed),
// asserting both the rolled-up replay_events.status and the
// replay_deliveries rows after each call.
func TestReplayStoreRecordOutcomeRollsUpStatus(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	url := startPostgres(t)
	ctx := context.Background()
	store := newTestReplayStore(t, url)

	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("pgx.Connect: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	readStatus := func(t *testing.T, id string) string {
		t.Helper()
		var status string
		if err := conn.QueryRow(ctx, `SELECT status FROM public.replay_events WHERE id = $1`, id).Scan(&status); err != nil {
			t.Fatalf("status query: %v", err)
		}
		return status
	}
	readDeliveryCount := func(t *testing.T, id, outcome string) int {
		t.Helper()
		var count int
		if err := conn.QueryRow(ctx,
			`SELECT count(*) FROM public.replay_deliveries WHERE event_id = $1 AND outcome = $2`, id, outcome).
			Scan(&count); err != nil {
			t.Fatalf("delivery count query: %v", err)
		}
		return count
	}

	t.Run("2 delivered -> delivered", func(t *testing.T) {
		id := "33333333-4444-5555-6666-777777777771"
		if err := store.Capture(ctx, Event{ID: id, Endpoint: "ep", ReceivedAt: time.Now(), Envelope: queue.Message{ID: "e1"}}); err != nil {
			t.Fatalf("Capture: %v", err)
		}
		err := store.RecordOutcome(ctx, id, []Delivery{
			{DestinationID: "d1", Target: "t1", Outcome: "delivered"},
			{DestinationID: "d2", Target: "t2", Outcome: "delivered"},
		})
		if err != nil {
			t.Fatalf("RecordOutcome: %v", err)
		}
		if got := readStatus(t, id); got != "delivered" {
			t.Fatalf("status = %q, want delivered", got)
		}
		if got := readDeliveryCount(t, id, "delivered"); got != 2 {
			t.Fatalf("delivered rows = %d, want 2", got)
		}
	})

	t.Run("1+1 -> partial", func(t *testing.T) {
		id := "33333333-4444-5555-6666-777777777772"
		if err := store.Capture(ctx, Event{ID: id, Endpoint: "ep", ReceivedAt: time.Now(), Envelope: queue.Message{ID: "e2"}}); err != nil {
			t.Fatalf("Capture: %v", err)
		}
		err := store.RecordOutcome(ctx, id, []Delivery{
			{DestinationID: "d1", Target: "t1", Outcome: "delivered"},
			{DestinationID: "d2", Target: "t2", Outcome: "failed"},
		})
		if err != nil {
			t.Fatalf("RecordOutcome: %v", err)
		}
		if got := readStatus(t, id); got != "partial" {
			t.Fatalf("status = %q, want partial", got)
		}
		if got := readDeliveryCount(t, id, "delivered"); got != 1 {
			t.Fatalf("delivered rows = %d, want 1", got)
		}
		if got := readDeliveryCount(t, id, "failed"); got != 1 {
			t.Fatalf("failed rows = %d, want 1", got)
		}
	})

	t.Run("all failed -> failed", func(t *testing.T) {
		id := "33333333-4444-5555-6666-777777777773"
		if err := store.Capture(ctx, Event{ID: id, Endpoint: "ep", ReceivedAt: time.Now(), Envelope: queue.Message{ID: "e3"}}); err != nil {
			t.Fatalf("Capture: %v", err)
		}
		err := store.RecordOutcome(ctx, id, []Delivery{
			{DestinationID: "d1", Target: "t1", Outcome: "failed"},
			{DestinationID: "d2", Target: "t2", Outcome: "failed"},
		})
		if err != nil {
			t.Fatalf("RecordOutcome: %v", err)
		}
		if got := readStatus(t, id); got != "failed" {
			t.Fatalf("status = %q, want failed", got)
		}
		if got := readDeliveryCount(t, id, "failed"); got != 2 {
			t.Fatalf("failed rows = %d, want 2", got)
		}
	})

	t.Run("re-record updates existing delivery row", func(t *testing.T) {
		id := "33333333-4444-5555-6666-777777777774"
		if err := store.Capture(ctx, Event{ID: id, Endpoint: "ep", ReceivedAt: time.Now(), Envelope: queue.Message{ID: "e4"}}); err != nil {
			t.Fatalf("Capture: %v", err)
		}
		if err := store.RecordOutcome(ctx, id, []Delivery{{DestinationID: "d1", Target: "t1", Outcome: "failed"}}); err != nil {
			t.Fatalf("RecordOutcome (1st): %v", err)
		}
		if err := store.RecordOutcome(ctx, id, []Delivery{{DestinationID: "d1", Target: "t1", Outcome: "delivered"}}); err != nil {
			t.Fatalf("RecordOutcome (2nd, retry): %v", err)
		}
		if got := readStatus(t, id); got != "delivered" {
			t.Fatalf("status = %q, want delivered after retry", got)
		}
		var total int
		if err := conn.QueryRow(ctx, `SELECT count(*) FROM public.replay_deliveries WHERE event_id = $1`, id).Scan(&total); err != nil {
			t.Fatalf("total delivery count: %v", err)
		}
		if total != 1 {
			t.Fatalf("expected exactly 1 delivery row after retry upsert, got %d", total)
		}
	})
}

// TestReplayStoreRecordOutcomeInvalidOutcomeWritesNothing is the integration half
// of Finding 1: RecordOutcome with an unrecognized outcome (a typo, e.g.
// "skipped") must return the validation error and write nothing at all --
// not the bad delivery, not any of its valid siblings, and it must not touch
// the event's rolled-up status.
func TestReplayStoreRecordOutcomeInvalidOutcomeWritesNothing(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	url := startPostgres(t)
	ctx := context.Background()
	store := newTestReplayStore(t, url)

	id := "44444444-5555-6666-7777-888888888881"
	if err := store.Capture(ctx, Event{ID: id, Endpoint: "ep", ReceivedAt: time.Now(), Envelope: queue.Message{ID: "e1"}}); err != nil {
		t.Fatalf("Capture: %v", err)
	}

	err := store.RecordOutcome(ctx, id, []Delivery{
		{DestinationID: "d1", Target: "t1", Outcome: "delivered"},
		{DestinationID: "d2", Target: "t2", Outcome: "skipped"}, // invalid
	})
	if err == nil {
		t.Fatalf("RecordOutcome with invalid outcome = nil error, want validation error")
	}
	if !strings.Contains(err.Error(), "invalid delivery outcome") {
		t.Fatalf("error = %q, want it to mention %q", err.Error(), "invalid delivery outcome")
	}

	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("pgx.Connect: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	var deliveryCount int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM public.replay_deliveries WHERE event_id = $1`, id).Scan(&deliveryCount); err != nil {
		t.Fatalf("delivery count query: %v", err)
	}
	if deliveryCount != 0 {
		t.Fatalf("delivery rows = %d, want 0 (nothing should be written, including the valid sibling delivery)", deliveryCount)
	}

	var status string
	if err := conn.QueryRow(ctx, `SELECT status FROM public.replay_events WHERE id = $1`, id).Scan(&status); err != nil {
		t.Fatalf("status query: %v", err)
	}
	if status != "pending" {
		t.Fatalf("status = %q, want unchanged pending", status)
	}
}

// TestReplayStoreRecordOutcomeUnknownEventErrors is Finding 2: RecordOutcome
// against an id that was never Captured must fail on the
// replay_deliveries.event_id foreign key, and must write no delivery row.
func TestReplayStoreRecordOutcomeUnknownEventErrors(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	url := startPostgres(t)
	ctx := context.Background()
	store := newTestReplayStore(t, url)

	unknownID := "99999999-8888-7777-6666-555555555555"
	err := store.RecordOutcome(ctx, unknownID, []Delivery{
		{DestinationID: "d1", Target: "t1", Outcome: OutcomeDelivered},
	})
	if err == nil {
		t.Fatalf("RecordOutcome for a never-captured event id = nil error, want FK violation error")
	}

	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("pgx.Connect: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	var deliveryCount int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM public.replay_deliveries WHERE event_id = $1`, unknownID).Scan(&deliveryCount); err != nil {
		t.Fatalf("delivery count query: %v", err)
	}
	if deliveryCount != 0 {
		t.Fatalf("delivery rows = %d, want 0 (FK violation must write nothing)", deliveryCount)
	}
}

// TestReplayStoreRecordOutcomeEmptyDeliveries is Finding 3: RecordOutcome with a
// nil/empty deliveries slice must succeed as a no-op that leaves the event's
// status at "pending" (rollupStatus(0,0,0) == "pending") and writes no
// delivery rows.
func TestReplayStoreRecordOutcomeEmptyDeliveries(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	url := startPostgres(t)
	ctx := context.Background()
	store := newTestReplayStore(t, url)

	id := "44444444-5555-6666-7777-888888888882"
	if err := store.Capture(ctx, Event{ID: id, Endpoint: "ep", ReceivedAt: time.Now(), Envelope: queue.Message{ID: "e2"}}); err != nil {
		t.Fatalf("Capture: %v", err)
	}

	if err := store.RecordOutcome(ctx, id, nil); err != nil {
		t.Fatalf("RecordOutcome(nil deliveries): %v", err)
	}

	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("pgx.Connect: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	var status string
	if err := conn.QueryRow(ctx, `SELECT status FROM public.replay_events WHERE id = $1`, id).Scan(&status); err != nil {
		t.Fatalf("status query: %v", err)
	}
	if status != "pending" {
		t.Fatalf("status = %q, want pending", status)
	}

	var deliveryCount int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM public.replay_deliveries WHERE event_id = $1`, id).Scan(&deliveryCount); err != nil {
		t.Fatalf("delivery count query: %v", err)
	}
	if deliveryCount != 0 {
		t.Fatalf("delivery rows = %d, want 0", deliveryCount)
	}
}

// TestReplayStoreEnsuresSchema asserts New() on a fresh database creates both
// tables and all five indexes named in the brief's DDL.
func TestReplayStoreEnsuresSchema(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	url := startPostgres(t)
	ctx := context.Background()
	_ = newTestReplayStore(t, url)

	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("pgx.Connect: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	for _, table := range []string{"replay_events", "replay_deliveries"} {
		var exists bool
		if err := conn.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = 'public' AND table_name = $1)`, table).
			Scan(&exists); err != nil {
			t.Fatalf("table existence query for %q: %v", table, err)
		}
		if !exists {
			t.Fatalf("table %q was not created", table)
		}
	}

	wantIndexes := []string{
		"replay_events_endpoint_received_at",
		"replay_events_status_received_at",
		"replay_deliveries_dest_outcome",
	}
	for _, idx := range wantIndexes {
		var exists bool
		if err := conn.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM pg_indexes WHERE schemaname = 'public' AND indexname = $1)`, idx).
			Scan(&exists); err != nil {
			t.Fatalf("index existence query for %q: %v", idx, err)
		}
		if !exists {
			t.Fatalf("index %q was not created", idx)
		}
	}
}

// captureN is a small integration-test helper: it Captures one event with
// the given id/endpoint/receivedAt and a minimal envelope, failing the test
// on error.
func captureN(t *testing.T, ctx context.Context, store *Store, id, endpoint string, receivedAt time.Time) {
	t.Helper()
	if err := store.Capture(ctx, Event{
		ID:         id,
		Endpoint:   endpoint,
		ReceivedAt: receivedAt,
		Envelope:   queue.Message{ID: "evt-" + id, Endpoint: endpoint},
	}); err != nil {
		t.Fatalf("Capture(%s): %v", id, err)
	}
}

// summaryIDs extracts the IDs from a []EventSummary in order, for compact
// order/subset assertions.
func summaryIDs(summaries []EventSummary) []string {
	ids := make([]string, len(summaries))
	for i, s := range summaries {
		ids[i] = s.ID
	}
	return ids
}

// TestReplayStoreListFiltersAndPaginates captures 5 events across 2 endpoints with
// staggered received_at, then asserts: (1) filtering by endpoint returns
// only that endpoint's events in received_at DESC order; (2) a small Limit
// pages through all 5 exactly once each, with no overlap or gap, and the
// cursor goes empty exactly on the last page.
func TestReplayStoreListFiltersAndPaginates(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	url := startPostgres(t)
	ctx := context.Background()
	store := newTestReplayStore(t, url)

	base := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	ids := []string{
		"55555555-0000-0000-0000-000000000001",
		"55555555-0000-0000-0000-000000000002",
		"55555555-0000-0000-0000-000000000003",
		"55555555-0000-0000-0000-000000000004",
		"55555555-0000-0000-0000-000000000005",
	}
	endpoints := []string{"orders_webhook", "orders_webhook", "shipping_webhook", "orders_webhook", "shipping_webhook"}
	for i, id := range ids {
		captureN(t, ctx, store, id, endpoints[i], base.Add(time.Duration(i)*time.Minute))
	}

	// Filter by endpoint: ids[0], ids[1], ids[3] are orders_webhook, with
	// ids[3] the newest (received_at DESC first).
	byEndpoint, cursor, err := store.List(ctx, Filter{Endpoint: "orders_webhook", Limit: 10})
	if err != nil {
		t.Fatalf("List(endpoint filter): %v", err)
	}
	wantOrder := []string{ids[3], ids[1], ids[0]}
	if got := summaryIDs(byEndpoint); !reflect.DeepEqual(got, wantOrder) {
		t.Fatalf("List(endpoint) ids = %v, want %v", got, wantOrder)
	}
	if cursor != "" {
		t.Fatalf("cursor = %q, want empty when all matches fit in one page", cursor)
	}

	// Paginate all 5 events with Limit=2: 3 pages of sizes 2,2,1; no overlap.
	wantAllDesc := []string{ids[4], ids[3], ids[2], ids[1], ids[0]}
	var seen []string
	f := Filter{Limit: 2}
	for page := 0; page < 10; page++ {
		got, next, err := store.List(ctx, f)
		if err != nil {
			t.Fatalf("List(page %d): %v", page, err)
		}
		seen = append(seen, summaryIDs(got)...)
		if next == "" {
			break
		}
		if len(got) != 2 {
			t.Fatalf("page %d: len = %d, want 2 for a non-final page", page, len(got))
		}
		f = Filter{Limit: 2, Cursor: next}
	}
	if !reflect.DeepEqual(seen, wantAllDesc) {
		t.Fatalf("paginated ids = %v, want %v (no gaps/overlaps/reorders)", seen, wantAllDesc)
	}
}

// TestReplayStoreListByFailedStatus captures 3 events, rolls one up to "failed" and
// one to "delivered" via RecordOutcome, leaves one "pending", and asserts
// Status:"failed" returns only the failed event.
func TestReplayStoreListByFailedStatus(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	url := startPostgres(t)
	ctx := context.Background()
	store := newTestReplayStore(t, url)

	base := time.Date(2026, 7, 2, 9, 0, 0, 0, time.UTC)
	pendingID := "55555555-1111-0000-0000-000000000001"
	failedID := "55555555-1111-0000-0000-000000000002"
	deliveredID := "55555555-1111-0000-0000-000000000003"
	captureN(t, ctx, store, pendingID, "ep", base)
	captureN(t, ctx, store, failedID, "ep", base.Add(time.Minute))
	captureN(t, ctx, store, deliveredID, "ep", base.Add(2*time.Minute))

	if err := store.RecordOutcome(ctx, failedID, []Delivery{{DestinationID: "d1", Target: "t1", Outcome: OutcomeFailed}}); err != nil {
		t.Fatalf("RecordOutcome(failed): %v", err)
	}
	if err := store.RecordOutcome(ctx, deliveredID, []Delivery{{DestinationID: "d1", Target: "t1", Outcome: OutcomeDelivered}}); err != nil {
		t.Fatalf("RecordOutcome(delivered): %v", err)
	}

	got, _, err := store.List(ctx, Filter{Status: "failed", Limit: 10})
	if err != nil {
		t.Fatalf("List(status=failed): %v", err)
	}
	if ids := summaryIDs(got); !reflect.DeepEqual(ids, []string{failedID}) {
		t.Fatalf("List(status=failed) ids = %v, want [%s]", ids, failedID)
	}
}

// TestReplayStoreListByDestination records two deliveries (different targets) to
// the same destination for one event, and one delivery to a different
// destination for a second event, then asserts filtering by the first
// destination returns exactly one row for the first event (no duplicate from
// the multi-delivery join) and none of the second event.
func TestReplayStoreListByDestination(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	url := startPostgres(t)
	ctx := context.Background()
	store := newTestReplayStore(t, url)

	base := time.Date(2026, 7, 3, 9, 0, 0, 0, time.UTC)
	eventA := "55555555-2222-0000-0000-000000000001"
	eventB := "55555555-2222-0000-0000-000000000002"
	captureN(t, ctx, store, eventA, "ep", base)
	captureN(t, ctx, store, eventB, "ep", base.Add(time.Minute))

	// eventA gets two deliveries to the SAME destination (different targets):
	// this is the case that would duplicate eventA's row without DISTINCT.
	if err := store.RecordOutcome(ctx, eventA, []Delivery{
		{DestinationID: "dest-1", Target: "https://a.example/hook1", Outcome: OutcomeDelivered},
		{DestinationID: "dest-1", Target: "https://a.example/hook2", Outcome: OutcomeFailed},
	}); err != nil {
		t.Fatalf("RecordOutcome(eventA): %v", err)
	}
	if err := store.RecordOutcome(ctx, eventB, []Delivery{
		{DestinationID: "dest-2", Target: "https://b.example/hook", Outcome: OutcomeDelivered},
	}); err != nil {
		t.Fatalf("RecordOutcome(eventB): %v", err)
	}

	got, _, err := store.List(ctx, Filter{DestinationID: "dest-1", Limit: 10})
	if err != nil {
		t.Fatalf("List(destination=dest-1): %v", err)
	}
	if ids := summaryIDs(got); !reflect.DeepEqual(ids, []string{eventA}) {
		t.Fatalf("List(destination=dest-1) ids = %v, want exactly one row for [%s] (no duplicates)", ids, eventA)
	}
}

// TestReplayStoreDeliveries captures one event, records two deliveries, and asserts
// Deliveries returns exactly those rows in (destination_id, target) order.
// An unknown event id returns an empty slice and nil error (not an error), so
// replay can treat "no recorded deliveries" as a distinct 422 case.
func TestReplayStoreDeliveries(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	url := startPostgres(t)
	ctx := context.Background()
	store := newTestReplayStore(t, url)

	base := time.Date(2026, 7, 5, 9, 0, 0, 0, time.UTC)
	id := "66666666-2222-0000-0000-000000000001"
	captureN(t, ctx, store, id, "ep", base)

	if err := store.RecordOutcome(ctx, id, []Delivery{
		{DestinationID: "billing", Target: "https://b.example/hook", Outcome: OutcomeDelivered},
		{DestinationID: "activity", Target: "orders", Outcome: OutcomeFailed},
	}); err != nil {
		t.Fatalf("RecordOutcome: %v", err)
	}

	got, err := store.Deliveries(ctx, id)
	if err != nil {
		t.Fatalf("Deliveries: %v", err)
	}
	want := []Delivery{
		{DestinationID: "activity", Target: "orders", Outcome: OutcomeFailed},
		{DestinationID: "billing", Target: "https://b.example/hook", Outcome: OutcomeDelivered},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Deliveries = %+v, want %+v", got, want)
	}

	unknown, err := store.Deliveries(ctx, "66666666-2222-0000-0000-000000009999")
	if err != nil {
		t.Fatalf("Deliveries(unknown): unexpected error %v", err)
	}
	if len(unknown) != 0 {
		t.Fatalf("Deliveries(unknown) = %+v, want empty slice", unknown)
	}
}

// TestReplayStoreGetRoundTripAndNotFound captures one event with all optional
// fields set, asserts Get round-trips every field including the envelope,
// and asserts an unknown (but validly formatted) id returns (_, false, nil).
func TestReplayStoreGetRoundTripAndNotFound(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	url := startPostgres(t)
	ctx := context.Background()
	store := newTestReplayStore(t, url)

	id := "55555555-3333-0000-0000-000000000001"
	msg := queue.Message{
		SchemaVersion: "1.0",
		ID:            "evt-get-1",
		Endpoint:      "orders_webhook",
		Queue:         "orders",
		ReceivedAt:    time.Date(2026, 7, 4, 8, 0, 0, 0, time.UTC),
		Body:          map[string]any{"hello": "world"},
	}
	event := Event{
		ID:             id,
		Endpoint:       "orders_webhook",
		IdempotencyKey: "idem-get-1",
		RequestID:      "req-get-1",
		TraceID:        "trace-get-1",
		ReceivedAt:     msg.ReceivedAt,
		Envelope:       msg,
	}
	if err := store.Capture(ctx, event); err != nil {
		t.Fatalf("Capture: %v", err)
	}

	got, ok, err := store.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok {
		t.Fatalf("Get ok = false, want true for a captured event")
	}
	if got.ID != id || got.Endpoint != "orders_webhook" || got.Status != "pending" {
		t.Fatalf("Get = %#v, unexpected core fields", got)
	}
	if got.IdempotencyKey != "idem-get-1" || got.RequestID != "req-get-1" || got.TraceID != "trace-get-1" {
		t.Fatalf("Get = %#v, optional fields did not round-trip", got)
	}
	if !got.ReceivedAt.Equal(msg.ReceivedAt) {
		t.Fatalf("Get.ReceivedAt = %v, want %v", got.ReceivedAt, msg.ReceivedAt)
	}
	if got.Envelope.ID != msg.ID || got.Envelope.Endpoint != msg.Endpoint || got.Envelope.Queue != msg.Queue {
		t.Fatalf("Get.Envelope did not round-trip: got=%#v want=%#v", got.Envelope, msg)
	}
	if body, ok := got.Envelope.Body.(map[string]any); !ok || body["hello"] != "world" {
		t.Fatalf("Get.Envelope.Body did not round-trip: %#v", got.Envelope.Body)
	}

	_, ok2, err2 := store.Get(ctx, "99999999-9999-9999-9999-999999999999")
	if err2 != nil {
		t.Fatalf("Get(unknown id) error = %v, want nil", err2)
	}
	if ok2 {
		t.Fatalf("Get(unknown id) ok = true, want false")
	}
}

// TestReplayStoreCount asserts Count matches the number of events matching a
// filter, and (for dry-run bulk-replay parity) equals the number of rows
// List pages through for the same filter.
func TestReplayStoreCount(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	url := startPostgres(t)
	ctx := context.Background()
	store := newTestReplayStore(t, url)

	base := time.Date(2026, 7, 5, 9, 0, 0, 0, time.UTC)
	captureN(t, ctx, store, "55555555-4444-0000-0000-000000000001", "orders_webhook", base)
	captureN(t, ctx, store, "55555555-4444-0000-0000-000000000002", "orders_webhook", base.Add(time.Minute))
	captureN(t, ctx, store, "55555555-4444-0000-0000-000000000003", "shipping_webhook", base.Add(2*time.Minute))
	captureN(t, ctx, store, "55555555-4444-0000-0000-000000000004", "shipping_webhook", base.Add(3*time.Minute))

	count, err := store.Count(ctx, Filter{Endpoint: "orders_webhook"})
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if count != 2 {
		t.Fatalf("Count = %d, want 2", count)
	}

	summaries, cursor, err := store.List(ctx, Filter{Endpoint: "orders_webhook", Limit: 100})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if cursor != "" {
		t.Fatalf("cursor = %q, want empty (single page)", cursor)
	}
	if int64(len(summaries)) != count {
		t.Fatalf("len(List) = %d, want it to equal Count = %d", len(summaries), count)
	}
}

// TestReplayStorePurgeExpired captures an old and a new event (the old one with a
// recorded delivery), then asserts PurgeExpired(cutoff) deletes only the old
// event and cascades to its delivery row.
func TestReplayStorePurgeExpired(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	url := startPostgres(t)
	ctx := context.Background()
	store := newTestReplayStore(t, url)

	oldID := "55555555-5555-0000-0000-000000000001"
	newID := "55555555-5555-0000-0000-000000000002"
	captureN(t, ctx, store, oldID, "ep", time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
	captureN(t, ctx, store, newID, "ep", time.Date(2026, 7, 6, 0, 0, 0, 0, time.UTC))
	if err := store.RecordOutcome(ctx, oldID, []Delivery{{DestinationID: "d1", Target: "t1", Outcome: OutcomeDelivered}}); err != nil {
		t.Fatalf("RecordOutcome(oldID): %v", err)
	}

	cutoff := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	deleted, err := store.PurgeExpired(ctx, cutoff)
	if err != nil {
		t.Fatalf("PurgeExpired: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("PurgeExpired deleted = %d, want 1", deleted)
	}

	if _, ok, err := store.Get(ctx, oldID); err != nil || ok {
		t.Fatalf("Get(oldID) after purge = (ok=%v, err=%v), want (false, nil)", ok, err)
	}
	if _, ok, err := store.Get(ctx, newID); err != nil || !ok {
		t.Fatalf("Get(newID) after purge = (ok=%v, err=%v), want (true, nil)", ok, err)
	}

	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("pgx.Connect: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	var deliveryCount int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM public.replay_deliveries WHERE event_id = $1`, oldID).Scan(&deliveryCount); err != nil {
		t.Fatalf("delivery count query: %v", err)
	}
	if deliveryCount != 0 {
		t.Fatalf("delivery rows for purged event = %d, want 0 (cascade)", deliveryCount)
	}
}

// TestReplayStorePurgeOverCount inserts 5 events and asserts PurgeOverCount(3)
// leaves exactly the 3 newest, and that PurgeOverCount(0) is a true no-op
// (deletes nothing, never "everything").
func TestReplayStorePurgeOverCount(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	url := startPostgres(t)
	ctx := context.Background()
	store := newTestReplayStore(t, url)

	base := time.Date(2026, 7, 7, 9, 0, 0, 0, time.UTC)
	ids := []string{
		"55555555-6666-0000-0000-000000000001",
		"55555555-6666-0000-0000-000000000002",
		"55555555-6666-0000-0000-000000000003",
		"55555555-6666-0000-0000-000000000004",
		"55555555-6666-0000-0000-000000000005",
	}
	for i, id := range ids {
		captureN(t, ctx, store, id, "ep", base.Add(time.Duration(i)*time.Minute))
	}

	deleted, err := store.PurgeOverCount(ctx, 3)
	if err != nil {
		t.Fatalf("PurgeOverCount(3): %v", err)
	}
	if deleted != 2 {
		t.Fatalf("PurgeOverCount(3) deleted = %d, want 2", deleted)
	}

	remaining, _, err := store.List(ctx, Filter{Limit: 10})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	wantRemaining := []string{ids[4], ids[3], ids[2]}
	if got := summaryIDs(remaining); !reflect.DeepEqual(got, wantRemaining) {
		t.Fatalf("remaining ids = %v, want %v (3 newest)", got, wantRemaining)
	}

	deletedZero, err := store.PurgeOverCount(ctx, 0)
	if err != nil {
		t.Fatalf("PurgeOverCount(0): %v", err)
	}
	if deletedZero != 0 {
		t.Fatalf("PurgeOverCount(0) deleted = %d, want 0 (no-op)", deletedZero)
	}
	count, err := store.Count(ctx, Filter{})
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if count != 3 {
		t.Fatalf("Count after PurgeOverCount(0) = %d, want 3 (unchanged)", count)
	}
}
