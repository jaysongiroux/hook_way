package httpapi

import (
	"context"
	"errors"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/jaysongiroux/hook_way/internal/flowconfig"
	"github.com/jaysongiroux/hook_way/internal/routing"
)

type routingReader func(context.Context, routing.Key) (routing.Record, bool, error)

func (f routingReader) Get(ctx context.Context, k routing.Key) (routing.Record, bool, error) {
	return f(ctx, k)
}
func TestRoutingOutcomesMetricsAndAdmissionPreventPublication(t *testing.T) {
	source, err := os.ReadFile("../../examples/routing/shared.yaml")
	if err != nil {
		t.Fatal(err)
	}
	source = []byte(strings.ReplaceAll(string(source), "${SAMPLE_WEBHOOK_SECRET}", "synthetic"))
	source = []byte(strings.ReplaceAll(string(source), `lookup_error: {status: 400, body: '{"error":"routing failed"}'}`, `lookup_error: {status: 200, body: '{"accepted":true}'}`))
	cfg, issues := flowconfig.LoadBytes(source, "test", false)
	if len(issues) != 0 {
		t.Fatal(issues)
	}
	for _, tc := range []struct {
		env, reason string
		found       bool
		backend     error
	}{{"", "miss", false, nil}, {"", "store_error", false, errors.New("outage")}, {"production", "environment_denied", true, nil}, {"unknown", "environment_denied", true, nil}, {"preview", "hit", true, nil}} {
		t.Run(tc.reason+tc.env, func(t *testing.T) {
			pub := &fakePublisher{}
			calls := 0
			srv, err := newFlowServer(cfg, pub, nil, nil, flowconfig.RegistryOptions{RoutingStores: map[string]routing.Reader{"primary": routingReader(func(_ context.Context, k routing.Key) (routing.Record, bool, error) {
				calls++
				if k != (routing.Key{ID: "same", IDType: "record_a"}) {
					t.Fatal(k)
				}
				return routing.Record{Environment: tc.env}, tc.found, tc.backend
			})}})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = srv.Close() }()
			post := func(body string) int {
				r := httptest.NewRequestWithContext(t.Context(), "POST", "/webhooks/sample", strings.NewReader(body))
				r.Header.Set("Authorization", "synthetic")
				w := httptest.NewRecorder()
				srv.ServeHTTP(w, r)
				return w.Code
			}
			if status := post(`{"event":"unused","data":{"object":{"id":"same"}}}`); status != 202 || calls != 0 || len(pub.published) != 0 {
				t.Fatal("drop reached lookup", status, calls)
			}
			status := post(`{"event":"changed","data":{"object":{"id":"same"}}}`)
			if tc.reason == "hit" {
				if status != 202 || len(pub.published) != 1 || pub.published[0].queue != "preview-events" {
					t.Fatal(status, pub.published)
				}
			} else if status != 200 || len(pub.published) != 0 {
				t.Fatal("compatibility response hid unsafe send", status, pub.published)
			}
			w := httptest.NewRecorder()
			promhttp.HandlerFor(srv.metrics.Registry(), promhttp.HandlerOpts{}).ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), "GET", "/metrics", nil))
			if !strings.Contains(w.Body.String(), `node_id="supported",node_type="filter",outcome="dropped"`) {
				t.Fatal("drop reason node missing from metrics", w.Body.String())
			}
			if !strings.Contains(w.Body.String(), `hookway_routing_total{node="routing",result="`+tc.reason+`"} 1`) {
				t.Fatal("missing truthful routing metric", w.Body.String())
			}
		})
	}
}
func TestRoutingStartupOfflineAndMissingBinding(t *testing.T) {
	src, err := os.ReadFile("../../examples/routing/shared.yaml")
	if err != nil {
		t.Fatal(err)
	}
	cfg, issues := flowconfig.LoadBytes(src, "test", false)
	if len(issues) != 0 {
		t.Fatal(issues)
	}
	delete(cfg.Routing.Stores, "primary")
	if _, err := NewFlowServer(cfg, &fakePublisher{}, nil, nil); err == nil {
		t.Fatal("missing native binding accepted")
	}
	if iss := flowconfig.ValidateBytes([]byte(strings.ReplaceAll(string(src), "timeout: 2s", "unknown_option: 1"))); len(iss) == 0 {
		t.Fatal("unknown routing setting accepted")
	}
}
