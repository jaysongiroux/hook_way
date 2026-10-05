package protectionstore

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jaysongiroux/hook_way/internal/protection"
	"github.com/redis/go-redis/v9"
)

type lostReplyHook struct {
	operation string
	lost      atomic.Bool
}

func (h *lostReplyHook) DialHook(next redis.DialHook) redis.DialHook {
	return func(ctx context.Context, network, addr string) (net.Conn, error) { return next(ctx, network, addr) }
}
func (h *lostReplyHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}
func (h *lostReplyHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		err := next(ctx, cmd)
		args := cmd.Args()
		if err == nil && cmd.Name() == "eval" && len(args) > 7 && args[7] == h.operation && !h.lost.Swap(true) {
			return errors.New("simulated reply lost after server applied operation")
		}
		return err
	}
}
func TestRedisUncertainWritesAreNotRetriedOrReleased(t *testing.T) {
	a, b, _ := backendPair(t, "redis")
	s := a.(*Redis)
	ctx := context.Background()
	p := protection.Policy{Kind: protection.RateLimit, ID: "lost_reply/quota", KeyScope: "id", Strategy: protection.TokenBucket, RPM: 1, Burst: 2, MaxEntries: 10}
	if err := a.EnsurePolicy(ctx, p); err != nil {
		t.Fatal(err)
	}
	s.client.AddHook(&lostReplyHook{operation: "allow"})
	if d, err := a.Allow(ctx, p, "one"); !errors.Is(err, protection.ErrUnavailable) || d.Allowed {
		t.Fatal(d, err)
	}
	d, err := b.Allow(ctx, p, "one")
	if err != nil || !d.Allowed || d.Remaining != 0 {
		t.Fatal("uncertain debit retried or forgotten", d, err)
	}
	d, err = b.Allow(ctx, p, "one")
	if err != nil || d.Allowed {
		t.Fatal(d, err)
	}
	guard := protection.Policy{Kind: protection.ReplayGuard, ID: "lost_reply/guard", KeyScope: "id", TTL: time.Minute, MaxEntries: 10}
	if err = a.EnsurePolicy(ctx, guard); err != nil {
		t.Fatal(err)
	}
	s.client.AddHook(&lostReplyHook{operation: "reserve"})
	if r, err := a.Reserve(ctx, guard, "event"); !errors.Is(err, protection.ErrUnavailable) || r.Accepted || r.Owner != "" {
		t.Fatal(r, err)
	}
	r, err := b.Reserve(ctx, guard, "event")
	if err != nil || r.Accepted {
		t.Fatal("uncertain reservation was not retained", r, err)
	}
	if err = a.Release(ctx, guard, "event", "unknown-owner"); err != nil {
		t.Fatal(err)
	}
	r, err = b.Reserve(ctx, guard, "event")
	if err != nil || r.Accepted {
		t.Fatal("unknown owner released reservation", r, err)
	}
}
