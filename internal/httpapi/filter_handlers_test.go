package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jasongiroux/hookway/internal/config"
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
