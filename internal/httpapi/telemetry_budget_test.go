package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jaysongiroux/hook_way/internal/flowconfig"
	"github.com/jaysongiroux/hook_way/internal/logging"
	"github.com/jaysongiroux/hook_way/internal/metrics"
)

func TestDynamicTelemetryKeepsConfiguredTargetsAndPaths(t *testing.T) {
	m := metrics.New()
	var logs bytes.Buffer
	src := strings.Replace(lookupFlowYAML, "path: /r,", "path: '/r/{customer}',", 1)
	srv, err := newFlowServer(loadFlowCfg(t, src), &fakePublisher{}, slog.New(logging.StormHandler(slog.NewJSONHandler(&logs, nil), m.LogSuppressed)), m, flowconfig.RegistryOptions{
		Lookups: func(flowconfig.LookupOptions) flowconfig.LookupFunc {
			return func(_ context.Context, key string) (string, bool, error) { return "env-" + key, true, nil }
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	baseline := map[string]int{}
	for i := range 1000 {
		marker := fmt.Sprintf("customer-marker-%d", i)
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/r/"+marker+"?token=QUERYSECRET", strings.NewReader(`{"id":"`+marker+`","payload":"PAYLOADSECRET"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Request-ID", marker)
		req.Header.Set("Authorization", "HEADERSECRET")
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != http.StatusAccepted {
			t.Fatalf("varied request%d status%d", i, rec.Code)
		}
		if i == 0 || i == 999 {
			families, gatherErr := m.Registry().Gather()
			if gatherErr != nil {
				t.Fatal(gatherErr)
			}
			for _, family := range families {
				if i == 0 {
					baseline[family.GetName()] = len(family.Metric)
				} else if baseline[family.GetName()] != len(family.Metric) {
					t.Fatalf("%s series grew%d->%d", family.GetName(), baseline[family.GetName()], len(family.Metric))
				}
				for _, sample := range family.Metric {
					for _, label := range sample.Label {
						if strings.Contains(label.GetValue(), "customer-marker-") || strings.Contains(label.GetValue(), "SECRET") {
							t.Fatalf("customer/requestvalue became metriclabel%s", label.GetName())
						}
					}
				}
			}
		}
	}
	if strings.Contains(logs.String(), "PAYLOADSECRET") || strings.Contains(logs.String(), "HEADERSECRET") || strings.Contains(logs.String(), "QUERYSECRET") {
		t.Fatal("body/header/query marker appeared in logs")
	}
	decoder := json.NewDecoder(&logs)
	for decoder.More() {
		var entry map[string]any
		if err := decoder.Decode(&entry); err != nil {
			t.Fatal(err)
		}
		if entry["msg"] == "request handled" && entry["path"] != "/r/{customer}" {
			t.Fatal("request path must use configured template, not caller identity")
		}
	}
	unknown := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/UNKNOWNPATHSECRET", nil)
	var unmatched bytes.Buffer
	srv.logger = slog.New(slog.NewJSONHandler(&unmatched, nil))
	srv.ServeHTTP(httptest.NewRecorder(), unknown)
	if strings.Contains(unmatched.String(), "UNKNOWNPATHSECRET") || !strings.Contains(unmatched.String(), `"path":"unmatched"`) {
		t.Fatal("unmatched path leaked or lost route classification")
	}
}
