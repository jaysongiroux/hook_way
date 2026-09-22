package config

import (
	"strings"
	"testing"
	"time"
)

// The v1 `endpoints:` YAML loader is gone, so per-request routing variables are
// validated by building the endpoint directly and calling validationIssues --
// the same entry SharedConfig.ValidationIssues uses.

var variableDestinations = map[string]DestinationConfig{
	"sink":  {Type: "s3", S3: S3Config{Region: "us-east-1", Bucket: "events"}},
	"queue": {Type: "sqs", SQS: SQSConfig{Region: "us-east-1"}},
	"mq":    {Type: "rabbitmq", RabbitMQ: RabbitMQConfig{URL: "amqp://x", Exchange: "e", ExchangeType: "direct"}},
	"wf": {Type: "temporal", Temporal: TemporalConfig{
		HostPort: "localhost:7233", Namespace: "default",
		ConnectTimeout: 10 * time.Second, StartTimeout: 5 * time.Second,
	}},
	"hub": {Type: "webhook", Webhook: WebhookConfig{URL: "https://{envName}-api.example.com/v3/events"}},
}

// varEndpoint builds a Config with one enabled endpoint declaring variables.
func varEndpoint(path string, variables map[string]VariableConfig, fallback *FallbackRouteConfig, routes ...EndpointDestinationConfig) Config {
	if len(routes) == 0 {
		routes = []EndpointDestinationConfig{{Destination: "sink", Target: "events-{envName}"}}
	}
	return Config{
		Server:       ServerConfig{Addr: ":8080", MaxBodyBytes: 1024},
		Destinations: variableDestinations,
		Endpoints: map[string]EndpointConfig{"e": {
			Enabled: true, Path: path,
			Auth:                 AuthConfig{Type: "no_auth", Scheme: "no_auth"},
			Variables:            variables,
			OnUnresolvedVariable: fallback,
			Destinations:         routes,
		}},
	}
}

func enumVar() map[string]VariableConfig {
	return map[string]VariableConfig{
		"envName": {Source: "header", Path: "X-Env", Values: []string{"tenant-a", "staging"}},
	}
}

func patternVar() map[string]VariableConfig {
	return map[string]VariableConfig{
		"envName": {Source: "header", Path: "X-Env", Pattern: "^[a-z0-9-]+$"},
	}
}

func lookupVar(mutate func(*LookupConfig)) map[string]VariableConfig {
	lc := LookupConfig{
		Key:      VariableKeyConfig{Source: "body", Path: "data.object.accountId"},
		Provider: "dynamodb",
		Timeout:  2 * time.Second,
		Cache:    LookupCacheConfig{TTL: 5 * time.Minute},
		DynamoDB: &LookupDynamoDBConfig{Table: "routing", KeyAttribute: "id", ValueAttribute: "env_name", Region: "us-east-1"},
	}
	if mutate != nil {
		mutate(&lc)
	}
	return map[string]VariableConfig{"envName": {Source: "lookup", Lookup: &lc}}
}

func TestVariablesValidateCleanly(t *testing.T) {
	cases := map[string]Config{
		"enumerated on s3":      varEndpoint("/webhooks/events", enumVar(), nil),
		"pattern on s3":         varEndpoint("/webhooks/events", patternVar(), nil),
		"path source":           varEndpoint("/webhooks/{envName}/events", map[string]VariableConfig{"envName": {Source: "path", Path: "envName", Pattern: "^[a-z0-9-]+$"}}, nil),
		"body source":           varEndpoint("/webhooks/events", map[string]VariableConfig{"envName": {Source: "body", Path: "account.env", Values: []string{"tenant-a"}}}, nil),
		"query source":          varEndpoint("/webhooks/events", map[string]VariableConfig{"envName": {Source: "query", Path: "env", Values: []string{"tenant-a"}}}, nil),
		"lookup source":         varEndpoint("/webhooks/events", lookupVar(nil), nil),
		"webhook url reference": varEndpoint("/webhooks/events", enumVar(), nil, EndpointDestinationConfig{Destination: "hub", Target: "events"}),
		"temporal prefix reference": varEndpoint("/webhooks/events", enumVar(), nil, EndpointDestinationConfig{
			Destination: "wf", Target: "orders",
			TemporalOptions: TemporalEndpointOptions{WorkflowType: "OrderWorkflow", WorkflowID: TemporalWorkflowIDConfig{Prefix: "{envName}-"}},
		}),
		"fallback route": varEndpoint("/webhooks/events", enumVar(), &FallbackRouteConfig{Destination: "sink", Target: "unrouted"}),
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			if issues := validationIssues(cfg); len(issues) != 0 {
				t.Fatalf("valid variables config rejected: %#v", issues)
			}
		})
	}
}

func TestLoadRejectsInvalidVariables(t *testing.T) {
	v := func(mutate func(*VariableConfig)) map[string]VariableConfig {
		vc := VariableConfig{Source: "header", Path: "X-Env", Values: []string{"tenant-a"}}
		mutate(&vc)
		return map[string]VariableConfig{"envName": vc}
	}
	cases := []struct {
		name string
		cfg  Config
		want string
	}{
		{"bad name", varEndpoint("/webhooks/events", map[string]VariableConfig{
			"9bad": {Source: "header", Path: "X-Env", Values: []string{"a"}},
		}, nil), "variables.9bad"},
		{"reserved name", varEndpoint("/webhooks/events", map[string]VariableConfig{
			"target": {Source: "header", Path: "X-Env", Values: []string{"a"}},
		}, nil), "variables.target"},
		{"missing source", varEndpoint("/webhooks/events", v(func(c *VariableConfig) { c.Source = "" }), nil), "variables.envName.source"},
		{"bad source", varEndpoint("/webhooks/events", v(func(c *VariableConfig) { c.Source = "cookie" }), nil), "variables.envName.source"},
		{"bad header path", varEndpoint("/webhooks/events", v(func(c *VariableConfig) { c.Path = "X Env" }), nil), "variables.envName.path"},
		{"empty body path segment", varEndpoint("/webhooks/events", v(func(c *VariableConfig) {
			c.Source, c.Path = "body", "account..env"
		}), nil), "variables.envName.path"},
		{"path source without segment", varEndpoint("/webhooks/events", v(func(c *VariableConfig) {
			c.Source, c.Path = "path", "envName"
		}), nil), "variables.envName.path"},
		{"values and pattern", varEndpoint("/webhooks/events", v(func(c *VariableConfig) { c.Pattern = "^a$" }), nil), "variables.envName"},
		{"neither values nor pattern", varEndpoint("/webhooks/events", v(func(c *VariableConfig) { c.Values = nil }), nil), "variables.envName"},
		{"empty values", varEndpoint("/webhooks/events", v(func(c *VariableConfig) { c.Values = []string{} }), nil), "variables.envName.values"},
		{"unsafe value", varEndpoint("/webhooks/events", v(func(c *VariableConfig) { c.Values = []string{"a/b"} }), nil), "variables.envName.values"},
		{"duplicate value", varEndpoint("/webhooks/events", v(func(c *VariableConfig) { c.Values = []string{"a", "a"} }), nil), "variables.envName.values"},
		{"fifo value", varEndpoint("/webhooks/events", v(func(c *VariableConfig) { c.Values = []string{"a.fifo"} }), nil), "variables.envName.values"},
		{"bad regex", varEndpoint("/webhooks/events", v(func(c *VariableConfig) { c.Values, c.Pattern = nil, "^[a$" }), nil), "variables.envName.pattern"},
		{"unanchored regex", varEndpoint("/webhooks/events", v(func(c *VariableConfig) { c.Values, c.Pattern = nil, "[a-z]+" }), nil), "variables.envName.pattern"},
		{"over-long pattern", varEndpoint("/webhooks/events", v(func(c *VariableConfig) {
			c.Values, c.Pattern = nil, "^"+strings.Repeat("a", 260)+"$"
		}), nil), "variables.envName.pattern"},
		{"default not accepted", varEndpoint("/webhooks/events", v(func(c *VariableConfig) { c.Default = "b" }), nil), "variables.envName.default"},
		{"unsafe default", varEndpoint("/webhooks/events", v(func(c *VariableConfig) {
			c.Values, c.Pattern, c.Default = nil, "^.+$", "a/b"
		}), nil), "variables.envName.default"},
		{"lookup block without lookup source", varEndpoint("/webhooks/events", v(func(c *VariableConfig) {
			c.Lookup = &LookupConfig{Provider: "http"}
		}), nil), "variables.envName.lookup"},
		{"unreferenced variable", varEndpoint("/webhooks/events", enumVar(), nil,
			EndpointDestinationConfig{Destination: "sink", Target: "events"}), "variables.envName"},
		{"path placeholder undeclared", varEndpoint("/webhooks/{envName}/events", nil, nil,
			EndpointDestinationConfig{Destination: "sink", Target: "events"}), "e.path"},
		{"path placeholder wrong source", varEndpoint("/webhooks/{envName}/events", enumVar(), nil), "e.path"},
		{"path malformed", varEndpoint("/webhooks/a{envName}/events", enumVar(), nil), "e.path"},
		{"target undeclared placeholder", varEndpoint("/webhooks/events", enumVar(), nil,
			EndpointDestinationConfig{Destination: "sink", Target: "events-{envName}-{other}"}), "destinations[0].target"},
		{"target malformed", varEndpoint("/webhooks/events", enumVar(), nil,
			EndpointDestinationConfig{Destination: "sink", Target: "events-{envName"}), "destinations[0].target"},
		{"target bad literal", varEndpoint("/webhooks/events", enumVar(), nil,
			EndpointDestinationConfig{Destination: "sink", Target: "events {envName}"}), "destinations[0].target"},
		{"target literal leaves no room for values", varEndpoint("/webhooks/events", patternVar(), nil,
			EndpointDestinationConfig{Destination: "sink", Target: strings.Repeat("e", 128) + "{envName}"}), "destinations[0].target"},
		{"rabbitmq create on dynamic target", varEndpoint("/webhooks/events", patternVar(), nil,
			EndpointDestinationConfig{Destination: "mq", Target: "events-{envName}", Create: true}), "destinations[0].create"},
		{"webhook url var not declared", varEndpoint("/webhooks/events", nil, nil,
			EndpointDestinationConfig{Destination: "hub", Target: "events"}), "webhook.url"},
		{"fallback without variables", varEndpoint("/webhooks/events", nil, &FallbackRouteConfig{Destination: "sink", Target: "unrouted"},
			EndpointDestinationConfig{Destination: "sink", Target: "events"}), "on_unresolved_variable"},
		{"fallback unknown destination", varEndpoint("/webhooks/events", enumVar(), &FallbackRouteConfig{Destination: "nope", Target: "unrouted"}), "on_unresolved_variable.destination"},
		{"fallback to webhook", varEndpoint("/webhooks/events", enumVar(), &FallbackRouteConfig{Destination: "hub", Target: "unrouted"}), "on_unresolved_variable.destination"},
		{"fallback missing destination", varEndpoint("/webhooks/events", enumVar(), &FallbackRouteConfig{Target: "unrouted"}), "on_unresolved_variable.destination"},
		{"fallback templated target", varEndpoint("/webhooks/events", enumVar(), &FallbackRouteConfig{Destination: "sink", Target: "unrouted-{envName}"}), "on_unresolved_variable.target"},
		{"fallback missing target", varEndpoint("/webhooks/events", enumVar(), &FallbackRouteConfig{Destination: "sink"}), "on_unresolved_variable.target"},
		{"fallback fifo target", varEndpoint("/webhooks/events", enumVar(), &FallbackRouteConfig{Destination: "sink", Target: "unrouted.fifo"}), "on_unresolved_variable.target"},
		{"fallback unsafe target", varEndpoint("/webhooks/events", enumVar(), &FallbackRouteConfig{Destination: "sink", Target: "un routed"}), "on_unresolved_variable.target"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ConfigValidationError{Issues: validationIssues(tc.cfg)}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not mention %q", err.Error(), tc.want)
			}
		})
	}
}

func TestLoadRejectsInvalidLookupVariables(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*LookupConfig)
		want   string
	}{
		{"missing key source", func(lc *LookupConfig) { lc.Key.Source = "" }, "lookup.key.source"},
		{"lookup key from lookup", func(lc *LookupConfig) { lc.Key.Source = "lookup" }, "lookup.key.source"},
		{"bad key source", func(lc *LookupConfig) { lc.Key.Source = "cookie" }, "lookup.key.source"},
		{"missing provider", func(lc *LookupConfig) { lc.Provider = "" }, "lookup.provider"},
		{"bad provider", func(lc *LookupConfig) { lc.Provider = "cassandra" }, "lookup.provider"},
		{"provider block missing", func(lc *LookupConfig) { lc.DynamoDB = nil }, "lookup.dynamodb"},
		{"two provider blocks", func(lc *LookupConfig) {
			lc.HTTP = &LookupHTTPConfig{URL: "https://r.example.com/?id={key}", ValuePath: "env_name"}
		}, "variables.envName.lookup"},
		{"dynamodb without table", func(lc *LookupConfig) { lc.DynamoDB.Table = "" }, "lookup.dynamodb.table"},
		{"dynamodb without value_attribute", func(lc *LookupConfig) { lc.DynamoDB.ValueAttribute = "" }, "lookup.dynamodb.value_attribute"},
		{"dynamodb half filter", func(lc *LookupConfig) { lc.DynamoDB.FilterAttribute = "tenant" }, "lookup.dynamodb.filter"},
		{"http without url", func(lc *LookupConfig) {
			lc.Provider, lc.DynamoDB = "http", nil
			lc.HTTP = &LookupHTTPConfig{ValuePath: "env_name"}
		}, "lookup.http.url"},
		{"http non-http scheme", func(lc *LookupConfig) {
			lc.Provider, lc.DynamoDB = "http", nil
			lc.HTTP = &LookupHTTPConfig{URL: "ftp://r.example.com/?id={key}", ValuePath: "env_name"}
		}, "lookup.http.url"},
		{"http without key placeholder", func(lc *LookupConfig) {
			lc.Provider, lc.DynamoDB = "http", nil
			lc.HTTP = &LookupHTTPConfig{URL: "https://r.example.com/env", ValuePath: "env_name"}
		}, "lookup.http.url"},
		{"http key in host", func(lc *LookupConfig) {
			lc.Provider, lc.DynamoDB = "http", nil
			lc.HTTP = &LookupHTTPConfig{URL: "https://{key}.example.com/env", ValuePath: "env_name"}
		}, "lookup.http.url"},
		{"http bad method", func(lc *LookupConfig) {
			lc.Provider, lc.DynamoDB = "http", nil
			lc.HTTP = &LookupHTTPConfig{URL: "https://r.example.com/?id={key}", Method: "DELETE", ValuePath: "env_name"}
		}, "lookup.http.method"},
		{"http without value_path", func(lc *LookupConfig) {
			lc.Provider, lc.DynamoDB = "http", nil
			lc.HTTP = &LookupHTTPConfig{URL: "https://r.example.com/?id={key}"}
		}, "lookup.http.value_path"},
		{"http bad header name", func(lc *LookupConfig) {
			lc.Provider, lc.DynamoDB = "http", nil
			lc.HTTP = &LookupHTTPConfig{URL: "https://r.example.com/?id={key}", ValuePath: "env_name",
				Headers: map[string]string{"Bad Header": "v"}}
		}, "lookup.http.headers"},
		{"postgres without url", func(lc *LookupConfig) {
			lc.Provider, lc.DynamoDB = "postgres", nil
			lc.Postgres = &LookupPostgresConfig{Query: "SELECT env_name FROM routing WHERE id = {key}"}
		}, "lookup.postgres.url"},
		{"postgres without query", func(lc *LookupConfig) {
			lc.Provider, lc.DynamoDB = "postgres", nil
			lc.Postgres = &LookupPostgresConfig{URL: "postgres://localhost/db"}
		}, "lookup.postgres.query"},
		{"postgres query without key placeholder", func(lc *LookupConfig) {
			lc.Provider, lc.DynamoDB = "postgres", nil
			lc.Postgres = &LookupPostgresConfig{URL: "postgres://localhost/db", Query: "SELECT env_name FROM routing"}
		}, "lookup.postgres.query"},
		{"negative cache max_entries", func(lc *LookupConfig) { lc.Cache.MaxEntries = -1 }, "lookup.cache.max_entries"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := varEndpoint("/webhooks/events", lookupVar(tc.mutate), nil)
			err := ConfigValidationError{Issues: validationIssues(cfg)}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not mention %q", err.Error(), tc.want)
			}
		})
	}
}

func TestLoadAcceptsEachLookupProvider(t *testing.T) {
	cases := map[string]func(*LookupConfig){
		"dynamodb": nil,
		"http": func(lc *LookupConfig) {
			lc.Provider, lc.DynamoDB = "http", nil
			lc.HTTP = &LookupHTTPConfig{URL: "https://resolver.example.com/env?id={key}", Method: "GET", ValuePath: "env_name"}
		},
		"postgres": func(lc *LookupConfig) {
			lc.Provider, lc.DynamoDB = "postgres", nil
			lc.Postgres = &LookupPostgresConfig{URL: "postgres://localhost/db", Query: "SELECT env_name FROM routing WHERE id = {key}"}
		},
		"lookup with values": func(lc *LookupConfig) {},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			decl := lookupVar(mutate)
			if name == "lookup with values" {
				v := decl["envName"]
				v.Values = []string{"tenant-a", "prod"}
				decl["envName"] = v
			}
			if issues := validationIssues(varEndpoint("/webhooks/events", decl, nil)); len(issues) != 0 {
				t.Fatalf("valid %s lookup rejected: %#v", name, issues)
			}
		})
	}
}

func TestDestinationRoutesWithoutVariablesIsUnchanged(t *testing.T) {
	ep := EndpointConfig{Destinations: []EndpointDestinationConfig{
		{Destination: "sink", Target: "events"},
		{Destination: "sink", Target: "raw"},
	}}
	routes := ep.DestinationRoutes()
	if len(routes) != 2 || routes[0].Target != "events" || routes[0].Index != 0 || routes[0].Dynamic {
		t.Fatalf("routes = %#v", routes)
	}
	if ep.DestinationLabel() != "sink,sink" {
		t.Fatalf("DestinationLabel: %q", ep.DestinationLabel())
	}
	if ep.TargetLabel() != "events,raw" {
		t.Fatalf("TargetLabel: %q", ep.TargetLabel())
	}
	if got := ep.DestinationIDs(); len(got) != 2 || got[0] != "sink" {
		t.Fatalf("DestinationIDs: %#v", got)
	}
}

func TestDestinationRoutesExpandsEnumeratedVariables(t *testing.T) {
	ep := EndpointConfig{
		Variables:            enumVar(),
		OnUnresolvedVariable: &FallbackRouteConfig{Destination: "sink", Target: "unrouted"},
		Destinations:         []EndpointDestinationConfig{{Destination: "sink", Target: "events-{envName}"}},
	}
	routes := ep.DestinationRoutes()
	if len(routes) != 3 {
		t.Fatalf("routes = %#v", routes)
	}
	got := map[string]bool{}
	for _, r := range routes {
		got[r.Target] = true
	}
	for _, want := range []string{"events-tenant-a", "events-staging", "unrouted"} {
		if !got[want] {
			t.Fatalf("missing expanded target %q: %#v", want, routes)
		}
	}
	if routes[len(routes)-1].Index != -1 {
		t.Fatalf("fallback route index = %d, want -1", routes[len(routes)-1].Index)
	}
	if routes[0].Bindings["envName"] == "" || routes[0].Template != "events-{envName}" {
		t.Fatalf("bindings/template not recorded: %#v", routes[0])
	}
}

func TestDestinationRoutesCartesianAndDynamic(t *testing.T) {
	ep := EndpointConfig{
		Variables: map[string]VariableConfig{
			"envName": {Source: "header", Path: "X-Env", Values: []string{"tenant-a", "staging"}},
			"kind":    {Source: "header", Path: "X-Kind", Values: []string{"orders", "refunds"}},
			"free":    {Source: "header", Path: "X-Free", Pattern: "^[a-z]+$"},
		},
		Destinations: []EndpointDestinationConfig{
			{Destination: "sink", Target: "{envName}-{kind}"},
			{Destination: "sink", Target: "raw-{free}"},
		},
	}
	routes := ep.DestinationRoutes()
	if len(routes) != 5 {
		t.Fatalf("routes = %d, want 4 cartesian + 1 dynamic: %#v", len(routes), routes)
	}
	dynamic := 0
	for _, r := range routes {
		if r.Dynamic {
			dynamic++
			if r.Target != "raw-{free}" {
				t.Fatalf("dynamic target = %q", r.Target)
			}
		}
	}
	if dynamic != 1 {
		t.Fatalf("dynamic routes = %d, want 1", dynamic)
	}
}

func TestDestinationRoutesCapsExpansion(t *testing.T) {
	values := make([]string, 0, 20)
	for i := 0; i < 20; i++ {
		values = append(values, string(rune('a'+i))+"-env")
	}
	ep := EndpointConfig{
		Variables: map[string]VariableConfig{
			"a": {Source: "header", Path: "X-A", Values: values},
			"b": {Source: "header", Path: "X-B", Values: values},
		},
		Destinations: []EndpointDestinationConfig{{Destination: "sink", Target: "{a}-{b}"}},
	}
	if got := len(ep.DestinationRoutes()); got <= MaxExpandedRoutes {
		t.Fatalf("expansion cap not reported: %d routes", got)
	}
	cfg := Config{
		Server:       ServerConfig{Addr: ":8080", MaxBodyBytes: 1024},
		Destinations: variableDestinations,
		Endpoints:    map[string]EndpointConfig{"e": {Enabled: true, Path: "/webhooks/events", Auth: AuthConfig{Type: "no_auth", Scheme: "no_auth"}, Variables: ep.Variables, Destinations: ep.Destinations}},
	}
	err := ConfigValidationError{Issues: validationIssues(cfg)}
	if !strings.Contains(err.Error(), "expand to more than") {
		t.Fatalf("expected an expansion-cap issue, got %v", err)
	}
}

func TestVariableSpecsAndPatternSources(t *testing.T) {
	ep := EndpointConfig{Variables: map[string]VariableConfig{
		"envName": {Source: "header", Path: "X-Env", Values: []string{"tenant-a"}},
		"free":    {Source: "header", Pattern: "^[a-z]+$"},
		"looked":  {Source: "lookup", Lookup: &LookupConfig{Key: VariableKeyConfig{Source: "body", Path: "account.id"}}},
	}}
	specs, err := ep.VariableSpecs()
	if err != nil {
		t.Fatalf("VariableSpecs: %v", err)
	}
	if len(specs) != 3 || specs[0].Name != "envName" || specs[1].Name != "free" || specs[2].Name != "looked" {
		t.Fatalf("specs not sorted by name: %#v", specs)
	}
	if specs[1].Pattern == nil || specs[1].Path != "free" {
		t.Fatalf("pattern/defaulted path not compiled: %#v", specs[1])
	}
	if specs[2].KeySource != "body" || specs[2].KeyPath != "account.id" {
		t.Fatalf("lookup key not plumbed: %#v", specs[2])
	}

	ps := ep.PatternSources()
	if ps["free"] != "^[a-z]+$" {
		t.Fatalf("declared pattern missing: %#v", ps)
	}
	if ps["looked"] != lookupAnyPattern {
		t.Fatalf("patternless lookup variable should default to the safe-name pattern: %#v", ps)
	}
	if _, ok := ps["envName"]; ok {
		t.Fatalf("enumerated variable must not appear in PatternSources: %#v", ps)
	}

	bad := EndpointConfig{Variables: map[string]VariableConfig{"x": {Source: "header", Pattern: "^[a$"}}}
	if _, err := bad.VariableSpecs(); err == nil {
		t.Fatal("expected an error for an uncompilable pattern")
	}
}

func TestFallbackRouteAccessor(t *testing.T) {
	var ep EndpointConfig
	if _, ok := ep.FallbackRoute(); ok {
		t.Fatal("no fallback configured")
	}
	ep.OnUnresolvedVariable = &FallbackRouteConfig{Destination: "sink", Target: "unrouted"}
	fb, ok := ep.FallbackRoute()
	if !ok || fb.Destination != "sink" || fb.Target != "unrouted" {
		t.Fatalf("fallback = %#v %v", fb, ok)
	}
}

func TestEffectiveDeliveryFailurePolicyAndFanoutLimit(t *testing.T) {
	var ep EndpointConfig
	if ep.EffectiveDeliveryFailurePolicy() != DeliveryFailurePolicyStopOnFailure {
		t.Fatalf("default policy = %q", ep.EffectiveDeliveryFailurePolicy())
	}
	ep.DeliveryFailurePolicy = DeliveryFailurePolicySkipFailed
	if ep.EffectiveDeliveryFailurePolicy() != DeliveryFailurePolicySkipFailed {
		t.Fatalf("explicit policy = %q", ep.EffectiveDeliveryFailurePolicy())
	}
	if (ServerConfig{}).FanoutConcurrencyLimit() != 8 {
		t.Fatal("fanout concurrency should default to 8")
	}
	if (ServerConfig{FanoutConcurrency: 3}).FanoutConcurrencyLimit() != 3 {
		t.Fatal("explicit fanout concurrency should pass through")
	}
}

func TestIsSafeName(t *testing.T) {
	if !IsSafeName("billing-events.v1_2") {
		t.Fatal("expected a conventional target name to be safe")
	}
	if IsSafeName("bad target") || IsSafeName("") {
		t.Fatal("spaces and empty names are not safe")
	}
}
