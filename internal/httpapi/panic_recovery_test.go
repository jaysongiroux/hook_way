package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/jasongiroux/hookway/internal/config"
	"github.com/jasongiroux/hookway/internal/metrics"
	"github.com/jasongiroux/hookway/internal/queue"
)

// panicPublisher panics on every Publish call. Used to exercise ServeHTTP's
// top-level panic recovery (plan item C2).
type panicPublisher struct{}

func (panicPublisher) Publish(context.Context, string, string, queue.Message) error {
	panic("boom: publisher panicked")
}
func (panicPublisher) Ready(context.Context) error { return nil }
func (panicPublisher) Close() error                { return nil }

// TestServeHTTPRecoversHandlerPanicReturns500AndCountsMetric covers C2's
// handler-level recovery. The key assertion is that the test process survives
// the panic (it would otherwise crash the whole test binary); on top of that,
// a panic before any bytes are written must produce a clean 500 and increment
// hookway_panics_total{location="handler"}.
func TestServeHTTPRecoversHandlerPanicReturns500AndCountsMetric(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	m := metrics.New()
	srv := NewServerWithMetrics(testConfig(), panicPublisher{}, logger, m)

	body := []byte(`true`)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/paddle/billing", bytes.NewReader(body))
	req.Header.Set("Paddle-Signature", signed("secret", time.Now(), body))
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req) // must not crash the test process

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body = %s", rec.Code, rec.Body.String())
	}
	var res struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode body: %v; body=%s", err, rec.Body.String())
	}
	if res.Error == "" {
		t.Fatalf("expected an error body, got %s", rec.Body.String())
	}
	if !strings.Contains(logs.String(), `"location":"handler"`) {
		t.Fatalf("expected a panic log with location=handler, got: %s", logs.String())
	}
	if err := testutil.GatherAndCompare(m.Registry(), strings.NewReader(`
# HELP hookway_panics_total Panics recovered by Hookway, by the location they were recovered in.
# TYPE hookway_panics_total counter
hookway_panics_total{location="handler"} 1
`), "hookway_panics_total"); err != nil {
		t.Fatal(err)
	}
}

// panicOnDestinationPublisher panics for one specific destination and
// succeeds for every other. Used to exercise the skip_failed fan-out worker's
// per-job panic recovery.
type panicOnDestinationPublisher struct{ panicOn string }

func (p *panicOnDestinationPublisher) Publish(_ context.Context, destinationID, _ string, _ queue.Message) error {
	if destinationID == p.panicOn {
		panic("boom: fanout publisher panicked")
	}
	return nil
}
func (p *panicOnDestinationPublisher) Ready(context.Context) error { return nil }
func (p *panicOnDestinationPublisher) Close() error                { return nil }

// TestFanoutWorkerRecoversPanicRecordsRouteFailureAndCountsMetric covers C2's
// fan-out worker recovery: a panicking publish for one route must not take
// down the process or wedge the WaitGroup, must be recorded as a route
// failure (not silently dropped), and must not affect the other route.
func TestFanoutWorkerRecoversPanicRecordsRouteFailureAndCountsMetric(t *testing.T) {
	cfg := testConfig()
	endpoint := cfg.Endpoints["paddle_billing"]
	endpoint.DeliveryFailurePolicy = "skip_failed"
	endpoint.Destinations = []config.EndpointDestinationConfig{
		{Destination: "billing", Target: "billing-events"},
		{Destination: "activity", Target: "activity-events"},
	}
	cfg.Endpoints["paddle_billing"] = endpoint
	pub := &panicOnDestinationPublisher{panicOn: "billing"}
	m := metrics.New()
	srv := NewServerWithMetrics(cfg, pub, slog.Default(), m)

	body := []byte(`true`)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/paddle/billing", bytes.NewReader(body))
	req.Header.Set("Paddle-Signature", signed("secret", time.Now(), body))
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req) // must not crash the test process, and must not hang

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (activity succeeded); body = %s", rec.Code, rec.Body.String())
	}
	var res struct {
		Destinations       []map[string]any `json:"destinations"`
		FailedDestinations []map[string]any `json:"failed_destinations"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode body: %v; body=%s", err, rec.Body.String())
	}
	if len(res.Destinations) != 1 || res.Destinations[0]["destination_queue_id"] != "activity" {
		t.Fatalf("expected activity to succeed unaffected by billing's panic: %s", rec.Body.String())
	}
	if len(res.FailedDestinations) != 1 || res.FailedDestinations[0]["destination_queue_id"] != "billing" {
		t.Fatalf("expected billing recorded as a failed route: %s", rec.Body.String())
	}
	if err := testutil.GatherAndCompare(m.Registry(), strings.NewReader(`
# HELP hookway_panics_total Panics recovered by Hookway, by the location they were recovered in.
# TYPE hookway_panics_total counter
hookway_panics_total{location="fanout"} 1
`), "hookway_panics_total"); err != nil {
		t.Fatal(err)
	}
}
