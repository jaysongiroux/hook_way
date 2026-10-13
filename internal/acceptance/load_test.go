package acceptance

import (
	"bytes"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"testing"
	"time"
)

// This is a bounded local acceptance workload, not a production capacity claim.
// Every accepted request must reach both actual HTTP destinations once.
func TestAcceptance_PayloadMixAndReceipts(t *testing.T) {
	s := startSystem(t)
	const requests, workers = 128, 8
	timings := make([]time.Duration, requests)
	statuses := make([]int, requests)
	errors := make([]error, requests)
	jobs := make(chan int)
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for i := range jobs {
				size := 1024
				if i%8 == 0 {
					size = 256 * 1024
				}
				body := []byte(fmt.Sprintf(`{"event":%d,"data":"%s"}`, i, bytes.Repeat([]byte("x"), size)))
				started := time.Now()
				statuses[i], errors[i] = s.postResult(body)
				timings[i] = time.Since(started)
			}
		})
	}
	started := time.Now()
	for i := range requests {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	elapsed := time.Since(started)
	for i, status := range statuses {
		if errors[i] != nil || status != http.StatusAccepted {
			t.Fatalf("request %d status %d error %v", i, status, errors[i])
		}
	}
	for name, receipts := range map[string]map[string]int{"orders": s.orders.counts(), "audit": s.audit.counts()} {
		if len(receipts) != requests {
			t.Fatalf("%s unique receipts %d, want %d", name, len(receipts), requests)
		}
		for id, count := range receipts {
			if count != 1 {
				t.Fatalf("%s event %s delivered %d times", name, id, count)
			}
		}
	}
	sort.Slice(timings, func(i, j int) bool { return timings[i] < timings[j] })
	t.Logf("local workload: requests=%d concurrency=%d bytes=1KiB/256KiB elapsed=%s p95=%s max=%s; all receipts verified", requests, workers, elapsed, timings[requests*95/100], timings[requests-1])
	if code := s.post(t, bytes.Repeat([]byte("x"), 16777217)); code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized request status %d, want 413", code)
	}
	if len(s.orders.counts()) != requests || len(s.audit.counts()) != requests {
		t.Fatal("oversized request reached a destination")
	}
}
