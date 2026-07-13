package apikey

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
}

func do(t *testing.T, h http.Handler, authHeader string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestEmptyKeyDisablesAuth(t *testing.T) {
	rec := do(t, Middleware("", okHandler()), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("empty key should pass through, got %d", rec.Code)
	}
}

func TestCorrectBearerAllowed(t *testing.T) {
	rec := do(t, Middleware("s3cret", okHandler()), "Bearer s3cret")
	if rec.Code != http.StatusOK {
		t.Fatalf("correct key = %d, want 200", rec.Code)
	}
}

func TestCaseInsensitiveScheme(t *testing.T) {
	rec := do(t, Middleware("s3cret", okHandler()), "bearer s3cret")
	if rec.Code != http.StatusOK {
		t.Fatalf("lowercase scheme = %d, want 200", rec.Code)
	}
}

func TestMissingHeaderRejected(t *testing.T) {
	rec := do(t, Middleware("s3cret", okHandler()), "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("missing header = %d, want 401", rec.Code)
	}
	if rec.Header().Get("WWW-Authenticate") != "Bearer" {
		t.Fatalf("missing WWW-Authenticate: Bearer challenge")
	}
}

func TestWrongKeyRejected(t *testing.T) {
	rec := do(t, Middleware("s3cret", okHandler()), "Bearer nope")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong key = %d, want 401", rec.Code)
	}
}

func TestNonBearerSchemeRejected(t *testing.T) {
	rec := do(t, Middleware("s3cret", okHandler()), "Basic s3cret")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("Basic scheme = %d, want 401", rec.Code)
	}
}
