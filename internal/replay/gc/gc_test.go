package gc

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jaysongiroux/hook_way/internal/replay/metrics"
	"github.com/jaysongiroux/hook_way/internal/replay/store"
)

type sweepFunc func() (store.SweepResult, error)

func (f sweepFunc) Sweep(context.Context, int, time.Duration) (store.SweepResult, error) { return f() }

func counter(t *testing.T, m *metrics.Metrics, name, outcome string) float64 {
	t.Helper()
	families, err := m.Registry().Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range families {
		if f.GetName() != name {
			continue
		}
		for _, s := range f.GetMetric() {
			match := outcome == ""
			for _, l := range s.GetLabel() {
				match = match || (l.GetName() == "outcome" && l.GetValue() == outcome)
			}
			if match {
				if s.GetCounter() != nil {
					return s.GetCounter().GetValue()
				}
				return s.GetGauge().GetValue()
			}
		}
	}
	return 0
}

var quiet = slog.New(slog.DiscardHandler)

func TestOnceRecordsEachOutcome(t *testing.T) {
	m := metrics.New()
	if res, err := Once(context.Background(), sweepFunc(func() (store.SweepResult, error) {
		return store.SweepResult{Records: 3, Bytes: 300}, nil
	}), m, quiet); err != nil || res.Records != 3 {
		t.Fatalf("ok sweep = %+v, %v", res, err)
	}
	if _, err := Once(context.Background(), sweepFunc(func() (store.SweepResult, error) {
		return store.SweepResult{}, store.ErrGCLocked
	}), m, quiet); !errors.Is(err, store.ErrGCLocked) {
		t.Fatalf("locked sweep = %v", err)
	}
	if _, err := Once(context.Background(), sweepFunc(func() (store.SweepResult, error) {
		return store.SweepResult{Records: 1, Bytes: 10}, errors.New("db down")
	}), m, quiet); err == nil {
		t.Fatal("failed sweep reported success")
	}
	for outcome, want := range map[string]float64{metrics.GCOK: 1, metrics.GCSkippedLocked: 1, metrics.GCError: 1} {
		if got := counter(t, m, "hookway_replay_gc_runs_total", outcome); got != want {
			t.Errorf("gc_runs_total{%s} = %v, want %v", outcome, got, want)
		}
	}
	if counter(t, m, "hookway_replay_gc_deleted_records_total", "") != 4 || counter(t, m, "hookway_replay_gc_deleted_bytes_total", "") != 310 {
		t.Fatal("deleted counters do not include partial progress")
	}
}

func TestLoopSweepsUntilCancelled(t *testing.T) {
	var sweeps atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		Loop(ctx, 10*time.Millisecond, sweepFunc(func() (store.SweepResult, error) {
			sweeps.Add(1)
			return store.SweepResult{}, nil
		}), metrics.New(), quiet)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for sweeps.Load() < 3 {
		if time.Now().After(deadline) {
			t.Fatal("Loop did not sweep repeatedly")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Loop did not stop after cancel")
	}
}

func TestLoopWithZeroIntervalIsDisabled(t *testing.T) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		Loop(context.Background(), 0, sweepFunc(func() (store.SweepResult, error) {
			t.Error("a disabled loop swept")
			return store.SweepResult{}, nil
		}), metrics.New(), quiet)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Loop with interval 0 did not return")
	}
}
