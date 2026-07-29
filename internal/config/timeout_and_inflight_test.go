package config

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// --- D1: non-positive server timeouts are rejected when explicitly set ---

func TestLoadRejectsNonPositiveServerTimeouts(t *testing.T) {
	cases := []struct {
		field      string
		key        string
		defaultVal string
	}{
		{"server.read_timeout", "read_timeout", "5s"},
		{"server.write_timeout", "write_timeout", "10s"},
		{"server.idle_timeout", "idle_timeout", "60s"},
		{"server.shutdown_timeout", "shutdown_timeout", "10s"},
		{"server.publish_timeout", "publish_timeout", "10s"},
	}
	for _, c := range cases {
		for _, bad := range []string{"-5s", "0s"} {
			t.Run(c.field+"/"+bad, func(t *testing.T) {
				t.Setenv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/")
				t.Setenv("PADDLE_SECRET", "pdl_secret")
				old := fmt.Sprintf("%s: %q", c.key, c.defaultVal)
				replacement := fmt.Sprintf("%s: %q", c.key, bad)
				body := replace(validYAML(), old, replacement)
				_, err := Load(writeConfig(t, body))
				if err == nil {
					t.Fatalf("expected validation error for %s = %s", c.field, bad)
				}
				if !strings.Contains(err.Error(), c.field) {
					t.Fatalf("error missing field %s: %v", c.field, err)
				}
			})
		}
	}
}

func TestLoadAllowsOmittedServerTimeoutsToUseDefaults(t *testing.T) {
	t.Setenv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/")
	t.Setenv("PADDLE_SECRET", "pdl_secret")
	body := validYAML()
	for _, kv := range []string{
		`read_timeout: "5s"`,
		`write_timeout: "10s"`,
		`idle_timeout: "60s"`,
		`shutdown_timeout: "10s"`,
		`publish_timeout: "10s"`,
	} {
		body = strings.Replace(body, "  "+kv+"\n", "", 1)
	}
	cfg, err := Load(writeConfig(t, body))
	if err != nil {
		t.Fatalf("Load returned error for omitted timeouts: %v", err)
	}
	if cfg.Server.ReadTimeout <= 0 || cfg.Server.WriteTimeout <= 0 || cfg.Server.IdleTimeout <= 0 || cfg.Server.ShutdownTimeout <= 0 || cfg.Server.PublishTimeout <= 0 {
		t.Fatalf("expected positive default timeouts, got %#v", cfg.Server)
	}
}

// --- B4: server.publish_timeout defaults and validation ---

func TestPublishTimeoutDefaultsTo10sWhenOmitted(t *testing.T) {
	t.Setenv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/")
	t.Setenv("PADDLE_SECRET", "pdl_secret")
	body := strings.Replace(validYAML(), "  publish_timeout: \"10s\"\n", "", 1)
	cfg, err := Load(writeConfig(t, body))
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.Server.PublishTimeout != 10*time.Second {
		t.Fatalf("PublishTimeout = %s, want 10s default (coherent with write_timeout/shutdown_timeout defaults)", cfg.Server.PublishTimeout)
	}
}

func TestPublishTimeoutRejectsExplicitNegativeValue(t *testing.T) {
	t.Setenv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/")
	t.Setenv("PADDLE_SECRET", "pdl_secret")
	body := replace(validYAML(), `publish_timeout: "10s"`, `publish_timeout: "-1s"`)
	_, err := Load(writeConfig(t, body))
	if err == nil {
		t.Fatal("expected validation error for server.publish_timeout = -1s")
	}
	if !strings.Contains(err.Error(), "server.publish_timeout") {
		t.Fatalf("error missing field: %v", err)
	}
}

// --- Item 1 (final review): publish_timeout cross-field coherence with
// shutdown_timeout/write_timeout ---

func TestPublishTimeoutRejectedWhenGreaterThanShutdownTimeout(t *testing.T) {
	t.Setenv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/")
	t.Setenv("PADDLE_SECRET", "pdl_secret")
	// Raise write_timeout out of the way so only the shutdown_timeout
	// relationship is under test; publish_timeout (20s) exceeds
	// shutdown_timeout (10s) but not write_timeout (30s).
	body := replace(validYAML(), `write_timeout: "10s"`, `write_timeout: "30s"`)
	body = replace(body, `publish_timeout: "10s"`, `publish_timeout: "20s"`)
	_, err := Load(writeConfig(t, body))
	if err == nil {
		t.Fatal("expected validation error when publish_timeout exceeds shutdown_timeout")
	}
	if !strings.Contains(err.Error(), "server.publish_timeout") || !strings.Contains(err.Error(), "server.shutdown_timeout") {
		t.Fatalf("error should mention both server.publish_timeout and server.shutdown_timeout: %v", err)
	}
}

func TestPublishTimeoutRejectedWhenGreaterThanWriteTimeout(t *testing.T) {
	t.Setenv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/")
	t.Setenv("PADDLE_SECRET", "pdl_secret")
	// Raise shutdown_timeout out of the way so only the write_timeout
	// relationship is under test; publish_timeout (20s) exceeds
	// write_timeout (10s) but not shutdown_timeout (30s).
	body := replace(validYAML(), `shutdown_timeout: "10s"`, `shutdown_timeout: "30s"`)
	body = replace(body, `publish_timeout: "10s"`, `publish_timeout: "20s"`)
	_, err := Load(writeConfig(t, body))
	if err == nil {
		t.Fatal("expected validation error when publish_timeout exceeds write_timeout")
	}
	if !strings.Contains(err.Error(), "server.publish_timeout") || !strings.Contains(err.Error(), "server.write_timeout") {
		t.Fatalf("error should mention both server.publish_timeout and server.write_timeout: %v", err)
	}
}

func TestPublishTimeoutCoherentExplicitConfigPasses(t *testing.T) {
	t.Setenv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/")
	t.Setenv("PADDLE_SECRET", "pdl_secret")
	body := replace(validYAML(), `write_timeout: "10s"`, `write_timeout: "20s"`)
	body = replace(body, `shutdown_timeout: "10s"`, `shutdown_timeout: "15s"`)
	body = replace(body, `publish_timeout: "10s"`, `publish_timeout: "8s"`)
	cfg, err := Load(writeConfig(t, body))
	if err != nil {
		t.Fatalf("Load returned error for coherent publish/shutdown/write timeouts: %v", err)
	}
	if cfg.Server.PublishTimeout != 8*time.Second {
		t.Fatalf("PublishTimeout = %s, want 8s", cfg.Server.PublishTimeout)
	}
}

func TestPublishTimeoutEqualToShutdownAndWriteTimeoutPasses(t *testing.T) {
	// The out-of-box default (all three at 10s) must validate cleanly:
	// the check is "greater than", not "greater than or equal to".
	t.Setenv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/")
	t.Setenv("PADDLE_SECRET", "pdl_secret")
	cfg, err := Load(writeConfig(t, validYAML()))
	if err != nil {
		t.Fatalf("Load returned error for default (equal) timeouts: %v", err)
	}
	if cfg.Server.PublishTimeout != 10*time.Second || cfg.Server.ShutdownTimeout != 10*time.Second || cfg.Server.WriteTimeout != 10*time.Second {
		t.Fatalf("unexpected timeouts: %#v", cfg.Server)
	}
}

// --- C1: server.readiness_interval / readiness_timeout defaults and validation ---

func TestReadinessIntervalAndTimeoutDefaultWhenOmitted(t *testing.T) {
	t.Setenv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/")
	t.Setenv("PADDLE_SECRET", "pdl_secret")
	// validYAML() omits both fields, so Load must apply the positive defaults.
	cfg, err := Load(writeConfig(t, validYAML()))
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.Server.ReadinessInterval != 10*time.Second {
		t.Fatalf("ReadinessInterval = %s, want 10s default", cfg.Server.ReadinessInterval)
	}
	if cfg.Server.ReadinessTimeout != 5*time.Second {
		t.Fatalf("ReadinessTimeout = %s, want 5s default", cfg.Server.ReadinessTimeout)
	}
}

func TestReadinessDurationsRejectExplicitNonPositiveValues(t *testing.T) {
	cases := []struct {
		field string
		key   string
	}{
		{"server.readiness_interval", "readiness_interval"},
		{"server.readiness_timeout", "readiness_timeout"},
	}
	for _, c := range cases {
		for _, bad := range []string{"-5s", "0s"} {
			t.Run(c.field+"/"+bad, func(t *testing.T) {
				t.Setenv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/")
				t.Setenv("PADDLE_SECRET", "pdl_secret")
				// Inject the field into the server block, then set the bad value.
				body := replace(validYAML(), `  addr: ":8080"`, fmt.Sprintf("  addr: \":8080\"\n  %s: %q", c.key, bad))
				_, err := Load(writeConfig(t, body))
				if err == nil {
					t.Fatalf("expected validation error for %s = %s", c.field, bad)
				}
				if !strings.Contains(err.Error(), c.field) {
					t.Fatalf("error missing field %s: %v", c.field, err)
				}
			})
		}
	}
}

// --- D1: timestamp_tolerance rejects non-positive explicit values ---

func TestLoadRejectsNonPositiveTimestampTolerance(t *testing.T) {
	for _, bad := range []string{"-5s", "0s"} {
		t.Run(bad, func(t *testing.T) {
			t.Setenv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/")
			t.Setenv("PADDLE_SECRET", "pdl_secret")
			body := replace(validYAML(), `timestamp_tolerance: "5s"`, fmt.Sprintf("timestamp_tolerance: %q", bad))
			_, err := Load(writeConfig(t, body))
			if err == nil {
				t.Fatalf("expected validation error for timestamp_tolerance = %s", bad)
			}
			if !strings.Contains(err.Error(), "timestamp_tolerance") {
				t.Fatalf("error missing field: %v", err)
			}
		})
	}
}

func TestLoadRejectsNonPositiveTimestampToleranceRegardlessOfScheme(t *testing.T) {
	t.Setenv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/")
	t.Setenv("PADDLE_SECRET", "pdl_secret")
	body := strings.Replace(validYAML(), `type: "hmac_sha256"`, "type: \"hmac_sha256\"\n      scheme: \"hmac_sha1_hex\"", 1)
	body = replace(body, `timestamp_tolerance: "5s"`, `timestamp_tolerance: "-5s"`)
	_, err := Load(writeConfig(t, body))
	if err == nil {
		t.Fatal("expected validation error even for a scheme that doesn't require a timestamp")
	}
	if !strings.Contains(err.Error(), "timestamp_tolerance") {
		t.Fatalf("error missing field: %v", err)
	}
}

// --- D2: max_inflight_requests defaults and sentinel semantics ---

func TestMaxInflightRequestsDefaultsTo256WhenOmitted(t *testing.T) {
	t.Setenv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/")
	t.Setenv("PADDLE_SECRET", "pdl_secret")
	cfg, err := Load(writeConfig(t, validYAML()))
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.Server.MaxInflightRequests != 256 {
		t.Fatalf("MaxInflightRequests = %d, want 256 (default)", cfg.Server.MaxInflightRequests)
	}
}

func TestMaxInflightRequestsExplicitZeroDefaultsTo256(t *testing.T) {
	t.Setenv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/")
	t.Setenv("PADDLE_SECRET", "pdl_secret")
	body := replace(validYAML(), "fanout_concurrency: 4", "fanout_concurrency: 4\n  max_inflight_requests: 0")
	cfg, err := Load(writeConfig(t, body))
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.Server.MaxInflightRequests != 256 {
		t.Fatalf("MaxInflightRequests = %d, want 256 (explicit 0 -> default)", cfg.Server.MaxInflightRequests)
	}
}

func TestMaxInflightRequestsExplicitNegativeOneMeansUnlimited(t *testing.T) {
	t.Setenv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/")
	t.Setenv("PADDLE_SECRET", "pdl_secret")
	body := replace(validYAML(), "fanout_concurrency: 4", "fanout_concurrency: 4\n  max_inflight_requests: -1")
	cfg, err := Load(writeConfig(t, body))
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.Server.MaxInflightRequests != -1 {
		t.Fatalf("MaxInflightRequests = %d, want -1 (unlimited sentinel)", cfg.Server.MaxInflightRequests)
	}
}

func TestMaxInflightRequestsRejectsBelowNegativeOne(t *testing.T) {
	t.Setenv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/")
	t.Setenv("PADDLE_SECRET", "pdl_secret")
	body := replace(validYAML(), "fanout_concurrency: 4", "fanout_concurrency: 4\n  max_inflight_requests: -2")
	_, err := Load(writeConfig(t, body))
	if err == nil {
		t.Fatal("expected validation error for max_inflight_requests = -2")
	}
	if !strings.Contains(err.Error(), "server.max_inflight_requests") {
		t.Fatalf("error missing field: %v", err)
	}
}
