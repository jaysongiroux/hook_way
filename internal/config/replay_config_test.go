package config

import (
	"strings"
	"testing"
	"time"
)

// replayYAML returns a valid shared config (validSharedYAML) with the given
// replay block appended, so each test only has to spell out the replay-specific
// fields it cares about. It also injects security.api_key into the existing
// security: block -- replay.enabled requires a non-empty api_key (see
// TestReplayEnabledRequiresAPIKey), which validSharedYAML leaves unset.
func replayYAML(replayBlock string) string {
	base := strings.Replace(validSharedYAML(), "security:\n", "security:\n  api_key: \"replay-test-key\"\n", 1)
	return base + "\n" + replayBlock
}

func TestReplayConfigParses(t *testing.T) {
	t.Setenv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/")
	t.Setenv("REPLAY_DB_URL", "postgres://u:p@localhost:5432/hookway_replay?sslmode=disable")
	body := replayYAML(`
replay:
  enabled: true
  addr: ":9091"
  skip_unauthenticated: true
  ttl: "168h"
  max_events: 1000
  cleanup_interval: "1h"
  postgres:
    url: "${REPLAY_DB_URL}"
`)
	cfg, err := loadSharedYAML(t, body)
	if err != nil {
		t.Fatalf("load returned error: %v", err)
	}
	r := cfg.Replay
	if !r.Enabled {
		t.Fatal("expected Replay.Enabled = true")
	}
	if r.Addr != ":9091" {
		t.Fatalf("Addr = %q, want %q", r.Addr, ":9091")
	}
	if !r.SkipUnauthenticated {
		t.Fatal("expected Replay.SkipUnauthenticated = true")
	}
	if r.TTL != 168*time.Hour {
		t.Fatalf("TTL = %v, want %v", r.TTL, 168*time.Hour)
	}
	if r.MaxEvents != 1000 {
		t.Fatalf("MaxEvents = %d, want 1000", r.MaxEvents)
	}
	if r.CleanupInterval != time.Hour {
		t.Fatalf("CleanupInterval = %v, want %v", r.CleanupInterval, time.Hour)
	}
	if r.Postgres.URL != "postgres://u:p@localhost:5432/hookway_replay?sslmode=disable" { // #nosec G101 -- test fixture
		t.Fatalf("Postgres.URL = %q", r.Postgres.URL)
	}
}

func TestReplayConfigDefaults(t *testing.T) {
	t.Setenv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/")
	t.Setenv("REPLAY_DB_URL", "postgres://localhost/hookway_replay")
	body := replayYAML(`
replay:
  enabled: true
  addr: ":9091"
  postgres:
    url: "${REPLAY_DB_URL}"
`)
	cfg, err := loadSharedYAML(t, body)
	if err != nil {
		t.Fatalf("load returned error: %v", err)
	}
	r := cfg.Replay
	if r.TTL != 0 {
		t.Fatalf("TTL = %v, want 0 (keep forever)", r.TTL)
	}
	if r.MaxEvents != 0 {
		t.Fatalf("MaxEvents = %d, want 0 (no cap)", r.MaxEvents)
	}
	if r.CleanupInterval != time.Hour {
		t.Fatalf("CleanupInterval = %v, want default of 1h", r.CleanupInterval)
	}
}

func TestReplayDisabledSkipsValidation(t *testing.T) {
	t.Setenv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/")
	body := replayYAML(`
replay:
  enabled: false
`)
	cfg, err := loadSharedYAML(t, body)
	if err != nil {
		t.Fatalf("load returned error for disabled replay with no postgres block: %v", err)
	}
	if cfg.Replay.Enabled {
		t.Fatal("expected Replay.Enabled = false")
	}
}

func TestReplayEnabledRequiresPostgresURL(t *testing.T) {
	c := Config{
		Replay: ReplayConfig{
			Enabled: true,
			Addr:    ":9091",
		},
	}
	issues := validationIssues(c)
	if !hasIssue(issues, "replay.postgres.url") {
		t.Fatalf("expected issue on replay.postgres.url; got %#v", issues)
	}
}

// TestReplayEnabledRequiresAPIKey guards against exposing an unauthenticated
// replay API: apikey.Middleware("") disables auth entirely, and the replay
// API re-injects traffic into destinations, so an empty security.api_key
// alongside replay.enabled must fail validation rather than silently serving
// an open re-injection endpoint.
func TestReplayEnabledRequiresAPIKey(t *testing.T) {
	c := Config{
		Replay: ReplayConfig{
			Enabled:  true,
			Addr:     ":9091",
			Postgres: PostgresConfig{URL: "postgres://localhost/hookway_replay"},
		},
	}
	issues := validationIssues(c)
	if !hasIssue(issues, "replay.enabled") {
		t.Fatalf("expected issue on replay.enabled when security.api_key is empty; got %#v", issues)
	}
}

// TestReplayEnabledWithAPIKeyPasses is the GREEN sibling of
// TestReplayEnabledRequiresAPIKey: setting security.api_key clears the guard.
func TestReplayEnabledWithAPIKeyPasses(t *testing.T) {
	c := Config{
		Security: SecurityConfig{APIKey: "s3cr3t"},
		Replay: ReplayConfig{
			Enabled:  true,
			Addr:     ":9091",
			Postgres: PostgresConfig{URL: "postgres://localhost/hookway_replay"},
		},
	}
	issues := validationIssues(c)
	if hasIssue(issues, "replay.enabled") {
		t.Fatalf("did not expect issue on replay.enabled when security.api_key is set; got %#v", issues)
	}
}

func TestReplayRejectsNegativeTTLAndMaxEvents(t *testing.T) {
	t.Setenv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/")
	t.Setenv("REPLAY_DB_URL", "postgres://localhost/hookway_replay")
	body := replayYAML(`
replay:
  enabled: true
  addr: ":9091"
  ttl: "-1s"
  max_events: -5
  postgres:
    url: "${REPLAY_DB_URL}"
`)
	_, err := loadSharedYAML(t, body)
	if err == nil {
		t.Fatal("expected validation error for negative ttl and max_events")
	}
	if !strings.Contains(err.Error(), "replay.ttl") {
		t.Fatalf("expected replay.ttl in error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "replay.max_events") {
		t.Fatalf("expected replay.max_events in error, got: %v", err)
	}
}

func TestReplayRejectsBadAddr(t *testing.T) {
	t.Setenv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/")
	t.Setenv("REPLAY_DB_URL", "postgres://localhost/hookway_replay")
	body := replayYAML(`
replay:
  enabled: true
  postgres:
    url: "${REPLAY_DB_URL}"
`)
	_, err := loadSharedYAML(t, body)
	if err == nil {
		t.Fatal("expected validation error for empty replay.addr")
	}
	if !strings.Contains(err.Error(), "replay.addr") {
		t.Fatalf("expected replay.addr in error, got: %v", err)
	}
}

func TestReplayRejectsNegativeCleanupInterval(t *testing.T) {
	c := Config{
		Replay: ReplayConfig{
			Enabled:         true,
			Addr:            ":9091",
			CleanupInterval: -1 * time.Second,
			Postgres:        PostgresConfig{URL: "postgres://localhost/db"},
		},
	}
	issues := validationIssues(c)
	if !hasIssue(issues, "replay.cleanup_interval") {
		t.Fatalf("expected issue on replay.cleanup_interval; got %#v", issues)
	}
}

// TestPostgresDestinationValidationUnaffectedByReplayRefactor confirms that
// factoring the shared postgres-connection checks (schema/table safety,
// max_conns, mutual TLS) into postgresConnValidationIssues for reuse by the
// replay path did not change destination postgres validation: a destination
// with a bad postgres config still produces the same issues.
//
// TestValidatePostgresRequiresURLAndTable and TestValidatePostgresValidTablePasses
// (config_test.go) already cover this same path end-to-end; this test adds
// explicit coverage of the mutual-TLS both-or-neither case, which those two
// don't exercise.
func TestPostgresDestinationValidationUnaffectedByReplayRefactor(t *testing.T) {
	c := Config{
		Destinations: map[string]DestinationConfig{
			"warehouse": {Type: "postgres", Postgres: PostgresConfig{
				URL:   "postgres://localhost/db",
				Table: "webhook_events",
				TLS:   PostgresTLSConfig{ClientCert: "client.crt"}, // ClientKey missing
			}},
		},
	}
	issues := validationIssues(c)
	if !hasIssue(issues, "destinations.warehouse.postgres.tls.client_cert") {
		t.Fatalf("expected mutual-TLS issue on destinations.warehouse.postgres.tls.client_cert; got %#v", issues)
	}
}
