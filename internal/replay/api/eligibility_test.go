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
	f := store.Record{RecordMetadata: testMetadata(), DeliveryStatus: store.StatusOpen, ExpiresAt: now.Add(time.Hour)}
	f.Retryable = true
	a := actionsOf(f, now)
	if !a.Retry.Allowed || a.Resolve.Allowed || !a.Dismiss.Allowed {
		t.Fatal(a)
	}
	f.DeliveryStatus = store.StatusUnknown
	a = actionsOf(f, now)
	if !a.Retry.RequiresDuplicateAcknowledgement || !a.Resolve.Allowed {
		t.Fatal(a)
	}
	f.DeliveryStatus = store.StatusRetrying
	a = actionsOf(f, now)
	if a.Retry.Allowed || a.Resolve.Allowed || a.Dismiss.Allowed {
		t.Fatal(a)
	}
	f.DeliveryStatus = store.StatusUnknown
	f.DismissedAt = &now
	a = actionsOf(f, now)
	if a.Retry.Allowed || a.Retry.RequiresDuplicateAcknowledgement || a.Resolve.Allowed || a.Dismiss.Allowed {
		t.Fatal(a)
	}
	f.DismissedAt = nil
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

func TestLegacyFailureRoutesAreRemoved(t *testing.T) {
	h := newHarness(t, notFoundStore())
	for _, method := range []string{"GET", "POST"} {
		for _, suffix := range []string{"", "/" + testID, "/" + testID + "/payload", "/" + testID + "/retries", "/" + testID + "/resolve", "/" + testID + "/dismiss"} {
			response := h.do(t, method, "/v1/failures"+suffix, opsToken, nil, nil)
			if response.Code != 404 {
				t.Fatalf("legacy route %s %s: %d", method, suffix, response.Code)
			}
		}
	}
}
