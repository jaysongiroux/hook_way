package httpapi

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/jaysongiroux/hook_way/internal/capture"
	"github.com/jaysongiroux/hook_way/internal/metrics"
)

func sampleFlowYAML(rate float64) string {
	return strings.Replace(filterDropReplayYAML,
		"type: filter, conditions: [{source: body, path: action, operator: eq, value: never}], on_no_match: drop",
		fmt.Sprintf("type: sample, rate: %g", rate), 1)
}

func TestSampleHTTPDropMetricsCaptureAndReservation(t *testing.T) {
	for _, override := range []bool{false, true} {
		t.Run(fmt.Sprintf("override=%v", override), func(t *testing.T) {
			var uploads atomic.Int32
			replay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				uploads.Add(1)
				w.WriteHeader(http.StatusCreated)
			}))
			defer replay.Close()
			src := sampleFlowYAML(0) + fmt.Sprintf(`
replay:
  url: %q
  capture_token: capture-token-0123456789abcdefghijkl
  control: {addr: ':9092', token: control-token-0123456789abcdefghij}
`, replay.URL)
			if override {
				src = strings.Replace(src, "  github:\n", "  github:\n    responses:\n      dropped: {status: 200, body: sampled}\n", 1)
			}
			cfg := loadFlowCfg(t, src)
			pub := &fakePublisher{}
			m := metrics.New()
			srv, err := NewFlowServer(cfg, pub, nil, m)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = srv.Close() }()
			c, err := capture.New(capture.Options{Config: cfg, Metrics: m})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = c.Close() }()
			srv.SetCapturer(c)
			body := []byte(`{"action":"opened"}`)
			rec := postSignedFlow(srv, body)
			wantStatus := http.StatusAccepted
			if override {
				wantStatus = http.StatusOK
			}
			if rec.Code != wantStatus || len(pub.published) != 0 {
				t.Fatalf("response=%d %s sends=%d", rec.Code, rec.Body.String(), len(pub.published))
			}
			if override && rec.Body.String() != "sampled" {
				t.Fatal(rec.Body.String())
			}
			metricRec := httptest.NewRecorder()
			promhttp.HandlerFor(m.Registry(), promhttp.HandlerOpts{}).ServeHTTP(metricRec, httptest.NewRequestWithContext(t.Context(), "GET", "/metrics", nil))
			for _, want := range []string{
				`hookway_node_total{flow="github",node_id="filt",node_type="sample",outcome="dropped"} 1`,
				`hookway_flow_outcomes_total{flow="github",outcome="dropped"} 1`,
				`hookway_filtered_total{destination_queue_id="",endpoint="github",level="sampled_out",queue=""} 1`,
			} {
				if !strings.Contains(metricRec.Body.String(), want) {
					t.Fatalf("missing metric %s\n%s", want, metricRec.Body.String())
				}
			}
			if rec := postSignedFlow(srv, body); rec.Code != http.StatusConflict {
				t.Fatalf("reservation lost: %d %s", rec.Code, rec.Body.String())
			}
			if err := c.Close(); err != nil {
				t.Fatal(err)
			}
			if uploads.Load() != 0 {
				t.Fatalf("sample drop captured %d failures", uploads.Load())
			}
		})
	}
}

func TestSampleHTTPKeepAndSiblingDelivery(t *testing.T) {
	for _, tc := range []struct {
		rate    float64
		sibling bool
	}{{1, false}, {0, true}} {
		src := sampleFlowYAML(tc.rate)
		if tc.sibling {
			src = strings.Replace(src, "    edges:", "      - {id: sibling, type: publish, destination: primary, target: sibling}\n    edges:", 1)
			src = strings.Replace(src, "env>filt,", "env>sibling, env>filt,", 1)
		}
		pub := &fakePublisher{}
		srv, err := NewFlowServer(loadFlowCfg(t, src), pub, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		rec := postSignedFlow(srv, []byte(`{"action":"opened"}`))
		_ = srv.Close()
		if rec.Code != http.StatusAccepted || len(pub.published) != 1 {
			t.Fatalf("rate=%g sibling=%v: %d %s sends=%d", tc.rate, tc.sibling, rec.Code, rec.Body.String(), len(pub.published))
		}
	}
}
