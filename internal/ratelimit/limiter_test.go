package ratelimit

import (
	"testing"
	"time"
)

func TestLimiterAllowsBurstThenRejects(t *testing.T) {
	limiter := New(true, 60, 2)
	now := time.Unix(0, 0)
	if !limiter.Allow("a", now) || !limiter.Allow("a", now) {
		t.Fatal("expected burst to pass")
	}
	if limiter.Allow("a", now) {
		t.Fatal("expected third request to be limited")
	}
}

func TestLimiterSeparatesKeysAndDisabledNoops(t *testing.T) {
	now := time.Unix(0, 0)
	limiter := New(true, 60, 1)
	if !limiter.Allow("a", now) || !limiter.Allow("b", now) {
		t.Fatal("expected separate keys")
	}
	disabled := New(false, 0, 0)
	for i := 0; i < 10; i++ {
		if !disabled.Allow("a", now) {
			t.Fatal("disabled limiter rejected request")
		}
	}
}
