package httpapi

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jaysongiroux/hook_way/internal/queue"
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
