package protectionstore

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jaysongiroux/hook_way/internal/protection"
)

// Docker-local measurements are evidence about this test environment, not a
// production throughput claim. Quantiles sample at most 10000 admissions.
func BenchmarkProtection(b *testing.B) {
	for _, backend := range []string{"memory", "redis", "postgres"} {
		for _, strategy := range []string{protection.TokenBucket, protection.SlidingWindowCounter} {
			for _, cardinality := range []string{"hot_key", "many_keys"} {
				for _, workers := range []int{1, 4} {
					b.Run(fmt.Sprintf("%s/%s/%s/workers_%d", backend, strategy, cardinality, workers), func(b *testing.B) {
						store, _, _ := backendPair(b, backend)
						p := protection.Policy{Kind: protection.RateLimit, ID: "bench", KeyScope: "ip", Strategy: strategy, RPM: 1000000, Burst: 1000000, MaxEntries: 1000000}
						if strategy == protection.SlidingWindowCounter {
							p.RPM = 0
							p.Burst = 0
							p.Limit = 1000000
							p.Window = time.Minute
						}
						ctx := context.Background()
						if err := store.EnsurePolicy(ctx, p); err != nil {
							b.Fatal(err)
						}
						durations := make([]int64, min(b.N, 10000))
						var next atomic.Int64
						var wg sync.WaitGroup
						var once sync.Once
						var firstErr error
						var before time.Duration
						if s, ok := store.(protection.Statser); ok {
							before = s.Statistics().WaitDuration
						}
						b.ReportAllocs()
						b.ResetTimer()
						for worker := 0; worker < workers; worker++ {
							wg.Add(1)
							go func() {
								defer wg.Done()
								for {
									i := int(next.Add(1)) - 1
									if i >= b.N {
										return
									}
									key := "hot"
									if cardinality == "many_keys" {
										key = fmt.Sprint(i % 100000)
									}
									start := time.Now()
									_, err := store.Allow(ctx, p, key)
									if i < len(durations) {
										durations[i] = time.Since(start).Nanoseconds()
									}
									if err != nil {
										once.Do(func() { firstErr = err })
										return
									}
								}
							}()
						}
						wg.Wait()
						b.StopTimer()
						if firstErr != nil {
							b.Fatal(firstErr)
						}
						sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
						if len(durations) > 0 {
							b.ReportMetric(float64(durations[(len(durations)*95+99)/100-1])/1e6, "p95_ms")
							b.ReportMetric(float64(durations[(len(durations)*99+99)/100-1])/1e6, "p99_ms")
						}
						if s, ok := store.(protection.Statser); ok {
							stats := s.Statistics()
							b.ReportMetric(float64(stats.InUse+stats.Idle), "pool_conns")
							b.ReportMetric(float64(stats.WaitDuration-before)/float64(max(1, b.N)), "pool_wait_ns/op")
						}
					})
				}
			}
		}
	}
}
