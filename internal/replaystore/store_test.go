package replaystore

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jasongiroux/hookway/internal/queue"
)

func TestRollupStatus(t *testing.T) {
	cases := []struct {
		name      string
		total     int
		delivered int
		failed    int
		want      string
	}{
		{"zero events pending", 0, 0, 0, "pending"},
		{"all delivered", 2, 2, 0, "delivered"},
		{"single delivered", 1, 1, 0, "delivered"},
		{"all failed", 2, 0, 2, "failed"},
		{"single failed", 1, 0, 1, "failed"},
		{"mixed one-one", 2, 1, 1, "partial"},
		{"mixed three total", 3, 2, 1, "partial"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := rollupStatus(tc.total, tc.delivered, tc.failed)
			if got != tc.want {
				t.Fatalf("rollupStatus(%d,%d,%d) = %q, want %q", tc.total, tc.delivered, tc.failed, got, tc.want)
			}
		})
	}
}

// TestCaptureMarshalsEnvelope pins down the marshaling helper Capture uses to
// build the envelope jsonb argument: it must equal json.Marshal(e.Envelope)
// byte-for-byte, and round-trip to an equal queue.Message. This is testable
// without a database, per the brief.
func TestCaptureMarshalsEnvelope(t *testing.T) {
	msg := queue.Message{
		SchemaVersion:      "1.0",
		ID:                 "evt_1",
		IdempotencyKey:     "idem-1",
		RequestID:          "req-1",
		TraceID:            "trace-1",
		Endpoint:           "orders_webhook",
		DestinationQueueID: "warehouse",
		Queue:              "orders",
		ReceivedAt:         time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC),
		Method:             "POST",
		Path:               "/orders",
		Headers:            http.Header{"X-Test": {"1"}},
		Query:              map[string]any{"page": float64(1)},
		Body:               map[string]any{"hello": "world"},
		SourceIP:           "10.0.0.1",
		Auth:               map[string]any{"scheme": "hmac"},
	}
	e := Event{
		ID:         msg.ID,
		Endpoint:   msg.Endpoint,
		ReceivedAt: msg.ReceivedAt,
		Envelope:   msg,
	}

	raw, err := marshalEnvelope(e.Envelope)
	if err != nil {
		t.Fatalf("marshalEnvelope: %v", err)
	}

	want, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("json.Marshal(msg): %v", err)
	}
	if raw != string(want) {
		t.Fatalf("marshalEnvelope output mismatch:\n got=%s\nwant=%s", raw, string(want))
	}

	var got queue.Message
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("json.Unmarshal round-trip: %v", err)
	}
	if !reflect.DeepEqual(got, msg) {
		t.Fatalf("round-trip mismatch:\n got=%#v\nwant=%#v", got, msg)
	}
}

func TestMarshalEnvelopePersistsRawBodyAndOrigHeaders(t *testing.T) {
	rawBody := []byte(`<xml/>`)
	msg := queue.Message{
		ID:          "evt_proxy",
		Endpoint:    "stripe",
		Body:        map[string]any{"ignored": true},
		RawBody:     rawBody,
		OrigHeaders: http.Header{"Stripe-Signature": {"t=1,v1=abc"}, "Content-Type": {"application/xml"}},
	}

	encoded, err := marshalEnvelope(msg)
	if err != nil {
		t.Fatalf("marshalEnvelope: %v", err)
	}

	got, err := unmarshalEnvelope([]byte(encoded))
	if err != nil {
		t.Fatalf("unmarshalEnvelope: %v", err)
	}
	if string(got.RawBody) != string(rawBody) {
		t.Fatalf("RawBody = %q, want %q", got.RawBody, rawBody)
	}
	if got.RawBodyBase64 == nil {
		t.Fatal("RawBodyBase64 was not persisted")
	}
	if got.OrigHeaders.Get("Stripe-Signature") != "t=1,v1=abc" {
		t.Fatalf("OrigHeaders = %#v", got.OrigHeaders)
	}
}

// noExecOps is a pgOps whose Exec fails the test if it is ever called. It is
// used to prove RecordOutcome validates every Delivery.Outcome before it
// issues any SQL -- an invalid outcome must short-circuit with no write
// attempt at all, not just a failed one.
type noExecOps struct {
	t *testing.T
}

func (o *noExecOps) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	o.t.Helper()
	o.t.Fatalf("Exec must not be called when a Delivery.Outcome fails validation; sql=%s", sql)
	return pgconn.CommandTag{}, nil
}

func (o *noExecOps) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return nil, nil
}

func (o *noExecOps) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row { return nil }

func (o *noExecOps) Ping(ctx context.Context) error { return nil }

func (o *noExecOps) Close() {}

// TestRecordOutcomeRejectsInvalidOutcome is the Go-layer guard against
// Finding 1: a typo'd or otherwise unrecognized Delivery.Outcome (anything
// other than OutcomeDelivered/OutcomeFailed) must be rejected before
// RecordOutcome executes any SQL, with an error naming the bad value.
func TestRecordOutcomeRejectsInvalidOutcome(t *testing.T) {
	cases := []struct {
		name    string
		outcome string
	}{
		{"wrong case", "Delivered"},
		{"unknown value", "skipped"},
		{"empty string", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &Store{ops: &noExecOps{t: t}}
			err := s.RecordOutcome(context.Background(), "11111111-2222-3333-4444-555555555555", []Delivery{
				{DestinationID: "d1", Target: "t1", Outcome: tc.outcome},
			})
			if err == nil {
				t.Fatalf("RecordOutcome(%q) = nil error, want validation error", tc.outcome)
			}
			if !strings.Contains(err.Error(), "invalid delivery outcome") {
				t.Fatalf("error = %q, want it to mention %q", err.Error(), "invalid delivery outcome")
			}
			if !strings.Contains(err.Error(), tc.outcome) {
				t.Fatalf("error = %q, want it to name the bad value %q", err.Error(), tc.outcome)
			}
		})
	}
}

// execOnlyOps is a pgOps stub whose Exec always succeeds and records that it
// was called; used to confirm the validation guard lets valid outcomes
// through to the SQL layer.
type execOnlyOps struct {
	execCalled bool
}

func (o *execOnlyOps) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	o.execCalled = true
	return pgconn.CommandTag{}, nil
}

func (o *execOnlyOps) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return nil, nil
}

func (o *execOnlyOps) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row { return nil }

func (o *execOnlyOps) Ping(ctx context.Context) error { return nil }

func (o *execOnlyOps) Close() {}

// TestRecordOutcomeAcceptsValidOutcomes is the GREEN sibling of
// TestRecordOutcomeRejectsInvalidOutcome: the two documented constants must
// always pass validation and reach Exec.
func TestRecordOutcomeAcceptsValidOutcomes(t *testing.T) {
	for _, outcome := range []string{OutcomeDelivered, OutcomeFailed} {
		t.Run(outcome, func(t *testing.T) {
			ops := &execOnlyOps{}
			s := &Store{ops: ops, recordOutcomeSQL: "SELECT 1"}
			err := s.RecordOutcome(context.Background(), "11111111-2222-3333-4444-555555555555", []Delivery{
				{DestinationID: "d1", Target: "t1", Outcome: outcome},
			})
			if err != nil {
				t.Fatalf("RecordOutcome(%q) = %v, want nil", outcome, err)
			}
			if !ops.execCalled {
				t.Fatalf("Exec was not called for a valid outcome %q", outcome)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Read path: buildListQuery / buildCountQuery / cursor codec / clampLimit.
// These are all pure, DB-free unit tests per the brief.
// ---------------------------------------------------------------------------

// TestBuildListQuery_NoFilters: an empty Filter should produce the base
// query (no WHERE at all, since there are no data-value predicates), the
// fixed DESC ordering, and a parameterized LIMIT defaulted to 100. args must
// contain nothing but that limit value.
func TestBuildListQuery_NoFilters(t *testing.T) {
	sql, args := buildListQuery(Filter{}, "public")

	if !strings.Contains(sql, `"public"."replay_events"`) {
		t.Fatalf("sql = %q, want it to reference the schema-qualified events table", sql)
	}
	if strings.Contains(sql, "WHERE") {
		t.Fatalf("sql = %q, want no WHERE clause for an empty filter", sql)
	}
	if !strings.Contains(sql, "ORDER BY e.received_at DESC, e.id DESC") {
		t.Fatalf("sql = %q, want the fixed DESC ordering", sql)
	}
	if !strings.Contains(sql, "LIMIT $1") {
		t.Fatalf("sql = %q, want a parameterized LIMIT", sql)
	}
	if len(args) != 1 || args[0] != 100 {
		t.Fatalf("args = %#v, want exactly [100] (the default limit)", args)
	}
}

// TestBuildListQuery_AllFilters asserts every filter value ends up bound in
// args (never spliced into the SQL string as a literal) and that the SQL
// only ever references those values via $n placeholders.
func TestBuildListQuery_AllFilters(t *testing.T) {
	since := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	until := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	f := Filter{
		Endpoint: "orders_webhook",
		Status:   "failed",
		Since:    since,
		Until:    until,
	}
	sql, args := buildListQuery(f, "public")

	if strings.Contains(sql, "orders_webhook") || strings.Contains(sql, "failed") {
		t.Fatalf("sql = %q, must not contain literal data values", sql)
	}
	if !strings.Contains(sql, "e.endpoint = $") || !strings.Contains(sql, "e.status = $") {
		t.Fatalf("sql = %q, want parameterized endpoint/status predicates", sql)
	}
	if !strings.Contains(sql, "e.received_at >= $") || !strings.Contains(sql, "e.received_at < $") {
		t.Fatalf("sql = %q, want parameterized since/until predicates", sql)
	}

	wantValues := []any{"orders_webhook", "failed", since, until}
	for _, want := range wantValues {
		found := false
		for _, got := range args {
			if got == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("args = %#v, want it to contain %#v", args, want)
		}
	}
}

// TestBuildListQuery_DestinationJoinsDeliveries: setting DestinationID must
// join replay_deliveries and de-duplicate (DISTINCT) so an event with
// multiple matching deliveries is not returned more than once.
func TestBuildListQuery_DestinationJoinsDeliveries(t *testing.T) {
	sql, args := buildListQuery(Filter{DestinationID: "dest-1", Target: "https://example.com/hook"}, "public")

	if !strings.Contains(sql, `JOIN "public"."replay_deliveries" d ON d.event_id = e.id`) {
		t.Fatalf("sql = %q, want a JOIN against replay_deliveries", sql)
	}
	if !strings.Contains(sql, "SELECT DISTINCT") {
		t.Fatalf("sql = %q, want DISTINCT to avoid duplicate event rows from the join", sql)
	}
	if !strings.Contains(sql, "d.destination_id = $") || !strings.Contains(sql, "d.target = $") {
		t.Fatalf("sql = %q, want parameterized destination_id/target predicates", sql)
	}
	if strings.Contains(sql, "dest-1") || strings.Contains(sql, "https://example.com/hook") {
		t.Fatalf("sql = %q, must not contain literal data values", sql)
	}
	foundDest, foundTarget := false, false
	for _, a := range args {
		if a == "dest-1" {
			foundDest = true
		}
		if a == "https://example.com/hook" {
			foundTarget = true
		}
	}
	if !foundDest || !foundTarget {
		t.Fatalf("args = %#v, want it to contain both the destination id and target", args)
	}
}

// TestBuildCountQuery_DestinationDeduplicates asserts Count's query counts
// DISTINCT event ids (not raw rows) when joined against replay_deliveries,
// so an event with N matching deliveries counts once, not N times.
func TestBuildCountQuery_DestinationDeduplicates(t *testing.T) {
	sql, _ := buildCountQuery(Filter{DestinationID: "dest-1"}, "public")
	if !strings.Contains(sql, "count(DISTINCT e.id)") {
		t.Fatalf("sql = %q, want count(DISTINCT e.id) when joined against deliveries", sql)
	}
	if strings.Contains(sql, "ORDER BY") || strings.Contains(sql, "LIMIT") {
		t.Fatalf("sql = %q, Count must not order or limit", sql)
	}
}

// TestBuildCountQuery_NoJoinUsesCountStar asserts the common, unjoined case
// counts all rows directly rather than paying for a DISTINCT it doesn't need.
func TestBuildCountQuery_NoJoinUsesCountStar(t *testing.T) {
	sql, args := buildCountQuery(Filter{Endpoint: "orders_webhook"}, "public")
	if !strings.Contains(sql, "count(*)") {
		t.Fatalf("sql = %q, want count(*) for an unjoined filter", sql)
	}
	if len(args) != 1 || args[0] != "orders_webhook" {
		t.Fatalf("args = %#v, want exactly [\"orders_webhook\"]", args)
	}
}

// TestCursorEncodeDecodeRoundTrip pins down encodeCursor/decodeCursor as
// inverses: encoding then decoding must reproduce the same (time, id).
func TestCursorEncodeDecodeRoundTrip(t *testing.T) {
	ts := time.Date(2026, 7, 15, 9, 30, 0, 123456789, time.UTC)
	id := "11111111-2222-3333-4444-555555555555"

	cursor := encodeCursor(ts, id)
	if cursor == "" {
		t.Fatalf("encodeCursor returned an empty string")
	}

	gotTS, gotID, err := decodeCursor(cursor)
	if err != nil {
		t.Fatalf("decodeCursor: %v", err)
	}
	if !gotTS.Equal(ts) {
		t.Fatalf("decodeCursor time = %v, want %v", gotTS, ts)
	}
	if gotID != id {
		t.Fatalf("decodeCursor id = %q, want %q", gotID, id)
	}
}

// TestDecodeCursorRejectsMalformed asserts decodeCursor returns an error
// (never panics) for input that isn't a validly-encoded cursor.
func TestDecodeCursorRejectsMalformed(t *testing.T) {
	cases := []string{
		"",
		"not-base64!!!",
		base64EncodeRaw("no-pipe-here"),
		base64EncodeRaw("abc|"),
		base64EncodeRaw("|some-id"),
		base64EncodeRaw("not-a-number|some-id"),
	}
	for _, c := range cases {
		t.Run(c, func(t *testing.T) {
			if _, _, err := decodeCursor(c); err == nil {
				t.Fatalf("decodeCursor(%q) = nil error, want an error", c)
			}
		})
	}
}

// base64EncodeRaw mirrors the RawURLEncoding encodeCursor/decodeCursor use,
// so malformed-input test cases can construct otherwise-valid-looking
// base64 payloads with bad inner content.
func base64EncodeRaw(s string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(s))
}

// TestListLimitClamp pins down clampLimit's boundaries: non-positive falls
// back to the default page size; anything over the max is capped, never
// rejected.
func TestListLimitClamp(t *testing.T) {
	cases := []struct {
		name  string
		limit int
		want  int
	}{
		{"zero uses default", 0, 100},
		{"negative uses default", -5, 100},
		{"over max clamps to 500", 501, 500},
		{"way over max clamps to 500", 100000, 500},
		{"in range passes through", 42, 42},
		{"exactly max passes through", 500, 500},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := clampLimit(tc.limit); got != tc.want {
				t.Fatalf("clampLimit(%d) = %d, want %d", tc.limit, got, tc.want)
			}
		})
	}
}

// TestGetMissingReturnsNotFoundNoError exercises Get's not-found contract
// against a pgOps stub whose QueryRow returns pgx.ErrNoRows, without a
// database: Get must return (Event{}, false, nil), not an error.
type noRowsRow struct{}

func (noRowsRow) Scan(dest ...any) error { return pgx.ErrNoRows }

type noRowsOps struct{}

func (o *noRowsOps) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}
func (o *noRowsOps) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return nil, nil
}
func (o *noRowsOps) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return noRowsRow{}
}
func (o *noRowsOps) Ping(ctx context.Context) error { return nil }
func (o *noRowsOps) Close()                         {}

func TestGetMissingReturnsNotFoundNoError(t *testing.T) {
	s := &Store{ops: &noRowsOps{}, getEventSQL: "SELECT 1"}
	e, ok, err := s.Get(context.Background(), "does-not-exist")
	if err != nil {
		t.Fatalf("Get = err %v, want nil error for a missing row", err)
	}
	if ok {
		t.Fatalf("Get ok = true, want false for a missing row")
	}
	if !reflect.DeepEqual(e, Event{}) {
		t.Fatalf("Get event = %#v, want zero value Event{}", e)
	}
}

// TestPurgeOverCountZeroIsNoOp asserts PurgeOverCount(0) (and negative keep)
// never issues a DELETE at all -- it must not be possible for a zero-value
// keep to fall through to "OFFSET 0", which would delete everything.
type failIfExecOps struct {
	t *testing.T
}

func (o *failIfExecOps) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	o.t.Helper()
	o.t.Fatalf("Exec must not be called for a non-positive keep; sql=%s args=%v", sql, args)
	return pgconn.CommandTag{}, nil
}
func (o *failIfExecOps) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return nil, nil
}
func (o *failIfExecOps) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row { return nil }
func (o *failIfExecOps) Ping(ctx context.Context) error                                { return nil }
func (o *failIfExecOps) Close()                                                        {}

func TestPurgeOverCountZeroIsNoOp(t *testing.T) {
	for _, keep := range []int64{0, -1, -100} {
		s := &Store{ops: &failIfExecOps{t: t}, purgeOverCountSQL: "irrelevant"}
		got, err := s.PurgeOverCount(context.Background(), keep)
		if err != nil {
			t.Fatalf("PurgeOverCount(%d) error = %v, want nil", keep, err)
		}
		if got != 0 {
			t.Fatalf("PurgeOverCount(%d) = %d, want 0", keep, got)
		}
	}
}
