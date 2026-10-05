package api

import (
	"github.com/jaysongiroux/hook_way/internal/replay/auth"
	"github.com/jaysongiroux/hook_way/internal/replay/metrics"
	"github.com/jaysongiroux/hook_way/internal/replay/store"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestActionEligibilityReflectsStateAndExpiry(t *testing.T) {
	now := time.Now()
	f := store.Failure{Status: store.StatusOpen, ExpiresAt: now.Add(time.Hour)}
	f.Retryable = true
	a := actionsOf(f, now)
	if !a.Retry.Allowed || a.Resolve.Allowed || !a.Dismiss.Allowed {
		t.Fatal(a)
	}
	f.Status = store.StatusUnknown
	a = actionsOf(f, now)
	if !a.Retry.RequiresDuplicateAcknowledgement || !a.Resolve.Allowed {
		t.Fatal(a)
	}
	f.Status = store.StatusRetrying
	a = actionsOf(f, now)
	if a.Retry.Allowed || a.Resolve.Allowed || a.Dismiss.Allowed {
		t.Fatal(a)
	}
	f.Status = store.StatusUnknown
	f.ExpiresAt = now
	a = actionsOf(f, now)
	if a.Retry.Allowed || a.Resolve.Allowed || !a.Dismiss.Allowed {
		t.Fatal(a)
	}
}

func TestAPIHasNoUIRoutes(t *testing.T) {
	handler := New(Options{Auth: auth.New(nil, nil), Metrics: metrics.New()})
	for _, path := range []string{"/", "/console", "/console/", "/console/assets/main.js", "/hookway.wasm", "/wasm_exec.js", "/test/reset"} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil))
		if w.Code != http.StatusNotFound {
			t.Fatalf("%s: %d", path, w.Code)
		}
	}
}
