package config

import (
	"strings"
	"testing"
)

func TestLoadFailsWhenReferencedEnvVarIsUnset(t *testing.T) {
	t.Setenv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/")
	body := replace(validYAML(), "${PADDLE_SECRET}", "${HOOKWAY_TEST_ENV_MISSING_XYZ}")

	_, err := Load(writeConfig(t, body))

	if err == nil {
		t.Fatal("expected an error for an unset referenced env var")
	}
	if !strings.Contains(err.Error(), "HOOKWAY_TEST_ENV_MISSING_XYZ") {
		t.Fatalf("error does not name the missing var: %v", err)
	}
}

func TestLoadCollectsAllMissingEnvVarsInOneError(t *testing.T) {
	body := replace(validYAML(), "${RABBITMQ_URL}", "${HOOKWAY_TEST_ENV_MISSING_A}")
	body = replace(body, "${PADDLE_SECRET}", "${HOOKWAY_TEST_ENV_MISSING_B}")

	_, err := Load(writeConfig(t, body))

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
	t.Setenv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/")
	body := replace(validYAML(), `secret: "${PADDLE_SECRET}"`, `secret: "pre$$post"`)

	cfg, err := Load(writeConfig(t, body))

	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if got := cfg.Endpoints["paddle_billing"].Auth.Secret; got != "pre$post" {
		t.Fatalf("secret = %q, want %q ($$ should expand to a literal $)", got, "pre$post")
	}
}

func TestLoadAllowsSetButEmptyEnvVar(t *testing.T) {
	t.Setenv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/")
	t.Setenv("PADDLE_SECRET", "") // explicitly set, but empty

	_, err := Load(writeConfig(t, validYAML()))

	// The expanded secret is empty, so the ordinary "secret is required"
	// validator still rejects the config -- but expansion itself must not
	// treat a set-but-empty var as missing.
	if err == nil {
		t.Fatal("expected a validation error because the expanded secret is empty")
	}
	if strings.Contains(err.Error(), "PADDLE_SECRET") {
		t.Fatalf("a set-but-empty env var must not be reported as missing: %v", err)
	}
	if !strings.Contains(err.Error(), "endpoints.paddle_billing.auth.secret") {
		t.Fatalf("expected the normal empty-secret validation error, got: %v", err)
	}
}

func TestLoadStillExpandsNormalEnvironmentReferences(t *testing.T) {
	// Existing behavior: a config with all referenced vars set loads and
	// expands normally (mirrors TestLoadExpandsEnvironmentAndValidates but
	// scoped to this file so it's easy to see it stays green next to the new
	// strict-expansion tests).
	t.Setenv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/")
	t.Setenv("PADDLE_SECRET", "pdl_secret")

	cfg, err := Load(writeConfig(t, validYAML()))

	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.Destinations["billing"].RabbitMQ.URL != "amqp://guest:guest@localhost:5672/" {
		t.Fatalf("RabbitMQ URL not expanded: %q", cfg.Destinations["billing"].RabbitMQ.URL)
	}
	if cfg.Endpoints["paddle_billing"].Auth.Secret != "pdl_secret" {
		t.Fatal("Paddle secret not expanded")
	}
}
