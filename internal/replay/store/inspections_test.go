package store

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jaysongiroux/hook_way/internal/replaywire"
	"github.com/jaysongiroux/hook_way/internal/uicontract"
)

func testInspection(t *testing.T) NewRecord {
	t.Helper()
	at := testOccurredAt
	payload := []byte(`{"version":1,"body":{"encoding":"utf8","content":"{}","content_type":"application/json"},"headers":{},"variables":{},"method":"POST","path":"/events"}`)
	f := NewRecord{Metadata: replaywire.RecordMetadata{ID: uuid.Must(uuid.NewV7()).String(), Kind: replaywire.KindInspection, Flow: "orders", NodeID: "look", NodeType: "inspect", EventID: "evt", ReceivedAt: at.Add(-time.Second), OccurredAt: at, InboundStatus: 503, NotRetryableReason: replaywire.NotRetryableInspectionOnly, Payload: artifactInfo(payload, 0, "application/json")}, Payload: payload, Actor: "core-capture"}
	if err := f.Metadata.Validate(); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestInspectionStorageLifecycleAndIsolation(t *testing.T) {
	s := newTestStore(t, defaultLimits())
	ctx := t.Context()
	f := testInspection(t)
	if result, err := s.Ingest(ctx, f); err != nil || result != Created {
		t.Fatalf("ingest %v %v", result, err)
	}
	if result, err := s.Ingest(ctx, f); err != nil || result != Duplicate {
		t.Fatalf("duplicate %v %v", result, err)
	}
	d, err := s.Get(ctx, f.Metadata.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !d.HasArtifacts || d.Snapshot != nil || d.OccurredAt.IsZero() || d.DeliveryStatus != "" {
		t.Fatalf("detail: %+v", d)
	}
	payload, err := s.Payload(ctx, f.Metadata.ID, "operator")
	if err != nil || !bytes.Equal(payload.Bytes, f.Payload) {
		t.Fatalf("payload: %v", err)
	}
	page, err := s.List(ctx, Filter{Kind: "failure"})
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("failure list: %+v %v", page, err)
	}
	page, err = s.List(ctx, Filter{Kind: "inspection"})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("inspection list: %+v %v", page, err)
	}
	if _, err := s.RequestRetry(ctx, RetryRequest{RecordID: f.Metadata.ID, Actor: "operator", IdempotencyKey: "retry-inspect"}); !errors.Is(err, ErrNotRetryable) {
		t.Fatalf("retry: %v", err)
	}
	if _, err := s.ResolveUnknown(ctx, ResolveRequest{RecordID: f.Metadata.ID, Actor: "operator", IdempotencyKey: "resolve-inspect", ResolutionInput: uicontract.ResolutionInput{Note: "checked"}}); !errors.Is(err, ErrNotResolvable) {
		t.Fatalf("resolve: %v", err)
	}
	var records, size int64
	if err := s.pool.QueryRow(ctx, `SELECT records,bytes FROM usage`).Scan(&records, &size); err != nil {
		t.Fatal(err)
	}
	if records != 1 || size != int64(len(f.Payload)) {
		t.Fatalf("usage: %d %d", records, size)
	}
	if _, err := s.Dismiss(ctx, f.Metadata.ID, "operator"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Payload(ctx, f.Metadata.ID, "operator"); !errors.Is(err, ErrGone) {
		t.Fatalf("dismissed payload: %v", err)
	}
	if err := s.pool.QueryRow(ctx, `SELECT records,bytes FROM usage`).Scan(&records, &size); err != nil || records != 0 || size != 0 {
		t.Fatalf("dismiss usage: %d %d %v", records, size, err)
	}
}

func TestInspectionPaginationExpiryAndSharedCapacity(t *testing.T) {
	limits := defaultLimits()
	limits.MaxRecords = 2
	s := newTestStore(t, limits)
	ctx := t.Context()
	for range 2 {
		if _, err := s.Ingest(ctx, testInspection(t)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Ingest(ctx, testInspection(t)); !errors.Is(err, ErrCapacity) {
		t.Fatal(err)
	}
	page, err := s.List(ctx, Filter{Kind: "inspection", Limit: 1})
	if err != nil || len(page.Items) != 1 || page.NextCursor == "" {
		t.Fatalf("first page: %+v %v", page, err)
	}
	next, err := s.List(ctx, Filter{Kind: "inspection", Limit: 1, Cursor: page.NextCursor})
	if err != nil || len(next.Items) != 1 || next.Items[0].ID == page.Items[0].ID {
		t.Fatalf("next: %+v %v", next, err)
	}
	s.now = func() time.Time { return testNow.Add(48 * time.Hour) }
	if _, err := s.Payload(ctx, page.Items[0].ID, "operator"); !errors.Is(err, ErrGone) {
		t.Fatal(err)
	}
	swept, err := s.Sweep(ctx, 100, time.Second)
	if err != nil || swept.Records != 2 || swept.Bytes == 0 {
		t.Fatalf("sweep %+v %v", swept, err)
	}
}

func TestInspectionDatabaseDisallowsExecutableAndResolvedState(t *testing.T) {
	s := newTestStore(t, defaultLimits())
	f := testInspection(t)
	ctx := t.Context()
	if _, err := s.Ingest(ctx, f); err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{`UPDATE records SET retryable=true WHERE id=$1`, `UPDATE records SET delivery_status='unknown' WHERE id=$1`, `UPDATE records SET snapshot_sha256='fake' WHERE id=$1`} {
		if _, err := s.pool.Exec(ctx, sql, f.Metadata.ID); err == nil {
			t.Fatalf("unsafe inspection accepted: %s", sql)
		}
	}
}
