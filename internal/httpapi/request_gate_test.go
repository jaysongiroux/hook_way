package httpapi

import (
	"context"
	"testing"
	"time"
)

func TestRequestGateWaitsWithoutIncreasingExecutionConcurrency(t *testing.T) {
	g := newRequestGate(1, 1, time.Second)
	defer g.close()
	if result := g.acquire(context.Background(), nil); result != gateAdmitted {
		t.Fatal(result)
	}
	waiting := make(chan struct{})
	finished := make(chan gateResult, 1)
	go func() { finished <- g.acquire(context.Background(), func() { close(waiting) }) }()
	<-waiting
	if len(g.active) != 1 || len(g.pending) != 1 {
		t.Fatal("execution/waiting bounds changed")
	}
	if result := g.acquire(context.Background(), nil); result != gateFull {
		t.Fatalf("full queue: %s", result)
	}
	g.release()
	select {
	case result := <-finished:
		if result != gateAdmitted {
			t.Fatal(result)
		}
	case <-time.After(time.Second):
		t.Fatal("waiting request never admitted")
	}
	g.release()
	if len(g.active) != 0 || len(g.pending) != 0 {
		t.Fatal("slots leaked")
	}
}

func TestRequestGateCancellationTimeoutAndShutdownReleaseWaiters(t *testing.T) {
	for _, kind := range []string{"canceled", "timeout", "closing"} {
		t.Run(kind, func(t *testing.T) {
			wait := time.Second
			if kind == "timeout" {
				wait = 20 * time.Millisecond
			}
			g := newRequestGate(1, 1, wait)
			defer g.close()
			if result := g.acquire(context.Background(), nil); result != gateAdmitted {
				t.Fatal(result)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			waiting := make(chan struct{})
			finished := make(chan gateResult, 1)
			go func() { finished <- g.acquire(ctx, func() { close(waiting) }) }()
			<-waiting
			if kind == "canceled" {
				cancel()
			}
			if kind == "closing" {
				g.close()
			}
			select {
			case result := <-finished:
				if string(result) != kind {
					t.Fatalf("got %s want %s", result, kind)
				}
			case <-time.After(time.Second):
				t.Fatal("waiter stuck")
			}
			if len(g.pending) != 0 || len(g.active) != 1 {
				t.Fatal("waiter leaked slots or released another request")
			}
			g.release()
		})
	}
}

func TestRequestGateDefaultsAndCanceledFreedSlot(t *testing.T) {
	g := newRequestGate(1, 0, 0)
	defer g.close()
	if g.acquire(context.Background(), nil) != gateAdmitted || g.acquire(context.Background(), nil) != gateFull {
		t.Fatal("default rejection behavior changed")
	}
	g.release()
	queued := newRequestGate(1, 1, time.Second)
	defer queued.close()
	_ = queued.acquire(context.Background(), nil)
	ctx, cancel := context.WithCancel(context.Background())
	result := queued.acquire(ctx, func() { cancel(); queued.release() })
	if result != gateCanceled || len(queued.active) != 0 || len(queued.pending) != 0 {
		t.Fatalf("canceled freed slot: %s", result)
	}
	unlimited := newRequestGate(-1, 0, 0)
	unlimited.close()
	if unlimited.acquire(context.Background(), nil) != gateClosing {
		t.Fatal("closed unlimited gate admitted work")
	}
}
