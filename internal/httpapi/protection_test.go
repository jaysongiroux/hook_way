package httpapi

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jaysongiroux/hook_way/internal/metrics"
	"github.com/jaysongiroux/hook_way/internal/protection"
)

type failingProtection struct {
	protection.Store
	failed atomic.Bool
}

func (s *failingProtection) Check(ctx context.Context) error {
	if s.failed.Load() {
		return protection.ErrUnavailable
	}
	return s.Store.Check(ctx)
}
func (s *failingProtection) Allow(ctx context.Context, p protection.Policy, key string) (protection.Decision, error) {
	if s.failed.Load() {
		return protection.Decision{}, protection.ErrUnavailable
	}
	return s.Store.Allow(ctx, p, key)
}

func TestProtectionReadinessWithdrawsAndRecoversWithoutResettingQuota(t *testing.T) {
	p := protection.Policy{Kind: protection.RateLimit, ID: "quota", KeyScope: "source_ip", Strategy: protection.TokenBucket, RPM: 1, Burst: 1, MaxEntries: 10}
	memory := protection.NewMemory("test")
	if err := memory.EnsurePolicy(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	backend := &failingProtection{Store: memory}
	s := &observedProtectionStore{Store: backend, name: "test", cfg: protection.Config{Namespace: "test", Memory: &protection.MemoryConfig{}}.Defaults(), policies: []protection.Policy{p}, metrics: metrics.New()}
	runtime := &protectionRuntime{stores: map[string]*observedProtectionStore{"test": s}}
	s.probe(context.Background())
	d, err := s.Allow(context.Background(), p, "client")
	if err != nil || !d.Allowed {
		t.Fatalf("first admission: %v %v", d, err)
	}
	backend.failed.Store(true)
	if _, err = s.Allow(context.Background(), p, "client"); !errors.Is(err, protection.ErrUnavailable) {
		t.Fatalf("outage: %v", err)
	}
	if len(runtime.unready(time.Now(), time.Second)) != 1 {
		t.Fatal("outage did not withdraw readiness")
	}
	backend.failed.Store(false)
	s.probe(context.Background())
	if len(runtime.unready(time.Now(), time.Second)) != 0 {
		t.Fatal("store did not recover")
	}
	d, err = s.Allow(context.Background(), p, "client")
	if err != nil || d.Allowed {
		t.Fatalf("recovery reset quota: %v %v", d, err)
	}
}

func TestProtectionProbeDoesNotRecreateLostPolicy(t *testing.T) {
	p := protection.Policy{Kind: protection.ReplayGuard, ID: "guard", KeyScope: "body", TTL: time.Minute, MaxEntries: 10}
	memory := protection.NewMemory("test")
	s := &observedProtectionStore{Store: memory, name: "test", cfg: protection.Config{Namespace: "test", Memory: &protection.MemoryConfig{}}.Defaults(), policies: []protection.Policy{p}, metrics: metrics.New()}
	s.probe(context.Background())
	if s.ready.Load() {
		t.Fatal("missing policy marked ready")
	}
	if _, err := memory.PolicyStatus(context.Background(), p.ID); !errors.Is(err, protection.ErrPolicyConflict) {
		t.Fatalf("probe recreated lost policy: %v", err)
	}
}

func TestProtectionMaintenanceStopsWithServer(t *testing.T) {
	cfg := protection.Config{Namespace: "test", Memory: &protection.MemoryConfig{}, Cleanup: protection.CleanupConfig{Interval: time.Second}}.Defaults()
	s := &observedProtectionStore{Store: protection.NewMemory("test"), name: "test", cfg: cfg, metrics: metrics.New()}
	runtime := &protectionRuntime{stores: map[string]*observedProtectionStore{"test": s}}
	done := runtime.run(context.Background(), time.Hour)
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("background workers leaked")
	}
}

type blockingProbe struct {
	protection.Store
	started, release chan struct{}
}

func (s *blockingProbe) Check(ctx context.Context) error {
	close(s.started)
	select {
	case <-s.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (s *blockingProbe) Allow(ctx context.Context, p protection.Policy, key string) (protection.Decision, error) {
	return protection.Decision{}, protection.ErrUnavailable
}

func TestProtectionProbeStartedBeforeFailureCannotRestoreReadiness(t *testing.T) {
	backend := &blockingProbe{Store: protection.NewMemory("test"), started: make(chan struct{}), release: make(chan struct{})}
	s := &observedProtectionStore{Store: backend, name: "test", cfg: protection.Config{Namespace: "test", Memory: &protection.MemoryConfig{}}.Defaults(), metrics: metrics.New()}
	s.ready.Store(true)
	done := make(chan struct{})
	go func() { s.probe(context.Background()); close(done) }()
	<-backend.started
	if _, err := s.Allow(context.Background(), protection.Policy{ID: "test"}, "key"); err == nil {
		t.Fatal("expected failure")
	}
	close(backend.release)
	<-done
	if s.ready.Load() {
		t.Fatal("a stale successful probe erased the later store failure")
	}
}

func TestProtectionDeadlineMarksUnready(t *testing.T) {
	backend := &failingProtection{Store: protection.NewMemory("test")}
	backend.failed.Store(true)
	s := &observedProtectionStore{Store: backend, name: "test", cfg: protection.Config{Namespace: "test", Memory: &protection.MemoryConfig{}}.Defaults(), metrics: metrics.New()}
	s.ready.Store(true)
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	_, _ = s.Allow(ctx, protection.Policy{ID: "test"}, "key")
	if s.ready.Load() {
		t.Fatal("expired node deadline masked failed protection")
	}
}

type poolProtection struct {
	protection.Store
	wait time.Duration
}

func (s *poolProtection) Statistics() protection.Statistics {
	return protection.Statistics{InUse: 2, Idle: 3, WaitDuration: s.wait}
}
func TestProtectionPoolWaitCountsOnlyNewWaiting(t *testing.T) {
	backend := &poolProtection{Store: protection.NewMemory("test"), wait: time.Second}
	m := metrics.New()
	s := &observedProtectionStore{Store: backend, name: "test", metrics: m}
	s.recordPool()
	s.recordPool()
	backend.wait = 1500 * time.Millisecond
	s.recordPool()
	families, err := m.Registry().Gather()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, family := range families {
		if family.GetName() == "hookway_protection_pool_wait_seconds_total" {
			found = true
			if value := family.Metric[0].GetCounter().GetValue(); value != 1.5 {
				t.Fatalf("cumulative pool wait was double-counted: %v", value)
			}
		}
	}
	if !found {
		t.Fatal("missing wait metric")
	}
}
