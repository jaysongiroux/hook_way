package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/jaysongiroux/hook_way/internal/replay/metrics"
	"github.com/jaysongiroux/hook_way/internal/replay/store"
	"github.com/jaysongiroux/hook_way/internal/replaywire"
)

type fakeStore struct {
	mu        sync.Mutex
	queue     []store.ClaimedRun
	claimErr  error
	snapshots map[string][]byte
	finalized []store.RunOutcome
	ctxErrs   []error
	late      bool
	expire    func() []string
}

func (f *fakeStore) ClaimRuns(_ context.Context, _ string, n int, _ time.Duration) ([]store.ClaimedRun, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.claimErr; err != nil {
		f.claimErr = nil
		return nil, err
	}
	take := min(n, len(f.queue))
	out := f.queue[:take]
	f.queue = f.queue[take:]
	return out, nil
}

func (f *fakeStore) Snapshot(_ context.Context, id string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if data, ok := f.snapshots[id]; ok {
		return data, nil
	}
	return nil, store.ErrGone
}

func (f *fakeStore) FinalizeRun(ctx context.Context, o store.RunOutcome) (store.FinalizeResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.finalized = append(f.finalized, o)
	f.ctxErrs = append(f.ctxErrs, ctx.Err())
	res := store.FinalizeResult{Late: f.late}
	if !f.late {
		for _, c := range o.Children {
			res.ChildIDs = append(res.ChildIDs, c.Metadata.ID)
		}
	}
	return res, nil
}

func (f *fakeStore) ExpireLeases(context.Context, string) ([]string, error) {
	if f.expire == nil {
		return nil, nil
	}
	return f.expire(), nil
}

func (f *fakeStore) finalizedCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.finalized)
}

type deliverFunc func(ctx context.Context, run store.ClaimedRun, snapshot []byte) Outcome

func TestWorkerBlocksInspectionBeforeLoadingSnapshotOrDelivering(t *testing.T) {
	// Nil dependencies make any attempt to load a snapshot or deliver fail.
	w := New(Options{})
	run := store.ClaimedRun{Record: store.Record{RecordMetadata: replaywire.RecordMetadata{Kind: replaywire.KindInspection}}}
	got := w.execute(t.Context(), run)
	if got.Status != store.RunBlocked || got.Reason != replaywire.NotRetryableInspectionOnly {
		t.Fatalf("inspection outcome = %+v", got)
	}
}

func (d deliverFunc) Deliver(ctx context.Context, run store.ClaimedRun, snapshot []byte) Outcome {
	return d(ctx, run, snapshot)
}

type delayedSnapshotStore struct{ *fakeStore }

func (s *delayedSnapshotStore) Snapshot(ctx context.Context, _ string) ([]byte, error) {
	<-ctx.Done()
	// Even if a dependency returns bytes alongside a canceled context,
	// the worker must not dispatch a run after its lease budget is spent.
	return testSnapshot, nil
}

func runsFor(n int) ([]store.ClaimedRun, map[string][]byte) {
	var runs []store.ClaimedRun
	snaps := map[string][]byte{}
	for i := range n {
		r := testRun()
		r.Run.RunID = fmt.Sprintf("01926d3c-9999-7b3a-9c1e-%012x", i)
		r.Record.ID = fmt.Sprintf("01926d3c-8d6f-7b3a-9c1e-%012x", i)
		r.Run.RecordID = r.Record.ID
		runs = append(runs, r)
		snaps[r.Record.ID] = testSnapshot
	}
	return runs, snaps
}

func startWorker(t *testing.T, fs *fakeStore, d Deliverer, m *metrics.Metrics, concurrency int) (cancel func(), done <-chan struct{}) {
	t.Helper()
	w := New(Options{Store: fs, Deliverer: d, Metrics: m, Logger: slog.New(slog.DiscardHandler), ID: "w1",
		Concurrency: concurrency, Lease: time.Minute, PollInterval: 10 * time.Millisecond, ExpireInterval: 10 * time.Millisecond})
	ctx, stop := context.WithCancel(context.Background())
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		w.Run(ctx)
	}()
	t.Cleanup(func() {
		stop()
		<-finished
	})
	return stop, finished
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func succeeded(context.Context, store.ClaimedRun, []byte) Outcome {
	return Outcome{Status: store.RunSucceeded, Call: metrics.CallOK, Result: &replaywire.DeliverResult{CoreVersion: "v1"}}
}

func TestWorkerDeliversAndFinalizes(t *testing.T) {
	runs, snaps := runsFor(1)
	fs := &fakeStore{queue: runs, snapshots: snaps}
	m := metrics.New()
	startWorker(t, fs, deliverFunc(succeeded), m, 2)
	waitFor(t, "finalize", func() bool { return fs.finalizedCount() == 1 })
	o := fs.finalized[0]
	if o.RunID != runs[0].Run.RunID || o.Status != store.RunSucceeded || o.Actor != "worker:w1" {
		t.Fatalf("finalized %+v", o)
	}
	var result map[string]any
	if err := json.Unmarshal(o.Result, &result); err != nil || result["deliver"] == nil {
		t.Fatalf("result %s: %v", o.Result, err)
	}
	waitFor(t, "runs_total", func() bool {
		return metricValue(t, m, "hookway_replay_runs_total", prometheus.Labels{"status": "succeeded"}) == 1
	})
}

func TestWorkerRespectsConcurrency(t *testing.T) {
	runs, snaps := runsFor(5)
	fs := &fakeStore{queue: runs, snapshots: snaps}
	var inflight, peak atomic.Int32
	release := make(chan struct{})
	d := deliverFunc(func(ctx context.Context, run store.ClaimedRun, snap []byte) Outcome {
		n := inflight.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		<-release
		inflight.Add(-1)
		return succeeded(ctx, run, snap)
	})
	startWorker(t, fs, d, metrics.New(), 2)
	waitFor(t, "two runs in flight", func() bool { return inflight.Load() == 2 })
	time.Sleep(50 * time.Millisecond)
	if peak.Load() != 2 {
		t.Fatalf("peak concurrency = %d, want 2", peak.Load())
	}
	close(release)
	waitFor(t, "all runs finalized", func() bool { return fs.finalizedCount() == 5 })
	if peak.Load() != 2 {
		t.Fatalf("peak concurrency = %d, want 2", peak.Load())
	}
}

func TestWorkerFinishesInFlightRunsOnShutdown(t *testing.T) {
	runs, snaps := runsFor(1)
	fs := &fakeStore{queue: runs, snapshots: snaps}
	started, release := make(chan struct{}), make(chan struct{})
	var deliverCtxErr atomic.Value
	d := deliverFunc(func(ctx context.Context, run store.ClaimedRun, snap []byte) Outcome {
		close(started)
		<-release
		deliverCtxErr.Store(fmt.Sprint(ctx.Err()))
		return succeeded(ctx, run, snap)
	})
	cancel, done := startWorker(t, fs, d, metrics.New(), 1)
	<-started
	cancel()
	select {
	case <-done:
		t.Fatal("Run returned while a run was still in flight")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	<-done
	if fs.finalizedCount() != 1 || fs.ctxErrs[0] != nil || deliverCtxErr.Load() != "<nil>" {
		t.Fatalf("finalized %d with ctx err %v; deliver ctx err %v", fs.finalizedCount(), fs.ctxErrs, deliverCtxErr.Load())
	}
}

func TestWorkerStopsClaimingOnShutdown(t *testing.T) {
	fs := &fakeStore{}
	cancel, done := startWorker(t, fs, deliverFunc(succeeded), metrics.New(), 1)
	cancel()
	<-done
	runs, snaps := runsFor(1)
	fs.mu.Lock()
	fs.queue, fs.snapshots = runs, snaps
	fs.mu.Unlock()
	time.Sleep(30 * time.Millisecond)
	if fs.finalizedCount() != 0 {
		t.Fatal("a stopped worker claimed a run")
	}
}

func TestWorkerRecordsLateResults(t *testing.T) {
	runs, snaps := runsFor(1)
	fs := &fakeStore{queue: runs, snapshots: snaps, late: true}
	m := metrics.New()
	startWorker(t, fs, deliverFunc(succeeded), m, 1)
	waitFor(t, "late result", func() bool { return metricValue(t, m, "hookway_replay_late_results_total", nil) == 1 })
	if metricValue(t, m, "hookway_replay_runs_total", prometheus.Labels{"status": "succeeded"}) != 0 {
		t.Fatal("a late result counted as a finished run")
	}
}

func TestWorkerFailsARunWhoseSnapshotIsUnavailable(t *testing.T) {
	runs, _ := runsFor(1)
	fs := &fakeStore{queue: runs, snapshots: map[string][]byte{}}
	var called atomic.Bool
	d := deliverFunc(func(ctx context.Context, run store.ClaimedRun, snap []byte) Outcome {
		called.Store(true)
		return succeeded(ctx, run, snap)
	})
	startWorker(t, fs, d, metrics.New(), 1)
	waitFor(t, "finalize", func() bool { return fs.finalizedCount() == 1 })
	if o := fs.finalized[0]; called.Load() || o.Status != store.RunFailed || !strings.Contains(string(o.Result), ReasonSnapshotUnavailable) {
		t.Fatalf("delivered %v; finalized %+v (%s)", called.Load(), o, o.Result)
	}
}

func TestWorkerDoesNotDeliverAfterLeaseBudgetIsSpent(t *testing.T) {
	run := testRun()
	leaseEnd := time.Now().Add(finalizeTimeout + 25*time.Millisecond)
	run.Run.LeaseExpiresAt = &leaseEnd
	fs := &fakeStore{}
	var called atomic.Bool
	w := New(Options{
		Store: &delayedSnapshotStore{fs},
		Deliverer: deliverFunc(func(context.Context, store.ClaimedRun, []byte) Outcome {
			called.Store(true)
			return Outcome{Status: store.RunSucceeded}
		}),
		Metrics: metrics.New(), Logger: slog.New(slog.DiscardHandler), ID: "w1", Lease: time.Minute,
	})
	w.process(context.Background(), run)
	if called.Load() || fs.finalizedCount() != 1 || fs.finalized[0].Status != store.RunFailed {
		t.Fatalf("delivered %v; finalized %+v", called.Load(), fs.finalized)
	}
}

func TestWorkerCountsChildren(t *testing.T) {
	runs, snaps := runsFor(1)
	fs := &fakeStore{queue: runs, snapshots: snaps}
	m := metrics.New()
	d := deliverFunc(func(ctx context.Context, run store.ClaimedRun, snap []byte) Outcome {
		out := succeeded(ctx, run, snap)
		out.Children = []store.NewRecord{{Metadata: replaywire.RecordMetadata{ID: "c1"}}, {Metadata: replaywire.RecordMetadata{ID: "c2"}}}
		return out
	})
	startWorker(t, fs, d, m, 1)
	waitFor(t, "children counted", func() bool { return metricValue(t, m, "hookway_replay_children_created_total", nil) == 2 })
}

func TestWorkerExpiresLeases(t *testing.T) {
	var calls atomic.Int32
	fs := &fakeStore{expire: func() []string {
		if calls.Add(1) == 1 {
			return []string{"r1", "r2"}
		}
		return nil
	}}
	m := metrics.New()
	startWorker(t, fs, deliverFunc(succeeded), m, 1)
	waitFor(t, "leases expired", func() bool { return metricValue(t, m, "hookway_replay_leases_expired_total", nil) == 2 })
}

func TestWorkerSurvivesClaimErrors(t *testing.T) {
	runs, snaps := runsFor(1)
	fs := &fakeStore{queue: runs, snapshots: snaps, claimErr: errors.New("connection refused")}
	startWorker(t, fs, deliverFunc(succeeded), metrics.New(), 1)
	waitFor(t, "finalize after a claim error", func() bool { return fs.finalizedCount() == 1 })
}

func TestWorkerRecoversPanics(t *testing.T) {
	runs, snaps := runsFor(2)
	fs := &fakeStore{queue: runs, snapshots: snaps}
	m := metrics.New()
	d := deliverFunc(func(ctx context.Context, run store.ClaimedRun, snap []byte) Outcome {
		if run.Run.RunID == runs[0].Run.RunID {
			panic("boom")
		}
		return succeeded(ctx, run, snap)
	})
	startWorker(t, fs, d, m, 1)
	waitFor(t, "the second run", func() bool { return fs.finalizedCount() == 1 })
	if metricValue(t, m, "hookway_replay_panics_total", prometheus.Labels{"location": "worker"}) != 1 {
		t.Fatal("panic not counted")
	}
	if fs.finalized[0].RunID != runs[1].Run.RunID {
		t.Fatal("the panicking run was finalized; its lease must expire it instead")
	}
}
