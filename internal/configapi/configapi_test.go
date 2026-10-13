package configapi

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

func TestValidate_RejectsLegacyTopLevelKeys(t *testing.T) {
	// There is no config version field and no endpoints format, so both are
	// ordinary unknown top-level keys at error severity.
	for _, tc := range []struct{ name, src, field string }{
		{"endpoints", "endpoints: {}\n", "endpoints"},
		{"schema_version", "schema_version: 2\nflows: {}\n", "schema_version"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := Validate([]byte(tc.src))
			var found bool
			for _, iss := range resp.Issues {
				if iss.Field == tc.field && iss.Severity == "error" && strings.Contains(iss.Message, "not found") {
					found = true
				}
			}
			if !found {
				t.Fatalf("issues = %+v, want an error-severity unknown-field issue on %q", resp.Issues, tc.field)
			}
		})
	}
}

const validDeliveryConfig = `state_stores: {test: {namespace: tests, memory: {}}}
server:
  addr: ":8080"
  max_body_bytes: 1048576
destinations:
  billing:
    type: "webhook"
    webhook:
      url: "https://example.test/hooks"
      delivery_mode: sync
flows:
  test:
    nodes:
      - {id: in, type: http_trigger, path: /test, methods: [POST]}
      - {id: rl, type: rate_limit, store: test, rpm: 60}
      - {id: body, type: read_body, max_bytes: 1048576}
      - {id: auth, type: auth, scheme: hmac_sha256_hex, secret: "test-secret", signature_header: X-Signature}
      - {id: env, type: envelope, body_format: json}
      - {id: out, type: publish, destination: billing, target: test-target}
    edges: [in>rl, rl>body, body>auth, auth>env, env>out]
`

func TestValidate_ValidConfigMarshalsEmptyIssuesArray(t *testing.T) {
	resp := Validate([]byte(validDeliveryConfig))
	out, _ := json.Marshal(resp)
	if string(out) != `{"issues":[]}` {
		t.Fatalf("expected empty issues array, got %s", out)
	}
}

func TestValidateAsyncDeliveryIsNonBlockingWarning(t *testing.T) {
	src := strings.ReplaceAll(validDeliveryConfig, "delivery_mode: sync", "delivery_mode: async")
	resp := Validate([]byte(src))
	if len(resp.Issues) != 1 {
		t.Fatalf("issues = %+v", resp.Issues)
	}
	issue := resp.Issues[0]
	if issue.Severity != "warning" || issue.Field != "destinations.billing.webhook.delivery_mode" || !strings.Contains(issue.Message, "in-memory queue") || issue.Suggestion == "" {
		t.Fatalf("expected actionable delivery warning, got %+v", issue)
	}
}

func TestValidate_InvalidConfigSetsErrorSeverity(t *testing.T) {
	resp := Validate([]byte("server:\n  max_body_bytes: 1048576\n"))
	if len(resp.Issues) == 0 {
		t.Fatal("expected at least one issue")
	}
	for _, iss := range resp.Issues {
		if iss.Severity != "error" {
			t.Errorf("expected severity error, got %q", iss.Severity)
		}
	}
}

func TestSchema_ReturnsValidJSON(t *testing.T) {
	data, err := Schema()
	if err != nil {
		t.Fatalf("Schema error: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("schema not valid JSON: %v", err)
	}
}

func TestStateStoreSchemaRequiresExactlyOneBackend(t *testing.T) {
	data, err := Schema()
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	stores := doc["properties"].(map[string]any)["state_stores"]
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("https://hookway.test/state-stores", stores); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile("https://hookway.test/state-stores")
	if err != nil {
		t.Fatal(err)
	}
	for mask := 0; mask < 8; mask++ {
		store := map[string]any{"namespace": "test"}
		count := 0
		for i, backend := range []string{"memory", "redis", "postgres"} {
			if mask&(1<<i) != 0 {
				store[backend] = map[string]any{}
				count++
			}
		}
		err := schema.Validate(map[string]any{"test": store})
		if (err == nil) != (count == 1) {
			t.Errorf("backend mask %d: validation error=%v, want valid=%t", mask, err, count == 1)
		}
	}
}

const configMissingEnvVar = `state_stores: {test: {namespace: tests, memory: {}}}
server:
  addr: ":8080"
  max_body_bytes: 1048576
destinations:
  billing:
    type: "rabbitmq"
    rabbitmq:
      url: "${DEFINITELY_UNSET_HOOKWAY_TEST_VAR}"
      exchange: "webhooks"
      exchange_type: "direct"
      durable: true
flows:
  test:
    nodes:
      - {id: in, type: http_trigger, path: /test, methods: [POST]}
      - {id: rl, type: rate_limit, store: test, rpm: 60}
      - {id: body, type: read_body, max_bytes: 1048576}
      - {id: auth, type: auth, scheme: hmac_sha256_hex, secret: "test-secret", signature_header: X-Signature}
      - {id: env, type: envelope, body_format: json}
      - {id: out, type: publish, destination: billing, target: test-target}
    edges: [in>rl, rl>body, body>auth, auth>env, env>out]
`

func TestValidateWithEnvSchemaKeywordsAreLiteral(t *testing.T) {
	data, err := os.ReadFile("../../examples/json-validation.yaml")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("schema", "not-a-keyword")
	t.Setenv("ref", "not-a-keyword")
	for _, issue := range ValidateWithEnv(data, true).Issues {
		if issue.Severity == "error" || strings.HasPrefix(issue.Field, "config.env.") {
			t.Fatalf("valid schema rejected after CLI environment expansion: %+v", issue)
		}
	}
}

func TestValidateWithEnv_MissingVarIsWarningSeverity(t *testing.T) {
	resp := ValidateWithEnv([]byte(configMissingEnvVar), true)
	if len(resp.Issues) != 1 {
		t.Fatalf("expected exactly one issue, got %#v", resp.Issues)
	}
	iss := resp.Issues[0]
	if iss.Severity != "warning" {
		t.Fatalf("severity = %q, want \"warning\"", iss.Severity)
	}
	if iss.Field != "config.env.DEFINITELY_UNSET_HOOKWAY_TEST_VAR" {
		t.Fatalf("field = %q", iss.Field)
	}
}

func TestValidateWithEnv_NoExpandSkipsMissingVarEntirely(t *testing.T) {
	resp := ValidateWithEnv([]byte(configMissingEnvVar), false)
	if len(resp.Issues) != 0 {
		t.Fatalf("expected no issues with expand=false, got %#v", resp.Issues)
	}
}

func TestValidateWithEnv_ExpandTrueStillReportsStructuralErrors(t *testing.T) {
	resp := ValidateWithEnv([]byte("server:\n  max_body_bytes: 1048576\n"), true)
	if len(resp.Issues) == 0 {
		t.Fatal("expected at least one structural issue")
	}
	for _, iss := range resp.Issues {
		if iss.Severity != "error" {
			t.Fatalf("expected severity error for structural issue, got %q (%#v)", iss.Severity, iss)
		}
	}
}

const configWebhookURLMissingEnvVar = `state_stores: {test: {namespace: tests, memory: {}}}
server:
  addr: ":8080"
  max_body_bytes: 1048576
destinations:
  forward:
    type: "webhook"
    webhook:
      url: "${DEFINITELY_UNSET_HOOKWAY_TEST_WEBHOOK_URL}"
flows:
  test:
    nodes:
      - {id: in, type: http_trigger, path: /test, methods: [POST]}
      - {id: rl, type: rate_limit, store: test, rpm: 60}
      - {id: body, type: read_body, max_bytes: 1048576}
      - {id: auth, type: auth, scheme: hmac_sha256_hex, secret: "test-secret", signature_header: X-Signature}
      - {id: env, type: envelope, body_format: json}
      - {id: out, type: publish, destination: forward, target: test-target}
    edges: [in>rl, rl>body, body>auth, auth>env, env>out]
`

// TestValidateWithEnv_MissingWebhookURLDowngradesFormatCheckToWarning is the
// regression test for the review finding: leaving a missing ${VAR} reference
// as the literal string "${NAME}" (ExpandEnvLenient's contract) still fails
// FORMAT validators, not just emptiness checks. webhook.url's "must be http
// or https" prefix check saw the literal "${DEFINITELY_UNSET_HOOKWAY_TEST_WEBHOOK_URL}"
// and raised it as a severity:"error" structural issue, which defeated C5's
// goal of letting CI validate a config without production secrets set (exit
// 0). This must now downgrade to "warning" (or be dropped) and the overall
// response must contain no "error"-severity issues.
func TestValidateWithEnv_MissingWebhookURLDowngradesFormatCheckToWarning(t *testing.T) {
	resp := ValidateWithEnv([]byte(configWebhookURLMissingEnvVar), true)

	for _, iss := range resp.Issues {
		if iss.Severity == "error" {
			t.Fatalf("expected no error-severity issues, got %#v in %#v", iss, resp.Issues)
		}
	}

	var sawMissingVarWarning, sawURLIssue bool
	for _, iss := range resp.Issues {
		if iss.Field == "config.env.DEFINITELY_UNSET_HOOKWAY_TEST_WEBHOOK_URL" && iss.Severity == "warning" {
			sawMissingVarWarning = true
		}
		if iss.Field == "destinations.forward.webhook.url" {
			sawURLIssue = true
			if iss.Severity != "warning" {
				t.Fatalf("webhook.url issue severity = %q, want \"warning\"", iss.Severity)
			}
		}
	}
	if !sawMissingVarWarning {
		t.Fatalf("expected a config.env.* warning for the missing var, got %#v", resp.Issues)
	}
	_ = sawURLIssue // downgraded-to-warning or absent are both acceptable per spec
}

const configWebhookURLGenuinelyInvalid = `state_stores: {test: {namespace: tests, memory: {}}}
server:
  addr: ":8080"
  max_body_bytes: 1048576
destinations:
  forward:
    type: "webhook"
    webhook:
      url: "ftp://x"
flows:
  test:
    nodes:
      - {id: in, type: http_trigger, path: /test, methods: [POST]}
      - {id: rl, type: rate_limit, store: test, rpm: 60}
      - {id: body, type: read_body, max_bytes: 1048576}
      - {id: auth, type: auth, scheme: hmac_sha256_hex, secret: "test-secret", signature_header: X-Signature}
      - {id: env, type: envelope, body_format: json}
      - {id: out, type: publish, destination: forward, target: test-target}
    edges: [in>rl, rl>body, body>auth, auth>env, env>out]
`

// TestValidateWithEnv_GenuineURLFormatErrorStaysError guards against
// over-correcting the fix above: a hardcoded, genuinely-invalid URL with NO
// env-var reference involved must still be reported at severity "error" (and
// therefore still fail CI), not swept into the same downgrade.
func TestValidateWithEnv_GenuineURLFormatErrorStaysError(t *testing.T) {
	resp := ValidateWithEnv([]byte(configWebhookURLGenuinelyInvalid), true)

	var found bool
	for _, iss := range resp.Issues {
		if iss.Field == "destinations.forward.webhook.url" {
			found = true
			if iss.Severity != "error" {
				t.Fatalf("severity = %q, want \"error\" for a hardcoded invalid URL with no env reference", iss.Severity)
			}
		}
	}
	if !found {
		t.Fatalf("expected a webhook.url issue, got %#v", resp.Issues)
	}
}

// TestValidate_NeverExpandsEnv guards the editor-safety boundary: the plain
// Validate function (used by internal/editor's POST /validate) must NEVER
// expand ${VAR} references against the process environment, even when the
// referenced variable is unset. If it did, this would surface as a
// "config.env.*" issue; it must not.
func TestValidate_NeverExpandsEnv(t *testing.T) {
	resp := Validate([]byte(configMissingEnvVar))
	for _, iss := range resp.Issues {
		if strings.HasPrefix(iss.Field, "config.env.") {
			t.Fatalf("Validate() expanded env and leaked a config.env issue: %#v", iss)
		}
	}
}

// TestValidate_ExfilBoundary_SetEnvVarValueNeverLeaks is a regression guard
// (final review item 4) for the exact function internal/editor's POST
// /validate calls: Validate(data) in this package. The editor accepts
// candidate config from an untrusted HTTP client. If Validate ever started
// expanding ${VAR} against the process environment, a client could probe for
// server-side secrets by referencing HOME-style env var names in a config
// field and reading the value back out of the validation response.
//
// The canary field is destinations.<id>.type: an unsupported value there
// produces a validation issue whose Message directly echoes the raw field
// value via %q (see internal/config/validation.go). That makes it a genuine
// leak vector if expansion ever happened — unlike a pure emptiness/required
// check, which would only reveal whether the var is set, not its value.
//
// This test sets a HOME-style env var to a distinctive secret value, feeds a
// config that references it with ${...} as the destination type, and asserts:
//  1. the literal "${...}" reference itself is echoed verbatim in the issue
//     message (proving no expansion — the message is the unresolved
//     reference, not empty and not the secret), and
//  2. the secret value does not appear anywhere in the JSON-serialized
//     response, for either a config referencing a SET var or one referencing
//     an UNSET var.
func TestValidate_ExfilBoundary_SetEnvVarValueNeverLeaks(t *testing.T) {
	const secretValue = "test-secret-must-never-appear-in-validate-json"
	t.Setenv("HOOKWAY_TEST_EXFIL_HOME_STYLE_VAR", secretValue)

	cases := map[string]string{
		"set var":   "${HOOKWAY_TEST_EXFIL_HOME_STYLE_VAR}",
		"unset var": "${DEFINITELY_UNSET_HOOKWAY_EXFIL_VAR}",
	}
	for name, envRef := range cases {
		t.Run(name, func(t *testing.T) {
			resp := Validate([]byte(exfilBoundaryConfig(envRef)))

			out, err := json.Marshal(resp)
			if err != nil {
				t.Fatalf("marshal response: %v", err)
			}
			if strings.Contains(string(out), secretValue) {
				t.Fatalf("Validate() response leaked the env var value: %s", out)
			}

			// No env-expansion issue of any kind (this is the plain,
			// never-expanding Validate path, not ValidateWithEnv).
			for _, iss := range resp.Issues {
				if strings.HasPrefix(iss.Field, "config.env.") {
					t.Fatalf("Validate() expanded env and leaked a config.env issue: %#v", iss)
				}
			}

			// The unexpanded "${...}" literal must be what's echoed by the
			// destinations.exfil.type issue — proving the raw reference
			// survived rather than having been resolved (successfully or not).
			found := false
			for _, iss := range resp.Issues {
				if iss.Field == "destinations.exfil.type" {
					if !strings.Contains(iss.Message, envRef) {
						t.Fatalf("expected destinations.exfil.type issue to echo the literal %q, got %#v", envRef, iss)
					}
					found = true
				}
			}
			if !found {
				t.Fatalf("expected a destinations.exfil.type issue, got %#v", resp.Issues)
			}
		})
	}
}

// exfilBoundaryConfig builds a minimal config whose only defect is an
// unsupported destination type set to envRef (an unexpanded "${VAR}"
// literal), which trips the destinations.<id>.type enum check — echoing the
// raw value in the issue message — when (and only when) envRef is NOT
// expanded against the environment.
func exfilBoundaryConfig(envRef string) string {
	return `state_stores: {test: {namespace: tests, memory: {}}}
server:
  addr: ":8080"
  max_body_bytes: 1048576
destinations:
  exfil:
    type: "` + envRef + `"
flows:
  test:
    nodes:
      - {id: in, type: http_trigger, path: /test, methods: [POST]}
      - {id: rl, type: rate_limit, store: test, rpm: 60}
      - {id: body, type: read_body, max_bytes: 1048576}
      - {id: auth, type: auth, scheme: hmac_sha256_hex, secret: "test-secret", signature_header: X-Signature}
      - {id: env, type: envelope, body_format: json}
      - {id: out, type: publish, destination: exfil, target: test-target}
    edges: [in>rl, rl>body, body>auth, auth>env, env>out]
`
}

func TestValidate_PreservesLintWarningSeverity(t *testing.T) {
	src := `
state_stores: {test: {namespace: tests, memory: {}}}
server: {addr: ":8080", max_body_bytes: 1024}
destinations:
  primary: {type: webhook, webhook: {url: "https://example.test/hooks"}}
flows:
  github:
    nodes:
      - {id: in, type: http_trigger, path: /webhooks/github, methods: [POST]}
      - {id: body, type: read_body, max_bytes: 1024}
      - {id: auth, type: auth, scheme: hmac_sha256_hex, secret: s, signature_header: X-S}
      - {id: env, type: envelope, body_format: json}
      - {id: out, type: publish, destination: primary, target: t}
    edges: [in>body, body>auth, auth>env, env>out]
`
	resp := Validate([]byte(src))
	if len(resp.Issues) == 0 {
		t.Fatal("expected lint warnings on an unthrottled flow")
	}
	found := false
	for _, iss := range resp.Issues {
		if strings.Contains(iss.Message, "unthrottled") {
			found = true
			if iss.Severity != "warning" {
				t.Fatalf("unthrottled severity = %q, want warning", iss.Severity)
			}
		}
		if iss.Severity == "" {
			t.Fatalf("issue %q has empty severity", iss.Message)
		}
	}
	if !found {
		t.Fatalf("issues = %#v", resp.Issues)
	}
}

const configConnectionModeMissingEnvVar = `state_stores: {test: {namespace: tests, memory: {}}}
server:
  addr: ":8080"
  max_body_bytes: 1048576
connections:
  cache:
    type: redis
    mode: "${DEFINITELY_UNSET_HOOKWAY_TEST_REDIS_MODE}"
    url: "redis://redis:6379"
destinations:
  events:
    type: "redis_stream"
    connection: cache
flows:
  test:
    nodes:
      - {id: in, type: http_trigger, path: /test, methods: [POST]}
      - {id: body, type: read_body, max_bytes: 1048576}
      - {id: auth, type: no_auth}
      - {id: env, type: envelope, body_format: json}
      - {id: out, type: publish, destination: events, target: test-target}
    edges: [in>body, body>auth, auth>env, env>out]
`

// TestValidateWithEnv_MissingConnectionVarDowngradesToWarning is the
// regression guard for the one thing about named connections that could not be
// handled purely by merging them into the destination.
//
// Structural issues are produced against the post-merge config, so without
// config.remapConnectionIssues this enum failure would be reported at
// "destinations.events.redis.mode" -- a path that does not exist in the
// document, because the operator wrote it under connections. fieldStillHasMissingRef
// would then fail to resolve it, the downgrade would not happen, and
// `hookway validate --json` would hard-fail in CI for every connection-based
// config whose secrets are not exported. That is precisely the workflow the
// lenient-expansion contract exists to support.
func TestValidateWithEnv_MissingConnectionVarDowngradesToWarning(t *testing.T) {
	resp := ValidateWithEnv([]byte(configConnectionModeMissingEnvVar), true)

	for _, iss := range resp.Issues {
		if iss.Severity == "error" {
			t.Fatalf("expected no error-severity issues, got %#v in %#v", iss, resp.Issues)
		}
	}
	var sawModeIssue bool
	for _, iss := range resp.Issues {
		if iss.Field == "connections.cache.mode" {
			sawModeIssue = true
		}
		if iss.Field == "destinations.events.redis.mode" {
			t.Errorf("issue was left on a path the operator never wrote: %#v", iss)
		}
	}
	if !sawModeIssue {
		t.Fatalf("expected the mode issue to be reported on connections.cache.mode, got %#v", resp.Issues)
	}
}

func TestNativeDeclarationSchemas(t *testing.T) {
	b, err := Schema()
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(b, &schema); err != nil {
		t.Fatal(err)
	}
	properties := schema["properties"].(map[string]any)
	for _, name := range []string{"credentials", "routing"} {
		if properties[name] == nil {
			t.Fatal("missing declaration schema", name)
		}
	}
}
