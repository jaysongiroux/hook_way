package replay

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
