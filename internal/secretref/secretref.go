// Package secretref resolves auth secrets from env, files, or AWS Secrets
// Manager. Resolvers are cached and refreshed on a timer — never fetched per
// request (scalability rule R4). Rotation overlap returns both the current
// and previous values so a sender still signing with the old secret is
// accepted (D7). Refresh failures keep the last known-good value.
package secretref

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

const defaultRefresh = 15 * time.Minute

// Resolver returns the currently-valid secrets for a reference. The slice
// exists so rotation can overlap: during the window both the new and previous
// secret must verify, or rotation is a flag day.
type Resolver interface {
	Secrets(ctx context.Context) ([][]byte, error)
	Close() error
}

// Ref is the authored secret_ref object.
type Ref struct {
	Provider string        // env | file | aws_secrets_manager
	Name     string
	Refresh  time.Duration // default 15m
	Overlap  time.Duration // how long the previous secret stays valid
}

// fetcher loads the current secret value from a backing store. Implementations
// must not cache; the wrapper below owns the cache and overlap window.
type fetcher interface {
	Fetch(ctx context.Context) ([]byte, error)
}

type cached struct {
	fetch    fetcher
	refresh  time.Duration
	overlap  time.Duration
	now      func() time.Time
	logger   *slog.Logger
	onFail   func()
	provider string
	name     string

	mu            sync.RWMutex
	current       []byte
	previous      []byte
	previousUntil time.Time

	stop   context.CancelFunc
	stopped chan struct{}
}

// New builds a resolver, fetches once so serving never starts without a
// secret, then refreshes in the background. A later refresh failure keeps
// the last good value and invokes onFail (metric + log live in the caller).
func New(ref Ref, logger *slog.Logger, onFail func()) (Resolver, error) {
	if logger == nil {
		logger = slog.Default()
	}
	if ref.Name == "" {
		return nil, errors.New("secret_ref.name is required")
	}
	if ref.Refresh < 0 || ref.Overlap < 0 {
		return nil, errors.New("secret_ref refresh and overlap must be non-negative")
	}
	if ref.Refresh == 0 {
		ref.Refresh = defaultRefresh
	}
	f, err := newFetcher(ref)
	if err != nil {
		return nil, err
	}
	return newCached(ref, f, logger, onFail, time.Now)
}

func newFetcher(ref Ref) (fetcher, error) {
	switch ref.Provider {
	case "env":
		return envFetcher{name: ref.Name}, nil
	case "file":
		return fileFetcher{path: ref.Name}, nil
	case "aws_secrets_manager":
		return newAWSFetcher(ref.Name)
	default:
		return nil, fmt.Errorf("secret_ref.provider %q is not supported", ref.Provider)
	}
}

func newCached(ref Ref, f fetcher, logger *slog.Logger, onFail func(), now func() time.Time) (*cached, error) {
	value, err := f.Fetch(context.Background())
	if err != nil {
		return nil, fmt.Errorf("secret_ref %s %q: %w", ref.Provider, ref.Name, err)
	}
	if len(value) == 0 {
		return nil, fmt.Errorf("secret_ref %s %q: empty secret", ref.Provider, ref.Name)
	}
	ctx, cancel := context.WithCancel(context.Background())
	c := &cached{
		fetch:    f,
		refresh:  ref.Refresh,
		overlap:  ref.Overlap,
		now:      now,
		logger:   logger,
		onFail:   onFail,
		provider: ref.Provider,
		name:     ref.Name,
		current:  append([]byte(nil), value...),
		stop:     cancel,
		stopped:  make(chan struct{}),
	}
	go c.loop(ctx)
	return c, nil
}

func (c *cached) loop(ctx context.Context) {
	defer close(c.stopped)
	ticker := time.NewTicker(c.refresh)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.refreshOnce(ctx)
		}
	}
}

func (c *cached) refreshOnce(ctx context.Context) {
	value, err := c.fetch.Fetch(ctx)
	if err != nil || len(value) == 0 {
		if err == nil {
			err = errors.New("empty secret")
		}
		c.logger.Error("secret_ref refresh failed; serving last known-good value",
			"provider", c.provider, "name", c.name, "error", err.Error())
		if c.onFail != nil {
			c.onFail()
		}
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if bytes.Equal(c.current, value) {
		return
	}
	c.previous = append([]byte(nil), c.current...)
	c.previousUntil = c.now().Add(c.overlap)
	c.current = append([]byte(nil), value...)
}

// Secrets returns a copy of the currently-valid secrets. It never calls the
// backing store.
func (c *cached) Secrets(context.Context) ([][]byte, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := [][]byte{append([]byte(nil), c.current...)}
	if len(c.previous) > 0 && c.now().Before(c.previousUntil) {
		out = append(out, append([]byte(nil), c.previous...))
	}
	return out, nil
}

func (c *cached) Close() error {
	if c.stop != nil {
		c.stop()
		<-c.stopped
	}
	return nil
}
