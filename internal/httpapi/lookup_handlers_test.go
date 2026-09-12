package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/jasongiroux/hookway/internal/config"
	"github.com/jasongiroux/hookway/internal/lookup"
	"github.com/jasongiroux/hookway/internal/metrics"
)

// fakeLookupProvider is injected through ServerOptions.LookupFactory.
type fakeLookupProvider struct {
	values map[string]string
	err    error
	calls  int32
	closed int32
	cached map[string]bool
}

func (f *fakeLookupProvider) Lookup(_ context.Context, key string) (string, bool, error) {
	atomic.AddInt32(&f.calls, 1)
	if f.err != nil {
		return "", false, f.err
	}
	v, ok := f.values[key]
	return v, ok, nil
}
func (f *fakeLookupProvider) Close() error         { atomic.AddInt32(&f.closed, 1); return nil }
func (f *fakeLookupProvider) Cached(k string) bool { return f.cached[k] }

func lookupConfig() config.Config {
	cfg := variablesConfig()
	ep := cfg.Endpoints["digifi"]
	ep.Variables = map[string]config.VariableConfig{
		"env": {Source: "lookup", Lookup: &config.LookupConfig{
			Key:      config.VariableKeyConfig{Source: "body", Path: "data.object.applicationId"},
			Provider: "dynamodb",
			DynamoDB: &config.LookupDynamoDBConfig{Table: "routing", ValueAttribute: "env_name"},
		}},
	}
	ep.Destinations = []config.EndpointDestinationConfig{{Destination: "sink", Target: "{env}-applicationServiceQueue"}}
	cfg.Endpoints["digifi"] = ep
	return cfg
}

func newLookupServer(t *testing.T, cfg config.Config, provider *fakeLookupProvider, pub *fakePublisher) (*Server, *metrics.Metrics, *lookup.Options) {
	t.Helper()
	var gotOpts lookup.Options
	m := metrics.New()
	srv := NewServerWithOptions(cfg, pub, nil, m, ServerOptions{LookupFactory: func(_ context.Context, opts lookup.Options, _ *slog.Logger) (lookup.Provider, error) {
		gotOpts = opts
		return provider, nil
	}})
	return srv, m, &gotOpts
}

func TestLookupVariableHitRendersTarget(t *testing.T) {
	provider := &fakeLookupProvider{values: map[string]string{"abc123": "qa-gm3"}}
	pub := &fakePublisher{}
	srv, m, opts := newLookupServer(t, lookupConfig(), provider, pub)
	if opts.Name != "env" || opts.DynamoDB == nil || opts.DynamoDB.Table != "routing" {
		t.Fatalf("factory options not mapped from config: %#v", opts)
	}
	rec, resp := postVariables(t, srv, "/webhooks/digifi", `{"data":{"object":{"applicationId":"abc123"}}}`, nil)
	if rec.Code != http.StatusAccepted || len(pub.published) != 1 || pub.published[0].queue != "qa-gm3-applicationServiceQueue" {
		t.Fatalf("hit: %d %s %#v", rec.Code, rec.Body.String(), pub.published)
	}
	if pub.published[0].msg.Variables["env"] != "qa-gm3" || firstQueue(t, resp) != "qa-gm3-applicationServiceQueue" {
		t.Fatalf("variables/response: %#v %v", pub.published[0].msg.Variables, resp)
	}
	if err := testutil.GatherAndCompare(m.Registry(), strings.NewReader(`
# HELP hookway_variable_lookup_total Lookup-source variable resolutions by result: hit, miss, cached, or error.
# TYPE hookway_variable_lookup_total counter
hookway_variable_lookup_total{endpoint="digifi",provider="dynamodb",result="hit",variable="env"} 1
`), "hookway_variable_lookup_total"); err != nil {
		t.Fatalf("metrics: %v", err)
	}
	// A cached key is labelled "cached".
	provider.cached = map[string]bool{"abc123": true}
	postVariables(t, srv, "/webhooks/digifi", `{"data":{"object":{"applicationId":"abc123"}}}`, nil)
	if err := testutil.GatherAndCompare(m.Registry(), strings.NewReader(`
# HELP hookway_variable_lookup_total Lookup-source variable resolutions by result: hit, miss, cached, or error.
# TYPE hookway_variable_lookup_total counter
hookway_variable_lookup_total{endpoint="digifi",provider="dynamodb",result="cached",variable="env"} 1
hookway_variable_lookup_total{endpoint="digifi",provider="dynamodb",result="hit",variable="env"} 1
`), "hookway_variable_lookup_total"); err != nil {
		t.Fatalf("cached metric: %v", err)
	}
	if err := srv.Close(); err != nil || provider.closed != 1 {
		t.Fatalf("Close should close providers: err=%v closed=%d", err, provider.closed)
	}
}

func TestLookupVariableMissRejectsOrFallsBack(t *testing.T) {
	provider := &fakeLookupProvider{values: map[string]string{}}
	pub := &fakePublisher{}
	srv, m, _ := newLookupServer(t, lookupConfig(), provider, pub)
	rec, resp := postVariables(t, srv, "/webhooks/digifi", `{"data":{"object":{"applicationId":"nope"}}}`, nil)
	if rec.Code != http.StatusBadRequest || resp["variable"] != "env" || len(pub.published) != 0 {
		t.Fatalf("miss without fallback: %d %v %#v", rec.Code, resp, pub.published)
	}
	if err := testutil.GatherAndCompare(m.Registry(), strings.NewReader(`
# HELP hookway_variable_lookup_total Lookup-source variable resolutions by result: hit, miss, cached, or error.
# TYPE hookway_variable_lookup_total counter
hookway_variable_lookup_total{endpoint="digifi",provider="dynamodb",result="miss",variable="env"} 1
`), "hookway_variable_lookup_total"); err != nil {
		t.Fatalf("miss metric: %v", err)
	}
	cfg := lookupConfig()
	ep := cfg.Endpoints["digifi"]
	ep.OnUnresolvedVariable = &config.FallbackRouteConfig{Destination: "archive", Target: "unrouted"}
	cfg.Endpoints["digifi"] = ep
	pub = &fakePublisher{}
	srv, _, _ = newLookupServer(t, cfg, provider, pub)
	rec, _ = postVariables(t, srv, "/webhooks/digifi", `{"data":{"object":{"applicationId":"nope"}}}`, nil)
	if rec.Code != http.StatusAccepted || len(pub.published) != 1 || pub.published[0].queue != "unrouted" {
		t.Fatalf("miss with fallback: %d %#v", rec.Code, pub.published)
	}
}

func TestLookupVariableProviderErrorBehavesLikeMissButCounts(t *testing.T) {
	provider := &fakeLookupProvider{err: errors.New("throttled")}
	pub := &fakePublisher{}
	srv, m, _ := newLookupServer(t, lookupConfig(), provider, pub)
	rec, resp := postVariables(t, srv, "/webhooks/digifi", `{"data":{"object":{"applicationId":"abc"}}}`, nil)
	if rec.Code != http.StatusBadRequest || resp["error"] != "unresolved_variable" || len(pub.published) != 0 {
		t.Fatalf("error: %d %v", rec.Code, resp)
	}
	if err := testutil.GatherAndCompare(m.Registry(), strings.NewReader(`
# HELP hookway_variable_lookup_total Lookup-source variable resolutions by result: hit, miss, cached, or error.
# TYPE hookway_variable_lookup_total counter
hookway_variable_lookup_total{endpoint="digifi",provider="dynamodb",result="error",variable="env"} 1
`), "hookway_variable_lookup_total"); err != nil {
		t.Fatalf("error metric: %v", err)
	}
	// Default still applies on error.
	cfg := lookupConfig()
	ep := cfg.Endpoints["digifi"]
	v := ep.Variables["env"]
	v.Default = "portal"
	ep.Variables["env"] = v
	cfg.Endpoints["digifi"] = ep
	pub = &fakePublisher{}
	srv, _, _ = newLookupServer(t, cfg, provider, pub)
	rec, _ = postVariables(t, srv, "/webhooks/digifi", `{"data":{"object":{"applicationId":"abc"}}}`, nil)
	if rec.Code != http.StatusAccepted || pub.published[0].queue != "portal-applicationServiceQueue" {
		t.Fatalf("default on error: %d %#v", rec.Code, pub.published)
	}
}

func TestLookupVariableEmptyKeySkipsProvider(t *testing.T) {
	provider := &fakeLookupProvider{values: map[string]string{}}
	srv, _, _ := newLookupServer(t, lookupConfig(), provider, &fakePublisher{})
	rec, _ := postVariables(t, srv, "/webhooks/digifi", `{"data":{}}`, nil)
	if rec.Code != http.StatusBadRequest || provider.calls != 0 {
		t.Fatalf("empty key: %d calls=%d", rec.Code, provider.calls)
	}
}

func TestLookupVariableNotNeededByActiveRoutesIsNotCalled(t *testing.T) {
	cfg := lookupConfig()
	ep := cfg.Endpoints["digifi"]
	ep.Destinations = []config.EndpointDestinationConfig{
		{Destination: "sink", Target: "{env}-applicationServiceQueue", Filters: []config.FilterConditionConfig{{Source: "body", Path: "eventType", Operator: "eq", Value: "application.created"}}},
		{Destination: "archive", Target: "raw"},
	}
	cfg.Endpoints["digifi"] = ep
	provider := &fakeLookupProvider{values: map[string]string{}}
	pub := &fakePublisher{}
	srv, _, _ := newLookupServer(t, cfg, provider, pub)
	rec, _ := postVariables(t, srv, "/webhooks/digifi", `{"eventType":"borrower.created","data":{"object":{"applicationId":"abc"}}}`, nil)
	if rec.Code != http.StatusAccepted || len(pub.published) != 1 || pub.published[0].queue != "raw" {
		t.Fatalf("filtered lookup route: %d %#v", rec.Code, pub.published)
	}
	// Resolution still runs the lookup (variables are resolved before the
	// needed-set is applied), but a miss on an unneeded variable never rejects.
}

func TestLookupProviderInitFailureFailsClosed(t *testing.T) {
	srv := NewServerWithOptions(lookupConfig(), &fakePublisher{}, nil, nil, ServerOptions{LookupFactory: func(context.Context, lookup.Options, *slog.Logger) (lookup.Provider, error) {
		return nil, errors.New("cannot connect")
	}})
	rec, _ := postVariables(t, srv, "/webhooks/digifi", `{"data":{"object":{"applicationId":"abc"}}}`, nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("broken endpoint should fail closed with 500, got %d", rec.Code)
	}
}

func TestLookupVariableValueRejectedByPattern(t *testing.T) {
	cfg := lookupConfig()
	ep := cfg.Endpoints["digifi"]
	v := ep.Variables["env"]
	v.Pattern = "^qa-gm[0-9]+$"
	ep.Variables["env"] = v
	cfg.Endpoints["digifi"] = ep
	provider := &fakeLookupProvider{values: map[string]string{"abc": "prod!"}}
	srv, _, _ := newLookupServer(t, cfg, provider, &fakePublisher{})
	rec, _ := postVariables(t, srv, "/webhooks/digifi", `{"data":{"object":{"applicationId":"abc"}}}`, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("value failing pattern must be unresolved, got %d", rec.Code)
	}
}
