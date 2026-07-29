package configapi

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestValidate_ValidConfigMarshalsEmptyIssuesArray(t *testing.T) {
	resp := Validate([]byte(`server:
  addr: ":8080"
  max_body_bytes: 1048576
destinations:
  billing:
    type: "rabbitmq"
    rabbitmq:
      url: "amqp://guest:guest@localhost:5672/"
      exchange: "webhooks"
      exchange_type: "direct"
      durable: true
endpoints:
  test:
    enabled: true
    path: "/test"
    destinations:
      - destination: "billing"
        target: "test-target"
        create: true
    auth:
      type: "hmac_sha256"
      secret: "test-secret"
      signature_header: "X-Signature"
`))
	out, _ := json.Marshal(resp)
	if string(out) != `{"issues":[]}` {
		t.Fatalf("expected empty issues array, got %s", out)
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

const configMissingEnvVar = `server:
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
endpoints:
  test:
    enabled: true
    path: "/test"
    destinations:
      - destination: "billing"
        target: "test-target"
        create: true
    auth:
      type: "hmac_sha256"
      secret: "test-secret"
      signature_header: "X-Signature"
`

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

const configWebhookURLMissingEnvVar = `server:
  addr: ":8080"
  max_body_bytes: 1048576
destinations:
  forward:
    type: "webhook"
    webhook:
      url: "${DEFINITELY_UNSET_HOOKWAY_TEST_WEBHOOK_URL}"
endpoints:
  test:
    enabled: true
    path: "/test"
    destinations:
      - destination: "forward"
        target: "test-target"
    auth:
      type: "hmac_sha256"
      secret: "test-secret"
      signature_header: "X-Signature"
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

const configWebhookURLGenuinelyInvalid = `server:
  addr: ":8080"
  max_body_bytes: 1048576
destinations:
  forward:
    type: "webhook"
    webhook:
      url: "ftp://x"
endpoints:
  test:
    enabled: true
    path: "/test"
    destinations:
      - destination: "forward"
        target: "test-target"
    auth:
      type: "hmac_sha256"
      secret: "test-secret"
      signature_header: "X-Signature"
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
	const secretValue = "sk_live_should_never_leave_the_server_abc123"
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
	return `server:
  addr: ":8080"
  max_body_bytes: 1048576
destinations:
  exfil:
    type: "` + envRef + `"
endpoints:
  test:
    enabled: true
    path: "/test"
    destinations:
      - destination: "exfil"
        target: "test-target"
    auth:
      type: "hmac_sha256"
      secret: "test-secret"
      signature_header: "X-Signature"
`
}
