package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/jaysongiroux/hook_way/internal/metrics"
	"github.com/jaysongiroux/hook_way/internal/queue"
	"github.com/jaysongiroux/hook_way/internal/replaystore"
)

func newReadyFlowServer(t *testing.T, pub queue.Publisher, logger *slog.Logger, m *metrics.Metrics) *FlowServer {
	t.Helper()
	if logger == nil {
		logger = slog.Default()
	}
	srv, err := NewFlowServer(loadFlowCfg(t, testFlowYAML), pub, logger, m)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	return srv
}

func TestReadyServedFromCacheNotPerRequest(t *testing.T) {
	pub := &countingReadyPublisher{}
	srv := newReadyFlowServer(t, pub, nil, nil)
	srv.readiness.probeOnce(context.Background())
	if got := pub.readyCalls.Load(); got != 1 {
		t.Fatalf("Ready calls after one probe = %d, want 1", got)
	}
	for i := 0; i < 50; i++ {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/ready", nil)
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("/ready status = %d, want 200", rec.Code)
		}
	}
	if got := pub.readyCalls.Load(); got != 1 {
		t.Fatalf("Ready calls after 50 /ready requests = %d, want 1 (served from cache)", got)
	}
}

func TestReadyPublicErrorIsGeneric(t *testing.T) {
	var logs bytes.Buffer
	pub := &fakePublisher{readyErr: errors.New("rabbitmq dial tcp secret-url failed")}
	srv := newReadyFlowServer(t, pub, slog.New(slog.NewJSONHandler(&logs, nil)), nil)
	srv.readiness.probeOnce(context.Background())
	for _, path := range []string{"/ready", "/readyz"} {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s status = %d", path, rec.Code)
		}
		if strings.Contains(rec.Body.String(), "rabbitmq") || !strings.Contains(rec.Body.String(), "not ready") {
			t.Fatalf("%s leaked readiness detail: %s", path, rec.Body.String())
		}
	}
	if !strings.Contains(logs.String(), "rabbitmq dial tcp") {
		t.Fatalf("internal readiness reason not logged: %s", logs.String())
	}
	for _, path := range []string{"/health", "/healthz"} {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want liveness OK while not ready", path, rec.Code)
		}
	}
}

func TestReadinessNotReadyUntilFirstProbe(t *testing.T) {
	srv := newReadyFlowServer(t, &fakePublisher{}, nil, nil)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/ready", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("/ready before first probe = %d, want 503", rec.Code)
	}
}

func TestStaleCachedResultCountsAsNotReady(t *testing.T) {
	srv := newReadyFlowServer(t, &fakePublisher{}, nil, nil)
	srv.readiness.result.Store(&readinessResult{checkedAt: time.Now().Add(-time.Hour)})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("/readyz with stale cache = %d, want 503", rec.Code)
	}

	srv.readiness.result.Store(&readinessResult{
		dest: map[string]error{"*": nil}, checkedAt: time.Now(),
	})
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("/readyz with fresh cache = %d, want 200", rec.Code)
	}
}

func TestReadinessGaugePerDestination(t *testing.T) {
	appMetrics := metrics.New()
	up := &fakePublisher{}
	down := &fakePublisher{readyErr: errors.New("down destination unavailable")}
	rp := queue.NewRoutingPublisher(map[string]queue.Publisher{"up_dest": up, "down_dest": down})
	src := `
server:
  addr: ":8080"
  max_body_bytes: 1024
destinations:
  up_dest:
    type: webhook
    webhook: {url: "https://up.test"}
  down_dest:
    type: webhook
    webhook: {url: "https://down.test"}
flows:
  both:
    nodes:
      - {id: in, type: http_trigger, path: /both, methods: [POST]}
      - {id: body, type: read_body, max_bytes: 1024}
      - {id: auth, type: no_auth}
      - {id: env, type: envelope}
      - {id: a, type: publish, destination: up_dest, target: a}
      - {id: b, type: publish, destination: down_dest, target: b}
    edges: [in>body, body>auth, auth>env, env>a, env>b]
`
	srv, err := NewFlowServer(loadFlowCfg(t, src), rp, slog.Default(), appMetrics)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	srv.readiness.probeOnce(context.Background())

	expected := `
# HELP hookway_destination_ready Readiness of each configured message queue destination as observed by the background readiness prober (1 ready, 0 not ready).
# TYPE hookway_destination_ready gauge
hookway_destination_ready{destination_queue_id="down_dest"} 0
hookway_destination_ready{destination_queue_id="up_dest"} 1
`
	if err := testutil.GatherAndCompare(appMetrics.Registry(), strings.NewReader(expected), "hookway_destination_ready"); err != nil {
		t.Fatalf("destination readiness gauge mismatch: %v", err)
	}

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/ready", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("/ready with a down destination on the only flow = %d, want 503", rec.Code)
	}
}

func TestReadinessProberExitsOnContextCancel(t *testing.T) {
	srv := newReadyFlowServer(t, &fakePublisher{}, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := srv.StartReadinessProbe(ctx)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("readiness prober goroutine did not exit after context cancel (leak)")
	}
}

func TestReadinessProbeConvergesImmediatelyOnStart(t *testing.T) {
	srv := newReadyFlowServer(t, &fakePublisher{}, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := srv.StartReadinessProbe(ctx)

	deadline := time.Now().Add(500 * time.Millisecond)
	var lastCode int
	for time.Now().Before(deadline) {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/ready", nil)
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		lastCode = rec.Code
		if lastCode == http.StatusOK {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if lastCode != http.StatusOK {
		t.Fatalf("/ready did not converge to 200 shortly after StartReadinessProbe, last status = %d", lastCode)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("readiness prober goroutine did not exit after context cancel (leak)")
	}
}

func TestStaleReadinessLogsWarningDistinctFromProbeFailure(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	srv := newReadyFlowServer(t, &fakePublisher{}, logger, nil)
	srv.readiness.result.Store(&readinessResult{checkedAt: time.Now().Add(-time.Hour)})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/ready", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("/ready with stale cache = %d, want 503", rec.Code)
	}

	if strings.Contains(logs.String(), "readiness probe failed") {
		t.Fatalf("expected no destination-failure log for a stale-only result, got: %s", logs.String())
	}
	if !strings.Contains(logs.String(), "stale") {
		t.Fatalf("expected a log line about staleness, got: %s", logs.String())
	}
	found := false
	for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		if line == "" {
			continue
		}
		var rec2 map[string]any
		if err := json.Unmarshal([]byte(line), &rec2); err != nil {
			t.Fatalf("log line not valid JSON: %v (%s)", err, line)
		}
		if lvl, _ := rec2["level"].(string); lvl == "WARN" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a WARN-level log line for stale readiness, got: %s", logs.String())
	}
}

func TestStaleReadinessLogsOncePerEpisode(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	srv := newReadyFlowServer(t, &fakePublisher{}, logger, nil)
	srv.readiness.result.Store(&readinessResult{checkedAt: time.Now().Add(-time.Hour)})

	for i := 0; i < 5; i++ {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/ready", nil)
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("/ready with stale cache = %d, want 503", rec.Code)
		}
	}

	count := strings.Count(logs.String(), "stale")
	if count != 1 {
		t.Fatalf("expected exactly 1 staleness log line across 5 stale /ready requests (same episode), got %d in: %s", count, logs.String())
	}
}

type pingCapturer struct {
	err error
}

func (p *pingCapturer) Capture(context.Context, replaystore.Event) error { return nil }
func (p *pingCapturer) RecordOutcome(context.Context, string, []replaystore.Delivery) error {
	return nil
}
func (p *pingCapturer) Ping(context.Context) error { return p.err }

func TestReadinessFailsWhenReplayStoreIsDown(t *testing.T) {
	appMetrics := metrics.New()
	srv := newReadyFlowServer(t, &fakePublisher{}, slog.Default(), appMetrics)
	srv.SetReplayCapturer(&pingCapturer{err: errors.New("replay postgres down")})
	srv.readiness.probeOnce(context.Background())

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/ready", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("/ready with down replay store = %d, want 503", rec.Code)
	}

	expected := `
# HELP hookway_replay_store_up Reachability of the replay store's Postgres connection as observed by the background readiness prober and cleanup sweep (1 up, 0 down).
# TYPE hookway_replay_store_up gauge
hookway_replay_store_up 0
`
	if err := testutil.GatherAndCompare(appMetrics.Registry(), strings.NewReader(expected), "hookway_replay_store_up"); err != nil {
		t.Fatalf("replay store up gauge: %v", err)
	}
}

func TestReadinessPassesWhenReplayStoreIsUp(t *testing.T) {
	appMetrics := metrics.New()
	srv := newReadyFlowServer(t, &fakePublisher{}, slog.Default(), appMetrics)
	srv.SetReplayCapturer(&pingCapturer{})
	srv.readiness.probeOnce(context.Background())

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/ready", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("/ready with up replay store = %d, want 200", rec.Code)
	}

	expected := `
# HELP hookway_replay_store_up Reachability of the replay store's Postgres connection as observed by the background readiness prober and cleanup sweep (1 up, 0 down).
# TYPE hookway_replay_store_up gauge
hookway_replay_store_up 1
`
	if err := testutil.GatherAndCompare(appMetrics.Registry(), strings.NewReader(expected), "hookway_replay_store_up"); err != nil {
		t.Fatalf("replay store up gauge: %v", err)
	}
}
