package protectionstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jaysongiroux/hook_way/internal/protection"
	"github.com/redis/go-redis/v9"
)

func BenchmarkProtectionMaintenance(b *testing.B) {
	for _, backend := range []string{"redis", "postgres"} {
		for _, operation := range []string{"policy_update", "cleanup"} {
			b.Run(backend+"/"+operation, func(b *testing.B) {
				store, _, _ := backendPair(b, backend)
				ctx := context.Background()
				p := protection.Policy{Kind: protection.RateLimit, ID: "maintenance_bench", KeyScope: "id", Strategy: protection.TokenBucket, RPM: 1, Burst: 1, MaxEntries: 10000}
				if operation == "cleanup" {
					p.Kind = protection.ReplayGuard
					p.Strategy = ""
					p.RPM = 0
					p.Burst = 0
					p.TTL = time.Hour
				}
				if err := store.EnsurePolicy(ctx, p); err != nil {
					b.Fatal(err)
				}
				for i := 0; i < 1000; i++ {
					key := fmt.Sprint(i)
					if operation == "cleanup" {
						if _, err := store.Reserve(ctx, p, key); err != nil {
							b.Fatal(err)
						}
					} else if _, err := store.Allow(ctx, p, key); err != nil {
						b.Fatal(err)
					}
				}
				if operation == "cleanup" {
					expireBenchmarkEntries(b, store, p)
				}
				var beforeLSN string
				if s, ok := store.(*Postgres); ok {
					if err := s.pool.QueryRow(ctx, `SELECT pg_current_wal_lsn()::text`).Scan(&beforeLSN); err != nil {
						b.Fatal(err)
					}
				}
				b.ReportAllocs()
				b.ResetTimer()
				var removed int64
				for i := 0; i < b.N; i++ {
					if operation == "cleanup" {
						n, err := store.GC(ctx)
						if err != nil {
							b.Fatal(err)
						}
						removed += n
					} else {
						next := p
						next.Burst = 3 - p.Burst
						if err := store.UpdatePolicy(ctx, next, p.Fingerprint()); err != nil {
							b.Fatal(err)
						}
						p = next
					}
				}
				b.StopTimer()
				if operation == "cleanup" {
					b.ReportMetric(float64(removed)/float64(max(1, b.N)), "deleted/op")
				}
				switch s := store.(type) {
				case *Postgres:
					var bytes, wal int64
					if err := s.pool.QueryRow(ctx, `SELECT pg_total_relation_size('hookway_protection.entries')+pg_total_relation_size('hookway_protection.policies'),pg_wal_lsn_diff(pg_current_wal_lsn(),$1::pg_lsn)::bigint`, beforeLSN).Scan(&bytes, &wal); err != nil {
						b.Fatal(err)
					}
					b.ReportMetric(float64(bytes), "table_bytes")
					b.ReportMetric(float64(wal)/float64(max(1, b.N)), "wal_bytes/op")
				case *Redis:
					var bytes int64
					for _, key := range s.keys(p.ID) {
						n, err := s.client.MemoryUsage(ctx, key).Result()
						if err != nil && !errors.Is(err, redis.Nil) {
							b.Fatal(err)
						}
						bytes += n
					}
					b.ReportMetric(float64(bytes), "key_bytes")
				}
			})
		}
	}
}

// Model a previously persisted expired guard. Both the serialized state and
// expiry index agree; maintenance measures cleanup after a period without instances.
func expireBenchmarkEntries(b *testing.B, store protection.Store, p protection.Policy) {
	b.Helper()
	ctx := context.Background()
	expires := time.Now().Add(-time.Minute).UnixMilli()
	switch s := store.(type) {
	case *Postgres:
		_, err := s.pool.Exec(ctx, `UPDATE hookway_protection.entries SET state=jsonb_set(state,'{expires_at}',to_jsonb($3::bigint)),expires_at=to_timestamp($3::double precision/1000) WHERE namespace=$1 AND policy_id=$2`, s.cfg.Namespace, p.ID, expires)
		if err != nil {
			b.Fatal(err)
		}
	case *Redis:
		keys := s.keys(p.ID)
		entries, err := s.client.HGetAll(ctx, keys[1]).Result()
		if err != nil {
			b.Fatal(err)
		}
		pipe := s.client.Pipeline()
		for key, raw := range entries {
			var state protection.State
			if json.Unmarshal([]byte(raw), &state) != nil {
				b.Fatal("invalid benchmark state")
			}
			state.ExpiresAt = expires
			next, _ := json.Marshal(state)
			pipe.HSet(ctx, keys[1], key, next)
			pipe.ZAdd(ctx, keys[2], redis.Z{Score: float64(expires), Member: key})
		}
		if _, err = pipe.Exec(ctx); err != nil {
			b.Fatal(err)
		}
	}
}
