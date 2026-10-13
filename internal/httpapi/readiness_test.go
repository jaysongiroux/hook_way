package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/jaysongiroux/hook_way/internal/metrics"
	"github.com/jaysongiroux/hook_way/internal/queue"
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

func sensitiveBackendErrors() map[string]error {
	return map[string]error{
		"URL": fmt.Errorf("backend failed: %w", &url.Error{Op: "Post", URL: `http://user:USERSECRET@backend.example.test/PATHSECRET?api_key=quoted"QUERYSECRET`, Err: context.DeadlineExceeded}), // #nosec G101 -- synthetic redaction markers, not credentials.
		"DSN": fmt.Errorf("connect host=backend.example.test password='DSNSECRET with spaces' token=TOKENSECRET: %w", context.DeadlineExceeded),
		"joined URLs": errors.Join(
			&url.Error{Op: "Post", URL: "http://first.example.test/private/FIRSTSECRET", Err: context.DeadlineExceeded},
			&url.Error{Op: "Post", URL: `http://user:USERSECRET@backend.example.test/PATHSECRET?api_key=quoted"QUERYSECRET`, Err: context.DeadlineExceeded}), // #nosec G101 -- synthetic redaction markers, not credentials.
	}
}

func assertSafeBackendDiagnostic(t *testing.T, logs string) {
	t.Helper()
	for _, marker := range []string{"USERSECRET", "PATHSECRET", "QUERYSECRET", "DSNSECRET", "TOKENSECRET", "FIRSTSECRET", "PAYLOADSECRET"} {
		if strings.Contains(logs, marker) {
			t.Fatalf("backend diagnostic retained %s", marker)
		}
	}
	if !strings.Contains(logs, "backend.example.test") || !strings.Contains(logs, "context deadline exceeded") {
		t.Fatal("backend diagnostic lost its host or underlying failure class")
	}
}

func TestReadinessFailureDiagnosticDoesNotLeakBackendCredentials(t *testing.T) {
	for name, backendErr := range sensitiveBackendErrors() {
		t.Run(name, func(t *testing.T) {
			var logs bytes.Buffer
			pub := &fakePublisher{readyErr: backendErr}
			srv := newReadyFlowServer(t, pub, slog.New(slog.NewJSONHandler(&logs, nil)), nil)
			srv.readiness.probeOnce(t.Context())
			for _, path := range []string{"/ready", "/readyz"} {
				rec := httptest.NewRecorder()
				srv.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil))
				if rec.Code != http.StatusServiceUnavailable || strings.Contains(rec.Body.String(), "backend.example.test") || !strings.Contains(rec.Body.String(), "not ready") {
					t.Fatal("readiness sender status/public detail contract changed")
				}
			}
			if srv.readiness.result.Load().err != backendErr || !errors.Is(pub.readyErr, context.DeadlineExceeded) { //nolint:errorlint // Assert exact error identity survives diagnostic redaction, not wrapped equivalence.
				t.Fatal("readiness diagnostic changed the cached error identity/class")
			}
			assertSafeBackendDiagnostic(t, logs.String())
		})
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

// Readiness depends only on flows and their destinations; nothing outside
// core can take a flow out of rotation.
func TestReadinessReportsOnlyFlowsAndDestinations(t *testing.T) {
	srv := newReadyFlowServer(t, &fakePublisher{}, slog.Default(), metrics.New())
	srv.readiness.probeOnce(context.Background())
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/ready", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("/ready = %d %s, want 200", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "replay_store") {
		t.Fatalf("/ready mentions replay_store: %s", rec.Body.String())
	}
	var body struct {
		Flows map[string]flowReadyState `json:"flows"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Flows) == 0 {
		t.Fatalf("/ready reported no flows: %s", rec.Body.String())
	}
	for name, state := range body.Flows {
		if !state.Ready || len(state.Unready) != 0 {
			t.Fatalf("flow %q = %+v, want ready with nothing unready", name, state)
		}
	}
}
