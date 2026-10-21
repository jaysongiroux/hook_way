package control

import (
	"context"
	"encoding/json"
	"github.com/jaysongiroux/hook_way/internal/eventops"
	"net/http/httptest"
	"strings"
	"testing"
)

type operatorFunc func(context.Context, eventops.Request) (any, error)

func (f operatorFunc) Operate(ctx context.Context, q eventops.Request) (any, error) { return f(ctx, q) }
func TestPrivateEventControlRequiresControlCredential(t *testing.T) {
	calls := 0
	s := New(Options{Token: "private-control-token", Events: operatorFunc(func(_ context.Context, q eventops.Request) (any, error) {
		calls++
		if q.Actor != "operator" {
			t.Fatal("missing actor")
		}
		return eventops.Status{State: "enabled"}, nil
	})})
	raw, _ := json.Marshal(eventops.Request{Operation: "status", Actor: "operator"})
	for _, token := range []string{"", "native-mcp-key", "operator-key", "private-control-token"} {
		r := httptest.NewRequestWithContext(context.Background(), "POST", eventops.ControlPath, strings.NewReader(string(raw)))
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		s.HTTPServer().Handler.ServeHTTP(w, r)
		want := 401
		if token == "private-control-token" {
			want = 200
		}
		if w.Code != want {
			t.Fatalf("status %d", w.Code)
		}
	}
	if calls != 1 {
		t.Fatal("invalid credential reached runtime")
	}
}
