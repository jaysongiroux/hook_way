package store

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/jaysongiroux/hook_way/internal/replaywire"
)

var testFailedAt = time.Date(2026, 9, 23, 12, 0, 1, 0, time.UTC)

func artifactInfo(data []byte, version int, contentType string) *replaywire.ArtifactInfo {
	if data == nil {
		return nil
	}
	sum := sha256.Sum256(data)
	return &replaywire.ArtifactInfo{Size: int64(len(data)), SHA256: hex.EncodeToString(sum[:]), Version: version, ContentType: contentType}
}

// testFailure builds a valid resume record; mutate runs before the artifact
// digests are computed, so it may change the artifact bytes.
func testFailure(t *testing.T, mutate func(*NewFailure)) NewFailure {
	t.Helper()
	id := uuid.Must(uuid.NewV7()).String()
	f := NewFailure{
		Metadata: replaywire.FailureMetadata{
			ID: id, Kind: replaywire.KindResume, Flow: "orders", NodeID: "out_s3", NodeType: "publish",
			DestinationID: "archive", DestinationType: "s3", Target: "uploads", RenderedTarget: "uploads",
			EventID: "evt-" + id, RequestID: "req-1", TraceID: "trace-1", IdempotencyKey: "idem-1",
			ReceivedAt: testFailedAt.Add(-time.Second), FailedAt: testFailedAt, InboundStatus: 202,
			ErrorClass: replaywire.ErrorClass5xx, ErrorMessage: "s3: 503 Slow Down", Attempts: 1,
			NodeConfigHash: "node-hash", DestinationHash: "dest-hash", CoreVersion: "v1.2.3", CoreInstance: "core-0",
			Retryable: true,
		},
		Snapshot: []byte("snapshot-bytes-" + id),
		Payload:  []byte(`{"event":"created"}`),
		Actor:    "core-capture",
	}
	if mutate != nil {
		mutate(&f)
	}
	f.Metadata.Snapshot = artifactInfo(f.Snapshot, 1, "")
	f.Metadata.Payload = artifactInfo(f.Payload, 0, "application/json")
	if err := f.Metadata.Validate(); err != nil {
		t.Fatalf("fixture is invalid: %v", err)
	}
	return f
}

func metadataOnly(f *NewFailure) {
	f.Snapshot, f.Payload = nil, nil
	f.Metadata.Retryable, f.Metadata.NotRetryableReason = false, replaywire.NotRetryableTooLarge
}

func mustIngest(t *testing.T, s *Store, f NewFailure) {
	t.Helper()
	if res, err := s.Ingest(context.Background(), f); err != nil || res != Created {
		t.Fatalf("Ingest = %v, %v; want Created", res, err)
	}
}

func usage(t *testing.T, s *Store) (records, bytes int64) {
	t.Helper()
	if err := s.pool.QueryRow(context.Background(), `SELECT records, bytes FROM usage`).Scan(&records, &bytes); err != nil {
		t.Fatal(err)
	}
	return records, bytes
}

func TestIngestStoresARecord(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t, defaultLimits())
	f := testFailure(t, nil)
	mustIngest(t, s, f)
	d, err := s.Get(ctx, f.Metadata.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(d.FailureMetadata, f.Metadata) {
		t.Fatalf("metadata = %+v\nwant       %+v", d.FailureMetadata, f.Metadata)
	}
	if d.Status != StatusOpen || d.RootID != f.Metadata.ID || d.ParentID != "" || !d.HasArtifacts {
		t.Fatalf("status %q root %q parent %q artifacts %v", d.Status, d.RootID, d.ParentID, d.HasArtifacts)
	}
	if !d.CapturedAt.Equal(testNow) || !d.ExpiresAt.Equal(testNow.Add(24*time.Hour)) {
		t.Fatalf("captured %v expires %v", d.CapturedAt, d.ExpiresAt)
	}
	if len(d.History) != 1 || d.History[0].Event != EventCaptured || d.History[0].Actor != "core-capture" {
		t.Fatalf("history = %+v", d.History)
	}
	if d.Runs == nil || len(d.Runs) != 0 || d.Children == nil || len(d.Children) != 0 {
		t.Fatalf("runs %v children %v, want empty non-nil slices", d.Runs, d.Children)
	}
	if r, b := usage(t, s); r != 1 || b != int64(len(f.Snapshot)+len(f.Payload)) {
		t.Fatalf("usage = %d records, %d bytes", r, b)
	}
}

func TestIngestDuplicateLeavesUsageUnchanged(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t, defaultLimits())
	f := testFailure(t, nil)
	mustIngest(t, s, f)
	r0, b0 := usage(t, s)
	if res, err := s.Ingest(ctx, f); err != nil || res != Duplicate {
		t.Fatalf("second Ingest = %v, %v; want Duplicate", res, err)
	}
	if r, b := usage(t, s); r != r0 || b != b0 {
		t.Fatalf("usage changed on a duplicate: %d/%d → %d/%d", r0, b0, r, b)
	}
	if d, _ := s.Get(ctx, f.Metadata.ID); len(d.History) != 1 {
		t.Fatalf("a duplicate appended history: %+v", d.History)
	}
}

func TestIngestConflictingContent(t *testing.T) {
	s := newTestStore(t, defaultLimits())
	f := testFailure(t, nil)
	mustIngest(t, s, f)
	f.Metadata.ErrorMessage = "different"
	if _, err := s.Ingest(context.Background(), f); !errors.Is(err, ErrConflict) {
		t.Fatalf("Ingest = %v, want ErrConflict", err)
	}
}

func TestIngestCapacityRejectsWithoutChangingUsage(t *testing.T) {
	ctx := context.Background()
	first := testFailure(t, nil)
	size := int64(len(first.Snapshot) + len(first.Payload))
	for name, limits := range map[string]Limits{
		"records": {TTL: time.Hour, MaxRecords: 1, MaxBytes: 1 << 30},
		"bytes":   {TTL: time.Hour, MaxRecords: 100, MaxBytes: size + 1},
	} {
		t.Run(name, func(t *testing.T) {
			s := newTestStore(t, limits)
			mustIngest(t, s, testFailure(t, nil))
			r0, b0 := usage(t, s)
			second := testFailure(t, nil)
			if _, err := s.Ingest(ctx, second); !errors.Is(err, ErrCapacity) {
				t.Fatalf("Ingest = %v, want ErrCapacity", err)
			}
			if r, b := usage(t, s); r != r0 || b != b0 {
				t.Fatalf("usage changed on rejection: %d/%d → %d/%d", r0, b0, r, b)
			}
			if _, err := s.Get(ctx, second.Metadata.ID); !errors.Is(err, ErrNotFound) {
				t.Fatalf("rejected record was stored: %v", err)
			}
		})
	}
}

func TestIngestMetadataOnly(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t, defaultLimits())
	f := testFailure(t, metadataOnly)
	mustIngest(t, s, f)
	d, err := s.Get(ctx, f.Metadata.ID)
	if err != nil || d.HasArtifacts || d.Snapshot != nil || d.Payload != nil || d.Retryable {
		t.Fatalf("detail = %+v, %v", d, err)
	}
	if r, b := usage(t, s); r != 1 || b != 0 {
		t.Fatalf("usage = %d/%d, want 1 record and 0 bytes", r, b)
	}
	if _, err := s.Payload(ctx, f.Metadata.ID, "ops"); !errors.Is(err, ErrNoPayload) {
		t.Fatalf("Payload = %v, want ErrNoPayload", err)
	}
}

func TestGetUnknownAndMalformedIDs(t *testing.T) {
	s := newTestStore(t, defaultLimits())
	for _, id := range []string{uuid.Must(uuid.NewV7()).String(), "not-a-uuid", "01926D3C-8D6F-7B3A-9C1E-2F4A5B6C7D8E"} {
		if _, err := s.Get(context.Background(), id); !errors.Is(err, ErrNotFound) {
			t.Errorf("Get(%q) = %v, want ErrNotFound", id, err)
		}
	}
}

func ids(page Page) []string {
	out := make([]string, len(page.Items))
	for i, f := range page.Items {
		out[i] = f.ID
	}
	return out
}

func TestListOrdersAndFilters(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t, defaultLimits())
	var all []NewFailure
	for i, mutate := range []func(*NewFailure){
		func(f *NewFailure) { f.Metadata.Flow = "orders" },
		func(f *NewFailure) {
			f.Metadata.Flow = "payments"
			f.Metadata.NodeID = "out_sqs"
			f.Metadata.DestinationID = "queue"
		},
		func(f *NewFailure) { f.Metadata.Kind = replaywire.KindPublishOnly },
		metadataOnly,
	} {
		f := testFailure(t, func(f *NewFailure) {
			mutate(f)
			f.Metadata.FailedAt = testFailedAt.Add(time.Duration(i) * time.Minute)
		})
		mustIngest(t, s, f)
		all = append(all, f)
	}
	no := false
	cases := map[string]struct {
		filter Filter
		want   []int
	}{
		"all newest first": {Filter{}, []int{3, 2, 1, 0}},
		"flow":             {Filter{Flow: "payments"}, []int{1}},
		"node_id":          {Filter{NodeID: "out_sqs"}, []int{1}},
		"destination_id":   {Filter{DestinationID: "queue"}, []int{1}},
		"kind":             {Filter{Kind: replaywire.KindPublishOnly}, []int{2}},
		"status":           {Filter{Status: StatusOpen}, []int{3, 2, 1, 0}},
		"retryable":        {Filter{Retryable: &no}, []int{3}},
		"since":            {Filter{Since: testFailedAt.Add(2 * time.Minute)}, []int{3, 2}},
		"until":            {Filter{Until: testFailedAt.Add(time.Minute)}, []int{0}},
		"combined":         {Filter{Flow: "orders", Retryable: &no}, []int{3}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			page, err := s.List(ctx, c.filter)
			if err != nil {
				t.Fatal(err)
			}
			want := make([]string, len(c.want))
			for i, idx := range c.want {
				want[i] = all[idx].Metadata.ID
			}
			if got := ids(page); !reflect.DeepEqual(got, want) || page.NextCursor != "" {
				t.Fatalf("ids = %v (cursor %q), want %v", got, page.NextCursor, want)
			}
		})
	}
}

func TestListPaginatesTiesWithoutGapsOrRepeats(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t, defaultLimits())
	want := map[string]bool{}
	for range 7 {
		f := testFailure(t, nil) // every record has the same failed_at
		mustIngest(t, s, f)
		want[f.Metadata.ID] = true
	}
	seen := map[string]bool{}
	var order []string
	cursor := ""
	for pages := 0; ; pages++ {
		if pages > 5 {
			t.Fatal("pagination did not terminate")
		}
		page, err := s.List(ctx, Filter{Limit: 3, Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		for _, id := range ids(page) {
			if seen[id] {
				t.Fatalf("%s returned twice", id)
			}
			seen[id] = true
			order = append(order, id)
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if !reflect.DeepEqual(seen, want) {
		t.Fatalf("paged through %d records, want %d", len(seen), len(want))
	}
	for i := 1; i < len(order); i++ {
		if order[i-1] < order[i] {
			t.Fatalf("ties are not ordered by id descending: %v", order)
		}
	}
}

func TestListRejectsMalformedCursor(t *testing.T) {
	s := newTestStore(t, defaultLimits())
	for _, cursor := range []string{"!!!", "bm8tc2VwYXJhdG9y", "MTIzfG5vdC1hLXV1aWQ"} {
		if _, err := s.List(context.Background(), Filter{Cursor: cursor}); !errors.Is(err, ErrBadCursor) {
			t.Errorf("List(cursor %q) = %v, want ErrBadCursor", cursor, err)
		}
	}
}

func TestCursorRoundTrip(t *testing.T) {
	at := time.Date(2026, 9, 23, 12, 0, 0, 123456000, time.UTC)
	id := uuid.Must(uuid.NewV7()).String()
	gotAt, gotID, err := decodeCursor(encodeCursor(at, id))
	if err != nil || !gotAt.Equal(at) || gotID != id {
		t.Fatalf("decodeCursor = %v %q %v", gotAt, gotID, err)
	}
}

func TestPayloadReturnsTheStoredBytes(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t, defaultLimits())
	f := testFailure(t, nil)
	f.Metadata.Payload.Filename = "order.json"
	mustIngest(t, s, f)
	p, err := s.Payload(ctx, f.Metadata.ID, "ops")
	if err != nil {
		t.Fatal(err)
	}
	if string(p.Bytes) != string(f.Payload) || p.ContentType != "application/json" || p.Filename != "order.json" {
		t.Fatalf("payload = %+v", p)
	}
	d, _ := s.Get(ctx, f.Metadata.ID)
	if last := d.History[len(d.History)-1]; last.Event != EventPayloadDownloaded || last.Actor != "ops" {
		t.Fatalf("history = %+v", d.History)
	}
}

func TestPayloadDetectsCorruption(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t, defaultLimits())
	f := testFailure(t, nil)
	mustIngest(t, s, f)
	if _, err := s.pool.Exec(ctx, `UPDATE failure_artifacts SET payload = 'tampered' WHERE failure_id = $1`, f.Metadata.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Payload(ctx, f.Metadata.ID, "ops"); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("Payload = %v, want ErrCorrupt", err)
	}
	if d, _ := s.Get(ctx, f.Metadata.ID); len(d.History) != 1 {
		t.Fatalf("a corrupt download was logged as a download: %+v", d.History)
	}
}

// A cursor is client input: a timestamp PostgreSQL cannot represent must be
// a malformed cursor, not a query error.
func TestDecodeCursorRejectsOutOfRangeTimes(t *testing.T) {
	id := uuid.Must(uuid.NewV7()).String()
	for _, micros := range []string{"-6307200000000000000", "253402300800000000", "-62135596800000001"} {
		cursor := base64.RawURLEncoding.EncodeToString([]byte(micros + "|" + id))
		if _, _, err := decodeCursor(cursor); !errors.Is(err, ErrBadCursor) {
			t.Errorf("decodeCursor(%s) = %v, want ErrBadCursor", micros, err)
		}
	}
	for _, at := range []time.Time{time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(9999, 12, 31, 23, 59, 59, 999999000, time.UTC)} {
		if _, _, err := decodeCursor(encodeCursor(at, id)); err != nil {
			t.Errorf("decodeCursor(%v) = %v, want it accepted", at, err)
		}
	}
}

// A flipped bit keeps the length, so only the digest can catch it.
func TestPayloadDetectsSameLengthCorruption(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t, defaultLimits())
	f := testFailure(t, nil)
	mustIngest(t, s, f)
	if _, err := s.pool.Exec(ctx, `UPDATE failure_artifacts SET payload = set_byte(payload, 0, get_byte(payload, 0) # 1) WHERE failure_id = $1`, f.Metadata.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Payload(ctx, f.Metadata.ID, "ops"); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("Payload = %v, want ErrCorrupt", err)
	}
}
