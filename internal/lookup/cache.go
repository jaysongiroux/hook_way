package lookup

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// CacheOptions bounds the per-provider result cache. TTL applies to hits;
// NegativeTTL to misses (zero disables negative caching, the default, because
// a row is often written moments before the first webhook arrives).
type CacheOptions struct {
	TTL         time.Duration
	NegativeTTL time.Duration
	MaxEntries  int
}

type cacheEntry struct {
	value   string
	found   bool
	expires time.Time
}

// cached wraps a Provider with a bounded TTL cache and singleflight so
// concurrent first lookups of one key make a single provider call. Errors are
// never cached.
type cached struct {
	inner  Provider
	opts   CacheOptions
	now    func() time.Time
	logger *slog.Logger

	mu      sync.Mutex
	entries map[string]cacheEntry
	order   []string // insertion order for eviction
	group   singleflight.Group
}

func newCached(inner Provider, opts CacheOptions, logger *slog.Logger) *cached {
	if opts.TTL <= 0 {
		opts.TTL = 5 * time.Minute
	}
	if opts.MaxEntries <= 0 {
		opts.MaxEntries = 10_000
	}
	return &cached{inner: inner, opts: opts, now: time.Now, logger: logger, entries: map[string]cacheEntry{}}
}

// Lookup returns a cached hit (or cached miss when negative caching is on),
// otherwise calls the provider once per key across concurrent callers.
func (c *cached) Lookup(ctx context.Context, key string) (string, bool, error) {
	if e, ok := c.get(key); ok {
		return e.value, e.found, nil
	}
	type result struct {
		value string
		found bool
	}
	v, err, _ := c.group.Do(key, func() (any, error) {
		if e, ok := c.get(key); ok {
			return result{e.value, e.found}, nil
		}
		value, found, err := c.inner.Lookup(context.WithoutCancel(ctx), key)
		if err != nil {
			return nil, err
		}
		c.put(key, value, found)
		return result{value, found}, nil
	})
	if err != nil {
		return "", false, err
	}
	r := v.(result)
	return r.value, r.found, nil
}

// Cached reports whether key currently has a live cache entry (for metrics).
func (c *cached) Cached(key string) bool {
	_, ok := c.get(key)
	return ok
}

func (c *cached) get(key string) (cacheEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok {
		return cacheEntry{}, false
	}
	// An expired entry stays in place (put overwrites it) so a key never
	// holds two positions in order; deleting here would let a later put
	// re-append the key and FIFO eviction then remove the live entry.
	if c.now().After(e.expires) {
		return cacheEntry{}, false
	}
	return e, true
}

func (c *cached) put(key, value string, found bool) {
	ttl := c.opts.TTL
	if !found {
		if c.opts.NegativeTTL <= 0 {
			return
		}
		ttl = c.opts.NegativeTTL
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.entries[key]; !exists {
		c.order = append(c.order, key)
	}
	c.entries[key] = cacheEntry{value: value, found: found, expires: c.now().Add(ttl)}
	for len(c.entries) > c.opts.MaxEntries && len(c.order) > 0 {
		oldest := c.order[0]
		c.order = c.order[1:]
		delete(c.entries, oldest)
	}
}

func (c *cached) Close() error { return c.inner.Close() }
