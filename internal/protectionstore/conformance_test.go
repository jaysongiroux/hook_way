package protectionstore

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jaysongiroux/hook_way/internal/protection"
)

func TestBackendConformance(t *testing.T) {
	for _, backend := range []string{"memory", "redis", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			a, b, _ := backendPair(t, backend)
			ctx := context.Background()
			t.Run("aggregate_atomic_admission", func(t *testing.T) {
				p := protection.Policy{Kind: protection.RateLimit, ID: "aggregate", KeyScope: "credential_id:trusted", Strategy: protection.TokenBucket, RPM: 1, Burst: 10, MaxEntries: 100}
				if err := a.EnsurePolicy(ctx, p); err != nil {
					t.Fatal(err)
				}
				if err := b.CheckPolicy(ctx, p); err != nil {
					t.Fatal(err)
				}
				var admitted atomic.Int64
				var wg sync.WaitGroup
				errs := make(chan error, 50)
				for i := 0; i < 50; i++ {
					wg.Add(1)
					go func(i int) {
						defer wg.Done()
						s := a
						if i%2 == 1 {
							s = b
						}
						d, err := s.Allow(ctx, p, "credential-with-secret")
						if err != nil {
							errs <- err
						} else if d.Allowed {
							admitted.Add(1)
						}
					}(i)
				}
				wg.Wait()
				close(errs)
				for err := range errs {
					t.Error(err)
				}
				if admitted.Load() != 10 {
					t.Fatalf("aggregate admitted %d, want10", admitted.Load())
				}
			})
			t.Run("guards_capacity_expiry_stale_owner", func(t *testing.T) {
				p := protection.Policy{Kind: protection.ReplayGuard, ID: "guards", KeyScope: "header.event_id", TTL: 200 * time.Millisecond, MaxEntries: 2}
				if err := a.EnsurePolicy(ctx, p); err != nil {
					t.Fatal(err)
				}
				first, err := a.Reserve(ctx, p, "one")
				if err != nil || !first.Accepted {
					t.Fatal(first, err)
				}
				duplicate, err := b.Reserve(ctx, p, "one")
				if err != nil || duplicate.Accepted || !duplicate.ExpiresAt.Equal(first.ExpiresAt) {
					t.Fatal(duplicate, err)
				}
				if err = b.Release(ctx, p, "one", "wrong"); err != nil {
					t.Fatal(err)
				}
				if _, err = a.Reserve(ctx, p, "two"); err != nil {
					t.Fatal(err)
				}
				if _, err = b.Reserve(ctx, p, "three"); !errors.Is(err, protection.ErrCapacity) {
					t.Fatal(err)
				}
				time.Sleep(230 * time.Millisecond)
				replacement, err := b.Reserve(ctx, p, "one")
				if err != nil || !replacement.Accepted {
					t.Fatal(replacement, err)
				}
				if err = a.Release(ctx, p, "one", first.Owner); err != nil {
					t.Fatal(err)
				}
				r, err := a.Reserve(ctx, p, "one")
				if err != nil || r.Accepted {
					t.Fatal("old reservation released replacement", r, err)
				}
				if err = b.Release(ctx, p, "one", replacement.Owner); err != nil {
					t.Fatal(err)
				}
				r, err = a.Reserve(ctx, p, "one")
				if err != nil || !r.Accepted {
					t.Fatal(r, err)
				}
			})
			t.Run("coordinated_update_preserves_quota_and_guard_expiry", func(t *testing.T) {
				p := protection.Policy{Kind: protection.RateLimit, ID: "updates", KeyScope: "ip", Strategy: protection.TokenBucket, RPM: 1, Burst: 3, MaxEntries: 1000}
				if err := a.EnsurePolicy(ctx, p); err != nil {
					t.Fatal(err)
				}
				for i := 0; i < 2; i++ {
					d, err := a.Allow(ctx, p, "one")
					if err != nil || !d.Allowed {
						t.Fatal(d, err)
					}
				}
				next := p
				next.Burst = 2
				if err := b.EnsurePolicy(ctx, next); !errors.Is(err, protection.ErrPolicyConflict) {
					t.Fatal(err)
				}
				if err := a.UpdatePolicy(ctx, next, p.Fingerprint()); err != nil {
					t.Fatal(err)
				}
				if _, err := b.Allow(ctx, p, "one"); !errors.Is(err, protection.ErrPolicyConflict) {
					t.Fatal(err)
				}
				d, err := b.Allow(ctx, next, "one")
				if err != nil || !d.Allowed {
					t.Fatal(d, err)
				}
				d, err = a.Allow(ctx, next, "one")
				if err != nil || d.Allowed {
					t.Fatal("update reset consumed quota", d, err)
				}
				if err = a.UpdatePolicy(ctx, next, p.Fingerprint()); !errors.Is(err, protection.ErrPolicyConflict) {
					t.Fatal(err)
				}
				status, err := a.PolicyStatus(ctx, p.ID)
				if err != nil || status.Fingerprint != next.Fingerprint() || status.Updating {
					t.Fatal(status, err)
				}
				guard := protection.Policy{Kind: protection.ReplayGuard, ID: "guard_update", KeyScope: "id", TTL: 10 * time.Second, MaxEntries: 10}
				if err = a.EnsurePolicy(ctx, guard); err != nil {
					t.Fatal(err)
				}
				r, err := a.Reserve(ctx, guard, "old")
				if err != nil {
					t.Fatal(err)
				}
				changed := guard
				changed.TTL = 20 * time.Second
				if err = b.UpdatePolicy(ctx, changed, guard.Fingerprint()); err != nil {
					t.Fatal(err)
				}
				duplicate, err := a.Reserve(ctx, changed, "old")
				if err != nil || duplicate.Accepted || !duplicate.ExpiresAt.Equal(r.ExpiresAt) {
					t.Fatal("TTL changed existing reservation", duplicate, err)
				}
			})
			t.Run("sliding_counter_and_missing_key", func(t *testing.T) {
				p := protection.Policy{Kind: protection.RateLimit, ID: "counter", KeyScope: "ip", Strategy: protection.SlidingWindowCounter, Limit: 2, Window: time.Second, MaxEntries: 10}
				if err := a.EnsurePolicy(ctx, p); err != nil {
					t.Fatal(err)
				}
				for i := 0; i < 3; i++ {
					d, err := b.Allow(ctx, p, "one")
					if err != nil || d.Allowed != (i < 2) {
						t.Fatal(i, d, err)
					}
				}
				if _, err := a.Allow(ctx, p, ""); !errors.Is(err, protection.ErrMissingKey) {
					t.Fatal(err)
				}
				changed := p
				changed.Window = 2 * time.Second
				if err := a.UpdatePolicy(ctx, changed, p.Fingerprint()); !errors.Is(err, protection.ErrPolicyConflict) {
					t.Fatal(err)
				}
			})
			t.Run("cleanup_and_shutdown", func(t *testing.T) {
				p := protection.Policy{Kind: protection.ReplayGuard, ID: "cleanup", KeyScope: "id", TTL: time.Millisecond, MaxEntries: 1000}
				if err := a.EnsurePolicy(ctx, p); err != nil {
					t.Fatal(err)
				}
				for i := 0; i < 10; i++ {
					if _, err := b.Reserve(ctx, p, fmt.Sprint(i)); err != nil {
						t.Fatal(err)
					}
				}
				time.Sleep(5 * time.Millisecond)
				if _, err := a.GC(ctx); err != nil {
					t.Fatal(err)
				}
				if err := a.Check(ctx); err != nil {
					t.Fatal(err)
				}
				if err := a.Close(); err != nil {
					t.Fatal(err)
				}
				if _, err := a.Reserve(ctx, p, "closed"); !errors.Is(err, protection.ErrUnavailable) {
					t.Fatal(err)
				}
			})
		})
	}
}
