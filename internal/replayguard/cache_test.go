package replayguard

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

func TestReleaseUndoesReservationSoRetriesArentTreatedAsReplays(t *testing.T) {
	cache := NewCache(time.Minute, 2)
	now := time.Unix(100, 0)

	if !cache.Accept("endpoint", "signature", now) {
		t.Fatal("first signature rejected")
	}
	// A downstream failure releases the reservation; the sender's retry of the
	// same signature must be accepted again rather than rejected as a replay.
	cache.Release("endpoint", "signature")
	if !cache.Accept("endpoint", "signature", now.Add(time.Second)) {
		t.Fatal("released signature should be accepted on retry")
	}
	// Once committed (not released), a genuine duplicate is still rejected.
	if cache.Accept("endpoint", "signature", now.Add(2*time.Second)) {
		t.Fatal("committed signature should still reject duplicates")
	}
}

func TestReleaseUnknownSignatureIsNoop(t *testing.T) {
	cache := NewCache(time.Minute, 2)
	cache.Release("endpoint", "never-seen") // must not panic
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
