package store

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
)

func setStatus(t *testing.T, s *Store, id, status string) {
	t.Helper()
	if _, err := s.pool.Exec(context.Background(), `UPDATE failures SET status = $2 WHERE id = $1`, id, status); err != nil {
		t.Fatal(err)
	}
}

func retry(s *Store, id, key string, ack bool) (RetryResult, error) {
	return s.RequestRetry(context.Background(), RetryRequest{FailureID: id, Actor: "ops", IdempotencyKey: key, AcknowledgeDuplicateRisk: ack})
}

func TestRequestRetryQueuesARun(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t, defaultLimits())
	f := testFailure(t, nil)
	mustIngest(t, s, f)
	res, err := retry(s, f.Metadata.ID, "key-1", false)
	if err != nil || !res.Created || !validID(res.RunID) {
		t.Fatalf("RequestRetry = %+v, %v", res, err)
	}
	d, _ := s.Get(ctx, f.Metadata.ID)
	if d.Status != StatusRetrying || len(d.Runs) != 1 || d.Runs[0].Status != RunQueued || d.Runs[0].RequestedBy != "ops" {
		t.Fatalf("detail = status %q runs %+v", d.Status, d.Runs)
	}
	if last := d.History[len(d.History)-1]; last.Event != EventRunRequested || last.Actor != "ops" {
		t.Fatalf("history = %+v", d.History)
	}
	run, err := s.GetRun(ctx, res.RunID)
	if err != nil || run.FailureID != f.Metadata.ID || run.IdempotencyKey != "key-1" || !run.CreatedAt.Equal(testNow) || run.Result != nil {
		t.Fatalf("GetRun = %+v, %v", run, err)
	}
}

func TestRequestRetryIsIdempotentPerKey(t *testing.T) {
	s := newTestStore(t, defaultLimits())
	f := testFailure(t, nil)
	mustIngest(t, s, f)
	first, _ := retry(s, f.Metadata.ID, "key-1", false)
	again, err := retry(s, f.Metadata.ID, "key-1", false)
	if err != nil || again.Created || again.RunID != first.RunID {
		t.Fatalf("repeat = %+v, %v; want the first run, not created", again, err)
	}
}

func TestRequestRetryRejectsAReusedKeyWithADifferentRequest(t *testing.T) {
	s := newTestStore(t, defaultLimits())
	a, b := testFailure(t, nil), testFailure(t, nil)
	mustIngest(t, s, a)
	mustIngest(t, s, b)
	if _, err := retry(s, a.Metadata.ID, "key-1", false); err != nil {
		t.Fatal(err)
	}
	if _, err := retry(s, a.Metadata.ID, "key-1", true); !errors.Is(err, ErrIdempotencyKeyMismatch) {
		t.Fatalf("different ack = %v, want ErrIdempotencyKeyMismatch", err)
	}
	if _, err := retry(s, b.Metadata.ID, "key-1", false); !errors.Is(err, ErrIdempotencyKeyMismatch) {
		t.Fatalf("different record = %v, want ErrIdempotencyKeyMismatch", err)
	}
}

func TestRequestRetryStateRules(t *testing.T) {
	cases := map[string]struct {
		prepare func(t *testing.T, s *Store, id string)
		ack     bool
		want    error
	}{
		"metadata only": {nil, false, ErrNotRetryable},
		"resolved":      {func(t *testing.T, s *Store, id string) { setStatus(t, s, id, StatusResolved) }, false, ErrNotRetryable},
		"dismissed":     {func(t *testing.T, s *Store, id string) { setStatus(t, s, id, StatusDismissed) }, false, ErrNotRetryable},
		"unknown":       {func(t *testing.T, s *Store, id string) { setStatus(t, s, id, StatusUnknown) }, false, ErrDuplicateRiskUnacknowledged},
		"unknown acked": {func(t *testing.T, s *Store, id string) { setStatus(t, s, id, StatusUnknown) }, true, nil},
		"active run": {func(t *testing.T, s *Store, id string) {
			if _, err := retry(s, id, "earlier", false); err != nil {
				t.Fatal(err)
			}
		}, false, ErrRunActive},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			s := newTestStore(t, defaultLimits())
			var f NewFailure
			if name == "metadata only" {
				f = testFailure(t, metadataOnly)
			} else {
				f = testFailure(t, nil)
			}
			mustIngest(t, s, f)
			if c.prepare != nil {
				c.prepare(t, s, f.Metadata.ID)
			}
			res, err := retry(s, f.Metadata.ID, "key-1", c.ack)
			if !errors.Is(err, c.want) || (c.want == nil && !res.Created) {
				t.Fatalf("RequestRetry = %+v, %v; want %v", res, err, c.want)
			}
		})
	}
	s := newTestStore(t, defaultLimits())
	if _, err := retry(s, uuid.Must(uuid.NewV7()).String(), "key-1", false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown record = %v, want ErrNotFound", err)
	}
}

func TestRequestRetryConcurrentKeysCreateOneRun(t *testing.T) {
	s := newTestStore(t, defaultLimits())
	f := testFailure(t, nil)
	mustIngest(t, s, f)
	results := make([]error, 8)
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, results[i] = retry(s, f.Metadata.ID, uuid.NewString(), false)
		}()
	}
	wg.Wait()
	created := 0
	for _, err := range results {
		switch {
		case err == nil:
			created++
		case !errors.Is(err, ErrRunActive):
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if created != 1 {
		t.Fatalf("%d runs created, want exactly 1", created)
	}
}

func TestRequestRetrySameKeyConcurrentlyReturnsOneRun(t *testing.T) {
	s := newTestStore(t, defaultLimits())
	f := testFailure(t, nil)
	mustIngest(t, s, f)
	results := make([]RetryResult, 8)
	errs := make([]error, 8)
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i], errs[i] = retry(s, f.Metadata.ID, "shared-key", false)
		}()
	}
	wg.Wait()
	created := 0
	for i, err := range errs {
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		if results[i].RunID != results[0].RunID {
			t.Fatalf("run ids differ: %v", results)
		}
		if results[i].Created {
			created++
		}
	}
	if created != 1 {
		t.Fatalf("%d requests report Created, want exactly 1", created)
	}
}

func TestGetRunUnknown(t *testing.T) {
	s := newTestStore(t, defaultLimits())
	for _, id := range []string{uuid.Must(uuid.NewV7()).String(), "nope"} {
		if _, err := s.GetRun(context.Background(), id); !errors.Is(err, ErrNotFound) {
			t.Errorf("GetRun(%q) = %v, want ErrNotFound", id, err)
		}
	}
}

func TestDismissFreesCapacity(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t, Limits{TTL: defaultLimits().TTL, MaxRecords: 2, MaxBytes: 1 << 30})
	a, b := testFailure(t, nil), testFailure(t, nil)
	mustIngest(t, s, a)
	mustIngest(t, s, b)
	if _, err := s.Ingest(ctx, testFailure(t, nil)); !errors.Is(err, ErrCapacity) {
		t.Fatalf("third Ingest = %v, want ErrCapacity", err)
	}
	_, bytesBefore := usage(t, s)
	f, err := s.Dismiss(ctx, a.Metadata.ID, "ops")
	if err != nil || f.Status != StatusDismissed || f.HasArtifacts {
		t.Fatalf("Dismiss = %+v, %v", f, err)
	}
	if r, bytes := usage(t, s); r != 1 || bytes != bytesBefore-int64(len(a.Snapshot)+len(a.Payload)) {
		t.Fatalf("usage after dismiss = %d/%d", r, bytes)
	}
	if _, err := s.Payload(ctx, a.Metadata.ID, "ops"); !errors.Is(err, ErrGone) {
		t.Fatalf("Payload after dismiss = %v, want ErrGone", err)
	}
	d, _ := s.Get(ctx, a.Metadata.ID)
	if last := d.History[len(d.History)-1]; last.Event != EventDismissed || last.Actor != "ops" {
		t.Fatalf("history = %+v", d.History)
	}
	mustIngest(t, s, testFailure(t, nil))
}

func TestDismissRejectsOtherStates(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t, defaultLimits())
	f := testFailure(t, nil)
	mustIngest(t, s, f)
	if _, err := retry(s, f.Metadata.ID, "key-1", false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Dismiss(ctx, f.Metadata.ID, "ops"); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("Dismiss(retrying) = %v, want ErrInvalidState", err)
	}
	g := testFailure(t, metadataOnly)
	mustIngest(t, s, g)
	if _, err := s.Dismiss(ctx, g.Metadata.ID, "ops"); err != nil {
		t.Fatalf("Dismiss(metadata-only) = %v", err)
	}
	if _, err := s.Dismiss(ctx, g.Metadata.ID, "ops"); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("second Dismiss = %v, want ErrInvalidState", err)
	}
	if _, err := s.Dismiss(ctx, uuid.Must(uuid.NewV7()).String(), "ops"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Dismiss(unknown) = %v, want ErrNotFound", err)
	}
}

func TestStatsCountsStatusesAndUsage(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t, defaultLimits())
	a, b := testFailure(t, nil), testFailure(t, nil)
	mustIngest(t, s, a)
	mustIngest(t, s, b)
	if _, err := retry(s, b.Metadata.ID, "key-1", false); err != nil {
		t.Fatal(err)
	}
	st, err := s.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.RecordsByStatus[StatusOpen] != 1 || st.RecordsByStatus[StatusRetrying] != 1 || st.UsageRecords != 2 ||
		st.UsageBytes != int64(len(a.Snapshot)+len(a.Payload)+len(b.Snapshot)+len(b.Payload)) || st.SchemaVersion != LatestSchemaVersion() {
		t.Fatalf("stats = %+v", st)
	}
}
