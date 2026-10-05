package auth

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestFailedAuthThrottlesWithoutLockingOutValidCredentials(t *testing.T) {
	a, reasons := testAuth(t)
	now := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	a.failures.now = func() time.Time { return now }
	h := a.Require(ScopeRead, actorEcho())
	request := func(header, remote, forwarded string) *httptest.ResponseRecorder {
		r := httptest.NewRequestWithContext(t.Context(), "GET", "/", nil)
		r.RemoteAddr = remote
		if header != "" {
			r.Header.Set("Authorization", header)
		}
		r.Header.Set("X-Forwarded-For", forwarded)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	for i := 0; i < 10; i++ {
		w := request("Bearer wrong", fmt.Sprintf("192.0.2.1:%d", 1000+i), fmt.Sprintf("198.51.100.%d", i))
		if w.Code != 401 || w.Header().Get("Retry-After") != "" {
			t.Fatalf("attempt %d: %d %v", i, w.Code, w.Header())
		}
	}
	if w := request("", "192.0.2.1:2000", "other"); w.Code != 429 || w.Header().Get("Retry-After") != "6" {
		t.Fatalf("throttle: %d %v", w.Code, w.Header())
	}
	for i := 0; i < 20; i++ {
		if w := request("Bearer "+opsToken, "192.0.2.1:2000", ""); w.Code != 200 || w.Body.String() != "ops" {
			t.Fatalf("valid credential locked out: %d", w.Code)
		}
		if w := request("Bearer "+captureToken, "192.0.2.1:2000", ""); w.Code != 403 || strings.Contains(w.Body.String(), "core-capture") {
			t.Fatalf("scope should stay generic and not throttled: %d %s", w.Code, w.Body)
		}
	}
	now = now.Add(6 * time.Second)
	if w := request("Basic bad", "192.0.2.1:2000", ""); w.Code != 401 {
		t.Fatalf("refill: %d", w.Code)
	}
	if len(*reasons) == 0 || !strings.Contains(strings.Join(*reasons, ","), ReasonThrottled) {
		t.Fatal("throttle metric not recorded")
	}
}

func TestFailedAuthGlobalBoundOverflowAndIdleEviction(t *testing.T) {
	l := newFailureLimiter()
	now := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	l.now = func() time.Time { return now }
	for i := 0; i < 200; i++ {
		if wait := l.reject(fmt.Sprintf("192.0.%d.%d:1", i/256, i%256)); wait != 0 {
			t.Fatal(wait)
		}
	}
	if wait := l.reject("203.0.113.1:1"); wait != 10*time.Millisecond {
		t.Fatalf("global wait %v", wait)
	}
	now = now.Add(10 * time.Millisecond)
	if wait := l.reject("203.0.113.2:1"); wait != 0 {
		t.Fatalf("global refill %v", wait)
	}
	// Let the global bucket refill between unique peers while maintaining a
	// bounded number of per-peer entries and a shared overflow allowance.
	for i := len(l.peers); i < maxFailedPeers+100; i++ {
		now = now.Add(20 * time.Millisecond)
		_ = l.reject(fmt.Sprintf("10.%d.%d.%d:1", i/65536, i/256%256, i%256))
	}
	if len(l.peers) != maxFailedPeers {
		t.Fatalf("peer map size %d", len(l.peers))
	}
	if l.overflow.updated.IsZero() {
		t.Fatal("overflow bucket unused")
	}
	now = now.Add(failedPeerIdle + time.Minute)
	if wait := l.reject("198.51.100.1:1"); wait != 0 || len(l.peers) != 1 {
		t.Fatalf("idle eviction: wait %v peers %d", wait, len(l.peers))
	}
}

func TestAuthLimiterConcurrentInvalidAndValidRequests(t *testing.T) {
	a := New([]Key{{Name: "ops", Token: opsToken, Scopes: []Scope{ScopeRead}}}, nil)
	h := a.Require(ScopeRead, actorEcho())
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r := httptest.NewRequestWithContext(t.Context(), "GET", "/", nil)
			r.RemoteAddr = "192.0.2.1:1234"
			if i%2 == 0 {
				r.Header.Set("Authorization", "Bearer "+opsToken)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if i%2 == 0 && w.Code != 200 {
				t.Errorf("valid credential got %d", w.Code)
			}
		}(i)
	}
	wg.Wait()
}

func TestTokenLimitAndGenericErrors(t *testing.T) {
	a := New([]Key{{Name: "max", Token: strings.Repeat("x", MaxTokenBytes), Scopes: []Scope{ScopeRead}}, {Name: "oversized", Token: strings.Repeat("y", MaxTokenBytes+1), Scopes: []Scope{ScopeRead}}}, nil)
	h := a.Require(ScopeRead, actorEcho())
	if w, _ := call(t, h, "Bearer "+strings.Repeat("x", MaxTokenBytes)); w.Code != 200 {
		t.Fatal(w.Code)
	}
	for _, token := range []string{strings.Repeat("x", MaxTokenBytes+1), strings.Repeat("y", MaxTokenBytes+1), "x y", ""} {
		if w, _ := call(t, h, "Bearer "+token); w.Code != 401 || !strings.Contains(w.Body.String(), "authentication required") {
			t.Fatalf("%d %s", w.Code, w.Body)
		}
	}
	missing, _ := call(t, h, "")
	invalid, _ := call(t, h, "Bearer wrong")
	if missing.Body.String() != invalid.Body.String() {
		t.Fatal("401 body distinguishes credential failures")
	}
}
