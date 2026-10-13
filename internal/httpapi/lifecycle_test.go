package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestWithdrawalStopsReadinessButServesUntilDrain(t *testing.T) {
	pub := &fakePublisher{}
	srv := newReadyFlowServer(t, pub, nil, nil)
	srv.readiness.probeOnce(context.Background())
	srv.Withdraw()
	// A background destination probe must not restore readiness during shutdown.
	srv.readiness.probeOnce(context.Background())
	for _, path := range []string{"/ready", "/readyz", "/health", "/healthz"} {
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil))
		expected := http.StatusOK
		if path == "/ready" || path == "/readyz" {
			expected = http.StatusServiceUnavailable
		}
		if rec.Code != expected {
			t.Fatalf("%s = %d; want %d", path, rec.Code, expected)
		}
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, githubPOST([]byte(`{"ok":true}`)))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("withdrawal rejected flow: %d %s", rec.Code, rec.Body.String())
	}
	srv.BeginDrain()
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, githubPOST([]byte(`{"ok":true}`)))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("draining flow = %d", rec.Code)
	}
	srv.Withdraw() // lifecycle transitions cannot restore a previous state
	if srv.lifecycle.Load() != 2 {
		t.Fatal("drain regressed to withdrawal")
	}
}

func TestDrainFinishesAdmittedRequestAndRejectsNewWork(t *testing.T) {
	pub := &gatedPublisher{entered: make(chan struct{}), release: make(chan struct{})}
	srv := newReadyFlowServer(t, pub, nil, nil)
	first := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { defer close(done); srv.ServeHTTP(first, githubPOST([]byte(`{"ok":true}`))) }()
	<-pub.entered
	srv.BeginDrain()
	next := httptest.NewRecorder()
	srv.ServeHTTP(next, githubPOST([]byte(`{"ok":true}`)))
	if next.Code != http.StatusServiceUnavailable {
		t.Fatalf("new flow = %d", next.Code)
	}
	select {
	case <-done:
		t.Fatal("admitted work was interrupted")
	default:
	}
	close(pub.release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("admitted request failed to drain")
	}
	if first.Code != http.StatusAccepted || pub.calls.Load() != 1 {
		t.Fatalf("first=%d publishes=%d", first.Code, pub.calls.Load())
	}
}
