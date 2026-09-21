package httpapi

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasongiroux/hookway/internal/metrics"
	"github.com/jasongiroux/hookway/internal/queue"
	dto "github.com/prometheus/client_model/go"
)

type nodePanicPublisher struct{ fakePublisher }

func (p *nodePanicPublisher) Publish(context.Context, string, string, queue.Message) error {
	panic("node boom")
}

func TestNodePanicIncrementsNodeLocationAndOutcome(t *testing.T) {
	m := metrics.New()
	srv, err := NewFlowServer(loadFlowCfg(t, testFlowYAML), &nodePanicPublisher{}, slog.New(slog.DiscardHandler), m)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, githubPOST([]byte(`{"ok":true}`)))
	if rec.Code < 500 {
		t.Fatalf("status = %d, want 5xx after node panic", rec.Code)
	}
	if got := counterValue(t, m, "hookway_panics_total", "location", "node"); got != 1 {
		t.Fatalf("hookway_panics_total{location=node} = %v, want 1", got)
	}
	if got := counterValue(t, m, "hookway_panics_total", "location", "handler"); got != 0 {
		t.Fatalf("handler panics = %v, want 0 (node panic must not be counted as handler)", got)
	}
	if got := labelledCounter(t, m, "hookway_node_total", map[string]string{"node_type": "publish", "outcome": "panic"}); got < 1 {
		t.Fatalf("hookway_node_total{outcome=panic} = %v, want >= 1", got)
	}
}

func TestHandlerPanicIncrementsHandlerLocation(t *testing.T) {
	m := metrics.New()
	s := &Server{logger: slog.New(slog.DiscardHandler), metrics: m}
	rw := &recoveringResponseWriter{ResponseWriter: httptest.NewRecorder()}
	func() {
		defer s.recoverHandlerPanic(rw, "req", "trace")
		panic("handler boom")
	}()
	if got := counterValue(t, m, "hookway_panics_total", "location", "handler"); got != 1 {
		t.Fatalf("hookway_panics_total{location=handler} = %v, want 1", got)
	}
	if got := counterValue(t, m, "hookway_panics_total", "location", "node"); got != 0 {
		t.Fatalf("node panics = %v, want 0", got)
	}
}

func TestTraceIDFromRequestRejectsInvalidAndAllZero(t *testing.T) {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/", nil)
	req.Header.Set("X-Trace-ID", strings.Repeat("0", 32))
	got := traceIDFromRequest(req)
	if got == strings.Repeat("0", 32) || len(got) != 32 || !isHex(got) {
		t.Fatalf("all-zero X-Trace-ID adopted or invalid generated id %q", got)
	}

	req = httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/", nil)
	req.Header.Set("X-Trace-ID", "not-a-trace-id")
	got = traceIDFromRequest(req)
	if got == "not-a-trace-id" || len(got) != 32 {
		t.Fatalf("invalid X-Trace-ID adopted: %q", got)
	}

	valid := "4bf92f3577b34da6a3ce929d0e0e4736"
	req = httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/", nil)
	req.Header.Set("X-Trace-ID", valid)
	if got := traceIDFromRequest(req); got != valid {
		t.Fatalf("valid X-Trace-ID = %q, want %q", got, valid)
	}
}

func TestEmptyLoggingAllowlistLogsNoHeadersWhileForwarding(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	cfg := loadFlowCfg(t, testFlowYAML)
	cfg.Security.ForwardedHeaderAllowlist = []string{"Content-Type", "X-Request-ID"}
	cfg.Logging.HeaderAllowlist = nil
	pub := &fakePublisher{}
	srv, err := NewFlowServer(cfg, pub, logger, metrics.New())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	req := githubPOST([]byte(`{"ok":true}`))
	req.Header.Set("X-Custom", "header-should-not-log")
	req.Header.Set("X-Request-ID", "req_fwd")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(logs.String(), "header-should-not-log") {
		t.Fatalf("empty logging.header_allowlist logged a request header: %s", logs.String())
	}
	if len(pub.published) != 1 {
		t.Fatal(pub.published)
	}
	hdrs := pub.published[0].msg.Headers
	contentType, custom := "content-type", "x-custom"
	if _, ok := hdrs[contentType]; !ok {
		t.Fatalf("forwarding allowlist dropped Content-Type: %#v", hdrs)
	}
	if _, ok := hdrs[custom]; ok {
		t.Fatalf("non-allowlisted header still forwarded: %#v", hdrs)
	}
}

type failWriter struct {
	header http.Header
	writes int
}

func (f *failWriter) Header() http.Header {
	if f.header == nil {
		f.header = http.Header{}
	}
	return f.header
}
func (f *failWriter) WriteHeader(int) {}
func (f *failWriter) Write([]byte) (int, error) {
	f.writes++
	return 0, errors.New("write boom")
}

func TestResponseWriteFailureLoggedOnce(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	w := &failWriter{}
	writeJSONLogged(logger, w, http.StatusOK, map[string]string{"ok": "yes"}, "req_1", "trace_1")
	got := logs.String()
	if strings.Count(got, "response write failed") != 1 {
		t.Fatalf("write failures logged %d times, want 1: %s", strings.Count(got, "response write failed"), got)
	}
	if !strings.Contains(got, `"request_id":"req_1"`) || !strings.Contains(got, `"trace_id":"trace_1"`) {
		t.Fatalf("missing ids: %s", got)
	}
}

func counterValue(t *testing.T, m *metrics.Metrics, name, label, value string) float64 {
	t.Helper()
	return labelledCounter(t, m, name, map[string]string{label: value})
}

func labelledCounter(t *testing.T, m *metrics.Metrics, name string, want map[string]string) float64 {
	t.Helper()
	families, err := m.Registry().Gather()
	if err != nil {
		t.Fatal(err)
	}
	var total float64
	for _, mf := range families {
		if mf.GetName() != name {
			continue
		}
		for _, metric := range mf.Metric {
			if !labelsMatch(metric, want) {
				continue
			}
			if metric.Counter != nil {
				total += metric.Counter.GetValue()
			}
		}
	}
	return total
}

func labelsMatch(metric *dto.Metric, want map[string]string) bool {
	got := map[string]string{}
	for _, lp := range metric.Label {
		got[lp.GetName()] = lp.GetValue()
	}
	for k, v := range want {
		if got[k] != v {
			return false
		}
	}
	return true
}
