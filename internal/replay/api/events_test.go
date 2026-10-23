package api

import (
	"context"
	"encoding/json"
	"github.com/jaysongiroux/hook_way/internal/eventops"
	"github.com/jaysongiroux/hook_way/internal/replay/auth"
	"github.com/jaysongiroux/hook_way/internal/replay/metrics"
	"net/http/httptest"
	"strings"
	"testing"
)

type eventControlFunc func(context.Context, eventops.Request) (json.RawMessage, int, error)

func (f eventControlFunc) Call(ctx context.Context, q eventops.Request) (json.RawMessage, int, error) {
	return f(ctx, q)
}
func TestEventOperatorScopesAndCallerIsolation(t *testing.T) {
	var captured eventops.Request
	calls := 0
	bridge := eventControlFunc(func(_ context.Context, q eventops.Request) (json.RawMessage, int, error) {
		captured = q
		calls++
		return json.RawMessage(`{"state":"enabled"}`), 200, nil
	})
	h := New(Options{Metrics: metrics.New(), Events: bridge, Auth: auth.New([]auth.Key{{Name: "reader", Token: "reader", Scopes: []auth.Scope{auth.ScopeRead, auth.ScopeEventsRead}}, {Name: "operator", Token: "operator", Scopes: []auth.Scope{auth.ScopeRead, auth.ScopeEventsRead, auth.ScopeEventsPayload, auth.ScopeEventsManage, auth.ScopeEventsRetry}}}, nil)})
	request := func(method, path, token, body string) *httptest.ResponseRecorder {
		q := httptest.NewRequestWithContext(context.Background(), method, path, strings.NewReader(body))
		q.Header.Set("Authorization", "Bearer "+token)
		q.Header.Set("Idempotency-Key", "test")
		q.Header.Set("X-Hookway-Operator", "spoofed")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, q)
		return w
	}
	for _, tc := range []struct {
		method, path, token, body string
		status                    int
	}{
		{"GET", "/v1/mcp-events/status", "native-mcp-key", "", 401},
		{"GET", "/v1/mcp-events/status", "reader", "", 200},
		{"POST", "/v1/mcp-events/subscriptions/sub_1/revoke", "reader", `{"version":"1","reason":"Stop"}`, 403},
		{"GET", "/v1/mcp-events/deliveries/1/payload", "reader", "", 403},
		{"POST", "/v1/mcp-events/deliveries/1/retry", "reader", `{"version":"1","acknowledge_duplicate_risk":true}`, 403},
		{"POST", "/v1/mcp-events/deliveries/1/retry", "operator", `{"version":"1","acknowledge_duplicate_risk":true,"namespace":"other"}`, 400},
		{"GET", "/v1/mcp-events/subscriptions?namespace=other", "operator", "", 400},
		{"GET", "/v1/mcp-events/deliveries?state=failed&state=pending", "operator", "", 400},
		{"GET", "/v1/mcp-events/deliveries?limit=201", "operator", "", 400},
		{"POST", "/v1/mcp-events/deliveries/1/retry", "operator", `{"version":"1","acknowledge_duplicate_risk":true}`, 202},
	} {
		w := request(tc.method, tc.path, tc.token, tc.body)
		if w.Code != tc.status {
			t.Fatalf("%s %s: %d %s", tc.method, tc.path, w.Code, w.Body.String())
		}
	}
	if calls != 2 || captured.Actor != "operator" || captured.ID != "1" {
		t.Fatalf("untrusted caller data reached core: %+v calls %d", captured, calls)
	}
}
func TestEventControlCompatibilityDoesNotDisableReplay(t *testing.T) {
	for _, status := range []int{404, 401, 503} {
		h := New(Options{Metrics: metrics.New(), Events: eventControlFunc(func(context.Context, eventops.Request) (json.RawMessage, int, error) {
			return json.RawMessage(`{}`), status, nil
		}), Auth: auth.New([]auth.Key{{Name: "operator", Token: "operator", Scopes: []auth.Scope{auth.ScopeRead, auth.ScopeEventsRead}}}, nil)})
		for _, path := range []string{"/v1/me", "/v1/mcp-events/status"} {
			r := httptest.NewRequestWithContext(context.Background(), "GET", path, nil)
			r.Header.Set("Authorization", "Bearer operator")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != 200 {
				t.Fatalf("%s unavailable: %d", path, w.Code)
			}
			if path == "/v1/mcp-events/status" {
				want := "unavailable"
				if status == 404 {
					want = "unsupported"
				}
				if !strings.Contains(w.Body.String(), want) {
					t.Fatal(w.Body.String())
				}
			}
		}
	}
}
