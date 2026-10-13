package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/jaysongiroux/hook_way/internal/metrics"
)

func TestRateLimitCapacityMetricsSeparateFlowsAndBoundCardinality(t *testing.T) {
	src := strings.Replace(testFlowYAML, "type: rate_limit, store: test, rpm: 60", "type: rate_limit, store: test, rpm: 60, max_keys: 1, key_by: header.X-Identity", 1)
	cfg := loadFlowCfg(t, src)
	other := loadFlowCfg(t, strings.Replace(src, "/webhooks/github", "/other", 1))
	cfg.Flows["other"] = other.Flows["github"]
	m := metrics.New()
	srv, err := NewFlowServer(cfg, &fakePublisher{}, nil, m)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	for _, path := range []string{"/webhooks/github", "/other"} {
		for i := 0; i < 100; i++ {
			req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, path, strings.NewReader(`{}`))
			req.Header.Set("X-Identity", fmt.Sprint(i))
			req.Header.Set("X-Hub-Signature-256", githubHMAC([]byte(`{}`)))
			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, req)
			want := http.StatusServiceUnavailable
			if i == 0 {
				want = http.StatusAccepted
			}
			if rec.Code != want {
				t.Fatalf("%s identity %d: status %d want %d, %s", path, i, rec.Code, want, rec.Body.String())
			}
		}
	}
	if err := testutil.GatherAndCompare(m.Registry(), strings.NewReader(`
# HELP hookway_protection_operations_total Protection decisions by configured policy and bounded outcome; never request identities.
# TYPE hookway_protection_operations_total counter
hookway_protection_operations_total{kind="rate_limit",operation="allow",policy="flow/github/node/rl",result="allowed",store="test",strategy="token_bucket"} 1
hookway_protection_operations_total{kind="rate_limit",operation="allow",policy="flow/github/node/rl",result="capacity",store="test",strategy="token_bucket"} 99
hookway_protection_operations_total{kind="rate_limit",operation="allow",policy="flow/other/node/rl",result="allowed",store="test",strategy="token_bucket"} 1
hookway_protection_operations_total{kind="rate_limit",operation="allow",policy="flow/other/node/rl",result="capacity",store="test",strategy="token_bucket"} 99
`), "hookway_protection_operations_total"); err != nil {
		t.Fatal(err)
	}
}
