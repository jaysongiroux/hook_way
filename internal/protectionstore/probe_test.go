package protectionstore

import (
	"context"
	"testing"
	"time"

	"github.com/jaysongiroux/hook_way/internal/protection"
)

func TestRedisCapabilityProbeCannotResetNamedPolicy(t *testing.T) {
	a, b, _ := backendPair(t, "redis")
	p := protection.Policy{Kind: protection.ReplayGuard, ID: "__probe", KeyScope: "id", TTL: time.Minute, MaxEntries: 1}
	ctx := context.Background()
	if err := a.EnsurePolicy(ctx, p); err != nil {
		t.Fatal(err)
	}
	r, err := a.Reserve(ctx, p, "event")
	if err != nil || !r.Accepted {
		t.Fatal(r, err)
	}
	for i := 0; i < 3; i++ {
		if err := b.Check(ctx); err != nil {
			t.Fatal(err)
		}
	}
	duplicate, err := b.Reserve(ctx, p, "event")
	if err != nil || duplicate.Accepted || !duplicate.ExpiresAt.Equal(r.ExpiresAt) {
		t.Fatal("capability probe reset live policy", duplicate, err)
	}
	for _, id := range []string{"\x00capability", "\x00capability/anything"} {
		invalid := p
		invalid.ID = id
		if invalid.Validate() == nil {
			t.Fatal("reserved probe keyspace is a valid policy ID")
		}
	}
}

// Simulated complete state loss is deliberately distinguished from transport
// outage/recovery. This is not a Sentinel/Cluster failover durability test.
func TestRedisLostPolicyCannotSilentlyReopenAdmission(t *testing.T) {
	a, b, _ := backendPair(t, "redis")
	ctx := t.Context()
	p := protection.Policy{Kind: protection.ReplayGuard, ID: "state-loss", KeyScope: "id", TTL: time.Minute, MaxEntries: 10}
	if err := a.EnsurePolicy(ctx, p); err != nil {
		t.Fatal(err)
	}
	if r, err := a.Reserve(ctx, p, "event"); err != nil || !r.Accepted {
		t.Fatal(r, err)
	}
	s := a.(*Redis)
	// Delete only this synthetic policy's keys, never the whole Redis database.
	if err := s.client.Del(ctx, s.keys(p.ID)...).Err(); err != nil {
		t.Fatal(err)
	}
	if err := b.CheckPolicy(ctx, p); err == nil {
		t.Fatal("lost policy stayed healthy")
	}
	if r, err := b.Reserve(ctx, p, "event"); err == nil || r.Accepted {
		t.Fatal("lost state silently allowed duplicate", r, err)
	}
	if err := b.CheckPolicy(ctx, p); err == nil {
		t.Fatal("health probe recreated lost state")
	}
}
