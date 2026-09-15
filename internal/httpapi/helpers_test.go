package httpapi

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jasongiroux/hookway/internal/queue"
	"github.com/jasongiroux/hookway/internal/replaystore"
)

type publishedMessage struct {
	destinationID string
	queue         string
	msg           queue.Message
	err           error
}

type fakePublisher struct {
	err            error
	errDestination string
	readyErr       error
	delay          time.Duration
	mu             sync.Mutex
	active         int
	maxConcurrent  int
	published      []publishedMessage
}

func (f *fakePublisher) Publish(ctx context.Context, destinationID, q string, msg queue.Message) error {
	f.mu.Lock()
	f.active++
	if f.active > f.maxConcurrent {
		f.maxConcurrent = f.active
	}
	f.mu.Unlock()
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	defer func() {
		f.mu.Lock()
		f.active--
		f.mu.Unlock()
	}()
	if f.err != nil && (f.errDestination == "" || f.errDestination == destinationID) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.published = append(f.published, publishedMessage{destinationID: destinationID, queue: q, msg: msg, err: f.err})
		return f.err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.published = append(f.published, publishedMessage{destinationID: destinationID, queue: q, msg: msg})
	return nil
}
func (f *fakePublisher) Ready(ctx context.Context) error { return f.readyErr }
func (f *fakePublisher) Close() error                    { return nil }

type countingReadyPublisher struct {
	fakePublisher
	readyCalls atomic.Int64
}

func (c *countingReadyPublisher) Ready(ctx context.Context) error {
	c.readyCalls.Add(1)
	return c.fakePublisher.Ready(ctx)
}

type blockingPublisher struct {
	entered chan struct{}
	release chan struct{}
}

func (b *blockingPublisher) Publish(ctx context.Context, destinationID, q string, msg queue.Message) error {
	b.entered <- struct{}{}
	<-b.release
	return nil
}
func (b *blockingPublisher) Ready(ctx context.Context) error { return nil }
func (b *blockingPublisher) Close() error                    { return nil }

type deadlineAwarePublisher struct {
	mu      sync.Mutex
	invoked bool
	ctxErr  error
}

func (p *deadlineAwarePublisher) Publish(ctx context.Context, destinationID, q string, msg queue.Message) error {
	p.mu.Lock()
	p.invoked = true
	p.ctxErr = ctx.Err()
	p.mu.Unlock()
	return ctx.Err()
}
func (p *deadlineAwarePublisher) Ready(ctx context.Context) error { return nil }
func (p *deadlineAwarePublisher) Close() error                    { return nil }

func (p *deadlineAwarePublisher) snapshot() (invoked bool, ctxErr error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.invoked, p.ctxErr
}

// fakeReplayCapturer records Capture/RecordOutcome and can error or panic so
// tests can prove capture stays best-effort on FlowServer.
type fakeReplayCapturer struct {
	mu sync.Mutex

	captureErr   error
	capturePanic bool
	captured     []replaystore.Event

	outcomeErr   error
	outcomePanic bool
	outcomes     []recordedOutcome

	captureStarted chan struct{}
	captureRelease chan struct{}
}

type recordedOutcome struct {
	id         string
	deliveries []replaystore.Delivery
}

func (f *fakeReplayCapturer) Capture(ctx context.Context, e replaystore.Event) error {
	if f.captureStarted != nil {
		close(f.captureStarted)
	}
	if f.captureRelease != nil {
		<-f.captureRelease
	}
	if f.capturePanic {
		panic("capture panic")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.captureErr != nil {
		return f.captureErr
	}
	f.captured = append(f.captured, e)
	return nil
}

func (f *fakeReplayCapturer) RecordOutcome(ctx context.Context, id string, deliveries []replaystore.Delivery) error {
	if f.outcomePanic {
		panic("outcome panic")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.outcomeErr != nil {
		return f.outcomeErr
	}
	f.outcomes = append(f.outcomes, recordedOutcome{id: id, deliveries: deliveries})
	return nil
}

func (f *fakeReplayCapturer) capturedEvents() []replaystore.Event {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]replaystore.Event, len(f.captured))
	copy(out, f.captured)
	return out
}

func (f *fakeReplayCapturer) recordedOutcomes() []recordedOutcome {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]recordedOutcome, len(f.outcomes))
	copy(out, f.outcomes)
	return out
}
