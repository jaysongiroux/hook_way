package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

const (
	opsToken     = "ops-token-0123456789abcdefghijklmnop"
	captureToken = "capture-token-0123456789abcdefghijkl"
)

func testAuth(t *testing.T) (*Authenticator, *[]string) {
	t.Helper()
	var reasons []string
	a := New([]Key{
		{Name: "ops", Token: opsToken, Scopes: []Scope{ScopeRead, ScopeRetry}},
		{Name: "core-capture", Token: captureToken, Scopes: []Scope{ScopeCapture}},
	}, func(reason string) { reasons = append(reasons, reason) })
	return a, &reasons
}

func call(t *testing.T, h http.Handler, header string) (*httptest.ResponseRecorder, string) {
	t.Helper()
	var actor string
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/failures", nil)
	if header != "" {
		req.Header.Set("Authorization", header)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec, actor
}

func actorEcho() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(Actor(r.Context())))
	})
}

func problemCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body %q is not problem JSON: %v", rec.Body.String(), err)
	}
	return body.Code
}

func TestRequireAcceptsAKeyWithTheScope(t *testing.T) {
	a, reasons := testAuth(t)
	rec, _ := call(t, a.Require(ScopeRead, actorEcho()), "Bearer "+opsToken)
	if rec.Code != http.StatusOK || rec.Body.String() != "ops" {
		t.Fatalf("status %d body %q, want 200 and the key name as actor", rec.Code, rec.Body.String())
	}
	if len(*reasons) != 0 {
		t.Fatalf("failures recorded on success: %v", *reasons)
	}
}

func TestRequireAcceptsACaseInsensitiveScheme(t *testing.T) {
	a, _ := testAuth(t)
	if rec, _ := call(t, a.Require(ScopeRead, actorEcho()), "bearer "+opsToken); rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestRequireRejects(t *testing.T) {
	cases := map[string]struct {
		header, reason, code string
		status               int
	}{
		"no header":     {"", ReasonMissing, "unauthorized", 401},
		"wrong scheme":  {"Basic " + opsToken, ReasonMissing, "unauthorized", 401},
		"empty token":   {"Bearer ", ReasonMissing, "unauthorized", 401},
		"unknown token": {"Bearer nope-0123456789abcdefghijklmnopqrstu", ReasonInvalid, "unauthorized", 401},
		"token prefix":  {"Bearer " + opsToken[:10], ReasonInvalid, "unauthorized", 401},
		"missing scope": {"Bearer " + captureToken, ReasonScope, "forbidden", 403},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			a, reasons := testAuth(t)
			rec, _ := call(t, a.Require(ScopeRead, actorEcho()), c.header)
			if rec.Code != c.status || problemCode(t, rec) != c.code {
				t.Fatalf("status %d code %q, want %d %q", rec.Code, problemCode(t, rec), c.status, c.code)
			}
			if len(*reasons) != 1 || (*reasons)[0] != c.reason {
				t.Fatalf("reasons = %v, want [%s]", *reasons, c.reason)
			}
			if c.status == 401 && rec.Header().Get("WWW-Authenticate") != "Bearer" {
				t.Fatalf("WWW-Authenticate = %q", rec.Header().Get("WWW-Authenticate"))
			}
			if c.status == 403 && rec.Header().Get("WWW-Authenticate") != `Bearer error="insufficient_scope"` {
				t.Fatalf("WWW-Authenticate = %q", rec.Header().Get("WWW-Authenticate"))
			}
		})
	}
}

func TestNilFailureCallbackIsSafe(t *testing.T) {
	a := New([]Key{{Name: "ops", Token: opsToken, Scopes: []Scope{ScopeRead}}}, nil)
	if rec, _ := call(t, a.Require(ScopeRead, actorEcho()), ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestActorIsEmptyOutsideRequire(t *testing.T) {
	if got := Actor(context.Background()); got != "" {
		t.Fatalf("Actor = %q", got)
	}
}

func TestScopesListsEveryScope(t *testing.T) {
	want := []Scope{ScopeCapture, ScopeRead, ScopePayload, ScopeRetry, ScopeMetrics}
	got := Scopes()
	if len(got) != len(want) {
		t.Fatalf("Scopes = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Scopes = %v, want %v", got, want)
		}
	}
}
