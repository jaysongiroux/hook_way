package queue

import (
	"context"
	"sync"
	"time"
)

// Origin names the flow node that published a message and the inbound
// request it came from. An async webhook delivery keeps it, so a terminal
// failure can be captured for the replay service once the request's final
// status is known.
type Origin struct {
	Flow    string
	NodeID  string
	Settled *Settled
}

type originKey struct{}

// WithOrigin marks ctx with a publish's origin.
func WithOrigin(ctx context.Context, o Origin) context.Context {
	return context.WithValue(ctx, originKey{}, o)
}

// OriginFrom returns the origin WithOrigin stored, if any.
func OriginFrom(ctx context.Context) (Origin, bool) {
	o, ok := ctx.Value(originKey{}).(Origin)
	return o, ok
}

// Settled is a one-shot notice of an inbound request's final status. An
// async delivery can fail before the request that queued it has answered its
// sender, so a failure waits on it before it is kept.
type Settled struct {
	mu      sync.Mutex
	done    bool
	status  int
	waiters []func(status int)
}

func NewSettled() *Settled { return &Settled{} }

// Resolve records the final status and runs the waiters. Only the first call
// has an effect.
func (s *Settled) Resolve(status int) {
	s.mu.Lock()
	if s.done {
		s.mu.Unlock()
		return
	}
	s.done, s.status = true, status
	waiters := s.waiters
	s.waiters = nil
	s.mu.Unlock()
	for _, fn := range waiters {
		fn(status)
	}
}

// AfterSettle runs fn with the final status: now if the request has settled,
// otherwise when it does. fn must not block.
func (s *Settled) AfterSettle(fn func(status int)) {
	s.mu.Lock()
	if !s.done {
		s.waiters = append(s.waiters, fn)
		s.mu.Unlock()
		return
	}
	status := s.status
	s.mu.Unlock()
	fn(status)
}

// TerminalFailure is an async webhook delivery that failed for good — its
// retries exhausted or a terminal outcome — and that no dead-letter
// destination accepted.
type TerminalFailure struct {
	DestinationID  string
	Target         string // as configured (the template, for dynamic targets)
	RenderedTarget string
	Message        Message
	Outcome        string // 4xx, 5xx, 429, timeout, or error
	Attempts       int
	Err            error
	FailedAt       time.Time
	Origin         Origin
}

// TerminalFailureFunc receives terminal async failures on a delivery worker;
// it must not block.
type TerminalFailureFunc func(TerminalFailure)
