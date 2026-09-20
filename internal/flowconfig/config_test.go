package flowconfig

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/jasongiroux/hookway/internal/config"
)

// TestLoadBytesRejectsLegacyTopLevelKeys pins the fresh-reset contract: there
// is no config version field and no endpoints-based format, so both are just
// unknown top-level keys. If a version-detection path is ever reintroduced,
// the schema_version case here starts passing for the wrong reason and this
// test fails.
func TestLoadBytesRejectsLegacyTopLevelKeys(t *testing.T) {
	cases := []struct{ name, src, wantField string }{
		{"endpoints", "endpoints:\n  github:\n    path: /webhooks/github\n", "endpoints"},
		{"schema_version", "schema_version: 2\nflows: {}\n", "schema_version"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, issues := LoadBytes([]byte(tc.src), "test", false)
			if len(issues) == 0 {
				t.Fatalf("LoadBytes(%q) = no issues; %q must be rejected as an unknown field", tc.src, tc.wantField)
			}
			var found bool
			for _, iss := range issues {
				if iss.Field == tc.wantField && strings.Contains(iss.Message, "not found") {
					found = true
				}
			}
			if !found {
				t.Fatalf("issues = %+v, want an unknown-field error on %q", issues, tc.wantField)
			}
		})
	}
}

func TestLoadBytesValidFlow(t *testing.T) {
	const src = `
server:
  addr: ":8080"
  max_body_bytes: 1048576
destinations:
  primary:
    type: webhook
    webhook:
      url: "https://example.test"
flows:
  github:
    nodes:
      - {id: in,   type: http_trigger, path: /webhooks/github, methods: [POST]}
      - {id: body, type: read_body,    max_bytes: 1048576}
      - {id: auth, type: auth,         scheme: hmac_sha256_hex, secret: "shh",
                                       signature_header: X-Hub-Signature-256}
      - {id: env,  type: envelope,     body_format: json}
      - {id: out,  type: publish,      destination: primary, target: github-webhooks}
    edges: [in>body, body>auth, auth>env, env>out]
`
	cfg, issues := LoadBytes([]byte(src), "test", false)
	if len(issues) > 0 {
		t.Fatalf("LoadBytes() issues = %+v, want none", issues)
	}
	if _, ok := cfg.Flows["github"]; !ok {
		t.Fatal("flow github missing")
	}
	if len(cfg.Flows["github"].Nodes) != 5 {
		t.Errorf("got %d nodes, want 5", len(cfg.Flows["github"].Nodes))
	}
}

func TestExampleConfigIsValid(t *testing.T) {
	t.Setenv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/")
	t.Setenv("GITHUB_WEBHOOK_SECRET", "gh_secret")
	cfg, issues := Load(filepath.Join("..", "..", "examples", "config.yaml"))
	for _, iss := range issues {
		if iss.Severity != config.SeverityWarning {
			t.Fatalf("example config invalid: %+v", issues)
		}
	}
	if _, ok := cfg.Flows["github_events"]; !ok {
		t.Fatal("example config should include github_events")
	}
}

func TestOnErrorExampleIsValid(t *testing.T) {
	t.Setenv("WEBHOOK_URL", "https://example.test/hooks")
	t.Setenv("GITHUB_WEBHOOK_SECRET", "gh_secret")
	t.Setenv("SF_TOKEN", "token")
	t.Setenv("DIGIFI_BASE", "https://example.test")
	cfg, issues := Load(filepath.Join("..", "..", "examples", "on_error.yaml"))
	for _, iss := range issues {
		if iss.EffectiveSeverity() == config.SeverityError {
			t.Fatalf("on_error example invalid: %+v", issues)
		}
	}
	for _, name := range []string{"github_ack_or_dlq", "proxy_or_502", "rate_or_429", "auth_or_401", "parse_or_400"} {
		if _, ok := cfg.Flows[name]; !ok {
			t.Fatalf("missing %s", name)
		}
	}
	wantPorts := []string{"ack:on_error>", "up:on_error>", "rl:on_error>", "auth:on_error>", "env:on_error>"}
	joined := ""
	for _, fc := range cfg.Flows {
		for _, e := range fc.Edges {
			joined += e + " "
		}
	}
	for _, port := range wantPorts {
		if !strings.Contains(joined, port) {
			t.Fatalf("example missing %s", port)
		}
	}
}

func TestLoadBytesRejectsGateAfterReadBody(t *testing.T) {
	const src = `
server: {addr: ":8080", max_body_bytes: 1024}
destinations:
  primary: {type: webhook, webhook: {url: "https://example.test"}}
flows:
  bad:
    nodes:
      - {id: in,   type: http_trigger, path: /w, methods: [POST]}
      - {id: body, type: read_body,    max_bytes: 1024}
      - {id: rl,   type: rate_limit,   rpm: 60}
      - {id: auth, type: auth,         scheme: hmac_sha256_hex, secret: s, signature_header: X-S}
      - {id: env,  type: envelope,     body_format: json}
      - {id: out,  type: publish,      destination: primary, target: t}
    edges: [in>body, body>rl, rl>auth, auth>env, env>out]
`
	_, issues := LoadBytes([]byte(src), "test", false)
	if len(issues) == 0 {
		t.Fatal("LoadBytes() = no issues; a gate after read_body must be rejected (I1-I4)")
	}
	joined := issuesText(issues)
	if !strings.Contains(joined, "RequestHead") || !strings.Contains(joined, "RawRequest") {
		t.Errorf("issues %q must name both port types so the operator sees the mismatch", joined)
	}
}

func TestLoadBytesAggregatesAcrossFlows(t *testing.T) {
	const src = `
server: {addr: ":8080", max_body_bytes: 1024}
destinations:
  primary: {type: webhook, webhook: {url: "https://example.test"}}
flows:
  a:
    nodes: [{id: in, type: http_trigger, path: /a, methods: [POST]}, {id: x, type: nonexistent}]
    edges: [in>x]
  b:
    nodes: [{id: in, type: http_trigger, path: /b, methods: [POST]}, {id: y, type: alsomissing}]
    edges: [in>y]
`
	_, issues := LoadBytes([]byte(src), "test", false)
	joined := issuesText(issues)
	if !strings.Contains(joined, "nonexistent") || !strings.Contains(joined, "alsomissing") {
		t.Errorf("issues %q must report problems from BOTH flows", joined)
	}
}

func TestLoadBytesRejectsUnknownField(t *testing.T) {
	const src = `
server: {addr: ":8080", max_body_bytes: 1024}
flows:
  a:
    nodes: [{id: in, type: http_trigger, path: /a, methods: [POST]}]
    edges: []
    typo_field: 1
`
	_, issues := LoadBytes([]byte(src), "test", false)
	if len(issues) == 0 {
		t.Fatal("unknown field typo_field must be rejected: a silently-ignored option is a config bug")
	}
}

func TestLoadBytesRejectsDuplicateTriggerPaths(t *testing.T) {
	const src = `
server: {addr: ":8080", max_body_bytes: 1024}
destinations:
  primary: {type: webhook, webhook: {url: "https://example.test"}}
flows:
  a:
    nodes: [{id: in, type: http_trigger, path: /same, methods: [POST]}, {id: r, type: respond, status: 200}]
    edges: [in>r]
  b:
    nodes: [{id: in, type: http_trigger, path: /same, methods: [POST]}, {id: r, type: respond, status: 200}]
    edges: [in>r]
`
	_, issues := LoadBytes([]byte(src), "test", false)
	if !strings.Contains(issuesText(issues), "/same") {
		t.Error("two flows claiming the same trigger path must be rejected; routing would be ambiguous")
	}
}

func TestLoadBytesSetsExplicitErrorSeverity(t *testing.T) {
	_, issues := LoadBytes([]byte("server: {addr: ':8080'}\n"), "test", false)
	if len(issues) == 0 {
		t.Fatal("expected issues")
	}
	for _, issue := range issues {
		if issue.Severity != config.SeverityError {
			t.Errorf("%s severity = %q", issue.Field, issue.Severity)
		}
	}
}

func issuesText(issues []config.ValidationIssue) string {
	var b strings.Builder
	for _, i := range issues {
		b.WriteString(i.Field)
		b.WriteString(": ")
		b.WriteString(i.Message)
		b.WriteString("\n")
	}
	return b.String()
}

func TestReviewRejectsAmbiguousAndReservedTriggerPaths(t *testing.T) {
	for _, path := range []string{"/webhooks/{id}", "/healthz"} {
		src := `flows:
  open:
    nodes:
      - {id: in, type: http_trigger, path: /webhooks/test}
      - {id: body, type: read_body, max_bytes: 1024}
      - {id: auth, type: no_auth}
      - {id: env, type: envelope}
      - {id: out, type: respond, status: 200}
    edges: [in>body, body>auth, auth>env, env>out]
  secure:
    nodes:
      - {id: in, type: http_trigger, path: "` + path + `"}
      - {id: body, type: read_body, max_bytes: 1024}
      - {id: auth, type: auth, scheme: exact, secret: s, signature_header: Authorization}
      - {id: env, type: envelope}
      - {id: out, type: respond, status: 200}
    edges: [in>body, body>auth, auth>env, env>out]
`
		_, issues := LoadBytes([]byte(src), "routing", false)
		if !containsIssue(issues, "overlaps") {
			t.Fatalf("path %s: %v", path, issues)
		}
	}
}

func TestReviewServingConfigPreservesDynamicTargetMatcher(t *testing.T) {
	src := strings.Replace(cleanLintYAML, "target: t", "target: '{route}-events'", 1)
	cfg, issues := LoadBytes([]byte(src), "dynamic", false)
	if len(issues) > 0 {
		t.Fatal(issues)
	}
	for _, endpoint := range cfg.ServingConfig().Endpoints {
		if endpoint.PatternSources()["route"] == "" {
			t.Fatal("factory cannot match rendered target without a pattern")
		}
	}
}

func TestObjectPayloadRejectedOnNonObjectDestination(t *testing.T) {
	const src = `
server:
  addr: ":8080"
  max_body_bytes: 1048576
destinations:
  primary:
    type: webhook
    webhook:
      url: "https://example.test"
flows:
  github:
    nodes:
      - {id: in, type: http_trigger, path: /webhooks/github, methods: [POST]}
      - {id: body, type: read_body, max_bytes: 1048576}
      - {id: auth, type: no_auth}
      - {id: env, type: envelope, body_format: json}
      - {id: out, type: publish, destination: primary, target: t, payload: object}
    edges: [in>body, body>auth, auth>env, env>out]
`
	_, issues := LoadBytes([]byte(src), "test", false)
	if !containsIssue(issues, "payload: object") && !containsMessage(issues, "object") {
		t.Fatalf("issues = %+v, want rejection of payload: object on webhook", issues)
	}
}

func TestObjectPayloadAllowedOnS3(t *testing.T) {
	const src = `
server:
  addr: ":8080"
  max_body_bytes: 1048576
destinations:
  archive:
    type: s3
    s3:
      region: us-east-1
      bucket: b
flows:
  github:
    nodes:
      - {id: in, type: http_trigger, path: /webhooks/github, methods: [POST]}
      - {id: body, type: read_body, max_bytes: 1048576}
      - {id: auth, type: no_auth}
      - {id: env, type: envelope, body_format: json}
      - {id: out, type: publish, destination: archive, target: uploads, payload: object}
    edges: [in>body, body>auth, auth>env, env>out]
`
	_, issues := LoadBytes([]byte(src), "test", false)
	for _, iss := range issues {
		if iss.Severity != config.SeverityWarning && strings.Contains(iss.Message, "payload") {
			t.Fatalf("unexpected payload issue: %+v", issues)
		}
		if iss.Severity != config.SeverityWarning && iss.Severity != "" {
			// Allow only warnings (e.g. missing rate_limit).
			if iss.Severity == config.SeverityError {
				t.Fatalf("LoadBytes issues = %+v", issues)
			}
		}
	}
	for _, iss := range issues {
		if iss.Severity == config.SeverityError {
			t.Fatalf("LoadBytes issues = %+v", issues)
		}
	}
}
