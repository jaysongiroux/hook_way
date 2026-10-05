package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jaysongiroux/hook_way/internal/replay/metrics"
	"github.com/jaysongiroux/hook_way/internal/replay/store"
	"github.com/jaysongiroux/hook_way/internal/replayresult"
)

const (
	defaultPollInterval   = time.Second
	defaultExpireInterval = 15 * time.Second
	finalizeTimeout       = 10 * time.Second
)

// Store is what the worker needs from the replay store.
type Store interface {
	ClaimRuns(ctx context.Context, workerID string, n int, lease time.Duration) ([]store.ClaimedRun, error)
	Snapshot(ctx context.Context, failureID string) ([]byte, error)
	FinalizeRun(ctx context.Context, o store.RunOutcome) (store.FinalizeResult, error)
	ExpireLeases(ctx context.Context, actor string) ([]string, error)
}

// Deliverer executes one run against core.
type Deliverer interface {
	Deliver(ctx context.Context, run store.ClaimedRun, snapshot []byte) Outcome
}

// Options configures a Worker.
type Options struct {
	Store          Store
	Deliverer      Deliverer
	Metrics        *metrics.Metrics
	Logger         *slog.Logger
	ID             string
	Concurrency    int
	Lease          time.Duration
	PollInterval   time.Duration // default 1s, jittered
	ExpireInterval time.Duration // default 15s
}

// Worker claims and executes runs on one replica.
type Worker struct {
	o    Options
	busy atomic.Int64
}

func New(o Options) *Worker {
	if o.PollInterval <= 0 {
		o.PollInterval = defaultPollInterval
	}
	if o.ExpireInterval <= 0 {
		o.ExpireInterval = defaultExpireInterval
	}
	if o.Concurrency < 1 {
		o.Concurrency = 1
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	return &Worker{o: o}
}

func (w *Worker) actor() string { return "worker:" + w.o.ID }

// jitter spreads polls across replicas: d ± 20%.
func jitter(d time.Duration) time.Duration {
	return d - d/5 + time.Duration(rand.Int64N(int64(d/5)*2+1)) // #nosec G404 -- poll jitter, not a credential
}

// Run claims and executes runs until ctx ends, then stops claiming and waits
// for in-flight runs. In-flight runs use a context the shutdown does not
// cancel, so each finishes within its own execution deadline.
func (w *Worker) Run(ctx context.Context) {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		w.expireLoop(ctx)
	}()
	slots := make(chan struct{}, w.o.Concurrency)
	for ctx.Err() == nil {
		free := cap(slots) - len(slots)
		claimed := 0
		if free > 0 {
			runs, err := w.o.Store.ClaimRuns(ctx, w.o.ID, free, w.o.Lease)
			if err != nil && ctx.Err() == nil {
				w.o.Logger.Warn("claim retry runs failed", "error", err)
			}
			for _, run := range runs {
				slots <- struct{}{}
				wg.Add(1)
				go func() {
					defer wg.Done()
					defer func() { <-slots }()
					w.process(context.WithoutCancel(ctx), run)
				}()
			}
			claimed = len(runs)
		}
		if claimed > 0 && claimed == free {
			continue // the queue may hold more; claim again as slots free up
		}
		select {
		case <-ctx.Done():
		case <-time.After(jitter(w.o.PollInterval)):
		}
	}
	wg.Wait()
}

func (w *Worker) expireLoop(ctx context.Context) {
	ticker := time.NewTicker(w.o.ExpireInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			expired, err := w.o.Store.ExpireLeases(ctx, w.actor())
			if err != nil {
				if ctx.Err() == nil {
					w.o.Logger.Warn("expire run leases failed", "error", err)
				}
				continue
			}
			if len(expired) > 0 {
				w.o.Metrics.LeasesExpired(len(expired))
				w.o.Logger.Warn("run leases expired; their outcome is unknown", "run_ids", expired)
			}
		}
	}
}

// process executes and finalizes one run. A panic is recovered and the run
// left running, so its lease expires it to unknown: whether core delivered
// is not known.
func (w *Worker) process(ctx context.Context, run store.ClaimedRun) {
	w.o.Metrics.SetWorkersBusy(w.busy.Add(1))
	defer func() { w.o.Metrics.SetWorkersBusy(w.busy.Add(-1)) }()
	defer func() {
		if rec := recover(); rec != nil {
			w.o.Metrics.Panic("worker")
			w.o.Logger.Error("panic recovered in retry worker", "run_id", run.Run.RunID,
				"panic", fmt.Sprint(rec), "stack", string(debug.Stack()))
		}
	}()
	start := time.Now()
	// Snapshot retrieval is part of the run's lease budget. If it stalls,
	// never start a delivery after another replica has expired the lease.
	deliveryCtx := ctx
	if leaseEnd := run.Run.LeaseExpiresAt; leaseEnd != nil {
		var cancel context.CancelFunc
		deliveryCtx, cancel = context.WithDeadline(ctx, leaseEnd.Add(-finalizeTimeout))
		defer cancel()
	}
	out := w.execute(deliveryCtx, run)
	result, err := json.Marshal(replayresult.Result{Reason: out.Reason, Detail: out.Detail, Deliver: out.Result})
	if err != nil {
		result = []byte(`{}`)
	}
	fctx, cancel := context.WithTimeout(ctx, finalizeTimeout)
	defer cancel()
	res, err := w.o.Store.FinalizeRun(fctx, store.RunOutcome{RunID: run.Run.RunID, Status: out.Status, Result: result,
		Children: out.Children, Actor: w.actor()})
	if err != nil {
		w.o.Logger.Error("finalize retry run failed; its lease will expire it to unknown",
			"run_id", run.Run.RunID, "failure_id", run.Failure.ID, "status", out.Status, "error", err)
		return
	}
	if res.Late {
		w.o.Metrics.LateResult()
		w.o.Logger.Warn("retry run finished after its lease expired", "run_id", run.Run.RunID, "status", out.Status)
		return
	}
	w.o.Metrics.RunFinished(out.Status, time.Since(start))
	if n := len(res.ChildIDs); n > 0 {
		w.o.Metrics.ChildrenCreated(n)
	}
	w.o.Logger.Info("retry run finished", "run_id", run.Run.RunID, "failure_id", run.Failure.ID,
		"status", out.Status, "reason", out.Reason, "children", len(res.ChildIDs))
}

func (w *Worker) execute(ctx context.Context, run store.ClaimedRun) Outcome {
	snapshot, err := w.o.Store.Snapshot(ctx, run.Failure.ID)
	if err != nil {
		return Outcome{Status: store.RunFailed, Reason: ReasonSnapshotUnavailable, Detail: detail(err.Error())}
	}
	if err := ctx.Err(); err != nil {
		return Outcome{Status: store.RunFailed, Reason: ReasonSnapshotUnavailable, Detail: detail(err.Error())}
	}
	return w.o.Deliverer.Deliver(ctx, run, snapshot)
}
