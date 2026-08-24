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
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/jasongiroux/hookway/internal/metrics"
	"github.com/jasongiroux/hookway/internal/queue"
)

// countingReadyPublisher records how many times Ready is invoked so tests can
// prove /ready is served from cache rather than probing per request.
type countingReadyPublisher struct {
	fakePublisher
	readyCalls atomic.Int64
}

func (c *countingReadyPublisher) Ready(ctx context.Context) error {
	c.readyCalls.Add(1)
	return c.fakePublisher.Ready(ctx)
}

func TestReadyServedFromCacheNotPerRequest(t *testing.T) {
	pub := &countingReadyPublisher{}
	srv := NewServer(testConfig(), pub, slog.Default())
	// One probe cycle populates the cache.
	srv.readiness.probeOnce(context.Background())
	if got := pub.readyCalls.Load(); got != 1 {
		t.Fatalf("Ready calls after one probe = %d, want 1", got)
	}
	for i := 0; i < 50; i++ {
		req := httptest.NewRequest(http.MethodGet, "/ready", nil)
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

func TestReadinessNotReadyUntilFirstProbe(t *testing.T) {
	srv := NewServer(testConfig(), &fakePublisher{}, slog.Default())
	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("/ready before first probe = %d, want 503", rec.Code)
	}
}

func TestStaleCachedResultCountsAsNotReady(t *testing.T) {
	srv := NewServer(testConfig(), &fakePublisher{}, slog.Default())
	// A nil-error result whose timestamp is far older than 3×interval must
	// still be treated as not ready (prober wedged / not running).
	srv.readiness.result.Store(&readinessResult{err: nil, checkedAt: time.Now().Add(-time.Hour)})
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("/readyz with stale cache = %d, want 503", rec.Code)
	}

	// A fresh nil-error result is ready.
	srv.readiness.result.Store(&readinessResult{err: nil, checkedAt: time.Now()})
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("/readyz with fresh cache = %d, want 200", rec.Code)
	}
}

func TestReadinessGaugePerDestination(t *testing.T) {
	appMetrics := metrics.New()
	up := &fakePublisher{}
	down := &fakePublisher{readyErr: errors.New("down destination unavailable")}
	rp := queue.NewRoutingPublisher(map[string]queue.Publisher{"up_dest": up, "down_dest": down})
	srv := NewServerWithMetrics(testConfig(), rp, slog.Default(), appMetrics)
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

	// One destination down => aggregate not ready.
	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("/ready with a down destination = %d, want 503", rec.Code)
	}
}

func TestReadinessProberExitsOnContextCancel(t *testing.T) {
	srv := NewServer(testConfig(), &fakePublisher{}, slog.Default())
	ctx, cancel := context.WithCancel(context.Background())
	done := srv.StartReadinessProbe(ctx)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("readiness prober goroutine did not exit after context cancel (leak)")
	}
}

// TestReadinessProbeConvergesImmediatelyOnStart drives the real
// StartReadinessProbe/run path end-to-end (no seeding the cache directly via
// probeOnce/result.Store) with a fake publisher that reports ready, and
// asserts /ready flips to 200 shortly after start. This guards run()'s
// immediate probe-before-the-first-ticker-fire: without it, /ready would stay
// 503 (errNotProbed) until a full readiness_interval elapsed.
func TestReadinessProbeConvergesImmediatelyOnStart(t *testing.T) {
	srv := NewServer(testConfig(), &fakePublisher{}, slog.Default())
	ctx, cancel := context.WithCancel(context.Background())
	done := srv.StartReadinessProbe(ctx)

	deadline := time.Now().Add(500 * time.Millisecond)
	var lastCode int
	for time.Now().Before(deadline) {
		req := httptest.NewRequest(http.MethodGet, "/ready", nil)
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

// TestStaleReadinessLogsWarningDistinctFromProbeFailure guards item 3 from the
// final review: a /ready 503 caused by a stale (never-refreshed) cached result
// must produce a server-side log signal distinguishing it from an ordinary
// destination-down probe failure, since the public response body is the same
// generic "not ready" either way.
func TestStaleReadinessLogsWarningDistinctFromProbeFailure(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	srv := NewServer(testConfig(), &fakePublisher{}, logger)

	// Seed a fresh, healthy result directly (bypassing probeOnce, so no
	// "readiness probe failed" log is emitted), then age it past the
	// staleness threshold.
	srv.readiness.result.Store(&readinessResult{err: nil, checkedAt: time.Now().Add(-time.Hour)})

	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
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
	var rec2 map[string]any
	found := false
	for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		if line == "" {
			continue
		}
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

// TestStaleReadinessLogsOncePerEpisode ensures repeated /ready polling against
// the same stale cached result does not spam a WARN line on every request —
// only once per staleness episode (i.e. until a fresh probe result replaces
// it).
func TestStaleReadinessLogsOncePerEpisode(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	srv := NewServer(testConfig(), &fakePublisher{}, logger)
	srv.readiness.result.Store(&readinessResult{err: nil, checkedAt: time.Now().Add(-time.Hour)})

	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodGet, "/ready", nil)
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
	fakeReplayCapturer
	err error
}

func (p *pingCapturer) Ping(context.Context) error { return p.err }

func TestReadinessFailsWhenReplayStoreIsDown(t *testing.T) {
	appMetrics := metrics.New()
	srv := NewServerWithMetrics(testConfig(), &fakePublisher{}, slog.Default(), appMetrics)
	srv.SetReplayCapturer(&pingCapturer{err: errors.New("replay postgres down")})
	srv.readiness.probeOnce(context.Background())

	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
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
	srv := NewServerWithMetrics(testConfig(), &fakePublisher{}, slog.Default(), appMetrics)
	srv.SetReplayCapturer(&pingCapturer{})
	srv.readiness.probeOnce(context.Background())

	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
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
