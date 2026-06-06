package replay

import (
	"testing"
	"time"
)

func TestCacheRejectsDuplicateUntilWindowExpires(t *testing.T) {
	cache := NewCache(time.Minute, 2)
	now := time.Unix(100, 0)

	if !cache.Accept("endpoint", "signature", now) {
		t.Fatal("first signature rejected")
	}
	if cache.Accept("endpoint", "signature", now.Add(time.Second)) {
		t.Fatal("duplicate signature accepted")
	}
	if !cache.Accept("endpoint", "signature", now.Add(2*time.Minute)) {
		t.Fatal("expired signature rejected")
	}
}

func TestCacheIsScopedAndBoundedPerEndpoint(t *testing.T) {
	cache := NewCache(time.Minute, 1)
	now := time.Unix(100, 0)

	if !cache.Accept("a", "signature", now) || !cache.Accept("b", "signature", now) {
		t.Fatal("cache should scope signatures per endpoint")
	}
	if !cache.Accept("a", "other", now.Add(time.Second)) {
		t.Fatal("second signature rejected")
	}
	if !cache.Accept("a", "signature", now.Add(2*time.Second)) {
		t.Fatal("oldest entry should be evicted when endpoint cache is full")
	}
}
