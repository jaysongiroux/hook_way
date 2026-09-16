package httpapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sort"
	"sync/atomic"
	"time"

	"github.com/jasongiroux/hookway/internal/metrics"
	"github.com/jasongiroux/hookway/internal/queue"
)

// Default readiness prober cadence, applied when the configured interval or
// timeout is non-positive (e.g. a Config built directly in tests without the
// server.readiness_* fields). Config load already supplies these defaults for
// real deployments via parsePositiveDefaultDuration.
const (
	defaultReadinessInterval = 10 * time.Second
	defaultReadinessTimeout  = 5 * time.Second
)

// stalenessFactor multiplies the probe interval to decide when a cached
// readiness result is too old to trust. A result older than
// stalenessFactor×interval means the prober is wedged or has not run recently,
// which is treated as not-ready so a stuck pod fails its readiness probe rather
// than serving a stale "ready".
const stalenessFactor = 3

// errNotProbed is the cached-readiness error before the prober's first probe
// has completed. The initial state is deliberately not-ready: a pod must prove
// its destinations are reachable at least once before receiving traffic.
var errNotProbed = errors.New("readiness not yet probed")

// readinessResult is the cached outcome of the most recent readiness probe.
type readinessResult struct {
	err       error
	dest      map[string]error
	replayErr error
	checkedAt time.Time
}

// destWildcard is stored when the publisher does not expose per-id readiness,
// so every flow dest maps to the single aggregate Ready() result.
const destWildcard = "*"

// readinessByID is satisfied by *queue.RoutingPublisher. When the publisher
// implements it, the prober collects per-destination readiness so it can drive
// the hookway_destination_ready gauge; otherwise it falls back to the aggregate
// Publisher.Ready (e.g. a single-destination or test publisher).
type readinessByID interface {
	ReadyByID(ctx context.Context) map[string]error
}

// readinessProber periodically probes destination readiness in the background
// and caches the aggregate result so per-request readiness checks (/ready,
// /readyz) are served from memory rather than triggering a live destination
// fan-out on every kubelet probe.
type readinessProber struct {
	publisher queue.Publisher
	metrics   *metrics.Metrics
	logger    *slog.Logger
	interval  time.Duration
	timeout   time.Duration
	result    atomic.Pointer[readinessResult]
	// replayPing, when set, is included in every probe so a replay-store
	// outage takes the pod out of rotation (AND-joined with destinations).
	// Capture itself stays best-effort; this only affects /ready.
	replayPing func(context.Context) error

	// staleLoggedAt records the checkedAt of the cached result that most
	// recently triggered the "reported unhealthy due to staleness" warning
	// (see status). A stale result's checkedAt never changes until a new
	// probe replaces it, so comparing against this lets status log once per
	// staleness episode instead of once per /ready request while the pod
	// stays stuck on the same wedged result.
	staleLoggedAt atomic.Pointer[time.Time]
}

func newReadinessProber(publisher queue.Publisher, appMetrics *metrics.Metrics, logger *slog.Logger, interval, timeout time.Duration) *readinessProber {
	if interval <= 0 {
		interval = defaultReadinessInterval
	}
	if timeout <= 0 {
		timeout = defaultReadinessTimeout
	}
	return &readinessProber{
		publisher: publisher,
		metrics:   appMetrics,
		logger:    logger,
		interval:  interval,
		timeout:   timeout,
	}
}

// status reports the cached readiness error and whether the pod should be
// considered ready as of now. A missing (never-probed) or stale result is
// not-ready even if the last recorded error was nil.
//
// A stale result (the prober is wedged or has never completed a probe
// recently enough) is logged as a distinct Warn, separate from a per-probe
// destination failure logged by probeOnce, so an operator looking at a /ready
// 503 can tell "the prober itself is stuck" apart from "a destination is
// down." It's logged at most once per staleness episode (i.e. once per
// distinct cached result's checkedAt) rather than once per /ready request,
// since kubelet polls /ready frequently and the underlying cached result does
// not change again until the next successful probe.
func (p *readinessProber) setReplayPing(ping func(context.Context) error) {
	p.replayPing = ping
}

func (p *readinessProber) destinationSnapshot(now time.Time) (dest map[string]error, replayErr error, ready bool, err error) {
	res := p.result.Load()
	if res == nil {
		return nil, nil, false, errNotProbed
	}
	if age := now.Sub(res.checkedAt); age > stalenessFactor*p.interval {
		p.logStaleOnce(res.checkedAt, age)
		return res.dest, res.replayErr, false, fmt.Errorf("readiness result is stale (last probed %s ago)", age.Round(time.Second))
	}
	return res.dest, res.replayErr, true, res.err
}

// logStaleOnce logs the staleness warning the first time status observes a
// given cached result's checkedAt as stale, and suppresses repeats for that
// same result on subsequent calls (e.g. repeated /ready polls before the next
// probe runs).
func (p *readinessProber) logStaleOnce(checkedAt time.Time, age time.Duration) {
	if last := p.staleLoggedAt.Load(); last != nil && last.Equal(checkedAt) {
		return
	}
	p.staleLoggedAt.Store(&checkedAt)
	p.logger.Warn("readiness reported unhealthy due to stale probe result, not a destination failure",
		"last_probed_at", checkedAt, "age", age.Round(time.Second))
}

// probeOnce runs a single readiness probe with the configured per-probe
// timeout, updates the per-destination gauge (when the publisher exposes
// per-id readiness), and stores the aggregate result in the cache.
func (p *readinessProber) probeOnce(ctx context.Context) {
	probeCtx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	var aggErr error
	dest := map[string]error{}
	if byID, ok := p.publisher.(readinessByID); ok {
		byIDResults := byID.ReadyByID(probeCtx)
		ids := make([]string, 0, len(byIDResults))
		for id := range byIDResults {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		var errs []error
		for _, id := range ids {
			err := byIDResults[id]
			dest[id] = err
			p.metrics.DestinationReady(id, err == nil)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", id, err))
			}
		}
		aggErr = errors.Join(errs...)
	} else {
		aggErr = p.publisher.Ready(probeCtx)
		dest[destWildcard] = aggErr
	}

	var replayErr error
	if p.replayPing != nil {
		if err := p.replayPing(probeCtx); err != nil {
			p.metrics.ReplayStoreUp(false)
			replayErr = err
			aggErr = errors.Join(aggErr, fmt.Errorf("replay_store: %w", err))
		} else {
			p.metrics.ReplayStoreUp(true)
		}
	}

	if aggErr != nil {
		p.logger.Error("readiness probe failed", "error", aggErr.Error())
	}
	p.result.Store(&readinessResult{err: aggErr, dest: dest, replayErr: replayErr, checkedAt: time.Now()})
}

// run kicks an immediate probe, then re-probes every interval until ctx is
// cancelled. The returned channel is closed when the goroutine exits, so
// callers (shutdown, leak tests) can confirm it stopped. The loop is
// panic-safe: a panic inside a probe is recovered, counted, and the loop
// continues.
func (p *readinessProber) run(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(p.interval)
		defer ticker.Stop()
		p.safeProbe(ctx)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				p.safeProbe(ctx)
			}
		}
	}()
	return done
}

// safeProbe runs probeOnce under a panic guard so a panicking destination
// Ready implementation cannot take down the prober goroutine.
func (p *readinessProber) safeProbe(ctx context.Context) {
	defer func() {
		if rec := recover(); rec != nil {
			p.logger.Error("panic recovered in readiness prober",
				"location", "readiness_prober",
				"panic", fmt.Sprint(rec),
				"stack", string(debug.Stack()),
			)
			p.metrics.Panic("readiness_prober")
		}
	}()
	p.probeOnce(ctx)
}
