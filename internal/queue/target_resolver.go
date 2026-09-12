package queue

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"golang.org/x/sync/singleflight"

	"github.com/jasongiroux/hookway/internal/vars"
)

// errTargetNotConfigured is returned by targetResolver.resolve when a target
// matches neither a static route nor a dynamic route template.
var errTargetNotConfigured = errors.New("target is not configured")

// defaultMaxDynamicTargets bounds the per-publisher cache of rendered dynamic
// targets. Eviction is FIFO; for publishers that provision remote resources an
// eviction only costs a re-lookup on next sight, never a re-create.
const defaultMaxDynamicTargets = 10_000

// templateEntry pairs a dynamic route's matcher with the per-target state
// cloned for every rendered target it produces.
type templateEntry[T any] struct {
	matcher vars.Matcher
	state   T
}

// targetResolver serves a publisher's targets: static targets from a
// write-once map (the pre-variables model, no locking), and dynamic targets
// (rendered from a {variable} template per request) from a bounded, lazily
// populated cache. First sight of a rendered target runs the publisher's
// provision function exactly once even under concurrent fan-out.
type targetResolver[T any] struct {
	static    map[string]T
	templates []templateEntry[T]

	mu         sync.RWMutex
	dynamic    map[string]T
	order      []string
	maxDynamic int
	group      singleflight.Group
	logger     *slog.Logger
}

func newTargetResolver[T any](maxDynamic int, logger *slog.Logger) *targetResolver[T] {
	if maxDynamic <= 0 {
		maxDynamic = defaultMaxDynamicTargets
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &targetResolver[T]{static: map[string]T{}, dynamic: map[string]T{}, maxDynamic: maxDynamic, logger: logger}
}

// addStatic registers a concrete target. Only valid before the first resolve.
func (r *targetResolver[T]) addStatic(name string, state T) { r.static[name] = state }

// addTemplate registers a dynamic route template. Only valid before the first
// resolve. Templates are tried in registration order; the factory registers
// them sorted by raw template so matching is deterministic.
func (r *targetResolver[T]) addTemplate(m vars.Matcher, state T) {
	if !m.IsTemplate() {
		return
	}
	r.templates = append(r.templates, templateEntry[T]{matcher: m, state: state})
}

// lookup returns the state for a static or already-provisioned dynamic target.
func (r *targetResolver[T]) lookup(target string) (T, bool) {
	if s, ok := r.static[target]; ok {
		return s, true
	}
	r.mu.RLock()
	s, ok := r.dynamic[target]
	r.mu.RUnlock()
	return s, ok
}

// matchTemplate returns the first template whose matcher accepts target.
func (r *targetResolver[T]) matchTemplate(target string) (templateEntry[T], bool) {
	for _, e := range r.templates {
		if e.matcher.Match(target) {
			return e, true
		}
	}
	var zero templateEntry[T]
	return zero, false
}

// resolve returns the state for target: a static hit, a cached dynamic hit,
// or, on first sight of a rendered dynamic target, the result of provision
// (run once under singleflight; concurrent callers share the outcome).
// Provision errors are returned to every waiter and not cached, so the next
// publish retries. An unknown target yields errTargetNotConfigured.
func (r *targetResolver[T]) resolve(ctx context.Context, target string, provision func(ctx context.Context, base T, rendered string) (T, error)) (T, error) {
	if s, ok := r.lookup(target); ok {
		return s, nil
	}
	entry, ok := r.matchTemplate(target)
	if !ok {
		var zero T
		return zero, errTargetNotConfigured
	}
	v, err, _ := r.group.Do(target, func() (any, error) {
		// Re-check under the flight: a concurrent caller may have populated it.
		if s, ok := r.lookup(target); ok {
			return s, nil
		}
		// Provision under a context that outlives the first caller's request
		// so one sender disconnecting cannot fail every sharer of this flight,
		// but keep its deadline so a hung backend cannot block every sharer
		// past server.publish_timeout.
		pctx := context.WithoutCancel(ctx)
		if deadline, ok := ctx.Deadline(); ok {
			var cancel context.CancelFunc
			pctx, cancel = context.WithDeadline(pctx, deadline)
			defer cancel()
		}
		s, err := provision(pctx, entry.state, target)
		if err != nil {
			return nil, err
		}
		r.store(target, s)
		return s, nil
	})
	if err != nil {
		var zero T
		return zero, err
	}
	return v.(T), nil
}

func (r *targetResolver[T]) store(target string, s T) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.dynamic[target]; !exists {
		r.order = append(r.order, target)
	}
	r.dynamic[target] = s
	for len(r.dynamic) > r.maxDynamic && len(r.order) > 0 {
		oldest := r.order[0]
		r.order = r.order[1:]
		delete(r.dynamic, oldest)
		r.logger.Warn("dynamic target cache full; evicted oldest target",
			slog.String("evicted", oldest), slog.Int("max", r.maxDynamic))
	}
}

// dynamicLen reports the number of cached dynamic targets (for tests).
func (r *targetResolver[T]) dynamicLen() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.dynamic)
}

// mustMatcher compiles a matcher for tests and factory code paths where the
// template and patterns were already validated; it panics on error.
func mustMatcher(raw string, patterns map[string]string) vars.Matcher {
	m, err := vars.CompileMatcher(raw, patterns)
	if err != nil {
		panic(fmt.Sprintf("compile matcher %q: %v", raw, err))
	}
	return m
}

// passthroughProvision is the provision function for publishers whose
// per-target state does not depend on the rendered name (the name itself is
// passed to Publish and used directly).
func passthroughProvision[T any](_ context.Context, base T, _ string) (T, error) {
	return base, nil
}
