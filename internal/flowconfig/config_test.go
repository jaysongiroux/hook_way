package flowconfig

import (
	"strings"
	"testing"

	"github.com/jasongiroux/hookway/internal/config"
)

func TestDetectSchemaVersion(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want int
	}{
		{"explicit 2", "schema_version: 2\nflows: {}\n", 2},
		{"explicit 1", "schema_version: 1\nendpoints: {}\n", 1},
		{"absent means 1", "endpoints: {}\n", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := DetectSchemaVersion([]byte(tc.src))
			if err != nil {
				t.Fatalf("DetectSchemaVersion() = %v", err)
			}
			if got != tc.want {
				t.Errorf("= %d, want %d", got, tc.want)
			}
		})
	}
}

func TestLoadBytesValidFlow(t *testing.T) {
	const src = `
schema_version: 2
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
	if cfg.SchemaVersion != 2 {
		t.Errorf("SchemaVersion = %d, want 2", cfg.SchemaVersion)
	}
	if _, ok := cfg.Flows["github"]; !ok {
		t.Fatal("flow github missing")
	}
	if len(cfg.Flows["github"].Nodes) != 5 {
		t.Errorf("got %d nodes, want 5", len(cfg.Flows["github"].Nodes))
	}
}

func TestLoadBytesRejectsGateAfterReadBody(t *testing.T) {
	const src = `
schema_version: 2
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
schema_version: 2
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
schema_version: 2
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
schema_version: 2
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
	_, issues := LoadBytes([]byte("schema_version: 2\nserver: {addr: ':8080'}\n"), "test", false)
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
