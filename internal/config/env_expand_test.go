package config

import (
	"strings"
	"testing"
)

func TestLoadFailsWhenReferencedEnvVarIsUnset(t *testing.T) {
	body := replace(validSharedYAML(), "${RABBITMQ_URL}", "${HOOKWAY_TEST_ENV_MISSING_XYZ}")

	_, err := loadSharedYAML(t, body)

	if err == nil {
		t.Fatal("expected an error for an unset referenced env var")
	}
	if !strings.Contains(err.Error(), "HOOKWAY_TEST_ENV_MISSING_XYZ") {
		t.Fatalf("error does not name the missing var: %v", err)
	}
}

func TestLoadCollectsAllMissingEnvVarsInOneError(t *testing.T) {
	body := replace(validSharedYAML(), "${RABBITMQ_URL}", "${HOOKWAY_TEST_ENV_MISSING_A}")
	body = replace(body, "security:\n", "security:\n  api_key: \"${HOOKWAY_TEST_ENV_MISSING_B}\"\n")

	_, err := loadSharedYAML(t, body)

	if err == nil {
		t.Fatal("expected an error for unset referenced env vars")
	}
	for _, want := range []string{"HOOKWAY_TEST_ENV_MISSING_A", "HOOKWAY_TEST_ENV_MISSING_B"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error missing %s, collected errors should name every missing var: %v", want, err)
		}
	}
}

func TestLoadExpandsDoubleDollarAsLiteralDollar(t *testing.T) {
	body := replace(validSharedYAML(), `url: "${RABBITMQ_URL}"`, `url: "amqp://guest:pre$$post@localhost:5672/"`)

	cfg, err := loadSharedYAML(t, body)

	if err != nil {
		t.Fatalf("load returned error: %v", err)
	}
	// Dummy broker credentials; the point of the fixture is the literal $ that
	// the $$ escape must produce, not the userinfo.
	const wantURL = "amqp://guest:pre$post@localhost:5672/" //nolint:gosec // G101: not a real credential
	if got := cfg.Destinations["billing"].RabbitMQ.URL; got != wantURL {
		t.Fatalf("url = %q, want a literal $ from the $$ escape", got)
	}
}

func TestLoadAllowsSetButEmptyEnvVar(t *testing.T) {
	t.Setenv("RABBITMQ_URL", "") // explicitly set, but empty

	_, err := loadSharedYAML(t, validSharedYAML())

	// The expanded url is empty, so the ordinary "url is required" validator
	// still rejects the config -- but expansion itself must not treat a
	// set-but-empty var as missing.
	if err == nil {
		t.Fatal("expected a validation error because the expanded url is empty")
	}
	if strings.Contains(err.Error(), "but is not set") {
		t.Fatalf("a set-but-empty env var must not be reported as missing: %v", err)
	}
	if !strings.Contains(err.Error(), "destinations.billing.rabbitmq.url") {
		t.Fatalf("expected the normal empty-url validation error, got: %v", err)
	}
}

func TestLoadStillExpandsNormalEnvironmentReferences(t *testing.T) {
	// Existing behavior: a config with all referenced vars set loads and
	// expands normally (mirrors TestLoadExpandsEnvironmentAndValidates but
	// scoped to this file so it's easy to see it stays green next to the new
	// strict-expansion tests).
	t.Setenv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/")
	t.Setenv("HOOKWAY_TEST_API_KEY", "admin-key")
	body := replace(validSharedYAML(), "security:\n", "security:\n  api_key: \"${HOOKWAY_TEST_API_KEY}\"\n")

	cfg, err := loadSharedYAML(t, body)

	if err != nil {
		t.Fatalf("load returned error: %v", err)
	}
	if cfg.Destinations["billing"].RabbitMQ.URL != "amqp://guest:guest@localhost:5672/" { // #nosec G101 -- expanded test fixture
		t.Fatalf("RabbitMQ URL not expanded: %q", cfg.Destinations["billing"].RabbitMQ.URL)
	}
	if cfg.Security.APIKey != "admin-key" {
		t.Fatalf("security.api_key not expanded: %q", cfg.Security.APIKey)
	}
}

// TestValidateBytesDoesNotExpandEnvironment pins the untrusted-bytes contract:
// the non-expanding validation path leaves ${VAR} as an opaque literal so a
// caller cannot read process secrets back out of a validation response.
func TestValidateBytesDoesNotExpandEnvironment(t *testing.T) {
	t.Setenv("RABBITMQ_URL", "amqp://guest:supersecret@localhost:5672/")
	for _, issue := range sharedIssues(validSharedYAML()) {
		if strings.Contains(issue.Message, "supersecret") {
			t.Fatalf("validation leaked an expanded env var: %#v", issue)
		}
	}
}
