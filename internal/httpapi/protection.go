package httpapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jaysongiroux/hook_way/internal/flowconfig"
	"github.com/jaysongiroux/hook_way/internal/metrics"
	"github.com/jaysongiroux/hook_way/internal/protection"
	"github.com/jaysongiroux/hook_way/internal/protectionstore"
)

// protectionRuntime owns native connections and context-bound health/cleanup
// work. Admission errors invalidate readiness immediately; only a complete
// read-only probe restores it. All metric labels come from configuration.
type protectionRuntime struct {
	stores map[string]*observedProtectionStore
	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

type observedProtectionStore struct {
	protection.Store
	name     string
	cfg      protection.Config
	policies []protection.Policy
	metrics  *metrics.Metrics
	ready    atomic.Bool
	checked  atomic.Int64
	failures atomic.Uint64
	healthMu sync.Mutex
	poolMu   sync.Mutex
	poolWait time.Duration
}

func bindProtection(cfg flowconfig.Config, m *metrics.Metrics) (*protectionRuntime, []io.Closer, error) {
	policies, err := flowconfig.ProtectionPolicies(cfg)
	if err != nil {
		return nil, nil, err
	}
	p := &protectionRuntime{stores: map[string]*observedProtectionStore{}}
	var closers []io.Closer
	names := make([]string, 0, len(cfg.StateStores))
	for name := range cfg.StateStores {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		c := cfg.StateStores[name].Defaults()
		ctx, cancel := context.WithTimeout(context.Background(), c.StartupTimeout)
		s, openErr := protectionstore.Open(ctx, c)
		if openErr == nil {
			for _, policy := range policies[name] {
				if openErr = s.EnsurePolicy(ctx, policy); openErr != nil {
					break
				}
			}
		}
		cancel()
		if openErr != nil {
			if s != nil {
				_ = s.Close()
			}
			return nil, closers, fmt.Errorf("protection store %q initialization failed: verify connection, schema and policy; use hookway state-store status/migrate/policy-update", name)
		}
		observed := &observedProtectionStore{Store: s, name: name, cfg: c, policies: policies[name], metrics: m}
		observed.ready.Store(true)
		observed.checked.Store(time.Now().UnixNano())
		m.ProtectionReady(name, true)
		p.stores[name] = observed
		closers = append(closers, observed)
	}
	return p, closers, nil
}

func (p *protectionRuntime) resolve(name string) protection.Store {
	if s := p.stores[name]; s != nil {
		return s
	}
	return nil
}

func protectionResult(err error, allowed bool) string {
	switch {
	case errors.Is(err, protection.ErrCapacity):
		return "capacity"
	case errors.Is(err, protection.ErrPolicyConflict):
		return "policy_conflict"
	case err != nil:
		return "unavailable"
	case !allowed:
		return "denied"
	default:
		return "allowed"
	}
}

func (s *observedProtectionStore) observe(ctx context.Context, policy protection.Policy, operation string, start time.Time, allowed bool, err error) {
	s.recordPool()
	s.metrics.ProtectionOperation(s.name, policy.ID, policy.Kind, policy.Strategy, operation, protectionResult(err, allowed), time.Since(start))
	if errors.Is(err, protection.ErrPolicyConflict) || (err != nil && ctx.Err() != context.Canceled && !errors.Is(err, protection.ErrCapacity) && !errors.Is(err, protection.ErrMissingKey) && !errors.Is(err, protection.ErrInvalidPolicy)) {
		s.healthMu.Lock()
		defer s.healthMu.Unlock()
		s.failures.Add(1)
		s.ready.Store(false)
		s.metrics.ProtectionReady(s.name, false)
	}
}

func (s *observedProtectionStore) Allow(ctx context.Context, p protection.Policy, key string) (protection.Decision, error) {
	start := time.Now()
	op, cancel := context.WithTimeout(ctx, s.cfg.Timeout)
	defer cancel()
	d, err := s.Store.Allow(op, p, key)
	s.observe(ctx, p, "allow", start, d.Allowed, err)
	return d, err
}

func (s *observedProtectionStore) Reserve(ctx context.Context, p protection.Policy, key string) (protection.Reservation, error) {
	start := time.Now()
	op, cancel := context.WithTimeout(ctx, s.cfg.Timeout)
	defer cancel()
	r, err := s.Store.Reserve(op, p, key)
	s.observe(ctx, p, "reserve", start, r.Accepted, err)
	return r, err
}

func (s *observedProtectionStore) Release(ctx context.Context, p protection.Policy, key, owner string) error {
	start := time.Now()
	op, cancel := context.WithTimeout(ctx, s.cfg.Timeout)
	defer cancel()
	err := s.Store.Release(op, p, key, owner)
	s.observe(ctx, p, "release", start, true, err)
	return err
}

func (s *observedProtectionStore) probe(ctx context.Context) {
	before := s.failures.Load()
	op, cancel := context.WithTimeout(ctx, s.cfg.StartupTimeout)
	defer cancel()
	err := s.Check(op)
	if err == nil {
		for _, policy := range s.policies {
			if err = s.CheckPolicy(op, policy); err != nil {
				break
			}
		}
	}
	s.recordPool()
	s.healthMu.Lock()
	defer s.healthMu.Unlock()
	s.checked.Store(time.Now().UnixNano())
	if err == nil && s.failures.Load() != before {
		return
	}
	s.ready.Store(err == nil)
	s.metrics.ProtectionReady(s.name, err == nil)
}

func (p *protectionRuntime) unready(now time.Time, interval time.Duration) []string {
	if interval <= 0 {
		interval = defaultReadinessInterval
	}
	var names []string
	for name, s := range p.stores {
		if !s.ready.Load() || now.Sub(time.Unix(0, s.checked.Load())) > stalenessFactor*interval {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func (p *protectionRuntime) run(parent context.Context, interval time.Duration) <-chan struct{} {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.done != nil {
		return p.done
	}
	if interval <= 0 {
		interval = defaultReadinessInterval
	}
	ctx, cancel := context.WithCancel(parent)
	p.cancel = cancel
	p.done = make(chan struct{})
	var wg sync.WaitGroup
	for _, s := range p.stores {
		wg.Add(2)
		go func() {
			defer wg.Done()
			backoff := 500 * time.Millisecond
			for {
				s.probe(ctx)
				wait := interval
				if !s.ready.Load() {
					// #nosec G404 -- This randomness only spreads health-probe retries; reservation ownership uses crypto/rand.
					wait = backoff + time.Duration(rand.Int64N(int64(backoff/4)+1))
					backoff = min(backoff*2, 30*time.Second)
				} else {
					backoff = 500 * time.Millisecond
				}
				timer := time.NewTimer(wait)
				select {
				case <-ctx.Done():
					timer.Stop()
					return
				case <-timer.C:
				}
			}
		}()
		go func() {
			defer wg.Done()
			ticker := time.NewTicker(s.cfg.Cleanup.Interval)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					start := time.Now()
					gcCtx, stop := context.WithTimeout(ctx, s.cfg.Cleanup.Budget)
					n, err := s.GC(gcCtx)
					stop()
					s.metrics.ProtectionCleanup(s.name, n, err == nil, time.Since(start))
				}
			}
		}()
	}
	go func() { wg.Wait(); close(p.done) }()
	return p.done
}

func (p *protectionRuntime) Close() error {
	p.mu.Lock()
	cancel, done := p.cancel, p.done
	p.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
	return nil
}

func (s *observedProtectionStore) recordPool() {
	client, ok := s.Store.(protection.Statser)
	if !ok {
		return
	}
	s.poolMu.Lock()
	defer s.poolMu.Unlock()
	stats := client.Statistics()
	wait := max(time.Duration(0), stats.WaitDuration-s.poolWait)
	s.poolWait = stats.WaitDuration
	s.metrics.ProtectionPool(s.name, stats.InUse, stats.Idle, wait)
}
