package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/jaysongiroux/hook_way/internal/replay/replaytest"
)

func otherConn(t *testing.T, s *Store) *pgx.Conn {
	t.Helper()
	conn, err := pgx.Connect(context.Background(), s.pool.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	return conn
}

func eventCount(t *testing.T, s *Store, id string) int {
	t.Helper()
	var n int
	if err := s.pool.QueryRow(context.Background(), `SELECT count(*) FROM failure_events WHERE failure_id = $1`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestSweepDeletesExpiredRecordsExceptActiveOnes(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t, Limits{TTL: time.Hour, MaxRecords: 100, MaxBytes: 1 << 30})
	open, dismissed, active := testFailure(t, nil), testFailure(t, nil), testFailure(t, nil)
	mustIngest(t, s, open)
	mustIngest(t, s, dismissed)
	if _, err := s.Dismiss(ctx, dismissed.Metadata.ID, "ops"); err != nil {
		t.Fatal(err)
	}
	queueRun(t, s, active)
	s.now = func() time.Time { return testNow.Add(90 * time.Minute) }
	fresh := testFailure(t, nil)
	mustIngest(t, s, fresh)
	s.now = func() time.Time { return testNow.Add(2 * time.Hour) }
	r0, b0 := usage(t, s)

	res, err := s.Sweep(ctx, 500, time.Minute)
	openBytes := int64(len(open.Snapshot) + len(open.Payload))
	if err != nil || res.Records != 2 || res.Bytes != openBytes {
		t.Fatalf("Sweep = %+v, %v; want 2 records and %d bytes (the dismissed record holds no artifacts)", res, err, openBytes)
	}
	for _, f := range []NewFailure{open, dismissed} {
		if _, err := s.Get(ctx, f.Metadata.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("expired record survived: %v", err)
		}
		if eventCount(t, s, f.Metadata.ID) != 0 {
			t.Fatal("history of a deleted record survived")
		}
	}
	for _, f := range []NewFailure{active, fresh} {
		if _, err := s.Get(ctx, f.Metadata.ID); err != nil {
			t.Fatalf("GC deleted a record it must keep: %v", err)
		}
	}
	if r, b := usage(t, s); r != r0-1 || b != b0-openBytes {
		t.Fatalf("usage %d/%d → %d/%d; want only the open record's share removed", r0, b0, r, b)
	}
}

func TestSweepWorksInBatches(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t, Limits{TTL: time.Hour, MaxRecords: 100, MaxBytes: 1 << 30})
	for range 5 {
		mustIngest(t, s, testFailure(t, nil))
	}
	s.now = func() time.Time { return testNow.Add(2 * time.Hour) }
	if res, err := s.Sweep(ctx, 2, time.Minute); err != nil || res.Records != 5 {
		t.Fatalf("Sweep = %+v, %v", res, err)
	}
	if r, b := usage(t, s); r != 0 || b != 0 {
		t.Fatalf("usage = %d/%d", r, b)
	}
}

func TestSweepSkipsWhileAnotherReplicaHoldsTheLock(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t, defaultLimits())
	holder := otherConn(t, s)
	if _, err := holder.Exec(ctx, `SELECT pg_advisory_lock($1)`, gcLockKey); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Sweep(ctx, 500, time.Minute); !errors.Is(err, ErrGCLocked) {
		t.Fatalf("Sweep = %v, want ErrGCLocked", err)
	}
	if _, err := holder.Exec(ctx, `SELECT pg_advisory_unlock($1)`, gcLockKey); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Sweep(ctx, 500, time.Minute); err != nil {
		t.Fatalf("Sweep after unlock = %v", err)
	}
}

func TestSweepReleasesItsLock(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t, defaultLimits())
	if _, err := s.Sweep(ctx, 500, time.Minute); err != nil {
		t.Fatal(err)
	}
	var got bool
	if err := otherConn(t, s).QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, gcLockKey).Scan(&got); err != nil || !got {
		t.Fatalf("lock still held after Sweep: %v, %v", got, err)
	}
}

func TestSweepOrphansChildrenOfADeletedParent(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t, Limits{TTL: time.Hour, MaxRecords: 100, MaxBytes: 1 << 30})
	parent := testFailure(t, nil)
	runID := queueRun(t, s, parent)
	claim(t, s)
	child := childOf(t, parent, nil)
	if _, err := s.FinalizeRun(ctx, RunOutcome{RunID: runID, Status: RunSucceeded, Children: []NewFailure{child}, Actor: "w"}); err != nil {
		t.Fatal(err)
	}
	if _, err := retry(s, child.Metadata.ID, "child-key", false); err != nil { // an active run keeps the child
		t.Fatal(err)
	}
	s.now = func() time.Time { return testNow.Add(2 * time.Hour) }
	if res, err := s.Sweep(ctx, 500, time.Minute); err != nil || res.Records != 1 {
		t.Fatalf("Sweep = %+v, %v", res, err)
	}
	c, err := s.Get(ctx, child.Metadata.ID)
	if err != nil || c.ParentID != "" || c.RootID != parent.Metadata.ID {
		t.Fatalf("child = parent %q root %q, %v", c.ParentID, c.RootID, err)
	}
}

// A run can be queued after the sweep chose its rows but before it locked
// them (RequestRetry committing between the two). The delete must re-check
// with a fresh snapshot and keep the record. session_replication_role skips
// the foreign-key check, which would otherwise wait on the lock GC holds.
func TestSweepRechecksActiveRunsAfterLocking(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t, Limits{TTL: time.Hour, MaxRecords: 100, MaxBytes: 1 << 30})
	f := testFailure(t, nil)
	mustIngest(t, s, f)
	s.now = func() time.Time { return testNow.Add(2 * time.Hour) }
	other := otherConn(t, s)
	s.afterGCLock = func() {
		if _, err := other.Exec(ctx, `SET session_replication_role = replica`); err != nil {
			t.Error(err)
		}
		if _, err := other.Exec(ctx, `INSERT INTO retry_runs (run_id, failure_id, status, requested_by, idempotency_key, request_hash,
			ack_duplicate_risk, created_at) VALUES (gen_random_uuid(), $1, 'queued', 'ops', 'late', 'h', false, now())`, f.Metadata.ID); err != nil {
			t.Error(err)
		}
	}
	res, err := s.Sweep(ctx, 500, time.Minute)
	if err != nil || res.Records != 0 {
		t.Fatalf("Sweep = %+v, %v; want nothing deleted", res, err)
	}
	if _, err := s.Get(ctx, f.Metadata.ID); err != nil {
		t.Fatalf("GC deleted a record whose run was queued while it swept: %v", err)
	}
}

// The advisory lock holds one connection; the batches must not need a second.
func TestSweepWorksWithASingleConnectionPool(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	url := replaytest.Database(t)
	setup := openTestStore(t, url, defaultLimits())
	if _, err := setup.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	setup.now = func() time.Time { return testNow }
	mustIngest(t, setup, testFailure(t, nil))
	s, err := Open(ctx, Options{URL: url, MaxConns: 1, Limits: defaultLimits()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.now = func() time.Time { return testNow.Add(48 * time.Hour) }
	if res, err := s.Sweep(ctx, 500, time.Minute); err != nil || res.Records != 1 {
		t.Fatalf("Sweep with one pooled connection = %+v, %v", res, err)
	}
}
