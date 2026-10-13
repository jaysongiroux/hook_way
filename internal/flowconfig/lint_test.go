package flowconfig

import (
	"strings"
	"testing"

	"github.com/jaysongiroux/hook_way/internal/config"
)

func TestLint(t *testing.T) {
	t.Run("clean config is silent", func(t *testing.T) {
		issues := Lint(mustLoad(t, cleanLintYAML))
		if len(issues) != 0 {
			t.Fatalf("Lint() = %q, want none", issuesText(issues))
		}
	})
	t.Run("missing rate_limit is a warning", func(t *testing.T) {
		src := strings.ReplaceAll(cleanLintYAML, "      - {id: rl, type: rate_limit, store: test, rpm: 60}\n", "")
		src = strings.ReplaceAll(src, "in>rl, rl>body", "in>body")
		assertLint(t, mustLoad(t, src), config.SeverityWarning, "unthrottled")
	})
	t.Run("no_auth is a warning", func(t *testing.T) {
		src := strings.ReplaceAll(cleanLintYAML, "type: auth, scheme: hmac_sha256_hex, secret: s, signature_header: X-S", "type: no_auth")
		assertLint(t, mustLoad(t, src), config.SeverityWarning, "unverified")
	})
	t.Run("code without timeout is a warning", func(t *testing.T) {
		src := strings.ReplaceAll(cleanLintYAML,
			"      - {id: out, type: publish, destination: primary, target: t}\n    edges: [in>rl, rl>body, body>auth, auth>env, env>out]\n",
			"      - {id: code, type: code, script: 'return input.body'}\n      - {id: out, type: publish, destination: primary, target: t}\n    edges: [in>rl, rl>body, body>auth, auth>env, env>code, code>out]\n")
		assertLint(t, loadAllowingIssues(t, src), config.SeverityWarning, "1s default")
	})
	t.Run("read_body above server max is a warning", func(t *testing.T) {
		src := strings.ReplaceAll(cleanLintYAML, "max_bytes: 1024", "max_bytes: 2048")
		assertLint(t, mustLoad(t, src), config.SeverityWarning, "server.max_body_bytes")
	})
	t.Run("no publish and no respond is an error", func(t *testing.T) {
		src := strings.ReplaceAll(cleanLintYAML,
			"      - {id: out, type: publish, destination: primary, target: t}\n    edges: [in>rl, rl>body, body>auth, auth>env, env>out]\n",
			"    edges: [in>rl, rl>body, body>auth, auth>env]\n")
		assertLint(t, loadAllowingIssues(t, src), config.SeverityError, "never deliver or reply")
	})
	t.Run("parallel respond is an error", func(t *testing.T) {
		src := strings.ReplaceAll(cleanLintYAML,
			"      - {id: out, type: publish, destination: primary, target: t}\n    edges: [in>rl, rl>body, body>auth, auth>env, env>out]\n",
			"      - {id: r1, type: respond, status: 200}\n      - {id: r2, type: respond, status: 201}\n    edges: [in>rl, rl>body, body>auth, auth>env, env>r1, env>r2]\n")
		assertLint(t, loadAllowingIssues(t, src), config.SeverityError, "only one response")
	})
	t.Run("switch-branch responds are legal", func(t *testing.T) {
		src := strings.ReplaceAll(cleanLintYAML,
			"      - {id: out, type: publish, destination: primary, target: t}\n    edges: [in>rl, rl>body, body>auth, auth>env, env>out]\n",
			"      - {id: sw, type: switch, cases: [{port: a, when: {source: body, path: event, operator: eq, value: one}}]}\n      - {id: r1, type: respond, status: 200}\n      - {id: r2, type: respond, status: 201}\n    edges: [in>rl, rl>body, body>auth, auth>env, env>sw, sw:a>r1, sw:default>r2]\n")
		if issues := Lint(loadAllowingIssues(t, src)); containsMessage(issues, "only one response") {
			t.Fatalf("labelled switch branches must not be treated as parallel fan-out: %q", issuesText(issues))
		}
	})
	t.Run("missing publish destination is an error", func(t *testing.T) {
		src := strings.ReplaceAll(cleanLintYAML, "destination: primary", "destination: missing")
		assertLint(t, loadAllowingIssues(t, src), config.SeverityError, "missing")
	})
	t.Run("lookup without cache is a warning", func(t *testing.T) {
		src := strings.ReplaceAll(cleanLintYAML,
			"      - {id: out, type: publish, destination: primary, target: t}\n    edges: [in>rl, rl>body, body>auth, auth>env, env>out]\n",
			"      - {id: look, type: lookup, provider: dynamodb, key: id}\n      - {id: out, type: publish, destination: primary, target: t}\n    edges: [in>rl, rl>body, body>auth, auth>env, env>look, look>out]\n")
		assertLint(t, mustLoad(t, src), config.SeverityWarning, "per event")
	})
	t.Run("lookup before filter is an I8 error", func(t *testing.T) {
		src := strings.ReplaceAll(cleanLintYAML,
			"      - {id: out, type: publish, destination: primary, target: t}\n    edges: [in>rl, rl>body, body>auth, auth>env, env>out]\n",
			"      - {id: look, type: lookup, provider: dynamodb, key: id, cache: {ttl: 5m}}\n      - {id: filt, type: filter, conditions: [{source: body, path: event, operator: eq, value: created}]}\n      - {id: out, type: publish, destination: primary, target: t}\n    edges: [in>rl, rl>body, body>auth, auth>env, env>look, look>filt, filt>out]\n")
		assertLint(t, mustLoad(t, src), config.SeverityError, "before variable resolution")
	})
	t.Run("filter then lookup is legal", func(t *testing.T) {
		src := strings.ReplaceAll(cleanLintYAML,
			"      - {id: out, type: publish, destination: primary, target: t}\n    edges: [in>rl, rl>body, body>auth, auth>env, env>out]\n",
			"      - {id: filt, type: filter, conditions: [{source: body, path: event, operator: eq, value: created}]}\n      - {id: look, type: lookup, provider: dynamodb, key: id, cache: {ttl: 5m}}\n      - {id: out, type: publish, destination: primary, target: t}\n    edges: [in>rl, rl>body, body>auth, auth>env, env>filt, filt>look, look>out]\n")
		if issues := Lint(mustLoad(t, src)); containsMessage(issues, "before variable resolution") {
			t.Fatalf("filter-then-lookup must be legal: %q", issuesText(issues))
		}
	})
}

const cleanLintYAML = `
state_stores: {test: {namespace: tests, memory: {}}}
server: {addr: ":8080", max_body_bytes: 1024}
destinations:
  primary: {type: webhook, webhook: {url: "https://example.test", delivery_mode: sync}}
flows:
  demo:
    nodes:
      - {id: in, type: http_trigger, path: /w, methods: [POST]}
      - {id: rl, type: rate_limit, store: test, rpm: 60}
      - {id: body, type: read_body, max_bytes: 1024}
      - {id: auth, type: auth, scheme: hmac_sha256_hex, secret: s, signature_header: X-S}
      - {id: env, type: envelope, body_format: json}
      - {id: out, type: publish, destination: primary, target: t}
    edges: [in>rl, rl>body, body>auth, auth>env, env>out]
`

func mustLoad(t *testing.T, src string) Config {
	t.Helper()
	cfg, issues := LoadBytes([]byte(src), "lint", false)
	if len(issues) > 0 {
		t.Fatalf("LoadBytes issues = %q", issuesText(issues))
	}
	return cfg
}

func loadAllowingIssues(t *testing.T, src string) Config {
	t.Helper()
	cfg, _ := LoadBytes([]byte(src), "lint", false)
	if cfg.Flows == nil {
		t.Fatal("expected flows to decode")
	}
	return cfg
}

func assertLint(t *testing.T, cfg Config, severity, want string) {
	t.Helper()
	issues := Lint(cfg)
	if !containsMessage(issues, want) {
		t.Fatalf("Lint() = %q, want %q", issuesText(issues), want)
	}
	for _, issue := range issues {
		if strings.Contains(issue.Message, want) || strings.Contains(issue.Field, want) {
			if issue.Severity != severity {
				t.Fatalf("severity = %q, want %q for %q", issue.Severity, severity, issue.Message)
			}
			return
		}
	}
}

func containsMessage(issues []config.ValidationIssue, want string) bool {
	for _, issue := range issues {
		if strings.Contains(issue.Message, want) || strings.Contains(issue.Field, want) {
			return true
		}
	}
	return false
}

func TestBodyFormatGuardRejectsUnreadableBodyCombinations(t *testing.T) {
	cases := []struct {
		name    string
		flow    string
		wantErr string
	}{
		{
			name: "json_validate after a raw envelope",
			flow: `
      - {id: env, type: envelope, body_format: raw}
      - {id: filt, type: json_validate, schema: {type: object}}`,
			wantErr: "json_validate requires a JSON envelope",
		},
		{
			name: "body filter after a raw envelope",
			flow: `
      - {id: env, type: envelope, body_format: raw}
      - {id: filt, type: filter, conditions: [{source: body, path: a.b, operator: exists}]}`,
			wantErr: "body_format: raw",
		},
		{
			name: "field_map transform on an xml body",
			flow: `
      - {id: env, type: envelope, body_format: xml}
      - {id: filt, type: transform, kind: field_map, name: out, mappings: [{from: a, to: b}]}`,
			wantErr: "field_map",
		},
		{
			name: "xml_prune transform on a json body",
			flow: `
      - {id: env, type: envelope, body_format: json}
      - {id: filt, type: transform, kind: xml_prune, rules: [remove_empty_parents]}`,
			wantErr: "xml_prune",
		},
		{
			name: "xml_prune transform after a raw envelope",
			flow: `
      - {id: env, type: envelope, body_format: raw}
      - {id: filt, type: transform, kind: xml_prune, rules: [remove_empty_parents]}`,
			wantErr: "xml_prune",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertLint(t, loadAllowingIssues(t, bodyFormatFlow(tc.flow)), config.SeverityError, tc.wantErr)
		})
	}
}

func TestBodyFormatGuardAllowsWorkingCombinations(t *testing.T) {
	cases := []struct {
		name string
		flow string
	}{
		{
			name: "body filter after a json envelope",
			flow: `
      - {id: env, type: envelope, body_format: json}
      - {id: filt, type: filter, conditions: [{source: body, path: a.b, operator: exists}]}`,
		},
		{
			// Task 1 made this work, so the guard must not reject it.
			name: "body filter after an xml envelope",
			flow: `
      - {id: env, type: envelope, body_format: xml}
      - {id: filt, type: filter, conditions: [{source: body, path: ORDER.Amount, operator: exists}]}`,
		},
		{
			name: "header filter after a raw envelope",
			flow: `
      - {id: env, type: envelope, body_format: raw}
      - {id: filt, type: filter, conditions: [{source: header, path: X-Event, operator: exists}]}`,
		},
		{
			name: "xml_prune after an xml envelope",
			flow: `
      - {id: env, type: envelope, body_format: xml}
      - {id: filt, type: transform, kind: xml_prune, rules: [remove_empty_parents]}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, issue := range Lint(loadAllowingIssues(t, bodyFormatFlow(tc.flow))) {
				if issue.Severity == config.SeverityError {
					t.Fatalf("unexpected error: %s (%s)", issue.Message, issue.Field)
				}
			}
		})
	}
}

// bodyFormatFlow wraps two mid-flow nodes in a complete, compilable flow. The
// guard walks from the trigger, so the prefix and the terminal node have to be
// real.
// A body condition behind body_format: raw is uniformly false, so a routing
// node degenerates to a single branch. The guard covers every node type that
// routes on a condition, not just filter.
func TestBodyFormatGuardCoversRoutingNodes(t *testing.T) {
	flow := func(nodes, edges string) string {
		return `server:
  addr: ":8080"
  max_body_bytes: 1048576
flows:
  f:
    nodes:
      - {id: in, type: http_trigger, path: /w, methods: [POST]}
      - {id: body, type: read_body, max_bytes: 1048576}
      - {id: auth, type: no_auth}
      - {id: env, type: envelope, body_format: raw}` + nodes + `
      - {id: ack, type: respond, status: 202, body: ok}
      - {id: nak, type: respond, status: 204}
    edges: [in>body, body>auth, auth>env, ` + edges + `]
`
	}
	cases := []struct {
		name, nodes, edges, wantErr string
	}{
		{
			name:    "switch case with a when condition",
			nodes:   "\n      - {id: rt, type: switch, default: other, cases: [{port: hit, when: {source: body, path: a.b, operator: exists}}]}",
			edges:   "env>rt, rt:hit>ack, rt:other>nak",
			wantErr: `switch "rt" reads source: body`,
		},
		{
			name:    "switch case with a conditions list",
			nodes:   "\n      - {id: rt, type: switch, default: other, cases: [{port: hit, match: any, conditions: [{source: header, path: X-A, operator: exists}, {source: body, path: a.b, operator: exists}]}]}",
			edges:   "env>rt, rt:hit>ack, rt:other>nak",
			wantErr: `switch "rt" reads source: body`,
		},
		{
			name:    "if_else",
			nodes:   "\n      - {id: rt, type: if_else, conditions: [{source: body, path: a.b, operator: exists}]}",
			edges:   "env>rt, rt:true>ack, rt:false>nak",
			wantErr: `if_else "rt" reads source: body`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertLint(t, loadAllowingIssues(t, flow(tc.nodes, tc.edges)), config.SeverityError, tc.wantErr)
		})
	}

	t.Run("header-only conditions are fine", func(t *testing.T) {
		nodes := "\n      - {id: rt, type: if_else, conditions: [{source: header, path: X-A, operator: exists}]}"
		issues := Lint(loadAllowingIssues(t, flow(nodes, "env>rt, rt:true>ack, rt:false>nak")))
		if containsMessage(issues, "body_format: raw") {
			t.Fatalf("issues = %q, want no body_format complaint", issuesText(issues))
		}
	})
}

func bodyFormatFlow(nodes string) string {
	return `server:
  addr: ":8080"
  max_body_bytes: 1048576
flows:
  f:
    nodes:
      - {id: in, type: http_trigger, path: /w, methods: [POST]}
      - {id: body, type: read_body, max_bytes: 1048576}
      - {id: auth, type: no_auth}` + nodes + `
      - {id: ack, type: respond, status: 202, body: ok}
    edges: [in>body, body>auth, auth>env, env>filt, filt>ack]
`
}

func TestMultipartBodyFormatGuard(t *testing.T) {
	flow := func(nodes string) string {
		return `server:
  addr: ":8080"
  max_body_bytes: 1048576
flows:
  f:
    nodes:
      - {id: in, type: http_trigger, path: /w, methods: [POST]}
      - {id: body, type: read_body, max_bytes: 1048576}
      - {id: auth, type: no_auth}` + nodes + `
      - {id: ack, type: respond, status: 202, body: ok}
    edges: [in>body, body>auth, auth>env, env>mid, mid>ack]
`
	}

	t.Run("field_map transform on a multipart body is rejected", func(t *testing.T) {
		src := flow(`
      - {id: env, type: envelope, body_format: multipart}
      - {id: mid, type: transform, kind: field_map, name: out, mappings: [{from: a, to: b}]}`)
		assertLint(t, loadAllowingIssues(t, src), config.SeverityError, "field_map")
	})

	t.Run("xml_prune transform on a multipart body is rejected", func(t *testing.T) {
		src := flow(`
      - {id: env, type: envelope, body_format: multipart}
      - {id: mid, type: transform, kind: xml_prune, rules: [remove_empty_parents]}`)
		assertLint(t, loadAllowingIssues(t, src), config.SeverityError, "xml_prune")
	})

	t.Run("a body filter on a multipart body is allowed", func(t *testing.T) {
		// This is the feature working: files.document.content_type resolves
		// through the existing dotted-path code, so the guard must not reject it.
		src := flow(`
      - {id: env, type: envelope, body_format: multipart}
      - {id: mid, type: filter, conditions: [{source: body, path: files.document.content_type, operator: eq, value: application/pdf}]}`)
		for _, issue := range Lint(loadAllowingIssues(t, src)) {
			if issue.Severity == config.SeverityError {
				t.Fatalf("unexpected error: %s (%s)", issue.Message, issue.Field)
			}
		}
	})

	t.Run("a code node on a multipart body is allowed", func(t *testing.T) {
		src := flow(`
      - {id: env, type: envelope, body_format: multipart}
      - {id: mid, type: code, timeout: 50ms, script: "return { vars: { n: String(Object.keys(input.body.files).length) } };"}`)
		for _, issue := range Lint(loadAllowingIssues(t, src)) {
			if issue.Severity == config.SeverityError {
				t.Fatalf("unexpected error: %s (%s)", issue.Message, issue.Field)
			}
		}
	})
}

func TestLintWarnsWhenReplayTLSSkipsVerification(t *testing.T) {
	cfg := Config{Replay: config.ReplayConfig{Configured: true, TLS: config.ReplayTLSConfig{InsecureSkipVerify: true}}}
	for _, issue := range Lint(cfg) {
		if issue.Field == "replay.tls.insecure_skip_verify" && issue.EffectiveSeverity() == config.SeverityWarning {
			return
		}
	}
	t.Fatal("no warning for replay.tls.insecure_skip_verify")
}
