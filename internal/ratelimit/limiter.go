package ratelimit

import (
	"sync"
	"time"

	"golang.org/x/time/rate"
)

const defaultIdleTTL = 10 * time.Minute

type Limiter struct {
	enabled   bool
	rate      rate.Limit
	burst     int
	idleTTL   time.Duration
	lastSweep time.Time
	mu        sync.Mutex
	buckets   map[string]*bucket
}

type bucket struct {
	limiter *rate.Limiter
	seen    time.Time
}

// New returns a keyed token-bucket limiter. It uses x/time/rate per key and keeps
// a small wrapper only for key isolation and idle-key eviction.
func New(enabled bool, requestsPerMinute int, burst int) *Limiter {
	return &Limiter{
		enabled: enabled,
		rate:    rate.Limit(float64(requestsPerMinute) / 60),
		burst:   burst,
		idleTTL: defaultIdleTTL,
		buckets: map[string]*bucket{},
	}
}

// Allow reports whether one request for key may proceed at now.
func (l *Limiter) Allow(key string, now time.Time) bool {
	if !l.enabled {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{limiter: rate.NewLimiter(l.rate, l.burst), seen: now}
		l.buckets[key] = b
	}
	b.seen = now
	if l.lastSweep.IsZero() || now.Sub(l.lastSweep) > l.idleTTL {
		l.sweepLocked(now)
	}
	return b.limiter.AllowN(now, 1)
}

// Sweep removes keys that have not been seen within the idle TTL.
func (l *Limiter) Sweep(now time.Time) {
	if !l.enabled {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sweepLocked(now)
}

func (l *Limiter) sweepLocked(now time.Time) {
	for key, bucket := range l.buckets {
		if now.Sub(bucket.seen) > l.idleTTL {
			delete(l.buckets, key)
		}
	}
	l.lastSweep = now
}

// Len returns the number of keyed buckets. It is mainly useful for tests and diagnostics.
func (l *Limiter) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.buckets)
}
