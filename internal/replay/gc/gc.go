// Package gc deletes expired failure records. Every replica runs the loop;
// the store's advisory lock makes one sweep at a time do the work, and the
// others skip rather than wait.
package gc

import (
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"time"

	"github.com/jaysongiroux/hook_way/internal/logging"
	"github.com/jaysongiroux/hook_way/internal/replay/metrics"
	"github.com/jaysongiroux/hook_way/internal/replay/store"
)

// A sweep deletes up to BatchSize records per transaction and stops after
// about Budget (spec "Retention and GC").
const (
	BatchSize = 500
	Budget    = time.Minute
)

// Sweeper is the store's sweep.
type Sweeper interface {
	Sweep(ctx context.Context, batchSize int, budget time.Duration) (store.SweepResult, error)
}

// Once runs one sweep and records it. store.ErrGCLocked means another
// replica is sweeping; it is returned so callers can report it, and counted
// as skipped_locked, not as an error.
func Once(ctx context.Context, s Sweeper, m *metrics.Metrics, logger *slog.Logger) (store.SweepResult, error) {
	start := time.Now()
	res, err := s.Sweep(ctx, BatchSize, Budget)
	switch {
	case errors.Is(err, store.ErrGCLocked):
		m.GCRun(metrics.GCSkippedLocked, time.Since(start), 0, 0)
		logger.Debug("gc sweep skipped: another replica holds the lock")
	case err != nil:
		m.GCRun(metrics.GCError, time.Since(start), res.Records, res.Bytes)
		logger.Error("gc sweep failed", "error", logging.ErrorText(err), "deleted_records", res.Records)
	default:
		m.GCRun(metrics.GCOK, time.Since(start), res.Records, res.Bytes)
		logger.Info("gc sweep finished", "deleted_records", res.Records, "freed_bytes", res.Bytes)
	}
	return res, err
}

// Loop sweeps every interval (±10%) until ctx ends. An interval of 0
// disables it, for platforms that run hookway-replay gc on a schedule.
func Loop(ctx context.Context, interval time.Duration, s Sweeper, m *metrics.Metrics, logger *slog.Logger) {
	if interval <= 0 {
		return
	}
	for {
		wait := interval - interval/10 + time.Duration(rand.Int64N(int64(interval/10)*2+1)) // #nosec G404 -- poll jitter, not a credential
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		_, _ = Once(ctx, s, m, logger)
	}
}
