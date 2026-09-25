// Package replayguard is the in-process duplicate-request cache behind the
// replay_guard flow node.
package replayguard

import (
	"sync"
	"time"
)

type Cache struct {
	window     time.Duration
	maxEntries int
	mu         sync.Mutex
	endpoints  map[string]map[string]time.Time
}

func NewCache(window time.Duration, maxEntries int) *Cache {
	if window <= 0 {
		window = 5 * time.Minute
	}
	if maxEntries <= 0 {
		maxEntries = 1024
	}
	return &Cache{window: window, maxEntries: maxEntries, endpoints: map[string]map[string]time.Time{}}
}

func (c *Cache) Accept(endpoint, signature string, now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	entries, ok := c.endpoints[endpoint]
	if !ok {
		entries = map[string]time.Time{}
		c.endpoints[endpoint] = entries
	}
	c.evictExpired(entries, now)
	if _, ok := entries[signature]; ok {
		return false
	}
	for len(entries) >= c.maxEntries {
		c.evictOldest(entries)
	}
	entries[signature] = now
	return true
}

// Release removes a previously accepted signature. The server calls this when
// publishing fails so a sender's retry of the same event is not rejected as a
// replay; the reservation is only meant to stick once the event is published.
func (c *Cache) Release(endpoint, signature string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if entries, ok := c.endpoints[endpoint]; ok {
		delete(entries, signature)
	}
}

func (c *Cache) evictExpired(entries map[string]time.Time, now time.Time) {
	cutoff := now.Add(-c.window)
	for signature, seen := range entries {
		if seen.Before(cutoff) {
			delete(entries, signature)
		}
	}
}

func (c *Cache) evictOldest(entries map[string]time.Time) {
	var oldestSignature string
	var oldest time.Time
	for signature, seen := range entries {
		if oldestSignature == "" || seen.Before(oldest) {
			oldestSignature = signature
			oldest = seen
		}
	}
	delete(entries, oldestSignature)
}
