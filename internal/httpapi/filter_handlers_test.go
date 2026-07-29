package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/jasongiroux/hookway/internal/config"
	"github.com/jasongiroux/hookway/internal/metrics"
)

// filterConfig returns a testConfig whose paddle_billing endpoint carries the
// given endpoint-level filters, filter response status, and route filters.
func filterConfig(t *testing.T, endpointFilters []config.FilterConditionConfig, responseStatus int, routes []config.EndpointDestinationConfig) config.Config {
	t.Helper()
	cfg := testConfig()
	ep := cfg.Endpoints["paddle_billing"]
	ep.Filters = endpointFilters
	ep.FilterResponse = config.FilterResponseConfig{Status: responseStatus}
	if routes != nil {
		ep.Destinations = routes
	}
	cfg.Endpoints["paddle_billing"] = ep
	return cfg
}

func postSigned(t *testing.T, srv *Server, body string) *httptest.ResponseRecorder {
	t.Helper()
	raw := []byte(body)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/paddle/billing", bytes.NewReader(raw))
	req.Header.Set("Paddle-Signature", signed("secret", time.Now(), raw))
	req.Header.Set("X-GitHub-Event", "push")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

func TestWebhookEndpointFilterDropDefaultsToSilentAck(t *testing.T) {
	cfg := filterConfig(t, []config.FilterConditionConfig{
		{Source: "body", Path: "action", Operator: "eq", Value: "opened"},
	}, 0, nil)
	pub := &fakePublisher{}
	srv := NewServer(cfg, pub, slog.Default())

	rec := postSigned(t, srv, `{"action":"closed"}`)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body = %s", rec.Code, rec.Body.String())
	}
	if len(pub.published) != 0 {
		t.Fatalf("published = %#v, want nothing published", pub.published)
	}
	var res struct {
		Status       string           `json:"status"`
		Destinations []map[string]any `json:"destinations"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.Status != "accepted" || len(res.Destinations) != 0 {
		t.Fatalf("bad response: %s", rec.Body.String())
	}
}

func TestWebhookEndpointFilterDropReturnsConfiguredStatus(t *testing.T) {
	cfg := filterConfig(t, []config.FilterConditionConfig{
		{Source: "body", Path: "action", Operator: "eq", Value: "opened"},
	}, http.StatusUnprocessableEntity, nil)
	// Enable replay protection to verify the reservation is released on rejection.
	ep := cfg.Endpoints["paddle_billing"]
	ep.ReplayProtection = config.ReplayProtectionConfig{Enabled: true, TTL: time.Minute, MaxEntries: 100}
	cfg.Endpoints["paddle_billing"] = ep

	pub := &fakePublisher{}
	srv := NewServer(cfg, pub, slog.Default())

	rec := postSigned(t, srv, `{"action":"closed"}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body = %s", rec.Code, rec.Body.String())
	}
	if len(pub.published) != 0 {
		t.Fatalf("published = %#v, want nothing", pub.published)
	}
	var res struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.Error != "filtered" {
		t.Fatalf("error = %q, want filtered", res.Error)
	}

	// An immediate identical retry must be filtered again (422), not rejected as
	// a replay (409) — the reservation was released.
	retry := postSigned(t, srv, `{"action":"closed"}`)
	if retry.Code != http.StatusUnprocessableEntity {
		t.Fatalf("retry status = %d, want 422 (reservation should be released)", retry.Code)
	}
}

func TestWebhookEndpointFilterPassPublishes(t *testing.T) {
	cfg := filterConfig(t, []config.FilterConditionConfig{
		{Source: "header", Path: "X-GitHub-Event", Operator: "eq", Value: "push"},
		{Source: "body", Path: "repository.private", Operator: "eq", Value: false},
	}, 0, nil)
	pub := &fakePublisher{}
	srv := NewServer(cfg, pub, slog.Default())

	rec := postSigned(t, srv, `{"repository":{"private":false}}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body = %s", rec.Code, rec.Body.String())
	}
	if len(pub.published) != 1 {
		t.Fatalf("published = %#v, want 1", pub.published)
	}
}

func TestWebhookRouteFilterSkipsOnlyThatRoute(t *testing.T) {
	routes := []config.EndpointDestinationConfig{
		{Destination: "billing", Target: "billing-events", Filters: []config.FilterConditionConfig{
			{Source: "body", Path: "ref", Operator: "eq", Value: "refs/heads/main"},
		}},
		{Destination: "activity", Target: "activity-events"},
	}
	cfg := filterConfig(t, nil, 0, routes)
	pub := &fakePublisher{}
	srv := NewServer(cfg, pub, slog.Default())

	rec := postSigned(t, srv, `{"ref":"refs/heads/dev"}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body = %s", rec.Code, rec.Body.String())
	}
	if len(pub.published) != 1 || pub.published[0].destinationID != "activity" {
		t.Fatalf("published = %#v, want only activity", pub.published)
	}
	var res struct {
		Destinations []struct {
			DestinationQueueID string `json:"destination_queue_id"`
		} `json:"destinations"`
		FailedDestinations []map[string]any `json:"failed_destinations"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Destinations) != 1 || res.Destinations[0].DestinationQueueID != "activity" {
		t.Fatalf("response destinations = %s", rec.Body.String())
	}
	if len(res.FailedDestinations) != 0 {
		t.Fatalf("filtered route must not be a failure: %s", rec.Body.String())
	}
}

func TestWebhookAllRoutesFilteredStillAcks(t *testing.T) {
	routes := []config.EndpointDestinationConfig{
		{Destination: "billing", Target: "billing-events", Filters: []config.FilterConditionConfig{
			{Source: "body", Path: "ref", Operator: "eq", Value: "refs/heads/main"},
		}},
		{Destination: "activity", Target: "activity-events", Filters: []config.FilterConditionConfig{
			{Source: "body", Path: "ref", Operator: "eq", Value: "refs/heads/main"},
		}},
	}
	// A configured endpoint filter_response must NOT apply to route filtering.
	cfg := filterConfig(t, nil, http.StatusUnprocessableEntity, routes)
	pub := &fakePublisher{}
	srv := NewServer(cfg, pub, slog.Default())

	rec := postSigned(t, srv, `{"ref":"refs/heads/dev"}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (route filtering always acks); body = %s", rec.Code, rec.Body.String())
	}
	if len(pub.published) != 0 {
		t.Fatalf("published = %#v, want nothing", pub.published)
	}
	var res struct {
		Destinations []map[string]any `json:"destinations"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Destinations) != 0 {
		t.Fatalf("want empty destinations, got %s", rec.Body.String())
	}
}

func TestWebhookRouteFilterWithSkipFailedDoesNotCountAsFailure(t *testing.T) {
	routes := []config.EndpointDestinationConfig{
		{Destination: "billing", Target: "billing-events", Filters: []config.FilterConditionConfig{
			{Source: "body", Path: "ref", Operator: "eq", Value: "refs/heads/main"},
		}},
		{Destination: "activity", Target: "activity-events"},
	}
	cfg := filterConfig(t, nil, 0, routes)
	ep := cfg.Endpoints["paddle_billing"]
	ep.DeliveryFailurePolicy = "skip_failed"
	cfg.Endpoints["paddle_billing"] = ep

	// activity fails to publish; billing is filtered out entirely.
	pub := &fakePublisher{err: errors.New("activity down"), errDestination: "activity"}
	srv := NewServer(cfg, pub, slog.Default())

	rec := postSigned(t, srv, `{"ref":"refs/heads/dev"}`)
	// Only activity was attempted and it failed → all attempted routes failed → 503.
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body = %s", rec.Code, rec.Body.String())
	}
	for _, p := range pub.published {
		if p.destinationID == "billing" {
			t.Fatalf("filtered billing route must not be attempted: %#v", pub.published)
		}
	}
}

func TestWebhookBodyFilterOnNonJSONBodyDrops(t *testing.T) {
	cfg := filterConfig(t, []config.FilterConditionConfig{
		{Source: "body", Path: "action", Operator: "eq", Value: "opened"},
	}, 0, nil)
	pub := &fakePublisher{}
	srv := NewServer(cfg, pub, slog.Default())

	rec := postSigned(t, srv, `not json`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 silent drop; body = %s", rec.Code, rec.Body.String())
	}
	if len(pub.published) != 0 {
		t.Fatalf("published = %#v, want nothing (non-JSON body fails body condition)", pub.published)
	}
}

func TestWebhookNotExistsPassesOnNonJSONBody(t *testing.T) {
	cfg := filterConfig(t, []config.FilterConditionConfig{
		{Source: "body", Path: "action", Operator: "not_exists"},
	}, 0, nil)
	pub := &fakePublisher{}
	srv := NewServer(cfg, pub, slog.Default())

	rec := postSigned(t, srv, `not json`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body = %s", rec.Code, rec.Body.String())
	}
	if len(pub.published) != 1 {
		t.Fatalf("published = %#v, want 1 (not_exists passes on non-JSON body)", pub.published)
	}
}

// TestWebhookEndpointBodyFilterOnNonJSONBodyRecordsBodyUnparsedAndWarns covers
// plan item D9: a source:body endpoint filter evaluated against a body that
// didn't parse as JSON (env.Body == nil) must not change match semantics or
// delivery outcome (kept identical to TestWebhookBodyFilterOnNonJSONBodyDrops
// above), but must surface a distinct signal: the hookway_filtered_total
// label "body_unparsed" instead of "endpoint", plus a warning log — so a
// form-encoded/non-JSON provider isn't silently dropped with no visibility.
func TestWebhookEndpointBodyFilterOnNonJSONBodyRecordsBodyUnparsedAndWarns(t *testing.T) {
	cfg := filterConfig(t, []config.FilterConditionConfig{
		{Source: "body", Path: "action", Operator: "eq", Value: "opened"},
	}, 0, nil)
	pub := &fakePublisher{}
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	m := metrics.New()
	srv := NewServerWithMetrics(cfg, pub, logger, m)

	rec := postSigned(t, srv, `not json`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 silent drop; body = %s", rec.Code, rec.Body.String())
	}
	if len(pub.published) != 0 {
		t.Fatalf("published = %#v, want nothing (match semantics unchanged)", pub.published)
	}
	if err := testutil.GatherAndCompare(m.Registry(), strings.NewReader(`
# HELP hookway_filtered_total Webhook deliveries dropped by endpoint or route filters.
# TYPE hookway_filtered_total counter
hookway_filtered_total{destination_queue_id="billing",endpoint="paddle_billing",level="body_unparsed",queue="billing-events"} 1
`), "hookway_filtered_total"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logs.String(), "body filter evaluated against unparsed body") {
		t.Fatalf("expected a warning log about the unparsed body, got: %s", logs.String())
	}
	if !strings.Contains(logs.String(), `"level":"WARN"`) {
		t.Fatalf("expected the body_unparsed log at WARN level, got: %s", logs.String())
	}
	// D9: the log record's filter_level field must match the metric label
	// (body_unparsed), not the hardcoded "endpoint" literal, so an operator
	// filtering logs on filter_level=body_unparsed actually finds this record.
	if !strings.Contains(logs.String(), `"filter_level":"body_unparsed"`) {
		t.Fatalf("expected log field filter_level=body_unparsed to match the metric label, got: %s", logs.String())
	}
}

// TestWebhookEndpointBodyFilterOnJSONBodyUsesNormalEndpointLabel guards the
// precision requirement in D9: a body filter that legitimately fails against
// a *present* JSON body (not because the body was unparsed) must still use
// the generic "endpoint" label, not "body_unparsed".
func TestWebhookEndpointBodyFilterOnJSONBodyUsesNormalEndpointLabel(t *testing.T) {
	cfg := filterConfig(t, []config.FilterConditionConfig{
		{Source: "body", Path: "action", Operator: "eq", Value: "opened"},
	}, 0, nil)
	pub := &fakePublisher{}
	m := metrics.New()
	srv := NewServerWithMetrics(cfg, pub, slog.Default(), m)

	rec := postSigned(t, srv, `{"action":"closed"}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body = %s", rec.Code, rec.Body.String())
	}
	if err := testutil.GatherAndCompare(m.Registry(), strings.NewReader(`
# HELP hookway_filtered_total Webhook deliveries dropped by endpoint or route filters.
# TYPE hookway_filtered_total counter
hookway_filtered_total{destination_queue_id="billing",endpoint="paddle_billing",level="endpoint",queue="billing-events"} 1
`), "hookway_filtered_total"); err != nil {
		t.Fatal(err)
	}
}

// TestWebhookHeaderFilterOnNonJSONBodyNotFlaggedBodyUnparsed guards the
// precision requirement in D9 from the other side: a header-only filter
// evaluated against a non-JSON body must NOT be flagged as body_unparsed —
// only a body-sourced condition failing against a nil body counts.
func TestWebhookHeaderFilterOnNonJSONBodyNotFlaggedBodyUnparsed(t *testing.T) {
	cfg := filterConfig(t, []config.FilterConditionConfig{
		{Source: "header", Path: "X-GitHub-Event", Operator: "eq", Value: "pull_request"},
	}, 0, nil)
	pub := &fakePublisher{}
	m := metrics.New()
	srv := NewServerWithMetrics(cfg, pub, slog.Default(), m)

	rec := postSigned(t, srv, `not json`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body = %s", rec.Code, rec.Body.String())
	}
	if err := testutil.GatherAndCompare(m.Registry(), strings.NewReader(`
# HELP hookway_filtered_total Webhook deliveries dropped by endpoint or route filters.
# TYPE hookway_filtered_total counter
hookway_filtered_total{destination_queue_id="billing",endpoint="paddle_billing",level="endpoint",queue="billing-events"} 1
`), "hookway_filtered_total"); err != nil {
		t.Fatal(err)
	}
}

// TestWebhookRouteBodyFilterOnNonJSONBodyRecordsBodyUnparsed covers D9 at the
// route-filter level (not just endpoint-level): a route whose filter chain's
// failing condition is body-sourced against a nil body must record
// level="body_unparsed", not level="route".
func TestWebhookRouteBodyFilterOnNonJSONBodyRecordsBodyUnparsed(t *testing.T) {
	routes := []config.EndpointDestinationConfig{
		{Destination: "billing", Target: "billing-events", Filters: []config.FilterConditionConfig{
			{Source: "body", Path: "ref", Operator: "eq", Value: "refs/heads/main"},
		}},
	}
	cfg := filterConfig(t, nil, 0, routes)
	pub := &fakePublisher{}
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	m := metrics.New()
	srv := NewServerWithMetrics(cfg, pub, logger, m)

	rec := postSigned(t, srv, `not json`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body = %s", rec.Code, rec.Body.String())
	}
	if err := testutil.GatherAndCompare(m.Registry(), strings.NewReader(`
# HELP hookway_filtered_total Webhook deliveries dropped by endpoint or route filters.
# TYPE hookway_filtered_total counter
hookway_filtered_total{destination_queue_id="billing",endpoint="paddle_billing",level="body_unparsed",queue="billing-events"} 1
`), "hookway_filtered_total"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logs.String(), "body filter evaluated against unparsed body") {
		t.Fatalf("expected a warning log about the unparsed body, got: %s", logs.String())
	}
	// D9: same requirement as the endpoint-level case above, but for the
	// route-filter warning log site.
	if !strings.Contains(logs.String(), `"filter_level":"body_unparsed"`) {
		t.Fatalf("expected log field filter_level=body_unparsed to match the metric label, got: %s", logs.String())
	}
}
