package httpapi

import (
	"context"
	"sync"
	"time"
)

type gateResult string

const (
	gateAdmitted gateResult = "admitted"
	gateFull     gateResult = "full"
	gateTimeout  gateResult = "timeout"
	gateCanceled gateResult = "canceled"
	gateClosing  gateResult = "closing"
)

// requestGate separates work executing now from handlers waiting briefly for
// a slot. Waiting uses the existing HTTP goroutine and reads no request body.
// Neither queue size nor execution concurrency grows when the server stalls.
type requestGate struct {
	active, pending chan struct{}
	done            chan struct{}
	once            sync.Once
	mu              sync.Mutex // linearizes immediate admission with close
	wait            time.Duration
}

func newRequestGate(active, pending int, wait time.Duration) *requestGate {
	g := &requestGate{done: make(chan struct{}), wait: wait}
	if active > 0 {
		g.active = make(chan struct{}, active)
	}
	if active > 0 && pending > 0 && wait > 0 {
		g.pending = make(chan struct{}, pending)
	}
	return g
}
func (g *requestGate) acquire(ctx context.Context, onWait func()) gateResult {
	if ctx.Err() != nil {
		return gateCanceled
	}
	g.mu.Lock()
	select {
	case <-g.done:
		g.mu.Unlock()
		return gateClosing
	default:
	}
	if g.active == nil {
		g.mu.Unlock()
		return gateAdmitted
	}
	select {
	case g.active <- struct{}{}:
		g.mu.Unlock()
		return gateAdmitted
	default:
	}
	g.mu.Unlock()
	if g.pending == nil {
		return gateFull
	}
	select {
	case g.pending <- struct{}{}:
	default:
		return gateFull
	}
	defer func() { <-g.pending }()
	if onWait != nil {
		onWait()
	}
	timer := time.NewTimer(g.wait)
	defer timer.Stop()
	select {
	case <-g.done:
		return gateClosing
	case <-ctx.Done():
		return gateCanceled
	case <-timer.C:
		return gateTimeout
	case g.active <- struct{}{}:
		// Cancellation/shutdown can race a newly freed slot. Never start flow
		// processing if either has already happened while this handler waited.
		if ctx.Err() != nil {
			g.release()
			return gateCanceled
		}
		select {
		case <-g.done:
			g.release()
			return gateClosing
		default:
		}
		return gateAdmitted
	}
}
func (g *requestGate) release() {
	if g.active != nil {
		<-g.active
	}
}
func (g *requestGate) close() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.once.Do(func() { close(g.done) })
}
