package httpapi

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMetricsRejectsDuplicateAuthorizationOnBothListeners(t *testing.T) {
	const key = "metrics-boundary-key"
	for _, dedicated := range []bool{false, true} {
		cfg := loadFlowCfg(t, testFlowYAML)
		cfg.Metrics.Enabled = true
		cfg.Metrics.Path = "/metrics"
		cfg.Security.APIKey = key
		if dedicated {
			cfg.Metrics.Addr = ":0"
		}
		core, err := NewFlowServer(cfg, &fakePublisher{}, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = core.Close() })
		var handler http.Handler = core
		if dedicated {
			handler = core.MetricsServer().Handler
		}
		srv := httptest.NewServer(handler)
		t.Cleanup(srv.Close)
		for _, headers := range [][]string{
			{"Bearer " + key, "Bearer wrong"},
			{"Bearer " + key, "Bearer " + key},
			{"Bearer " + key},
		} {
			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+"/metrics", nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Header["Authorization"] = headers
			resp, err := srv.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			want := http.StatusUnauthorized
			if len(headers) == 1 {
				want = http.StatusOK
			}
			if err != nil || resp.StatusCode != want || strings.Contains(string(body), key) {
				t.Fatalf("dedicated=%v, auth fields=%d: %d %q, err %v", dedicated, len(headers), resp.StatusCode, body, err)
			}
		}
	}
}
