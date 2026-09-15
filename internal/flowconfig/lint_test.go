package flowconfig

import (
	"strings"
	"testing"

	"github.com/jasongiroux/hookway/internal/config"
)

func TestLint(t *testing.T) {
	t.Run("clean config is silent", func(t *testing.T) {
		issues := Lint(mustLoadV2(t, cleanLintYAML))
		if len(issues) != 0 {
			t.Fatalf("Lint() = %q, want none", issuesText(issues))
		}
	})
	t.Run("missing rate_limit is a warning", func(t *testing.T) {
		src := strings.ReplaceAll(cleanLintYAML, "      - {id: rl, type: rate_limit, rpm: 60}\n", "")
		src = strings.ReplaceAll(src, "in>rl, rl>body", "in>body")
		assertLint(t, mustLoadV2(t, src), config.SeverityWarning, "unthrottled")
	})
	t.Run("no_auth is a warning", func(t *testing.T) {
		src := strings.ReplaceAll(cleanLintYAML, "type: auth, scheme: hmac_sha256_hex, secret: s, signature_header: X-S", "type: no_auth")
		assertLint(t, mustLoadV2(t, src), config.SeverityWarning, "unverified")
	})
	t.Run("code without timeout is a warning", func(t *testing.T) {
		src := strings.ReplaceAll(cleanLintYAML,
			"      - {id: out, type: publish, destination: primary, target: t}\n    edges: [in>rl, rl>body, body>auth, auth>env, env>out]\n",
			"      - {id: code, type: code, script: 'return input.body'}\n      - {id: out, type: publish, destination: primary, target: t}\n    edges: [in>rl, rl>body, body>auth, auth>env, env>code, code>out]\n")
		assertLint(t, loadV2AllowingIssues(t, src), config.SeverityWarning, "1s default")
	})
	t.Run("read_body above server max is a warning", func(t *testing.T) {
		src := strings.ReplaceAll(cleanLintYAML, "max_bytes: 1024", "max_bytes: 2048")
		assertLint(t, mustLoadV2(t, src), config.SeverityWarning, "server.max_body_bytes")
	})
	t.Run("no publish and no respond is an error", func(t *testing.T) {
		src := strings.ReplaceAll(cleanLintYAML,
			"      - {id: out, type: publish, destination: primary, target: t}\n    edges: [in>rl, rl>body, body>auth, auth>env, env>out]\n",
			"    edges: [in>rl, rl>body, body>auth, auth>env]\n")
		assertLint(t, loadV2AllowingIssues(t, src), config.SeverityError, "never deliver or reply")
	})
	t.Run("parallel respond is an error", func(t *testing.T) {
		src := strings.ReplaceAll(cleanLintYAML,
			"      - {id: out, type: publish, destination: primary, target: t}\n    edges: [in>rl, rl>body, body>auth, auth>env, env>out]\n",
			"      - {id: r1, type: respond, status: 200}\n      - {id: r2, type: respond, status: 201}\n    edges: [in>rl, rl>body, body>auth, auth>env, env>r1, env>r2]\n")
		assertLint(t, loadV2AllowingIssues(t, src), config.SeverityError, "only one response")
	})
	t.Run("switch-branch responds are legal", func(t *testing.T) {
		src := strings.ReplaceAll(cleanLintYAML,
			"      - {id: out, type: publish, destination: primary, target: t}\n    edges: [in>rl, rl>body, body>auth, auth>env, env>out]\n",
			"      - {id: sw, type: switch, cases: [{port: a, when: {source: body, path: event, operator: eq, value: one}}]}\n      - {id: r1, type: respond, status: 200}\n      - {id: r2, type: respond, status: 201}\n    edges: [in>rl, rl>body, body>auth, auth>env, env>sw, sw:a>r1, sw:default>r2]\n")
		if issues := Lint(loadV2AllowingIssues(t, src)); containsMessage(issues, "only one response") {
			t.Fatalf("labelled switch branches must not be treated as parallel fan-out: %q", issuesText(issues))
		}
	})
	t.Run("missing publish destination is an error", func(t *testing.T) {
		src := strings.ReplaceAll(cleanLintYAML, "destination: primary", "destination: missing")
		assertLint(t, loadV2AllowingIssues(t, src), config.SeverityError, "missing")
	})
	t.Run("lookup without cache is a warning", func(t *testing.T) {
		src := strings.ReplaceAll(cleanLintYAML,
			"      - {id: out, type: publish, destination: primary, target: t}\n    edges: [in>rl, rl>body, body>auth, auth>env, env>out]\n",
			"      - {id: look, type: lookup, provider: dynamodb, key: id}\n      - {id: out, type: publish, destination: primary, target: t}\n    edges: [in>rl, rl>body, body>auth, auth>env, env>look, look>out]\n")
		assertLint(t, mustLoadV2(t, src), config.SeverityWarning, "per event")
	})
}

const cleanLintYAML = `
schema_version: 2
server: {addr: ":8080", max_body_bytes: 1024}
destinations:
  primary: {type: webhook, webhook: {url: "https://example.test"}}
flows:
  demo:
    nodes:
      - {id: in, type: http_trigger, path: /w, methods: [POST]}
      - {id: rl, type: rate_limit, rpm: 60}
      - {id: body, type: read_body, max_bytes: 1024}
      - {id: auth, type: auth, scheme: hmac_sha256_hex, secret: s, signature_header: X-S}
      - {id: env, type: envelope, body_format: json}
      - {id: out, type: publish, destination: primary, target: t}
    edges: [in>rl, rl>body, body>auth, auth>env, env>out]
`

func mustLoadV2(t *testing.T, src string) Config {
	t.Helper()
	cfg, issues := LoadBytes([]byte(src), "lint", false)
	if len(issues) > 0 {
		t.Fatalf("LoadBytes issues = %q", issuesText(issues))
	}
	return cfg
}

func loadV2AllowingIssues(t *testing.T, src string) Config {
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
