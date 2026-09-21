package httpapi

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jasongiroux/hookway/internal/flowconfig"
	"github.com/jasongiroux/hookway/internal/metrics"
)

const lookupFlowYAML = `
server:
  addr: ":8080"
  max_body_bytes: 1048576
destinations:
  primary:
    type: webhook
    webhook: {url: "https://example.test"}
flows:
  router:
    nodes:
      - {id: in, type: http_trigger, path: /r, methods: [POST]}
      - {id: body, type: read_body, max_bytes: 1048576}
      - {id: auth, type: no_auth}
      - {id: env, type: envelope}
      - {id: lookup, type: lookup, provider: dynamodb, key: '{body.id}', name: route, table: routing}
      - {id: out, type: publish, destination: primary, target: '{route}-applicationServiceQueue'}
    edges: [in>body, body>auth, auth>env, env>lookup, lookup>out]
`

func TestFlowNodeMetricCardinalityDoesNotGrowWithLookupResults(t *testing.T) {
	m := metrics.New()
	srv, err := newFlowServer(loadFlowCfg(t, lookupFlowYAML), &fakePublisher{}, slog.Default(), m, flowconfig.RegistryOptions{
		Lookups: func(flowconfig.LookupOptions) flowconfig.LookupFunc {
			return func(_ context.Context, key string) (string, bool, error) {
				return "env-" + key, true, nil
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	for i := 0; i < 100; i++ {
		body := []byte(fmt.Sprintf(`{"id":"%d"}`, i))
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/r", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != http.StatusAccepted {
			t.Fatalf("request %d status = %d %s", i, rec.Code, rec.Body.String())
		}
	}
	families, err := m.Registry().Gather()
	if err != nil {
		t.Fatal(err)
	}
	var series int
	for _, mf := range families {
		if mf.GetName() != "hookway_node_total" {
			continue
		}
		series = len(mf.Metric)
		for _, metric := range mf.Metric {
			for _, lp := range metric.Label {
				if lp.GetName() == "queue" || lp.GetName() == "target" {
					t.Fatalf("data-dependent label %s=%s", lp.GetName(), lp.GetValue())
				}
			}
		}
	}
	if series > 8 {
		t.Fatalf("hookway_node_total series = %d after 100 distinct lookup results; cardinality grew with data", series)
	}
}
