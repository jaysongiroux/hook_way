// This file implements the replay store's background retention cleanup: a
// periodic sweep that purges events older than replay.ttl and/or beyond
// replay.max_events, wired up by cmd/hookway's runServe alongside the replay
// API listener. The goroutine lifecycle (ticker + immediate first run +
// ctx-cancel + closed done channel + per-tick panic recovery) intentionally
// mirrors internal/httpapi/readiness.go's background prober.
package replaystore

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"
	"time"
)

// defaultCleanupInterval is used by StartCleanup when interval<=0. This is
// defensive only -- config.Load already defaults replay.cleanup_interval to
// 1h -- but keeps StartCleanup safe to call directly (e.g. from a test or a
// future caller) without relying on that upstream default.
const defaultCleanupInterval = time.Hour

// CleanupMetrics is the metrics surface the background cleanup sweep needs:
// a gauge for replay-store reachability, a counter for purged events, and a
// counter for recovered panics. Satisfied by *metrics.Metrics (see
// ReplayStoreUp/ReplayPurged/Panic); declared narrowly here so this package
// stays unit-testable with a fake and has no import-cycle risk with
// internal/metrics.
type CleanupMetrics interface {
	ReplayPurged(n int)
	ReplayStoreUp(up bool)
	Panic(location string)
}

// purger is the small surface sweepOnce needs from *Store, so sweepOnce is
// unit-testable against a fake purger with no Postgres connection.
type purger interface {
	PurgeExpired(ctx context.Context, olderThan time.Time) (int64, error)
	PurgeOverCount(ctx context.Context, keep int64) (int64, error)
}

// sweepOnce runs one retention sweep against p: PurgeExpired(now-ttl) when
// ttl>0, then PurgeOverCount(maxEvents) when maxEvents>0, summing the purged
// counts from whichever of the two ran. ttl<=0 skips the age purge;
// maxEvents<=0 skips the count purge; both<=0 is a no-op that returns
// (0, nil) without issuing any query. Both purges are always attempted
// (a failure in one does not skip the other), and the first error
// encountered is returned alongside the total purged from every call that
// did succeed.
func sweepOnce(ctx context.Context, p purger, now time.Time, ttl time.Duration, maxEvents int64) (int, error) {
	var purged int
	var firstErr error

	if ttl > 0 {
		n, err := p.PurgeExpired(ctx, now.Add(-ttl))
		purged += int(n)
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if maxEvents > 0 {
		n, err := p.PurgeOverCount(ctx, maxEvents)
		purged += int(n)
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return purged, firstErr
}

// StartCleanup spawns a background goroutine that runs sweepOnce every
// interval (immediately on start, then on each tick) until ctx is cancelled.
// Each tick also Pings the store first and records reachability via
// m.ReplayStoreUp, independent of the sweep's own outcome. The returned
// channel is closed when the goroutine exits, so callers (shutdown, leak
// tests) can confirm it stopped -- mirroring readinessProber.run in
// internal/httpapi/readiness.go. interval<=0 defaults to
// defaultCleanupInterval (time.NewTicker panics on a non-positive duration,
// so this guard is required, not just defensive).
func (s *Store) StartCleanup(ctx context.Context, ttl time.Duration, maxEvents int64, interval time.Duration, m CleanupMetrics) (done <-chan struct{}) {
	if interval <= 0 {
		interval = defaultCleanupInterval
	}
	doneCh := make(chan struct{})
	go func() {
		defer close(doneCh)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		s.safeSweep(ctx, ttl, maxEvents, m)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.safeSweep(ctx, ttl, maxEvents, m)
			}
		}
	}()
	return doneCh
}

// safeSweep runs one cleanup tick under a panic guard, so a panicking
// PurgeExpired/PurgeOverCount/Ping implementation cannot take down the
// cleanup goroutine. It Pings the store and records reachability
// unconditionally, then runs sweepOnce and records the purged count on
// success or logs a warning on failure.
func (s *Store) safeSweep(ctx context.Context, ttl time.Duration, maxEvents int64, m CleanupMetrics) {
	defer func() {
		if rec := recover(); rec != nil {
			s.logger.Error("panic recovered in replay cleanup sweep",
				"location", "replay_cleanup",
				"panic", fmt.Sprint(rec),
				"stack", string(debug.Stack()),
			)
			m.Panic("replay_cleanup")
		}
	}()

	pingErr := s.Ping(ctx)
	m.ReplayStoreUp(pingErr == nil)
	if pingErr != nil {
		s.logger.Warn("replay cleanup: store ping failed", slog.Any("error", pingErr))
	}

	purged, err := sweepOnce(ctx, s, time.Now(), ttl, maxEvents)
	if err != nil {
		s.logger.Warn("replay cleanup sweep failed", slog.Any("error", err), slog.Int("purged", purged))
		return
	}
	m.ReplayPurged(purged)
	if purged > 0 {
		s.logger.Info("replay cleanup sweep purged events", slog.Int("purged", purged))
	} else {
		s.logger.Debug("replay cleanup sweep completed", slog.Int("purged", purged))
	}
}
