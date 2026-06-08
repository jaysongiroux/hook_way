package ratelimit

import (
	"sync"
	"time"
)

type Limiter struct {
	enabled bool
	rate    float64
	burst   float64
	mu      sync.Mutex
	buckets map[string]*bucket
}

type bucket struct {
	tokens float64
	seen   time.Time
}

func New(enabled bool, requestsPerMinute int, burst int) *Limiter {
	return &Limiter{
		enabled: enabled,
		rate:    float64(requestsPerMinute) / 60,
		burst:   float64(burst),
		buckets: map[string]*bucket{},
	}
}

func (l *Limiter) Allow(key string, now time.Time) bool {
	if !l.enabled {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.buckets[key]
	if !ok {
		l.buckets[key] = &bucket{tokens: l.burst - 1, seen: now}
		return true
	}
	elapsed := now.Sub(b.seen).Seconds()
	b.tokens += elapsed * l.rate
	if b.tokens > l.burst {
		b.tokens = l.burst
	}
	b.seen = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}
