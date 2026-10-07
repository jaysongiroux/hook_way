package protectionstore

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jaysongiroux/hook_way/internal/protection"
)

// All deletes/restores address the exact four keys for this fixture's unique
// namespace/policy. No FLUSHDB, host redis discovery, or operator data is used.
func TestRedisPolicyLossMissingEntryAndExplicitStateRestore(t *testing.T) {
	a, b, _ := backendPair(t, "redis")
	s := a.(*Redis)
	ctx := t.Context()
	p := protection.Policy{Kind: protection.ReplayGuard, ID: "state-recovery", KeyScope: "provider-event", TTL: time.Minute, MaxEntries: 10}
	if err := s.EnsurePolicy(ctx, p); err != nil {
		t.Fatal(err)
	}
	reservation, err := s.Reserve(ctx, p, "preserved-event")
	if err != nil || !reservation.Accepted {
		t.Fatal(reservation, err)
	}
	archive := map[string]string{}
	for _, key := range s.keys(p.ID) {
		data, err := s.client.Dump(ctx, key).Result()
		if err != nil {
			t.Fatal(err)
		}
		archive[key] = data
	}
	// Losing only an individual reservation is not detectable through policy
	// readiness: the next delivery can be admitted and cause a duplicate effect.
	if err := s.client.HDel(ctx, s.keys(p.ID)[1], protection.HashIdentity("preserved-event")).Err(); err != nil {
		t.Fatal(err)
	}
	if err := b.CheckPolicy(ctx, p); err != nil {
		t.Fatal(err)
	}
	newer, err := b.Reserve(ctx, p, "preserved-event")
	if err != nil || !newer.Accepted || newer.Owner == reservation.Owner {
		t.Fatal("missing entry duplicate window not demonstrated", newer, err)
	}
	if err := s.Release(ctx, p, "preserved-event", reservation.Owner); err != nil {
		t.Fatal(err)
	}
	if duplicate, err := b.Reserve(ctx, p, "preserved-event"); err != nil || duplicate.Accepted {
		t.Fatal("stale owner removed replacement", duplicate, err)
	}
	// Policy loss is different: probes/admission fail closed and do not recreate
	// it. Infrastructure reachability alone cannot authorize resuming traffic.
	if err := s.client.Del(ctx, s.keys(p.ID)[0]).Err(); err != nil {
		t.Fatal(err)
	}
	if err := b.Check(ctx); err != nil {
		t.Fatal("fixture lost connectivity rather than only policy", err)
	}
	for range 2 {
		if err := b.CheckPolicy(ctx, p); !errors.Is(err, protection.ErrPolicyConflict) {
			t.Fatal(err)
		}
		if _, err := b.Reserve(ctx, p, "new-event"); !errors.Is(err, protection.ErrPolicyConflict) {
			t.Fatal(err)
		}
	}
	if exists, err := s.client.Exists(ctx, s.keys(p.ID)[0]).Result(); err != nil || exists != 0 {
		t.Fatal("probe recreated lost policy", exists, err)
	}
	// Traffic is quiescent. Explicitly restore the COUPLED policy+entry state;
	// restoring only the policy would retain the missing-history duplicate risk.
	if err := s.client.Del(ctx, s.keys(p.ID)...).Err(); err != nil {
		t.Fatal(err)
	}
	for key, data := range archive {
		if err := s.client.Restore(ctx, key, 0, data).Err(); err != nil {
			t.Fatal(err)
		}
	}
	status, err := b.PolicyStatus(ctx, p.ID)
	if err != nil || status.Fingerprint != p.Fingerprint() || status.Updating {
		t.Fatal(status, err)
	}
	duplicate, err := b.Reserve(ctx, p, "preserved-event")
	if err != nil || duplicate.Accepted || !duplicate.ExpiresAt.Equal(reservation.ExpiresAt) {
		t.Fatal("restored reservation changed expiry", duplicate, reservation, err)
	}
	if err := b.Release(ctx, p, "preserved-event", newer.Owner); err != nil {
		t.Fatal(err)
	}
	if duplicate, err := b.Reserve(ctx, p, "preserved-event"); err != nil || duplicate.Accepted {
		t.Fatal("post-snapshot owner released restored owner", duplicate, err)
	}
	if err := b.Release(ctx, p, "preserved-event", reservation.Owner); err != nil {
		t.Fatal(err)
	}
	if fresh, err := b.Reserve(ctx, p, "preserved-event"); err != nil || !fresh.Accepted {
		t.Fatal("original owner not restored", fresh, err)
	}
	t.Log("Redis policy loss fails closed; missing entry with intact policy admits duplicate; explicit four-key state restore preserves fingerprint, original owner and original logical expiry. DUMP/RESTORE fixture is not RDB/AOF or failover durability proof.")
}

// Persisted maintenance survives an operator process replacement, not just two
// already connected objects. Resume the identical CAS target without resetting
// any quota entries that were already transformed before interruption.
func TestInterruptedPolicyUpdateSurvivesClientReplacement(t *testing.T) {
	for _, backend := range []string{"redis", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			a, _, cfg := backendPair(t, backend)
			ctx := t.Context()
			switch connection := a.(type) {
			case *Redis:
				info, err := connection.client.Info(ctx, "server").Result()
				if err != nil {
					t.Fatal(err)
				}
				for _, line := range strings.Split(info, "\r\n") {
					if strings.HasPrefix(line, "redis_version:") {
						t.Log(line)
					}
				}
			case *Postgres:
				var version string
				if err := connection.pool.QueryRow(ctx, "SHOW server_version").Scan(&version); err != nil {
					t.Fatal(err)
				}
				t.Logf("PostgreSQL=%s", version)
			}
			p := protection.Policy{Kind: protection.RateLimit, ID: "recovery-maintenance", KeyScope: "provider", Strategy: protection.TokenBucket, RPM: 1, Burst: 1, MaxEntries: 1000}
			if err := a.EnsurePolicy(ctx, p); err != nil {
				t.Fatal(err)
			}
			for i := range 250 {
				decision, err := a.Allow(ctx, p, fmt.Sprint(i))
				if err != nil || !decision.Allowed {
					t.Fatal(decision, err)
				}
			}
			next := p
			next.Burst = 2
			switch s := a.(type) {
			case *Redis:
				step, err := s.call(ctx, "update", next, "", "", p.Fingerprint())
				if err != nil || redisInt(step[1]) != 0 {
					t.Fatal(step, err)
				}
			case *Postgres:
				done, err := s.updateBatch(ctx, next, p.Fingerprint())
				if err != nil || done {
					t.Fatal(done, err)
				}
			}
			_ = a.Close()
			replacement, err := Open(ctx, cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = replacement.Close() }()
			status, err := replacement.PolicyStatus(ctx, p.ID)
			if err != nil || !status.Updating || status.Fingerprint != p.Fingerprint() || status.TargetFingerprint != next.Fingerprint() {
				t.Fatal(status, err)
			}
			for _, definition := range []protection.Policy{p, next} {
				if _, err := replacement.Allow(ctx, definition, "0"); !errors.Is(err, protection.ErrPolicyConflict) {
					t.Fatal("maintenance admitted traffic", err)
				}
			}
			if err := replacement.UpdatePolicy(ctx, next, "wrong-fingerprint"); !errors.Is(err, protection.ErrPolicyConflict) {
				t.Fatal("wrong CAS resumed maintenance", err)
			}
			if err := replacement.UpdatePolicy(ctx, next, p.Fingerprint()); err != nil {
				t.Fatal(err)
			}
			for i := range 250 {
				decision, err := replacement.Allow(ctx, next, fmt.Sprint(i))
				if err != nil || decision.Allowed {
					t.Fatalf("resumed identity %d reset %+v %v", i, decision, err)
				}
			}
			if err := replacement.CheckPolicy(context.Background(), p); !errors.Is(err, protection.ErrPolicyConflict) {
				t.Fatal("old configuration accepted after recovery", err)
			}
		})
	}
}
