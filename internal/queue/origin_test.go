package queue

import (
	"context"
	"sync"
	"testing"
)

func TestOriginRoundTripsThroughContext(t *testing.T) {
	if _, ok := OriginFrom(context.Background()); ok {
		t.Fatal("a bare context has an origin")
	}
	want := Origin{Flow: "orders", NodeID: "out", Settled: NewSettled()}
	got, ok := OriginFrom(WithOrigin(context.Background(), want))
	if !ok || got != want {
		t.Fatalf("OriginFrom = %+v, %v", got, ok)
	}
}

func TestSettledRunsWaitersOnceWithTheFinalStatus(t *testing.T) {
	s := NewSettled()
	var mu sync.Mutex
	var seen []int
	record := func(status int) { mu.Lock(); seen = append(seen, status); mu.Unlock() }
	s.AfterSettle(record) // before the request settles: deferred
	if len(seen) != 0 {
		t.Fatal("a waiter ran before the request settled")
	}
	s.Resolve(503)
	s.Resolve(202)        // only the first status counts
	s.AfterSettle(record) // after: runs at once with the final status
	if len(seen) != 2 || seen[0] != 503 || seen[1] != 503 {
		t.Fatalf("waiters saw %v, want [503 503]", seen)
	}
}
