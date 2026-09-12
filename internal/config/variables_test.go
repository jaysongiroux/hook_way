package config

import (
	"github.com/jasongiroux/hookway/internal/vars"
	"reflect"
	"strings"
	"testing"
	"time"
)

// variablesBody builds a config with two destinations (a mongodb sink, which
// accepts dynamic targets, and an s3 archive for fallbacks) and one endpoint
// whose path/variables/destinations are supplied by the caller.
func variablesBody(path, variables, fallback, routes string) string {
	return `
server:
  addr: ":8080"
  max_body_bytes: 1048576
destinations:
  sink:
    type: mongodb
    mongodb:
      uri: "mongodb://localhost:27017"
      database: "hooks"
  archive:
    type: s3
    s3:
      region: "us-east-1"
      bucket: "archive"
  hub:
    type: webhook
    webhook:
      url: "https://{envName}-api.htap.sh/api/v3/digifi/webhook"
endpoints:
  digifi:
    enabled: true
    path: "` + path + `"
` + variables + fallback + `
    destinations:
` + routes + `
    auth:
      type: "no_auth"
      scheme: "no_auth"
`
}

const enumVar = `    variables:
      envName:
        source: header
        path: X-Env
        values: [qa-gm3, staging-portal, portal]
`

const patternVar = `    variables:
      envName:
        source: header
        path: X-Env
        pattern: "^qa-gm[0-9]+$"
`

const sinkRoute = `      - destination: "sink"
        target: "{envName}-events"
`

func TestLoadParsesVariables(t *testing.T) {
	body := variablesBody("/webhooks/{envName}/digifi", `    variables:
      envName:
        source: path
        values: [qa-gm3, staging-portal, portal]
        default: "portal"
      tenant:
        source: body
        path: "account.id"
        pattern: "^[a-z]+$"
`, `    on_unresolved_variable:
      destination: "archive"
      target: "unrouted"
`, `      - destination: "sink"
        target: "{envName}-{tenant}-events"
`)
	cfg, err := Load(writeConfig(t, body))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	ep := cfg.Endpoints["digifi"]
	env := ep.Variables["envName"]
	if env.Source != "path" || env.Path != "envName" || !reflect.DeepEqual(env.Values, []string{"qa-gm3", "staging-portal", "portal"}) || env.Default != "portal" {
		t.Fatalf("envName: %#v", env)
	}
	tenant := ep.Variables["tenant"]
	if tenant.Source != "body" || tenant.Path != "account.id" || tenant.Pattern != "^[a-z]+$" {
		t.Fatalf("tenant: %#v", tenant)
	}
	if ep.OnUnresolvedVariable == nil || ep.OnUnresolvedVariable.Destination != "archive" || ep.OnUnresolvedVariable.Target != "unrouted" {
		t.Fatalf("fallback: %#v", ep.OnUnresolvedVariable)
	}
	specs, err := ep.VariableSpecs()
	if err != nil || len(specs) != 2 || specs[0].Name != "envName" || specs[1].Name != "tenant" || specs[1].Pattern == nil {
		t.Fatalf("specs: %#v %v", specs, err)
	}
	if src := ep.PatternSources(); !reflect.DeepEqual(src, map[string]string{"tenant": "^[a-z]+$"}) {
		t.Fatalf("PatternSources: %#v", src)
	}
}

func TestDestinationRoutesWithoutVariablesIsUnchanged(t *testing.T) {
	cfg, err := Load(writeConfig(t, variablesBody("/webhooks/digifi", "", "", `      - destination: "sink"
        target: "events"
      - destination: "archive"
        target: "raw"
`)))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	routes := cfg.Endpoints["digifi"].DestinationRoutes()
	if len(routes) != 2 {
		t.Fatalf("expected 2 routes, got %d", len(routes))
	}
	for i, r := range routes {
		if r.Index != i || r.Template != r.Target || r.Dynamic || r.Bindings != nil {
			t.Fatalf("route %d should be plain: %#v", i, r)
		}
	}
	if cfg.Endpoints["digifi"].TargetLabel() != "events,raw" {
		t.Fatalf("TargetLabel: %q", cfg.Endpoints["digifi"].TargetLabel())
	}
}

func TestDestinationRoutesExpandsEnumeratedVariables(t *testing.T) {
	cfg, err := Load(writeConfig(t, variablesBody("/webhooks/digifi", enumVar, `    on_unresolved_variable:
      destination: "archive"
      target: "unrouted"
`, sinkRoute+`      - destination: "archive"
        target: "static"
`)))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	ep := cfg.Endpoints["digifi"]
	routes := ep.DestinationRoutes()
	var got []string
	for _, r := range routes {
		got = append(got, r.Destination+":"+r.Target)
	}
	want := []string{"sink:qa-gm3-events", "sink:staging-portal-events", "sink:portal-events", "archive:static", "archive:unrouted"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("routes: got %v want %v", got, want)
	}
	if routes[1].Index != 0 || routes[1].Template != "{envName}-events" || routes[1].Bindings["envName"] != "staging-portal" || routes[1].Dynamic {
		t.Fatalf("expanded route meta: %#v", routes[1])
	}
	if routes[3].Index != 1 || routes[3].Template != "static" {
		t.Fatalf("static route meta: %#v", routes[3])
	}
	if routes[4].Index != -1 || routes[4].Template != "unrouted" {
		t.Fatalf("fallback route meta: %#v", routes[4])
	}
	// The label stays the authored templates.
	if ep.TargetLabel() != "{envName}-events,static" {
		t.Fatalf("TargetLabel: %q", ep.TargetLabel())
	}
	fb, ok := ep.FallbackRoute()
	if !ok || fb.Destination != "archive" || fb.Target != "unrouted" {
		t.Fatalf("FallbackRoute: %#v %v", fb, ok)
	}
}

func TestDestinationRoutesCartesianAndDynamic(t *testing.T) {
	body := variablesBody("/webhooks/digifi", `    variables:
      envName:
        source: header
        path: X-Env
        values: [qa, prod]
      region:
        source: header
        path: X-Region
        values: [us, eu, ap]
      tenant:
        source: header
        path: X-Tenant
        pattern: "^[a-z]+$"
`, "", `      - destination: "sink"
        target: "{envName}-{region}-events"
      - destination: "sink"
        target: "{envName}-{tenant}"
`)
	cfg, err := Load(writeConfig(t, body))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	routes := cfg.Endpoints["digifi"].DestinationRoutes()
	if len(routes) != 6+2 {
		t.Fatalf("expected 8 routes, got %d", len(routes))
	}
	if routes[0].Target != "qa-us-events" || routes[1].Target != "qa-eu-events" || routes[5].Target != "prod-ap-events" {
		t.Fatalf("cartesian order: %v %v %v", routes[0].Target, routes[1].Target, routes[5].Target)
	}
	if !routes[6].Dynamic || routes[6].Target != "qa-{tenant}" || routes[6].Bindings["envName"] != "qa" || routes[6].Template != "{envName}-{tenant}" {
		t.Fatalf("mixed route: %#v", routes[6])
	}
}

func TestLoadAcceptsVariablesExample(t *testing.T) {
	cases := map[string]string{
		"pattern var on dynamic-capable destination": variablesBody("/webhooks/digifi", patternVar, "", sinkRoute),
		"pattern var on sqs (provisioned on first sight)": strings.Replace(variablesBody("/webhooks/digifi", patternVar, "", `      - destination: "queue"
        target: "{envName}-events"
        create: true
`), "  archive:\n", "  queue:\n    type: sqs\n    sqs:\n      region: us-east-1\n  archive:\n", 1),
		"path var with webhook url host": variablesBody("/webhooks/{envName}/digifi", `    variables:
      envName:
        source: path
        pattern: "^qa-gm[0-9]+$"
`, "", `      - destination: "hub"
        target: "event"
`),
		"enumerated fifo target on sqs": strings.Replace(variablesBody("/webhooks/digifi", enumVar, "", `      - destination: "queue"
        target: "{envName}-events.fifo"
        fifo:
          group_id:
            source: endpoint
          dedup:
            content_based_fallback: true
`), "  archive:\n", "  queue:\n    type: sqs\n    sqs:\n      region: us-east-1\n  archive:\n", 1),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(writeConfig(t, body)); err != nil {
				t.Fatalf("Load: %v", err)
			}
		})
	}
}

func TestLoadAcceptsTemporalPrefixTemplate(t *testing.T) {
	body := strings.Replace(variablesBody("/webhooks/digifi", enumVar, "", `      - destination: "wf"
        target: "adverse-action"
        temporal_options:
          workflow_type: "W"
          workflow_id:
            source: body
            path: applicationId
            prefix: "{envName}-aan-"
`), "  archive:\n", "  wf:\n    type: temporal\n    temporal:\n      host_port: \"localhost:7233\"\n      namespace: \"default\"\n  archive:\n", 1)
	if _, err := Load(writeConfig(t, body)); err != nil {
		t.Fatalf("Load: %v", err)
	}
}

func TestLoadRejectsInvalidVariables(t *testing.T) {
	sqsDest := func(body string) string {
		return strings.Replace(body, "  archive:\n", "  queue:\n    type: sqs\n    sqs:\n      region: us-east-1\n  archive:\n", 1)
	}
	twoEndpoints := func(pathA, pathB string) string {
		return `
server:
  addr: ":8080"
  max_body_bytes: 1048576
destinations:
  sink:
    type: mongodb
    mongodb:
      uri: "mongodb://localhost:27017"
      database: "hooks"
endpoints:
  a:
    enabled: true
    path: "` + pathA + `"
    variables:
      x:
        source: path
        pattern: "^[a-z]+$"
    destinations:
      - destination: "sink"
        target: "{x}"
    auth:
      type: "no_auth"
      scheme: "no_auth"
  b:
    enabled: true
    path: "` + pathB + `"
    destinations:
      - destination: "sink"
        target: "b"
    auth:
      type: "no_auth"
      scheme: "no_auth"
`
	}
	v := func(spec string) string { return "    variables:\n      envName:\n" + spec }
	cases := []struct {
		name string
		body string
		want string
	}{
		{"bad name", variablesBody("/webhooks/digifi", "    variables:\n      9bad:\n        source: header\n        values: [a]\n", "", `      - destination: "sink"
        target: "{9bad}"
`), "variables.9bad"},
		{"reserved name", variablesBody("/webhooks/digifi", "    variables:\n      target:\n        source: header\n        values: [a]\n", "", `      - destination: "sink"
        target: "{target}"
`), "variables.target"},
		{"missing source", variablesBody("/webhooks/digifi", v("        values: [a]\n"), "", sinkRoute), "variables.envName.source"},
		{"bad source", variablesBody("/webhooks/digifi", v("        source: cookie\n        values: [a]\n"), "", sinkRoute), "variables.envName.source"},
		{"bad header path", variablesBody("/webhooks/digifi", v("        source: header\n        path: \"X Env\"\n        values: [a]\n"), "", sinkRoute), "variables.envName.path"},
		{"path source without segment", variablesBody("/webhooks/digifi", v("        source: path\n        values: [a]\n"), "", sinkRoute), "variables.envName.path"},
		{"values and pattern", variablesBody("/webhooks/digifi", v("        source: header\n        values: [a]\n        pattern: \"^a$\"\n"), "", sinkRoute), "variables.envName"},
		{"neither values nor pattern", variablesBody("/webhooks/digifi", v("        source: header\n"), "", sinkRoute), "variables.envName"},
		{"empty values", variablesBody("/webhooks/digifi", v("        source: header\n        values: []\n"), "", sinkRoute), "variables.envName.values"},
		{"unsafe value", variablesBody("/webhooks/digifi", v("        source: header\n        values: [\"a/b\"]\n"), "", sinkRoute), "variables.envName.values"},
		{"duplicate value", variablesBody("/webhooks/digifi", v("        source: header\n        values: [a, a]\n"), "", sinkRoute), "variables.envName.values"},
		{"fifo value", variablesBody("/webhooks/digifi", v("        source: header\n        values: [\"a.fifo\"]\n"), "", sinkRoute), "variables.envName.values"},
		{"bad regex", variablesBody("/webhooks/digifi", v("        source: header\n        pattern: \"^[a$\"\n"), "", sinkRoute), "variables.envName.pattern"},
		{"unanchored regex", variablesBody("/webhooks/digifi", v("        source: header\n        pattern: \"[a-z]+\"\n"), "", sinkRoute), "variables.envName.pattern"},
		{"default not accepted", variablesBody("/webhooks/digifi", v("        source: header\n        values: [a]\n        default: b\n"), "", sinkRoute), "variables.envName.default"},
		{"unreferenced variable", variablesBody("/webhooks/digifi", enumVar, "", `      - destination: "sink"
        target: "events"
`), "variables.envName"},
		{"path placeholder undeclared", variablesBody("/webhooks/{envName}/digifi", "", "", `      - destination: "sink"
        target: "events"
`), "digifi.path"},
		{"path placeholder wrong source", variablesBody("/webhooks/{envName}/digifi", enumVar, "", sinkRoute), "digifi.path"},
		{"path malformed", variablesBody("/webhooks/a{envName}/digifi", enumVar, "", sinkRoute), "digifi.path"},
		{"path overlap literal vs placeholder", twoEndpoints("/webhooks/{x}/digifi", "/webhooks/portal/digifi"), "path overlaps"},
		{"path overlap parent child", twoEndpoints("/webhooks/{x}", "/webhooks/portal/digifi"), "path overlaps"},
		{"target undeclared placeholder", variablesBody("/webhooks/digifi", enumVar, "", `      - destination: "sink"
        target: "{envName}-{other}"
`), "destinations[0].target"},
		{"target malformed", variablesBody("/webhooks/digifi", enumVar, "", `      - destination: "sink"
        target: "{envName"
`), "destinations[0].target"},
		{"target bad literal", variablesBody("/webhooks/digifi", enumVar, "", `      - destination: "sink"
        target: "{envName}/events"
`), "destinations[0].target"},
		{"target literal leaves no room for values", variablesBody("/webhooks/digifi", patternVar, "", `      - destination: "sink"
        target: "{envName}-`+strings.Repeat("a", 128)+`"
`), "destinations[0].target"},
		{"fifo suffix from placeholder", sqsDest(variablesBody("/webhooks/digifi", enumVar, "", `      - destination: "queue"
        target: "events-{envName}"
        fifo:
          group_id:
            source: endpoint
          dedup:
            content_based_fallback: true
`)), "fifo"},
		{"expanded duplicate", variablesBody("/webhooks/digifi", `    variables:
      envName:
        source: header
        path: X-Env
        values: [qa]
`, "", `      - destination: "sink"
        target: "{envName}-events"
      - destination: "sink"
        target: "qa-events"
`), "duplicated"},
		{"expansion cap", variablesBody("/webhooks/digifi", `    variables:
      a:
        source: header
        path: X-A
        pattern: "^x$"
      b:
        source: header
        path: X-B
        values: [`+strings.TrimSuffix(strings.Repeat("v,", 300), ",")+`]
`, "", `      - destination: "sink"
        target: "{a}-{b}"
`), "expand to"},
		{"rabbitmq create on dynamic", strings.Replace(variablesBody("/webhooks/digifi", patternVar, "", `      - destination: "mq"
        target: "{envName}-events"
        create: true
`), "  archive:\n", "  mq:\n    type: rabbitmq\n    rabbitmq:\n      url: \"amqp://x\"\n      exchange: \"e\"\n      exchange_type: \"direct\"\n  archive:\n", 1), "destinations[0].create"},
		{"temporal prefix undeclared variable", strings.Replace(variablesBody("/webhooks/digifi", enumVar, "", `      - destination: "wf"
        target: "{envName}-jobs"
        temporal_options:
          workflow_type: "W"
          workflow_id:
            prefix: "{other}-"
`), "  archive:\n", "  wf:\n    type: temporal\n    temporal:\n      host_port: \"localhost:7233\"\n      namespace: \"default\"\n  archive:\n", 1), "temporal_options.workflow_id.prefix"},
		{"webhook url malformed", strings.Replace(variablesBody("/webhooks/digifi", enumVar, "", `      - destination: "hub"
        target: "event"
`), "https://{envName}-api.htap.sh", "https://{envName-api.htap.sh", 1), "destinations.hub.webhook.url"},
		{"webhook url var not declared by endpoint", variablesBody("/webhooks/digifi", "", "", `      - destination: "hub"
        target: "event"
`), "destinations[0].destination"},
		{"webhook url whole-host placeholder", strings.Replace(variablesBody("/webhooks/digifi", enumVar, "", `      - destination: "hub"
        target: "event"
`), "https://{envName}-api.htap.sh", "https://{envName}", 1), "destinations.hub.webhook.url"},
		{"fallback without variables", variablesBody("/webhooks/digifi", "", `    on_unresolved_variable:
      destination: "archive"
      target: "unrouted"
`, `      - destination: "sink"
        target: "events"
`), "on_unresolved_variable"},
		{"fallback unknown destination", variablesBody("/webhooks/digifi", enumVar, `    on_unresolved_variable:
      destination: "nope"
      target: "unrouted"
`, sinkRoute), "on_unresolved_variable.destination"},
		{"fallback to webhook", variablesBody("/webhooks/digifi", enumVar, `    on_unresolved_variable:
      destination: "hub"
      target: "unrouted"
`, sinkRoute), "on_unresolved_variable.destination"},
		{"fallback templated target", variablesBody("/webhooks/digifi", enumVar, `    on_unresolved_variable:
      destination: "archive"
      target: "{envName}"
`, sinkRoute), "on_unresolved_variable.target"},
		{"fallback missing target", variablesBody("/webhooks/digifi", enumVar, `    on_unresolved_variable:
      destination: "archive"
`, sinkRoute), "on_unresolved_variable.target"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(writeConfig(t, tc.body))
			if err == nil {
				t.Fatalf("expected validation error containing %q, got nil", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not mention %q", err.Error(), tc.want)
			}
		})
	}
}

const lookupDest = "  sink:\n"

func lookupVar(provider, block string) string {
	return `    variables:
      env:
        source: lookup
        lookup:
          key:
            source: body
            path: "data.object.applicationId"
          provider: ` + provider + `
` + block
}

const dynamoBlock = `          dynamodb:
            table: "routing"
            index: "CreatedAtIndex"
            filter:
              attribute: "id_type"
              value: "application"
            value_attribute: "env_name"
`

func TestLoadParsesLookupVariable(t *testing.T) {
	body := variablesBody("/webhooks/digifi", lookupVar("dynamodb", dynamoBlock+`          timeout: "3s"
          cache:
            ttl: "10m"
            negative_ttl: "30s"
            max_entries: 500
        pattern: "^[a-z0-9-]+$"
`), "", `      - destination: "sink"
        target: "{env}-events"
`)
	cfg, err := Load(writeConfig(t, body))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	v := cfg.Endpoints["digifi"].Variables["env"]
	if v.Source != "lookup" || v.Lookup == nil {
		t.Fatalf("lookup not parsed: %#v", v)
	}
	lc := v.Lookup
	if lc.Key.Source != "body" || lc.Key.Path != "data.object.applicationId" || lc.Provider != "dynamodb" {
		t.Fatalf("key/provider: %#v", lc)
	}
	if lc.Timeout != 3*time.Second || lc.Cache.TTL != 10*time.Minute || lc.Cache.NegativeTTL != 30*time.Second || lc.Cache.MaxEntries != 500 {
		t.Fatalf("timeout/cache: %#v", lc)
	}
	d := lc.DynamoDB
	if d == nil || d.Table != "routing" || d.Index != "CreatedAtIndex" || d.FilterAttribute != "id_type" || d.FilterValue != "application" || d.ValueAttribute != "env_name" {
		t.Fatalf("dynamodb block: %#v", d)
	}
	specs, err := cfg.Endpoints["digifi"].VariableSpecs()
	if err != nil || specs[0].KeySource != "body" || specs[0].KeyPath != "data.object.applicationId" {
		t.Fatalf("specs: %#v %v", specs, err)
	}
}

func TestLoadDefaultsLookupTimeoutsAndKeyPath(t *testing.T) {
	body := variablesBody("/webhooks/digifi", `    variables:
      env:
        source: lookup
        lookup:
          key:
            source: header
          provider: http
          http:
            url: "https://resolver.internal/env?id={key}"
            value_path: "env_name"
`, "", `      - destination: "sink"
        target: "{env}-events"
`)
	cfg, err := Load(writeConfig(t, body))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	lc := cfg.Endpoints["digifi"].Variables["env"].Lookup
	if lc.Timeout != 2*time.Second || lc.Cache.TTL != 5*time.Minute || lc.Cache.NegativeTTL != 0 || lc.Key.Path != "env" {
		t.Fatalf("defaults: %#v", lc)
	}
}

func TestLoadAcceptsEachLookupProvider(t *testing.T) {
	route := `      - destination: "sink"
        target: "{env}-events"
`
	cases := map[string]string{
		"dynamodb": variablesBody("/webhooks/digifi", lookupVar("dynamodb", dynamoBlock), "", route),
		"http": variablesBody("/webhooks/digifi", lookupVar("http", `          http:
            url: "https://resolver.internal/env?id={key}"
            method: POST
            headers:
              Authorization: "Bearer x"
            value_path: "data.env_name"
`), "", route),
		"postgres": variablesBody("/webhooks/digifi", lookupVar("postgres", `          postgres:
            url: "postgres://localhost:5432/routing"
            query: "SELECT env_name FROM routing WHERE id = {key}"
`), "", route),
		"lookup with values": variablesBody("/webhooks/digifi", lookupVar("dynamodb", dynamoBlock+"        values: [qa-gm3, portal]\n"), "", route),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(writeConfig(t, body)); err != nil {
				t.Fatalf("Load: %v", err)
			}
		})
	}
}

func TestLoadRejectsInvalidLookupVariables(t *testing.T) {
	route := `      - destination: "sink"
        target: "{env}-events"
`
	lv := func(provider, block string) string {
		return variablesBody("/webhooks/digifi", lookupVar(provider, block), "", route)
	}
	cases := []struct{ name, body, want string }{
		{"lookup without block", variablesBody("/webhooks/digifi", "    variables:\n      env:\n        source: lookup\n", "", route), "variables.env.lookup"},
		{"block without lookup source", variablesBody("/webhooks/digifi", "    variables:\n      env:\n        source: header\n        values: [a]\n        lookup:\n          provider: http\n", "", route), "variables.env.lookup"},
		{"missing key source", variablesBody("/webhooks/digifi", `    variables:
      env:
        source: lookup
        lookup:
          provider: dynamodb
`+dynamoBlock, "", route), "lookup.key.source"},
		{"key from lookup", strings.Replace(lv("dynamodb", dynamoBlock), "source: body\n            path", "source: lookup\n            path", 1), "lookup.key.source"},
		{"key path source without segment", strings.Replace(lv("dynamodb", dynamoBlock), "source: body\n            path: \"data.object.applicationId\"", "source: path\n            path: envName", 1), "lookup.key.path"},
		{"missing provider", variablesBody("/webhooks/digifi", `    variables:
      env:
        source: lookup
        lookup:
          key:
            source: body
            path: id
`+dynamoBlock, "", route), "lookup.provider"},
		{"bad provider", lv("redis", ""), "lookup.provider"},
		{"provider block missing", lv("http", ""), "lookup.http"},
		{"two provider blocks", lv("dynamodb", dynamoBlock+"          http:\n            url: \"https://x/{key}\"\n            value_path: v\n"), "variables.env.lookup"},
		{"dynamodb missing table", lv("dynamodb", "          dynamodb:\n            value_attribute: env\n"), "dynamodb.table"},
		{"dynamodb missing value_attribute", lv("dynamodb", "          dynamodb:\n            table: t\n"), "dynamodb.value_attribute"},
		{"dynamodb half filter", lv("dynamodb", "          dynamodb:\n            table: t\n            value_attribute: env\n            filter:\n              attribute: id_type\n"), "dynamodb.filter"},
		{"http missing url", lv("http", "          http:\n            value_path: v\n"), "http.url"},
		{"http non-http url", lv("http", "          http:\n            url: \"ftp://x/{key}\"\n            value_path: v\n"), "http.url"},
		{"http no key placeholder", lv("http", "          http:\n            url: \"https://x/env\"\n            value_path: v\n"), "http.url"},
		{"http key in host", lv("http", "          http:\n            url: \"https://{key}/env\"\n            value_path: v\n"), "http.url"},
		{"http key in host suffix", lv("http", "          http:\n            url: \"https://{key}.internal:8443/env\"\n            value_path: v\n"), "http.url"},
		{"http bad method", lv("http", "          http:\n            url: \"https://x/{key}\"\n            method: DELETE\n            value_path: v\n"), "http.method"},
		{"http missing value_path", lv("http", "          http:\n            url: \"https://x/{key}\"\n"), "http.value_path"},
		{"postgres missing url", lv("postgres", "          postgres:\n            query: \"SELECT 1 WHERE id = {key}\"\n"), "postgres.url"},
		{"postgres query without key placeholder", lv("postgres", "          postgres:\n            url: \"postgres://x/db\"\n            query: \"SELECT env FROM t\"\n"), "postgres.query"},
		{"bad timeout", lv("dynamodb", dynamoBlock+"          timeout: \"soon\"\n"), "lookup.timeout"},
		{"zero ttl", lv("dynamodb", dynamoBlock+"          cache:\n            ttl: \"0s\"\n"), "lookup.cache.ttl"},
		{"negative max_entries", lv("dynamodb", dynamoBlock+"          cache:\n            max_entries: -1\n"), "cache.max_entries"},
		{"values and pattern", lv("dynamodb", dynamoBlock+"        values: [a]\n        pattern: \"^a$\"\n"), "variables.env"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(writeConfig(t, tc.body))
			if err == nil {
				t.Fatalf("expected validation error containing %q, got nil", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not mention %q", err.Error(), tc.want)
			}
		})
	}
}

// A lookup variable with neither values nor pattern is an open set bounded
// only by the safe-name rule; the queue layer must be able to compile a
// matcher for a dynamic route that uses it, or `validate` passes and `serve`
// exits at startup.
func TestPatternSourcesDefaultsPatternlessLookupVariable(t *testing.T) {
	cfg, err := Load(writeConfig(t, variablesBody("/webhooks/digifi", lookupVar("dynamodb", dynamoBlock), "", `      - destination: "sink"
        target: "{env}-events"
`)))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	ps := cfg.Endpoints["digifi"].PatternSources()
	if ps["env"] != lookupAnyPattern {
		t.Fatalf("PatternSources = %v, want implicit safe-name pattern for env", ps)
	}
	m, err := vars.CompileMatcher("{env}-events", ps)
	if err != nil {
		t.Fatalf("CompileMatcher: %v", err)
	}
	if !m.Match("qa-gm3-events") || m.Match("a/b-events") || m.Match("-events") {
		t.Fatal("implicit pattern must accept safe names only")
	}
	// An enumerated lookup variable stays out of PatternSources.
	enum := EndpointConfig{Variables: map[string]VariableConfig{"env": {Source: "lookup", Values: []string{"a"}}}}
	if ps := enum.PatternSources(); len(ps) != 0 {
		t.Fatalf("enumerated lookup variable must not get a pattern: %v", ps)
	}
}

// The fallback route carries no per-route options, so sharing a target with a
// route that sets some would make the factory pick options nondeterministically.
func TestLoadRejectsFallbackSharingTargetWithOptions(t *testing.T) {
	body := `
server:
  addr: ":8080"
  max_body_bytes: 1048576
destinations:
  queue:
    type: sqs
    sqs:
      region: us-east-1
endpoints:
  a:
    enabled: true
    path: "/webhooks/a"
    destinations:
      - destination: "queue"
        target: "unrouted"
        sqs_options:
          visibility_timeout: "5m"
    auth:
      type: "no_auth"
      scheme: "no_auth"
  b:
    enabled: true
    path: "/webhooks/b"
    variables:
      envName:
        source: header
        path: X-Env
        pattern: "^qa-gm[0-9]+$"
    on_unresolved_variable:
      destination: "queue"
      target: "unrouted"
    destinations:
      - destination: "queue"
        target: "{envName}-events"
    auth:
      type: "no_auth"
      scheme: "no_auth"
`
	_, err := Load(writeConfig(t, body))
	if err == nil || !strings.Contains(err.Error(), "unrouted") && !strings.Contains(err.Error(), "on_unresolved_variable") {
		t.Fatalf("expected fallback/options conflict, got %v", err)
	}
	// Without the options the shared target is fine.
	ok := strings.Replace(body, "        sqs_options:\n          visibility_timeout: \"5m\"\n", "", 1)
	if _, err := Load(writeConfig(t, ok)); err != nil {
		t.Fatalf("shared optionless target must load: %v", err)
	}
}
