package protection

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestExactRefillClockRegressionAndPolicyUpdate(t *testing.T) {
	p := Policy{Kind: RateLimit, ID: "quota", KeyScope: "ip", Strategy: TokenBucket, RPM: 3, Burst: 2, MaxEntries: 2}
	s, d := Admit(p, State{}, false, 1000)
	if !d.Allowed || d.Remaining != 1 {
		t.Fatal(d)
	}
	s, d = Admit(p, s, true, 1000)
	if !d.Allowed {
		t.Fatal(d)
	}
	s, d = Admit(p, s, true, 20999)
	if d.Allowed || d.RetryAfter != time.Millisecond {
		t.Fatal(d)
	}
	s, d = Admit(p, s, true, 21000)
	if !d.Allowed {
		t.Fatal(d)
	}
	s, d = Admit(p, s, true, 1000)
	if d.Allowed || s.At != 21000 {
		t.Fatalf("clock regression granted quota: %+v %+v", s, d)
	}
	next := p
	next.RPM = 1
	next.Burst = 1
	s = Reconfigure(p, next, s, 31000)
	if s.Tokens != 30000 {
		t.Fatalf("old refill must settle at update: %+v", s)
	}
	_, d = Admit(next, s, true, 60999)
	if d.Allowed {
		t.Fatal("new refill started too early")
	}
	_, d = Admit(next, s, true, 61000)
	if !d.Allowed {
		t.Fatal("new refill not applied")
	}
}
func TestSlidingWindowBoundaryWeightedCounts(t *testing.T) {
	p := Policy{Kind: RateLimit, ID: "window", KeyScope: "ip", Strategy: SlidingWindowCounter, Limit: 2, Window: time.Second, MaxEntries: 2}
	s, _ := Admit(p, State{}, false, 999)
	s, _ = Admit(p, s, true, 999)
	s, d := Admit(p, s, true, 1000)
	if d.Allowed {
		t.Fatal("window reset allowed boundary burst")
	}
	s, d = Admit(p, s, true, 1500)
	if !d.Allowed {
		t.Fatal("weighted previous window not applied")
	}
	_, d = Admit(p, s, true, 1500)
	if d.Allowed {
		t.Fatal("weighted budget overrun")
	}
	_, d = Admit(p, s, true, 3000)
	if !d.Allowed {
		t.Fatal("idle counter not expired")
	}
}
func TestMemoryExpiryOwnershipCapacityAndNoUnboundedHeap(t *testing.T) {
	m := NewMemory("test")
	now := time.UnixMilli(1000)
	m.now = func() time.Time { return now }
	p := Policy{Kind: ReplayGuard, ID: "guard", KeyScope: "id", TTL: time.Second, MaxEntries: 1}
	ctx := context.Background()
	if err := m.EnsurePolicy(ctx, p); err != nil {
		t.Fatal(err)
	}
	r, err := m.Reserve(ctx, p, "secret")
	if err != nil || !r.Accepted {
		t.Fatal(r, err)
	}
	if err = m.Release(ctx, p, "secret", "wrong"); err != nil {
		t.Fatal(err)
	}
	if _, err = m.Reserve(ctx, p, "other"); !errors.Is(err, ErrCapacity) {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	next, err := m.Reserve(ctx, p, "secret")
	if err != nil || !next.Accepted {
		t.Fatal(next, err)
	}
	if err = m.Release(ctx, p, "secret", r.Owner); err != nil {
		t.Fatal(err)
	}
	duplicate, err := m.Reserve(ctx, p, "secret")
	if err != nil || duplicate.Accepted {
		t.Fatal("stale owner released replacement", duplicate, err)
	}
	bucket := Policy{Kind: RateLimit, ID: "bucket", KeyScope: "ip", Strategy: TokenBucket, RPM: 1, Burst: 1, MaxEntries: 1}
	if err = m.EnsurePolicy(ctx, bucket); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10000; i++ {
		if _, err = m.Allow(ctx, bucket, "hot"); err != nil {
			t.Fatal(err)
		}
	}
	if len(m.policies[bucket.ID].expiry) != 1 {
		t.Fatal("hot-key heap grows with requests")
	}
}
func TestMemoryMaintenanceLockHonorsOperationTimeout(t *testing.T) {
	m := NewMemoryWithConfig(Config{Namespace: "test", Timeout: 10 * time.Millisecond})
	p := Policy{Kind: RateLimit, ID: "busy", KeyScope: "id", Strategy: TokenBucket, RPM: 1, Burst: 1, MaxEntries: 1}
	ctx := context.Background()
	if err := m.EnsurePolicy(ctx, p); err != nil {
		t.Fatal(err)
	}
	v := m.policies[p.ID]
	if err := v.lock(ctx); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, err := m.Allow(ctx, p, "one")
	if !errors.Is(err, ErrUnavailable) || time.Since(start) > time.Second {
		t.Fatal("operation did not honor bounded lock wait", err)
	}
	v.Unlock()
	d, err := m.Allow(ctx, p, "one")
	if err != nil || !d.Allowed {
		t.Fatal("timed out admission consumed quota", d, err)
	}
}
