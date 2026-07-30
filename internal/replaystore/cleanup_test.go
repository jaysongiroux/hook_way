package replaystore

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// fakePurger is a DB-free purger stub for sweepOnce: it records the
// olderThan/keep arguments each method was called with and returns
// caller-configured counts/errors, so sweepOnce's branching (ttl>0,
// maxEvents>0, both zero, error propagation) is testable with no Postgres.
type fakePurger struct {
	mu sync.Mutex

	purgeExpiredCalled    bool
	purgeExpiredOlderThan time.Time
	purgeExpiredReturn    int64
	purgeExpiredErr       error

	purgeOverCountCalled bool
	purgeOverCountKeep   int64
	purgeOverCountReturn int64
	purgeOverCountErr    error
}

func (f *fakePurger) PurgeExpired(ctx context.Context, olderThan time.Time) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.purgeExpiredCalled = true
	f.purgeExpiredOlderThan = olderThan
	return f.purgeExpiredReturn, f.purgeExpiredErr
}

func (f *fakePurger) PurgeOverCount(ctx context.Context, keep int64) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.purgeOverCountCalled = true
	f.purgeOverCountKeep = keep
	return f.purgeOverCountReturn, f.purgeOverCountErr
}

func (f *fakePurger) snapshot() fakePurger {
	f.mu.Lock()
	defer f.mu.Unlock()
	return fakePurger{
		purgeExpiredCalled:    f.purgeExpiredCalled,
		purgeExpiredOlderThan: f.purgeExpiredOlderThan,
		purgeOverCountCalled:  f.purgeOverCountCalled,
		purgeOverCountKeep:    f.purgeOverCountKeep,
	}
}

func TestSweepPurgesByAge(t *testing.T) {
	p := &fakePurger{purgeExpiredReturn: 7}
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	ttl := time.Hour

	purged, err := sweepOnce(context.Background(), p, now, ttl, 0)
	if err != nil {
		t.Fatalf("sweepOnce returned error: %v", err)
	}
	if purged != 7 {
		t.Fatalf("purged = %d, want 7", purged)
	}
	snap := p.snapshot()
	if !snap.purgeExpiredCalled {
		t.Fatal("expected PurgeExpired to be called")
	}
	wantOlderThan := now.Add(-ttl)
	if !snap.purgeExpiredOlderThan.Equal(wantOlderThan) {
		t.Fatalf("PurgeExpired olderThan = %v, want %v", snap.purgeExpiredOlderThan, wantOlderThan)
	}
	if snap.purgeOverCountCalled {
		t.Fatal("expected PurgeOverCount NOT to be called when maxEvents<=0")
	}
}

func TestSweepPurgesByCountAfterAge(t *testing.T) {
	p := &fakePurger{purgeExpiredReturn: 3, purgeOverCountReturn: 4}
	now := time.Now()

	purged, err := sweepOnce(context.Background(), p, now, time.Hour, 1000)
	if err != nil {
		t.Fatalf("sweepOnce returned error: %v", err)
	}
	if purged != 7 {
		t.Fatalf("purged = %d, want 7 (3+4)", purged)
	}
	snap := p.snapshot()
	if !snap.purgeExpiredCalled {
		t.Fatal("expected PurgeExpired to be called")
	}
	if !snap.purgeOverCountCalled {
		t.Fatal("expected PurgeOverCount to be called")
	}
	if snap.purgeOverCountKeep != 1000 {
		t.Fatalf("PurgeOverCount keep = %d, want 1000", snap.purgeOverCountKeep)
	}
}

func TestSweepTTLZeroSkipsAge(t *testing.T) {
	p := &fakePurger{purgeOverCountReturn: 2}

	purged, err := sweepOnce(context.Background(), p, time.Now(), 0, 500)
	if err != nil {
		t.Fatalf("sweepOnce returned error: %v", err)
	}
	if purged != 2 {
		t.Fatalf("purged = %d, want 2", purged)
	}
	snap := p.snapshot()
	if snap.purgeExpiredCalled {
		t.Fatal("expected PurgeExpired NOT to be called when ttl<=0")
	}
	if !snap.purgeOverCountCalled {
		t.Fatal("expected PurgeOverCount to be called")
	}
}

func TestSweepMaxZeroSkipsCount(t *testing.T) {
	p := &fakePurger{purgeExpiredReturn: 5}

	purged, err := sweepOnce(context.Background(), p, time.Now(), time.Hour, 0)
	if err != nil {
		t.Fatalf("sweepOnce returned error: %v", err)
	}
	if purged != 5 {
		t.Fatalf("purged = %d, want 5", purged)
	}
	snap := p.snapshot()
	if !snap.purgeExpiredCalled {
		t.Fatal("expected PurgeExpired to be called")
	}
	if snap.purgeOverCountCalled {
		t.Fatal("expected PurgeOverCount NOT to be called when maxEvents<=0")
	}
}

func TestSweepBothZeroNoop(t *testing.T) {
	p := &fakePurger{}

	purged, err := sweepOnce(context.Background(), p, time.Now(), 0, 0)
	if err != nil {
		t.Fatalf("sweepOnce returned error: %v", err)
	}
	if purged != 0 {
		t.Fatalf("purged = %d, want 0", purged)
	}
	snap := p.snapshot()
	if snap.purgeExpiredCalled || snap.purgeOverCountCalled {
		t.Fatal("expected neither purge method to be called when ttl and maxEvents are both zero")
	}
}

func TestSweepPropagatesError(t *testing.T) {
	wantErr := errors.New("boom")
	p := &fakePurger{purgeExpiredErr: wantErr}

	_, err := sweepOnce(context.Background(), p, time.Now(), time.Hour, 0)
	if !errors.Is(err, wantErr) {
		t.Fatalf("sweepOnce error = %v, want %v", err, wantErr)
	}
}

// TestSweepBothAttemptedWhenFirstErrors proves sweepOnce's documented
// first-error-wins-but-both-attempted contract: when both ttl>0 and
// maxEvents>0 are eligible and PurgeExpired errors, PurgeOverCount must
// still run (not be short-circuited), the returned error must be the first
// (PurgeExpired's) error, and the returned purged count must include BOTH
// purges' counts. TestSweepPropagatesError alone doesn't cover this because
// it sets maxEvents=0, so PurgeOverCount is never eligible there.
func TestSweepBothAttemptedWhenFirstErrors(t *testing.T) {
	wantErr := errors.New("purge expired boom")
	p := &fakePurger{
		purgeExpiredReturn:   3,
		purgeExpiredErr:      wantErr,
		purgeOverCountReturn: 4,
	}

	purged, err := sweepOnce(context.Background(), p, time.Now(), time.Hour, 1000)
	if !errors.Is(err, wantErr) {
		t.Fatalf("sweepOnce error = %v, want %v", err, wantErr)
	}
	snap := p.snapshot()
	if !snap.purgeExpiredCalled {
		t.Fatal("expected PurgeExpired to be called")
	}
	if !snap.purgeOverCountCalled {
		t.Fatal("expected PurgeOverCount to still be called even though PurgeExpired errored")
	}
	if purged != 7 {
		t.Fatalf("purged = %d, want 7 (3 from PurgeExpired + 4 from PurgeOverCount)", purged)
	}
}

// fakeCleanupMetrics is a DB-free CleanupMetrics stub that records calls so
// StartCleanup's wiring (Ping -> ReplayStoreUp, sweepOnce -> ReplayPurged,
// recover -> Panic) is unit-testable.
type fakeCleanupMetrics struct {
	storeUpCalls int32
	lastStoreUp  atomic.Bool
	purgedCalls  int32
	lastPurged   atomic.Int64
	panicCalls   int32
	lastPanicLoc atomic.Value
}

func (f *fakeCleanupMetrics) ReplayStoreUp(up bool) {
	atomic.AddInt32(&f.storeUpCalls, 1)
	f.lastStoreUp.Store(up)
}

func (f *fakeCleanupMetrics) ReplayPurged(n int) {
	atomic.AddInt32(&f.purgedCalls, 1)
	f.lastPurged.Store(int64(n))
}

func (f *fakeCleanupMetrics) Panic(location string) {
	atomic.AddInt32(&f.panicCalls, 1)
	f.lastPanicLoc.Store(location)
}

// panicOnExecOps is a pgOps stub whose Exec panics unconditionally, used to
// drive *Store.PurgeExpired (which calls s.ops.Exec) into panicking so
// safeSweep's recover block can be exercised without a real Postgres
// connection. Ping succeeds so the panic is isolated to the sweep itself.
type panicOnExecOps struct{ execOnlyOps }

func (panicOnExecOps) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	panic("boom: Exec panicked")
}

// TestSafeSweepRecordsPanicMetric proves a panicking sweep is recovered,
// increments hookway_panics_total{location="replay_cleanup"} via
// CleanupMetrics.Panic, and does not propagate the panic to the caller (so
// the cleanup loop survives it) -- previously the recover block only logged
// the panic, leaving an operator watching the panics metric blind to a
// panicking cleanup loop.
func TestSafeSweepRecordsPanicMetric(t *testing.T) {
	s := &Store{ops: &panicOnExecOps{}, purgeExpiredSQL: "irrelevant", logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	m := &fakeCleanupMetrics{}

	// safeSweep must not panic out of this call.
	s.safeSweep(context.Background(), time.Hour, 0, m)

	if got := atomic.LoadInt32(&m.panicCalls); got != 1 {
		t.Fatalf("Panic call count = %d, want 1", got)
	}
	if loc, _ := m.lastPanicLoc.Load().(string); loc != "replay_cleanup" {
		t.Fatalf("Panic location = %q, want %q", loc, "replay_cleanup")
	}

	// The loop survives: a second sweep on the same Store must run (and
	// panic-recover) again without issue.
	s.safeSweep(context.Background(), time.Hour, 0, m)
	if got := atomic.LoadInt32(&m.panicCalls); got != 2 {
		t.Fatalf("Panic call count after second sweep = %d, want 2", got)
	}
}

// TestStartCleanupExitsOnContextCancel proves the cleanup goroutine's
// lifecycle mirrors the readiness prober's: cancelling the context passed to
// StartCleanup makes the returned done channel close within a bounded wait,
// so no goroutine is leaked across shutdown. Uses execOnlyOps (already
// defined in store_test.go) since it implements the full pgOps surface
// (Exec/Query/QueryRow/Ping/Close) with fast no-op returns.
func TestStartCleanupExitsOnContextCancel(t *testing.T) {
	s := &Store{ops: &execOnlyOps{}, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	m := &fakeCleanupMetrics{}

	ctx, cancel := context.WithCancel(context.Background())
	done := s.StartCleanup(ctx, 0, 0, 5*time.Millisecond, m)

	// Let at least one tick run so we know the goroutine is alive and doing
	// work before we ask it to stop.
	time.Sleep(20 * time.Millisecond)

	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("StartCleanup goroutine did not exit within 2s of context cancellation (leak)")
	}

	if atomic.LoadInt32(&m.storeUpCalls) == 0 {
		t.Fatal("expected ReplayStoreUp to have been called at least once")
	}
}

// TestStartCleanupDefaultsNonPositiveInterval is a defensive check that a
// non-positive interval doesn't spin the ticker in a tight loop (which would
// panic anyway -- time.NewTicker rejects d<=0) and instead falls back to the
// package default, while still exiting cleanly on cancel.
func TestStartCleanupDefaultsNonPositiveInterval(t *testing.T) {
	s := &Store{ops: &execOnlyOps{}, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	m := &fakeCleanupMetrics{}

	ctx, cancel := context.WithCancel(context.Background())
	done := s.StartCleanup(ctx, 0, 0, 0, m)
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("StartCleanup goroutine did not exit within 2s of context cancellation (leak)")
	}
}
