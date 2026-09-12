package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/jasongiroux/hookway/internal/config"
	"github.com/jasongiroux/hookway/internal/metrics"
)

// variablesConfig returns a no_auth endpoint "digifi" with a header-sourced
// enumerated variable envName and one templated route to "sink". Callers mutate
// the endpoint for each scenario.
func variablesConfig() config.Config {
	cfg := testConfig()
	cfg.Destinations["sink"] = config.DestinationConfig{Type: "mongodb", Mongo: config.MongoConfig{URI: "mongodb://localhost", Database: "hooks"}}
	cfg.Destinations["archive"] = config.DestinationConfig{Type: "s3", S3: config.S3Config{Region: "us-east-1", Bucket: "archive"}}
	delete(cfg.Endpoints, "paddle_billing")
	cfg.Endpoints["digifi"] = config.EndpointConfig{
		Enabled: true, Path: "/webhooks/digifi",
		Variables: map[string]config.VariableConfig{
			"envName": {Source: "header", Path: "X-Env", Values: []string{"qa-gm3", "staging-portal", "portal"}},
		},
		Destinations: []config.EndpointDestinationConfig{{Destination: "sink", Target: "{envName}-events"}},
		Auth:         config.AuthConfig{Type: "no_auth", Scheme: "no_auth"},
	}
	return cfg
}

func postVariables(t *testing.T, srv http.Handler, path, body string, headers map[string]string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	var decoded map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &decoded)
	return rec, decoded
}

func firstQueue(t *testing.T, resp map[string]any) string {
	t.Helper()
	dests, _ := resp["destinations"].([]any)
	if len(dests) == 0 {
		t.Fatalf("no destinations in response: %v", resp)
	}
	d, _ := dests[0].(map[string]any)
	q, _ := d["queue"].(string)
	return q
}

func TestVariablesHeaderEnumeratedRendersTarget(t *testing.T) {
	pub := &fakePublisher{}
	m := metrics.New()
	srv := NewServerWithMetrics(variablesConfig(), pub, nil, m)
	rec, resp := postVariables(t, srv, "/webhooks/digifi", `{"a":1}`, map[string]string{"X-Env": "qa-gm3"})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	if len(pub.published) != 1 {
		t.Fatalf("expected 1 publish, got %d", len(pub.published))
	}
	got := pub.published[0]
	if got.queue != "qa-gm3-events" || got.msg.Queue != "qa-gm3-events" || got.destinationID != "sink" {
		t.Fatalf("rendered target not passed to publisher: %#v", got)
	}
	if got.msg.Variables["envName"] != "qa-gm3" {
		t.Fatalf("variables not on message: %#v", got.msg.Variables)
	}
	if q := firstQueue(t, resp); q != "qa-gm3-events" {
		t.Fatalf("response queue: %q", q)
	}
	// Metrics label by the authored template, not the rendered target.
	if err := testutil.GatherAndCompare(m.Registry(), strings.NewReader(`
# HELP hookway_publish_total RabbitMQ publish attempts.
# TYPE hookway_publish_total counter
hookway_publish_total{destination_queue_id="sink",endpoint="digifi",queue="{envName}-events",status="success"} 1
`), "hookway_publish_total"); err != nil {
		t.Fatalf("metrics: %v", err)
	}
}

func TestVariablesPathSource(t *testing.T) {
	cfg := variablesConfig()
	ep := cfg.Endpoints["digifi"]
	ep.Path = "/webhooks/{envName}/digifi"
	ep.Variables = map[string]config.VariableConfig{"envName": {Source: "path", Path: "envName", Values: []string{"qa-gm3", "portal"}}}
	cfg.Endpoints["digifi"] = ep
	pub := &fakePublisher{}
	srv := NewServer(cfg, pub, nil)
	rec, _ := postVariables(t, srv, "/webhooks/qa-gm3/digifi", `{}`, nil)
	if rec.Code != http.StatusAccepted || len(pub.published) != 1 || pub.published[0].queue != "qa-gm3-events" {
		t.Fatalf("path variable: status=%d published=%#v", rec.Code, pub.published)
	}
	rec, _ = postVariables(t, srv, "/webhooks/digifi", `{}`, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("non-matching path should 404, got %d", rec.Code)
	}
	// A path segment outside the enumerated values matches the endpoint but
	// fails resolution.
	rec, resp := postVariables(t, srv, "/webhooks/nope/digifi", `{}`, nil)
	if rec.Code != http.StatusBadRequest || resp["error"] != "unresolved_variable" || resp["variable"] != "envName" {
		t.Fatalf("unaccepted path value: %d %v", rec.Code, resp)
	}
}

func TestVariablesQueryAndBodySources(t *testing.T) {
	cfg := variablesConfig()
	ep := cfg.Endpoints["digifi"]
	ep.Variables = map[string]config.VariableConfig{
		"envName": {Source: "query", Path: "env", Values: []string{"qa-gm3"}},
		"tenant":  {Source: "body", Path: "account.id", Pattern: "^[a-z]+$"},
	}
	ep.Destinations = []config.EndpointDestinationConfig{{Destination: "sink", Target: "{envName}-{tenant}"}}
	cfg.Endpoints["digifi"] = ep
	pub := &fakePublisher{}
	srv := NewServer(cfg, pub, nil)
	rec, _ := postVariables(t, srv, "/webhooks/digifi?env=qa-gm3&env=other", `{"account":{"id":"acme"}}`, nil)
	if rec.Code != http.StatusAccepted || len(pub.published) != 1 || pub.published[0].queue != "qa-gm3-acme" {
		t.Fatalf("query+body: status=%d body=%s published=%#v", rec.Code, rec.Body.String(), pub.published)
	}
	// Non-JSON body: the body-sourced variable is unresolved.
	pub.published = nil
	rec, resp := postVariables(t, srv, "/webhooks/digifi?env=qa-gm3", `not json`, map[string]string{"Content-Type": "text/plain"})
	if rec.Code != http.StatusBadRequest || resp["variable"] != "tenant" {
		t.Fatalf("non-JSON body: %d %v", rec.Code, resp)
	}
	if len(pub.published) != 0 {
		t.Fatal("nothing should be published when a variable is unresolved")
	}
}

func TestVariablesPatternRenderedTargetMustBeSafe(t *testing.T) {
	cfg := variablesConfig()
	ep := cfg.Endpoints["digifi"]
	// A permissive pattern that validation would normally reject; the request
	// path must still refuse to render an unsafe target.
	ep.Variables = map[string]config.VariableConfig{"envName": {Source: "header", Path: "X-Env", Pattern: "^.+$"}}
	cfg.Endpoints["digifi"] = ep
	pub := &fakePublisher{}
	srv := NewServer(cfg, pub, nil)
	rec, _ := postVariables(t, srv, "/webhooks/digifi", `{}`, map[string]string{"X-Env": "qa-gm7"})
	if rec.Code != http.StatusAccepted || pub.published[0].queue != "qa-gm7-events" {
		t.Fatalf("pattern render: %d %#v", rec.Code, pub.published)
	}
	pub.published = nil
	rec, resp := postVariables(t, srv, "/webhooks/digifi", `{}`, map[string]string{"X-Env": "a/b"})
	if rec.Code != http.StatusBadRequest || resp["variable"] != "envName" || len(pub.published) != 0 {
		t.Fatalf("unsafe rendered target must be rejected: %d %v %#v", rec.Code, resp, pub.published)
	}
	// A value that smuggles a .fifo suffix onto a template that ends in the
	// placeholder is rejected too.
	ep.Destinations = []config.EndpointDestinationConfig{{Destination: "sink", Target: "events-{envName}"}}
	cfg.Endpoints["digifi"] = ep
	srv = NewServer(cfg, &fakePublisher{}, nil)
	rec, _ = postVariables(t, srv, "/webhooks/digifi", `{}`, map[string]string{"X-Env": "x.fifo"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf(".fifo smuggling must be rejected, got %d", rec.Code)
	}
}

func TestVariablesDefaultApplied(t *testing.T) {
	cfg := variablesConfig()
	ep := cfg.Endpoints["digifi"]
	v := ep.Variables["envName"]
	v.Default = "portal"
	ep.Variables["envName"] = v
	cfg.Endpoints["digifi"] = ep
	pub := &fakePublisher{}
	srv := NewServer(cfg, pub, nil)
	rec, _ := postVariables(t, srv, "/webhooks/digifi", `{}`, nil)
	if rec.Code != http.StatusAccepted || pub.published[0].queue != "portal-events" {
		t.Fatalf("default on missing: %d %#v", rec.Code, pub.published)
	}
	rec, _ = postVariables(t, srv, "/webhooks/digifi", `{}`, map[string]string{"X-Env": "not-allowed"})
	if rec.Code != http.StatusAccepted || pub.published[1].queue != "portal-events" {
		t.Fatalf("default on mismatch: %d %#v", rec.Code, pub.published)
	}
}

func TestVariablesUnresolvedRejectsAndReleasesReplayReservation(t *testing.T) {
	cfg := variablesConfig()
	ep := cfg.Endpoints["digifi"]
	ep.ReplayProtection = config.ReplayProtectionConfig{Enabled: true, TTL: time.Minute, MaxEntries: 100}
	cfg.Endpoints["digifi"] = ep
	pub := &fakePublisher{}
	m := metrics.New()
	srv := NewServerWithMetrics(cfg, pub, nil, m)
	body := `{"id":"evt-1"}`
	rec, resp := postVariables(t, srv, "/webhooks/digifi", body, nil)
	if rec.Code != http.StatusBadRequest || resp["error"] != "unresolved_variable" || resp["variable"] != "envName" {
		t.Fatalf("expected 400 unresolved_variable, got %d %v", rec.Code, resp)
	}
	if len(pub.published) != 0 {
		t.Fatalf("nothing should be published, got %#v", pub.published)
	}
	// The same body with the variable supplied must not be treated as a replay.
	rec, _ = postVariables(t, srv, "/webhooks/digifi", body, map[string]string{"X-Env": "portal"})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("retry after 400 should be accepted, got %d %s", rec.Code, rec.Body.String())
	}
	if err := testutil.GatherAndCompare(m.Registry(), strings.NewReader(`
# HELP hookway_variable_unresolved_total Webhook requests whose routing variables could not be resolved, by action taken (rejected or fallback).
# TYPE hookway_variable_unresolved_total counter
hookway_variable_unresolved_total{action="rejected",endpoint="digifi",variable="envName"} 1
`), "hookway_variable_unresolved_total"); err != nil {
		t.Fatalf("metrics: %v", err)
	}
	// No publish metric was emitted for the rejected request.
	if err := testutil.GatherAndCompare(m.Registry(), strings.NewReader(`
# HELP hookway_publish_total RabbitMQ publish attempts.
# TYPE hookway_publish_total counter
hookway_publish_total{destination_queue_id="sink",endpoint="digifi",queue="{envName}-events",status="success"} 1
`), "hookway_publish_total"); err != nil {
		t.Fatalf("publish metrics: %v", err)
	}
}

func TestVariablesUnresolvedFallbackRoute(t *testing.T) {
	cfg := variablesConfig()
	ep := cfg.Endpoints["digifi"]
	ep.OnUnresolvedVariable = &config.FallbackRouteConfig{Destination: "archive", Target: "unrouted"}
	cfg.Endpoints["digifi"] = ep
	pub := &fakePublisher{}
	m := metrics.New()
	srv := NewServerWithMetrics(cfg, pub, nil, m)
	rec, resp := postVariables(t, srv, "/webhooks/digifi", `{}`, nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("fallback should 202, got %d %s", rec.Code, rec.Body.String())
	}
	if len(pub.published) != 1 || pub.published[0].destinationID != "archive" || pub.published[0].queue != "unrouted" {
		t.Fatalf("expected one publish to the fallback route, got %#v", pub.published)
	}
	if q := firstQueue(t, resp); q != "unrouted" {
		t.Fatalf("response queue: %q", q)
	}
	unresolved, _ := resp["unresolved_variables"].([]any)
	if len(unresolved) != 1 || unresolved[0] != "envName" {
		t.Fatalf("response should list unresolved variables, got %v", resp)
	}
	if err := testutil.GatherAndCompare(m.Registry(), strings.NewReader(`
# HELP hookway_variable_unresolved_total Webhook requests whose routing variables could not be resolved, by action taken (rejected or fallback).
# TYPE hookway_variable_unresolved_total counter
hookway_variable_unresolved_total{action="fallback",endpoint="digifi",variable="envName"} 1
`), "hookway_variable_unresolved_total"); err != nil {
		t.Fatalf("metrics: %v", err)
	}
}

func TestVariablesEndpointFilterWinsOverUnresolved(t *testing.T) {
	cfg := variablesConfig()
	ep := cfg.Endpoints["digifi"]
	ep.Filters = []config.FilterConditionConfig{{Source: "body", Path: "action", Operator: "eq", Value: "opened"}}
	cfg.Endpoints["digifi"] = ep
	pub := &fakePublisher{}
	srv := NewServer(cfg, pub, nil)
	// Filter does not match and no X-Env header: the filter response (202
	// silent drop) wins; variables are never evaluated.
	rec, _ := postVariables(t, srv, "/webhooks/digifi", `{"action":"closed"}`, nil)
	if rec.Code != http.StatusAccepted || len(pub.published) != 0 {
		t.Fatalf("filter drop should win: %d %#v", rec.Code, pub.published)
	}
}

func TestVariablesUnresolvedOnFilteredRouteDoesNotReject(t *testing.T) {
	cfg := variablesConfig()
	ep := cfg.Endpoints["digifi"]
	ep.Destinations = []config.EndpointDestinationConfig{
		{Destination: "sink", Target: "{envName}-events", Filters: []config.FilterConditionConfig{{Source: "body", Path: "action", Operator: "eq", Value: "opened"}}},
		{Destination: "archive", Target: "raw"},
	}
	cfg.Endpoints["digifi"] = ep
	pub := &fakePublisher{}
	srv := NewServer(cfg, pub, nil)
	rec, _ := postVariables(t, srv, "/webhooks/digifi", `{"action":"closed"}`, nil)
	if rec.Code != http.StatusAccepted || len(pub.published) != 1 || pub.published[0].queue != "raw" {
		t.Fatalf("only the static route should deliver: %d %#v", rec.Code, pub.published)
	}
}

func TestVariablesReplayCaptureRecordsRenderedTargetAndVariables(t *testing.T) {
	cfg := variablesConfig()
	ep := cfg.Endpoints["digifi"]
	ep.Destinations = append(ep.Destinations, config.EndpointDestinationConfig{Destination: "archive", Target: "{envName}-raw"})
	cfg.Endpoints["digifi"] = ep
	pub := &fakePublisher{err: errors.New("boom"), errDestination: "archive"}
	fake := &fakeReplayCapturer{}
	srv := NewServer(cfg, pub, nil)
	srv.SetReplayCapturer(fake)
	rec, _ := postVariables(t, srv, "/webhooks/digifi", `{}`, map[string]string{"X-Env": "staging-portal"})
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 from failing archive publish, got %d", rec.Code)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.captured) != 1 || fake.captured[0].Envelope.Variables["envName"] != "staging-portal" {
		t.Fatalf("captured envelope should carry variables: %#v", fake.captured)
	}
	if len(fake.outcomes) != 1 {
		t.Fatalf("expected one outcome record, got %#v", fake.outcomes)
	}
	targets := map[string]string{}
	for _, d := range fake.outcomes[0].deliveries {
		targets[d.DestinationID] = d.Target + ":" + d.Outcome
	}
	if targets["sink"] != "staging-portal-events:delivered" || targets["archive"] != "staging-portal-raw:failed" {
		t.Fatalf("outcomes should use rendered targets: %v", targets)
	}
}

// A variable rendered only into a webhook url (not a route target) must still
// be bounded to a safe name: validation only pins the literal domain suffix, so
// a value with '/', '@', or ':' could otherwise pick an arbitrary host.
func TestVariablesURLOnlyVariableMustBeSafe(t *testing.T) {
	cfg := variablesConfig()
	cfg.Destinations["hook"] = config.DestinationConfig{Type: "webhook", Webhook: config.WebhookConfig{URL: "https://{envName}-api.example.com/hooks"}}
	ep := cfg.Endpoints["digifi"]
	ep.Variables = map[string]config.VariableConfig{"envName": {Source: "header", Path: "X-Env", Pattern: "^.+$"}}
	ep.Destinations = []config.EndpointDestinationConfig{{Destination: "hook", Target: "events"}}
	cfg.Endpoints["digifi"] = ep
	pub := &fakePublisher{}
	srv := NewServer(cfg, pub, nil)
	rec, _ := postVariables(t, srv, "/webhooks/digifi", `{}`, map[string]string{"X-Env": "qa-gm7"})
	if rec.Code != http.StatusAccepted || len(pub.published) != 1 || pub.published[0].msg.Variables["envName"] != "qa-gm7" {
		t.Fatalf("safe url variable: %d %#v", rec.Code, pub.published)
	}
	pub.published = nil
	for _, evil := range []string{"evil.com/x", "evil.com:443@", "a?b", "x#y"} {
		rec, resp := postVariables(t, srv, "/webhooks/digifi", `{}`, map[string]string{"X-Env": evil})
		if rec.Code != http.StatusBadRequest || resp["variable"] != "envName" || len(pub.published) != 0 {
			t.Fatalf("unsafe url variable %q must be rejected: %d %v %#v", evil, rec.Code, resp, pub.published)
		}
	}
}

// A variable referenced only by a temporal workflow_id prefix is needed by the
// route: leaving it unresolved must take the 400/fallback path, not surface as
// a publish failure the sender retries forever.
func TestVariablesTemporalPrefixVariableIsNeeded(t *testing.T) {
	cfg := variablesConfig()
	cfg.Destinations["orch"] = config.DestinationConfig{Type: "temporal", Temporal: config.TemporalConfig{HostPort: "temporal:7233", Namespace: "default"}}
	ep := cfg.Endpoints["digifi"]
	ep.Destinations = []config.EndpointDestinationConfig{{
		Destination:     "orch",
		Target:          "orders",
		TemporalOptions: config.TemporalEndpointOptions{WorkflowType: "W", WorkflowID: config.TemporalWorkflowIDConfig{Prefix: "{envName}-"}},
	}}
	cfg.Endpoints["digifi"] = ep
	pub := &fakePublisher{}
	srv := NewServer(cfg, pub, nil)
	rec, resp := postVariables(t, srv, "/webhooks/digifi", `{}`, nil)
	if rec.Code != http.StatusBadRequest || resp["variable"] != "envName" || len(pub.published) != 0 {
		t.Fatalf("unresolved prefix variable must be rejected before publish: %d %v %#v", rec.Code, resp, pub.published)
	}
	rec, _ = postVariables(t, srv, "/webhooks/digifi", `{}`, map[string]string{"X-Env": "portal"})
	if rec.Code != http.StatusAccepted || len(pub.published) != 1 || pub.published[0].msg.Variables["envName"] != "portal" {
		t.Fatalf("resolved prefix variable must publish: %d %#v", rec.Code, pub.published)
	}
}
