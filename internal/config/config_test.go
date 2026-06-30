package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestLoadExpandsEnvironmentAndValidates(t *testing.T) {
	t.Setenv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/")
	t.Setenv("PADDLE_SECRET", "pdl_secret")
	path := writeConfig(t, validYAML())

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.Destinations["billing"].Type != "rabbitmq" {
		t.Fatalf("message queue type = %q", cfg.Destinations["billing"].Type)
	}
	if cfg.Destinations["billing"].RabbitMQ.URL != "amqp://guest:guest@localhost:5672/" {
		t.Fatalf("RabbitMQ URL not expanded: %q", cfg.Destinations["billing"].RabbitMQ.URL)
	}
	destinations := cfg.Endpoints["paddle_billing"].Destinations
	if len(destinations) != 1 || destinations[0].Destination != "billing" {
		t.Fatalf("endpoint destinations = %#v", destinations)
	}
	if cfg.Endpoints["paddle_billing"].Path != "/webhooks/paddle/billing" {
		t.Fatalf("endpoint path = %q", cfg.Endpoints["paddle_billing"].Path)
	}
	if cfg.Endpoints["paddle_billing"].DeliveryFailurePolicy != "stop_on_failure" {
		t.Fatalf("delivery failure policy = %q", cfg.Endpoints["paddle_billing"].DeliveryFailurePolicy)
	}
	if destinations[0].Target != "billing-events" {
		t.Fatalf("endpoint target = %q", destinations[0].Target)
	}
	if cfg.Endpoints["paddle_billing"].Auth.Secret != "pdl_secret" {
		t.Fatalf("Paddle secret not expanded")
	}
	if cfg.Server.ReadTimeout != 5*time.Second {
		t.Fatalf("read timeout = %s", cfg.Server.ReadTimeout)
	}
	if cfg.Server.FanoutConcurrency != 4 {
		t.Fatalf("fanout concurrency = %d", cfg.Server.FanoutConcurrency)
	}
	if got := cfg.Security.RedactedHeaderDenylist; len(got) != 3 || got[0] != "Authorization" || got[2] != "X-Signature" {
		t.Fatalf("redacted header denylist = %#v", got)
	}
	if !cfg.Destinations["billing"].RabbitMQ.DeadLetter.Enabled {
		t.Fatal("dead-letter config not parsed")
	}
	if cfg.Destinations["billing"].RabbitMQ.QueueDefaults.MessageTTL != 24*time.Hour {
		t.Fatalf("queue default message ttl = %s", cfg.Destinations["billing"].RabbitMQ.QueueDefaults.MessageTTL)
	}
	if cfg.Destinations["billing"].RabbitMQ.QueueDefaults.MaxLength != 100000 {
		t.Fatalf("queue default max length = %d", cfg.Destinations["billing"].RabbitMQ.QueueDefaults.MaxLength)
	}
	if cfg.Destinations["billing"].RabbitMQ.QueueDefaults.Overflow != "reject-publish" {
		t.Fatalf("queue default overflow = %q", cfg.Destinations["billing"].RabbitMQ.QueueDefaults.Overflow)
	}
	if destinations[0].QueueOptions.MessageTTL != 72*time.Hour {
		t.Fatalf("endpoint queue message ttl = %s", destinations[0].QueueOptions.MessageTTL)
	}
	if destinations[0].QueueOptions.MaxLength != 500000 {
		t.Fatalf("endpoint queue max length = %d", destinations[0].QueueOptions.MaxLength)
	}
	if destinations[0].QueueOptions.DeadLetterRoutingKey != "paddle.dead" {
		t.Fatalf("endpoint dead letter routing key = %q", destinations[0].QueueOptions.DeadLetterRoutingKey)
	}
	if cfg.Destinations["billing"].RabbitMQ.DeadLetter.Exchange != "hookway.webhooks.dlx" {
		t.Fatalf("dead-letter exchange = %q", cfg.Destinations["billing"].RabbitMQ.DeadLetter.Exchange)
	}
	if !destinations[0].Create {
		t.Fatal("endpoint create not parsed")
	}
	if cfg.Endpoints["paddle_billing"].MaxBodyBytes != 2097152 {
		t.Fatalf("endpoint max body bytes = %d", cfg.Endpoints["paddle_billing"].MaxBodyBytes)
	}
	if cfg.Endpoints["paddle_billing"].RateLimit.Enabled == nil || !*cfg.Endpoints["paddle_billing"].RateLimit.Enabled {
		t.Fatalf("endpoint rate limit enabled = %#v", cfg.Endpoints["paddle_billing"].RateLimit.Enabled)
	}
	if cfg.Endpoints["paddle_billing"].RateLimit.RequestsPerMinute != 60 {
		t.Fatalf("endpoint rate limit rpm = %d", cfg.Endpoints["paddle_billing"].RateLimit.RequestsPerMinute)
	}
	if !cfg.Endpoints["paddle_billing"].ReplayProtection.Enabled {
		t.Fatal("endpoint replay protection not parsed")
	}
	if cfg.Endpoints["paddle_billing"].ReplayProtection.TTL != 10*time.Minute {
		t.Fatalf("endpoint replay ttl = %s", cfg.Endpoints["paddle_billing"].ReplayProtection.TTL)
	}
	if got := cfg.Endpoints["paddle_billing"].IPAllowlist; len(got) != 2 || got[0] != "203.0.113.10" || got[1] != "198.51.100.0/24" {
		t.Fatalf("endpoint ip allowlist = %#v", got)
	}
}

func TestLoadParsesEndpointFanoutDestinationObjects(t *testing.T) {
	t.Setenv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/")
	t.Setenv("PADDLE_SECRET", "pdl_secret")
	body := strings.Replace(validYAML(), `          dead_letter_routing_key: "paddle.dead"`, `          dead_letter_routing_key: "paddle.dead"
      - destination: "archive"
        target: "billing-archive-events"
        create: false
        queue_options:
          message_ttl: "6h"`, 1)
	body = strings.Replace(body, `destinations:
  billing:`, `destinations:
  archive:
    type: "rabbitmq"
    rabbitmq:
      url: "${RABBITMQ_URL}"
      exchange: "hookway.archive"
      exchange_type: "direct"
  billing:`, 1)

	cfg, err := Load(writeConfig(t, body))
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	got := cfg.Endpoints["paddle_billing"].Destinations
	if len(got) != 2 {
		t.Fatalf("destinations = %#v", got)
	}
	if got[0].Destination != "billing" || got[0].Target != "billing-events" || !got[0].Create || got[0].QueueOptions.MessageTTL != 72*time.Hour {
		t.Fatalf("first destination = %#v", got[0])
	}
	if got[1].Destination != "archive" || got[1].Target != "billing-archive-events" || got[1].Create || got[1].QueueOptions.MessageTTL != 6*time.Hour {
		t.Fatalf("second destination = %#v", got[1])
	}
}

func TestLoadParsesDeliveryFailurePolicy(t *testing.T) {
	t.Setenv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/")
	t.Setenv("PADDLE_SECRET", "pdl_secret")
	body := strings.Replace(validYAML(), `    destinations:`, `    delivery_failure_policy: "skip_failed"
    destinations:`, 1)

	cfg, err := Load(writeConfig(t, body))
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if got := cfg.Endpoints["paddle_billing"].DeliveryFailurePolicy; got != "skip_failed" {
		t.Fatalf("delivery failure policy = %q", got)
	}
}

func TestLoadRejectsInvalidConfig(t *testing.T) {
	tests := map[string]string{
		"missing destination type":   replace(validYAML(), "type: \"rabbitmq\"", "type: \"\""),
		"invalid destination id":     replace(validYAML(), "billing:", "bad queue:"),
		"missing destinations":       strings.Replace(validYAML(), "    destinations:\n      - destination: \"billing\"", "    destinations: []\n    #", 1),
		"missing destination":        replace(validYAML(), "destination: \"billing\"", "destination: \"\""),
		"unknown fanout destination": replace(validYAML(), "destination: \"billing\"", "destination: \"missing\""),
		"duplicate fanout destination route": strings.Replace(validYAML(), `          dead_letter_routing_key: "paddle.dead"`, `          dead_letter_routing_key: "paddle.dead"
      - destination: "billing"
        target: "billing-events"
        create: true
        queue_options:
          message_ttl: "72h"
          max_length: 500000
          dead_letter_routing_key: "paddle.dead"`, 1),
		"missing rabbitmq url":     replace(validYAML(), "url: \"${RABBITMQ_URL}\"", "url: \"\""),
		"invalid endpoint":         replace(validYAML(), "paddle_billing:", "bad/endpoint:"),
		"missing endpoint path":    replace(validYAML(), "path: \"/webhooks/paddle/billing\"", "path: \"\""),
		"invalid endpoint path":    replace(validYAML(), "path: \"/webhooks/paddle/billing\"", "path: \"webhooks/paddle/billing\""),
		"invalid target":           replace(validYAML(), "target: \"billing-events\"", "target: \"bad target\""),
		"missing secret":           replace(validYAML(), "secret: \"${PADDLE_SECRET}\"", "secret: \"\""),
		"missing signature header": replace(validYAML(), "signature_header: \"Paddle-Signature\"", "signature_header: \"\""),
		"unsupported auth":         replace(validYAML(), "auth:\n      type: \"hmac_sha256\"", "auth:\n      type: \"other\""),
		"invalid rate limit":       replace(validYAML(), "requests_per_minute: 120", "requests_per_minute: 0"),
		"invalid metrics path": replace(validYAML(),
			"path: \"/metrics\"", "path: \"metrics\""),
		"invalid dead letter exchange": replace(validYAML(), "exchange: \"hookway.webhooks.dlx\"", "exchange: \"bad exchange\""),
		"invalid dead letter queue":    replace(validYAML(), "queue: \"hookway.webhooks.dlq\"", "queue: \"bad queue\""),
		"invalid queue default ttl":    replace(validYAML(), "message_ttl: \"24h\"", "message_ttl: \"forever\""),
		"invalid queue default length": replace(validYAML(), "max_length: 100000", "max_length: -1"),
		"invalid queue overflow":       replace(validYAML(), "overflow: \"reject-publish\"", "overflow: \"drop-head\""),
		"invalid endpoint queue ttl":   replace(validYAML(), "message_ttl: \"72h\"", "message_ttl: \"later\""),
		"invalid endpoint body limit":  replace(validYAML(), "max_body_bytes: 2097152", "max_body_bytes: -1"),
		"invalid delivery failure policy": strings.Replace(validYAML(), `    destinations:`, `    delivery_failure_policy: "cancel_pending_on_failure"
    destinations:`, 1),
		"invalid endpoint rate limit": replace(validYAML(), "requests_per_minute: 60", "requests_per_minute: 0"),
		"invalid replay ttl":          replace(validYAML(), "ttl: \"10m\"", "ttl: \"soon\""),
		"invalid replay max entries":  replace(validYAML(), "max_entries: 50000", "max_entries: 0"),
		"invalid ip allowlist":        replace(validYAML(), "- \"203.0.113.10\"", "- \"not an ip\""),
		"invalid redacted header":     replace(validYAML(), "- \"Authorization\"", "- \"\""),
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			t.Setenv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/")
			t.Setenv("PADDLE_SECRET", "pdl_secret")
			_, err := Load(writeConfig(t, body))
			if err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestLoadRejectsOverlappingEndpointPaths(t *testing.T) {
	t.Setenv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/")
	t.Setenv("PADDLE_SECRET", "pdl_secret")
	body := strings.Replace(validYAML(), "endpoints:\n", `endpoints:
  paddle_child:
    enabled: true
    path: "/webhooks/paddle"
    destinations:
      - destination: "billing"
        target: "billing-child-events"
    auth:
      type: "hmac_sha256"
      secret: "${PADDLE_SECRET}"
      signature_header: "Paddle-Signature"

`, 1)

	_, err := Load(writeConfig(t, body))
	if err == nil {
		t.Fatal("expected overlapping endpoint paths to be rejected")
	}
	if !strings.Contains(err.Error(), "overlaps endpoint") {
		t.Fatalf("expected overlap error, got %v", err)
	}
}

func TestLoadAllowsEndpointPathsWithCommonPrefixButDifferentSegment(t *testing.T) {
	t.Setenv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/")
	t.Setenv("PADDLE_SECRET", "pdl_secret")
	body := strings.Replace(validYAML(), "endpoints:\n", `endpoints:
  paddle_archive:
    enabled: true
    path: "/webhooks/paddle-archive"
    destinations:
      - destination: "billing"
        target: "billing-archive-events"
    auth:
      type: "hmac_sha256"
      secret: "${PADDLE_SECRET}"
      signature_header: "Paddle-Signature"

`, 1)

	_, err := Load(writeConfig(t, body))
	if err != nil {
		t.Fatalf("expected non-overlapping common prefix to be valid, got %v", err)
	}
}

func TestLoadRejectsOldMessageQueueShape(t *testing.T) {
	t.Setenv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/")
	t.Setenv("PADDLE_SECRET", "pdl_secret")
	body := `
server:
  addr: ":8080"
  max_body_bytes: 1048576
message_queue:
  type: "rabbitmq"
  rabbitmq:
    url: "${RABBITMQ_URL}"
    exchange: "hookway.webhooks"
    exchange_type: "direct"
endpoints:
  paddle_billing:
    enabled: true
    path: "/webhooks/paddle/billing"
    queue: "billing-events"
    auth:
      type: "hmac_sha256"
      secret: "${PADDLE_SECRET}"
      signature_header: "Paddle-Signature"
`

	_, err := Load(writeConfig(t, body))
	if err == nil {
		t.Fatal("expected old message_queue shape to be rejected")
	}
	if !strings.Contains(err.Error(), "unknown or invalid YAML field") {
		t.Fatalf("unexpected error: %s", err)
	}
}

func TestLoadReturnsHelpfulValidationErrors(t *testing.T) {
	t.Setenv("RABBITMQ_URL", "")
	t.Setenv("PADDLE_SECRET", "")
	body := replace(validYAML(), "addr: \":8080\"", "addr: \"\"")
	body = replace(body, "max_body_bytes: 1048576", "max_body_bytes: 0")
	body = replace(body, "type: \"rabbitmq\"", "type: \"\"")
	body = replace(body, "path: \"/metrics\"", "path: \"metrics\"")
	body = replace(body, "path: \"/webhooks/paddle/billing\"", "path: \"webhooks/paddle/billing\"")
	body = replace(body, "target: \"billing-events\"", "target: \"bad target\"")
	body = replace(body, "- \"Authorization\"", "- \"\"")
	body = replace(body, "exchange: \"hookway.webhooks.dlx\"", "exchange: \"bad exchange\"")
	body = replace(body, "requests_per_minute: 60", "requests_per_minute: 0")
	body = replace(body, "max_entries: 50000", "max_entries: 0")
	body = replace(body, "- \"203.0.113.10\"", "- \"not an ip\"")
	body = replace(body, "signature_header: \"Paddle-Signature\"", "signature_header: \"\"")

	_, err := Load(writeConfig(t, body))
	if err == nil {
		t.Fatal("expected validation error")
	}
	text := err.Error()
	for _, want := range []string{
		"config validation failed",
		"server.addr",
		"server.max_body_bytes",
		"destinations.billing.type",
		"metrics.path",
		"endpoints.paddle_billing.path",
		"endpoints.paddle_billing.destinations[0].target",
		"endpoints.paddle_billing.auth.secret",
		"endpoints.paddle_billing.auth.signature_header",
		"security.redacted_header_denylist",
		"destinations.billing.rabbitmq.dead_letter.exchange",
		"endpoints.paddle_billing.rate_limit.requests_per_minute",
		"endpoints.paddle_billing.replay_protection.max_entries",
		"endpoints.paddle_billing.ip_allowlist[0]",
		"Suggestion:",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("error missing %q:\n%s", want, text)
		}
	}
}

func TestLoadReportsInvalidDurationWithFieldPath(t *testing.T) {
	body := replace(validYAML(), "read_timeout: \"5s\"", "read_timeout: \"soon\"")
	_, err := Load(writeConfig(t, body))
	if err == nil {
		t.Fatal("expected duration error")
	}
	if !strings.Contains(err.Error(), "server.read_timeout") || !strings.Contains(err.Error(), "valid Go duration") {
		t.Fatalf("unhelpful error: %s", err)
	}
}

func TestExampleConfigIsValid(t *testing.T) {
	t.Setenv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/")
	t.Setenv("PADDLE_WEBHOOK_SECRET", "pdl_secret")
	t.Setenv("GITHUB_WEBHOOK_SECRET", "gh_secret")
	t.Setenv("REVENUECAT_WEBHOOK_AUTH", "rc_secret")

	cfg, err := Load(filepath.Join("..", "..", "examples", "config.yaml"))
	if err != nil {
		t.Fatalf("example config invalid: %v", err)
	}
	if _, ok := cfg.Endpoints["github_events"]; !ok {
		t.Fatal("example config should include multiple endpoints")
	}
}

func TestLoadReportsUnknownFieldsAndYAMLSyntax(t *testing.T) {
	_, err := Load(writeConfig(t, replace(validYAML(), "server:", "server:\n  typo: true")))
	if err == nil {
		t.Fatal("expected unknown field error")
	}
	if !strings.Contains(err.Error(), "unknown or invalid YAML field") || !strings.Contains(err.Error(), "typo") {
		t.Fatalf("unhelpful unknown field error: %s", err)
	}

	_, err = Load(writeConfig(t, "server:\n  addr: ["))
	if err == nil {
		t.Fatal("expected yaml syntax error")
	}
	if !strings.Contains(err.Error(), "invalid YAML syntax") {
		t.Fatalf("unhelpful YAML error: %s", err)
	}
}

func TestConfigPathPrefersFlagOverEnv(t *testing.T) {
	t.Setenv("HOOKWAY_CONFIG", "env.yaml")
	path, err := ResolvePath("flag.yaml")
	if err != nil {
		t.Fatalf("ResolvePath returned error: %v", err)
	}
	if path != "flag.yaml" {
		t.Fatalf("path = %q", path)
	}
}

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadAcceptsHeaderExactAuth(t *testing.T) {
	t.Setenv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/")
	t.Setenv("REVENUECAT_WEBHOOK_AUTH", "Bearer rc-secret")
	path := writeConfig(t, validHeaderExactYAML())
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	ep := cfg.Endpoints["revenuecat_billing"]
	if ep.Auth.Type != "header_exact" || ep.Auth.Scheme != "exact" {
		t.Fatalf("auth = %#v", ep.Auth)
	}
}

func TestLoadRejectsExactSchemeWithHMACType(t *testing.T) {
	t.Setenv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/")
	t.Setenv("PADDLE_SECRET", "pdl_secret")
	yaml := strings.Replace(validYAML(), `type: "hmac_sha256"`, `type: "hmac_sha256"
      scheme: "exact"`, 1)
	_, err := Load(writeConfig(t, yaml))
	if err == nil {
		t.Fatal("expected validation error")
	}
}

func TestLoadAcceptsHeaderExactWithoutTimestampRequirement(t *testing.T) {
	t.Setenv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/")
	t.Setenv("REVENUECAT_WEBHOOK_AUTH", "rc-secret")
	path := writeConfig(t, validHeaderExactYAML())
	if _, err := Load(path); err != nil {
		t.Fatalf("Load: %v", err)
	}
}

func TestLoadAcceptsHeaderExactWithRemoveBearer(t *testing.T) {
	t.Setenv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/")
	t.Setenv("REVENUECAT_WEBHOOK_AUTH", "rc-secret")
	yaml := strings.Replace(validHeaderExactYAML(), `signature_header: "Authorization"`, `signature_header: "Authorization"
      remove_bearer: true`, 1)
	cfg, err := Load(writeConfig(t, yaml))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.Endpoints["revenuecat_billing"].Auth.RemoveBearer {
		t.Fatal("expected remove_bearer to be enabled")
	}
}

func TestLoadRejectsRemoveBearerForHMACAuth(t *testing.T) {
	t.Setenv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/")
	t.Setenv("PADDLE_SECRET", "pdl_secret")
	yaml := strings.Replace(validYAML(), `signature_header: "Paddle-Signature"`, `signature_header: "Paddle-Signature"
      remove_bearer: true`, 1)
	_, err := Load(writeConfig(t, yaml))
	if err == nil {
		t.Fatal("expected validation error")
	}
}

func TestLoadAcceptsNoAuth(t *testing.T) {
	t.Setenv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/")
	cfg, err := Load(writeConfig(t, validNoAuthYAML()))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	ep := cfg.Endpoints["open_webhook"]
	if ep.Auth.Type != "no_auth" || ep.Auth.Scheme != "no_auth" {
		t.Fatalf("auth = %#v", ep.Auth)
	}
}

func TestLoadAcceptsHMACSHA256TSDotScheme(t *testing.T) {
	t.Setenv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/")
	t.Setenv("WEBHOOK_SECRET", "whsec_123")
	yaml := strings.Replace(validYAML(),
		`type: "hmac_sha256"`,
		`type: "hmac_sha256"
      scheme: "hmac_sha256_ts_dot"`, 1)
	yaml = strings.Replace(yaml, "${PADDLE_SECRET}", "${WEBHOOK_SECRET}", 1)
	cfg, err := Load(writeConfig(t, yaml))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, ep := range cfg.Endpoints {
		if ep.Auth.Scheme == "hmac_sha256_ts_dot" {
			return
		}
	}
	t.Fatal("hmac_sha256_ts_dot scheme not present after load")
}

func TestLoadAcceptsHMACSHA256Base64Scheme(t *testing.T) {
	t.Setenv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/")
	t.Setenv("WEBHOOK_SECRET", "shpss_123")
	yaml := strings.Replace(validYAML(),
		`type: "hmac_sha256"`,
		`type: "hmac_sha256"
      scheme: "hmac_sha256_base64"`, 1)
	yaml = strings.Replace(yaml, "${PADDLE_SECRET}", "${WEBHOOK_SECRET}", 1)
	cfg, err := Load(writeConfig(t, yaml))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, ep := range cfg.Endpoints {
		if ep.Auth.Scheme == "hmac_sha256_base64" {
			return
		}
	}
	t.Fatal("hmac_sha256_base64 scheme not present after load")
}

func TestLoadAcceptsHMACSHA1HexScheme(t *testing.T) {
	t.Setenv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/")
	t.Setenv("WEBHOOK_SECRET", "ghsecret")
	yaml := strings.Replace(validYAML(),
		`type: "hmac_sha256"`,
		`type: "hmac_sha256"
      scheme: "hmac_sha1_hex"`, 1)
	yaml = strings.Replace(yaml, "${PADDLE_SECRET}", "${WEBHOOK_SECRET}", 1)
	cfg, err := Load(writeConfig(t, yaml))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, ep := range cfg.Endpoints {
		if ep.Auth.Scheme == "hmac_sha1_hex" {
			return
		}
	}
	t.Fatal("hmac_sha1_hex scheme not present after load")
}

func TestLoadRejectsNoAuthSchemeWithHMACType(t *testing.T) {
	t.Setenv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/")
	yaml := strings.Replace(validNoAuthYAML(), `type: "no_auth"`, `type: "hmac_sha256"`, 1)
	if _, err := Load(writeConfig(t, yaml)); err == nil {
		t.Fatal("expected validation error for no_auth scheme under hmac_sha256")
	}
}

func validNoAuthYAML() string {
	return `
server:
  addr: ":8080"
  max_body_bytes: 1048576
destinations:
  billing:
    type: "rabbitmq"
    rabbitmq:
      url: "${RABBITMQ_URL}"
      exchange: "hookway.webhooks"
      exchange_type: "direct"
endpoints:
  open_webhook:
    enabled: true
    path: "/webhooks/open"
    destinations:
      - destination: "billing"
        target: "open-events"
        create: true
    auth:
      type: "no_auth"
      scheme: "no_auth"
`
}

func validHeaderExactYAML() string {
	return `
server:
  addr: ":8080"
  max_body_bytes: 1048576
destinations:
  billing:
    type: "rabbitmq"
    rabbitmq:
      url: "${RABBITMQ_URL}"
      exchange: "hookway.webhooks"
      exchange_type: "direct"
endpoints:
  revenuecat_billing:
    enabled: true
    path: "/webhooks/revenuecat/billing"
    destinations:
      - destination: "billing"
        target: "revenuecat-events"
        create: true
    auth:
      type: "header_exact"
      scheme: "exact"
      secret: "${REVENUECAT_WEBHOOK_AUTH}"
      signature_header: "Authorization"
`
}

func validYAML() string {
	return `
server:
  addr: ":8080"
  read_timeout: "5s"
  write_timeout: "10s"
  idle_timeout: "60s"
  shutdown_timeout: "10s"
  max_body_bytes: 1048576
  fanout_concurrency: 4
security:
  trusted_proxy_headers: false
  request_id_header: "X-Request-ID"
  redacted_header_denylist:
    - "Authorization"
    - "Cookie"
    - "X-Signature"
rate_limit:
  enabled: true
  requests_per_minute: 120
  burst: 20
  key_by: "ip"
metrics:
  enabled: true
  path: "/metrics"
destinations:
  billing:
    type: "rabbitmq"
    rabbitmq:
      url: "${RABBITMQ_URL}"
      exchange: "hookway.webhooks"
      exchange_type: "direct"
      durable: true
      publish_timeout: "5s"
      declare_queues: true
      queue_defaults:
        message_ttl: "24h"
        max_length: 100000
        overflow: "reject-publish"
      dead_letter:
        enabled: true
        exchange: "hookway.webhooks.dlx"
        exchange_type: "direct"
        queue: "hookway.webhooks.dlq"
        routing_key: "webhooks.dead"
        durable: true
        create_queue: true
endpoints:
  paddle_billing:
    enabled: true
    path: "/webhooks/paddle/billing"
    destinations:
      - destination: "billing"
        target: "billing-events"
        create: true
        queue_options:
          message_ttl: "72h"
          max_length: 500000
          dead_letter_routing_key: "paddle.dead"
    max_body_bytes: 2097152
    rate_limit:
      enabled: true
      requests_per_minute: 60
      burst: 10
      key_by: "ip"
    replay_protection:
      enabled: true
      ttl: "10m"
      max_entries: 50000
    ip_allowlist:
      - "203.0.113.10"
      - "198.51.100.0/24"
    auth:
      type: "hmac_sha256"
      secret: "${PADDLE_SECRET}"
      signature_header: "Paddle-Signature"
      timestamp_tolerance: "5s"
`
}

func TestLoadAcceptsBasicScheme(t *testing.T) {
	t.Setenv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/")
	t.Setenv("REVENUECAT_WEBHOOK_AUTH", "user:pass")
	yaml := strings.Replace(validHeaderExactYAML(), `scheme: "exact"`, `scheme: "basic"`, 1)
	// Basic does not use remove_bearer; drop it so validation is exercised cleanly.
	yaml = strings.Replace(yaml, "\n      remove_bearer: true", "", 1)
	cfg, err := Load(writeConfig(t, yaml))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, ep := range cfg.Endpoints {
		if ep.Auth.Scheme == "basic" {
			return
		}
	}
	t.Fatal("basic scheme not present after load")
}

func replace(s, old, new string) string {
	return strings.ReplaceAll(s, old, new)
}

func hasIssue(issues []ValidationIssue, field string) bool {
	for _, issue := range issues {
		if issue.Field == field {
			return true
		}
	}
	return false
}

func TestConvertDestinationS3(t *testing.T) {
	raw := rawDestinationConfig{Type: "s3", OnExistingConflict: "warn"}
	raw.S3.Region = "us-east-1"
	raw.S3.Bucket = "my-events-bucket"
	raw.S3.Create = true
	raw.S3.SSE = "aws:kms"
	raw.S3.SSEKMSKeyID = "key-123"

	dest, issues := convertDestination("destinations.archive", raw)
	if len(issues) != 0 {
		t.Fatalf("unexpected issues: %#v", issues)
	}
	if dest.Type != "s3" {
		t.Fatalf("type = %q", dest.Type)
	}
	if dest.S3.Bucket != "my-events-bucket" || dest.S3.Region != "us-east-1" {
		t.Fatalf("s3 config not mapped: %#v", dest.S3)
	}
	if !dest.S3.Create || dest.S3.SSE != "aws:kms" || dest.S3.SSEKMSKeyID != "key-123" {
		t.Fatalf("s3 fields not mapped: %#v", dest.S3)
	}
	// on_existing_conflict accepted for s3 (no-op), defaults applied like sqs/sns.
	if dest.OnExistingConflict == "" {
		t.Fatalf("expected on_existing_conflict to be accepted/defaulted")
	}
}

func TestValidateS3RequiresRegionAndBucket(t *testing.T) {
	c := Config{
		Destinations: map[string]DestinationConfig{
			"archive": {Type: "s3", S3: S3Config{SSE: "bogus"}},
		},
		Endpoints: map[string]EndpointConfig{
			"e": {Enabled: true, Path: "/e", Destinations: []EndpointDestinationConfig{
				{Destination: "archive", Target: "orders"},
			}},
		},
	}
	issues := c.ValidationIssues()
	wantFields := []string{
		"destinations.archive.s3.region",
		"destinations.archive.s3.bucket",
		"destinations.archive.s3.sse",
	}
	for _, f := range wantFields {
		if !hasIssue(issues, f) {
			t.Fatalf("missing validation issue for %q; got %#v", f, issues)
		}
	}
}

func TestLoadDestinationsBlockWithSQSAndSNS(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	yaml := `
server:
  addr: ":8080"
  max_body_bytes: 1048576
destinations:
  jobs:
    type: sqs
    sqs:
      region: us-east-1
      endpoint: http://localhost:4566
      access_key_id: test
      secret_access_key: test
  alerts:
    type: sns
    sns:
      region: us-east-1
      endpoint: http://localhost:4566
endpoints:
  billing:
    enabled: true
    path: /webhooks/billing
    destinations:
      - destination: jobs
        target: billing-jobs.fifo
        create: true
        sqs_options:
          visibility_timeout: 30s
          message_retention: 96h
        fifo:
          group_id: { source: body, path: customer.id, fallback: endpoint }
          dedup: { use_idempotency_key: true, content_based_fallback: true }
      - destination: alerts
        target: billing-alerts
        create: true
        sns_options: { subject: "billing event" }
    auth:
      type: no_auth
      scheme: no_auth
`
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.Destinations["jobs"].Type != "sqs" || cfg.Destinations["jobs"].SQS.Region != "us-east-1" {
		t.Fatalf("bad sqs destination: %#v", cfg.Destinations["jobs"])
	}
	if cfg.Destinations["alerts"].Type != "sns" || cfg.Destinations["alerts"].SNS.Endpoint != "http://localhost:4566" {
		t.Fatalf("bad sns destination: %#v", cfg.Destinations["alerts"])
	}
	billing := cfg.Endpoints["billing"]
	if billing.Destinations[0].Destination != "jobs" || billing.Destinations[0].Target != "billing-jobs.fifo" || !billing.Destinations[0].Create {
		t.Fatalf("bad endpoint destination: %#v", billing.Destinations[0])
	}
	if billing.Destinations[0].SQSOptions.VisibilityTimeout != 30*time.Second {
		t.Fatalf("bad sqs options: %#v", billing.Destinations[0].SQSOptions)
	}
	if billing.Destinations[0].FIFO == nil || billing.Destinations[0].FIFO.GroupID.Source != "body" || billing.Destinations[0].FIFO.GroupID.Path != "customer.id" {
		t.Fatalf("bad fifo group id: %#v", billing.Destinations[0].FIFO)
	}
	if !billing.Destinations[0].FIFO.Dedup.UseIdempotencyKey {
		t.Fatalf("bad fifo dedup: %#v", billing.Destinations[0].FIFO)
	}
}

func TestValidateRejectsSQSOptionsOnNonSQSDestination(t *testing.T) {
	cfg := Config{
		Server: ServerConfig{Addr: ":8080", MaxBodyBytes: 1024},
		Destinations: map[string]DestinationConfig{
			"primary": {Type: "rabbitmq", RabbitMQ: RabbitMQConfig{URL: "amqp://x", Exchange: "e", ExchangeType: "direct"}},
		},
		Endpoints: map[string]EndpointConfig{
			"e": {Enabled: true, Path: "/p", Auth: AuthConfig{Type: "no_auth"},
				Destinations: []EndpointDestinationConfig{{
					Destination: "primary", Target: "q",
					SQSOptions: SQSEndpointOptions{VisibilityTimeout: time.Second},
				}}},
		},
	}
	issues := cfg.ValidationIssues()
	if !hasIssue(issues, "endpoints.e.destinations[0].sqs_options") {
		t.Fatalf("expected sqs_options-on-rabbitmq error, got %#v", issues)
	}
}

func TestValidateRejectsFIFOBlockOnNonFifoTarget(t *testing.T) {
	cfg := Config{
		Server: ServerConfig{Addr: ":8080", MaxBodyBytes: 1024},
		Destinations: map[string]DestinationConfig{
			"jobs": {Type: "sqs", SQS: SQSConfig{Region: "us-east-1"}},
		},
		Endpoints: map[string]EndpointConfig{
			"e": {Enabled: true, Path: "/p", Auth: AuthConfig{Type: "no_auth"},
				Destinations: []EndpointDestinationConfig{{
					Destination: "jobs", Target: "plain-queue",
					FIFO: &FIFOConfig{GroupID: FIFOGroupIDConfig{Source: "endpoint"}},
				}}},
		},
	}
	issues := cfg.ValidationIssues()
	if !hasIssue(issues, "endpoints.e.destinations[0].fifo") {
		t.Fatalf("expected fifo-on-non-fifo-target error, got %#v", issues)
	}
}

// TestValidateRejectsFIFOWithoutContentBasedFallback asserts that a FIFO target
// with content_based_fallback: false fails validation, while content_based_fallback: true passes.
func TestValidateRejectsFIFOWithoutContentBasedFallback(t *testing.T) {
	base := Config{
		Server: ServerConfig{Addr: ":8080", MaxBodyBytes: 1024},
		Destinations: map[string]DestinationConfig{
			"jobs": {Type: "sqs", SQS: SQSConfig{Region: "us-east-1"}},
		},
	}
	fifoField := "endpoints.e.destinations[0].fifo.dedup.content_based_fallback"

	// content_based_fallback: false (zero value) — must produce a validation issue.
	bad := base
	bad.Endpoints = map[string]EndpointConfig{
		"e": {Enabled: true, Path: "/p", Auth: AuthConfig{Type: "no_auth"},
			Destinations: []EndpointDestinationConfig{{
				Destination: "jobs", Target: "billing-jobs.fifo",
				FIFO: &FIFOConfig{
					GroupID: FIFOGroupIDConfig{Source: "endpoint"},
					Dedup:   FIFODedupConfig{ContentBasedFallback: false},
				},
			}}},
	}
	issues := bad.ValidationIssues()
	if !hasIssue(issues, fifoField) {
		t.Fatalf("expected content_based_fallback error, got %#v", issues)
	}

	// content_based_fallback: true — must not produce that issue.
	good := base
	good.Endpoints = map[string]EndpointConfig{
		"e": {Enabled: true, Path: "/p", Auth: AuthConfig{Type: "no_auth"},
			Destinations: []EndpointDestinationConfig{{
				Destination: "jobs", Target: "billing-jobs.fifo",
				FIFO: &FIFOConfig{
					GroupID: FIFOGroupIDConfig{Source: "endpoint"},
					Dedup:   FIFODedupConfig{ContentBasedFallback: true},
				},
			}}},
	}
	issues = good.ValidationIssues()
	if hasIssue(issues, fifoField) {
		t.Fatalf("expected no content_based_fallback error with true, got %#v", issues)
	}
}

func validJWTYAML() string {
	return `
server:
  addr: ":8080"
  max_body_bytes: 1048576
destinations:
  billing:
    type: "rabbitmq"
    rabbitmq:
      url: "${RABBITMQ_URL}"
      exchange: "hookway.webhooks"
      exchange_type: "direct"
endpoints:
  jwt_webhook:
    enabled: true
    path: "/webhooks/jwt"
    destinations:
      - destination: "billing"
        target: "jwt-events"
        create: true
    auth:
      type: "jwt"
      scheme: "jwt_hs256"
      secret: "${JWT_SHARED_SECRET}"
      signature_header: "Authorization"
      expected_issuer: "https://issuer.example.com"
      expected_audience: "hookway"
      body_hash_claim: "body_sha256"
`
}

func TestLoadAcceptsJWTAuth(t *testing.T) {
	t.Setenv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/")
	t.Setenv("JWT_SHARED_SECRET", "shared-secret")
	cfg, err := Load(writeConfig(t, validJWTYAML()))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	ep := cfg.Endpoints["jwt_webhook"]
	if ep.Auth.Type != "jwt" || ep.Auth.Scheme != "jwt_hs256" {
		t.Fatalf("auth = %#v", ep.Auth)
	}
	if ep.Auth.ExpectedIssuer != "https://issuer.example.com" || ep.Auth.ExpectedAudience != "hookway" || ep.Auth.BodyHashClaim != "body_sha256" {
		t.Fatalf("jwt fields not plumbed: %#v", ep.Auth)
	}
}

func TestLoadRejectsJWTSchemeUnderHMACType(t *testing.T) {
	t.Setenv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/")
	t.Setenv("JWT_SHARED_SECRET", "shared-secret")
	yaml := strings.Replace(validJWTYAML(), `type: "jwt"`, `type: "hmac_sha256"`, 1)
	if _, err := Load(writeConfig(t, yaml)); err == nil {
		t.Fatal("expected jwt_hs256 under hmac_sha256 to be rejected")
	}
}

func TestLoadRejectsJWTWithoutSecret(t *testing.T) {
	t.Setenv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/")
	yaml := strings.Replace(validJWTYAML(), "      secret: \"${JWT_SHARED_SECRET}\"\n", "", 1)
	if _, err := Load(writeConfig(t, yaml)); err == nil {
		t.Fatal("jwt without a key/secret should be rejected")
	}
}

func validSQSYAML() string {
	return `
server:
  addr: ":8080"
  max_body_bytes: 1048576
destinations:
  jobs:
    type: "sqs"
    sqs:
      region: "us-east-1"
endpoints:
  ep1:
    enabled: true
    path: "/webhooks/ep1"
    destinations:
      - destination: "jobs"
        target: "orders-queue"
        create: true
    auth:
      type: "no_auth"
      scheme: "no_auth"
`
}

func validSNSYAML() string {
	return `
server:
  addr: ":8080"
  max_body_bytes: 1048576
destinations:
  alerts:
    type: "sns"
    sns:
      region: "us-east-1"
endpoints:
  ep1:
    enabled: true
    path: "/webhooks/ep1"
    destinations:
      - destination: "alerts"
        target: "orders-topic"
        create: true
    auth:
      type: "no_auth"
      scheme: "no_auth"
`
}

// FIX 3: cross-endpoint SQS divergence test
func TestLoadRejectsSQSDivergentSettingsAcrossEndpoints(t *testing.T) {
	yaml := `
server:
  addr: ":8080"
  max_body_bytes: 1048576
destinations:
  jobs:
    type: "sqs"
    sqs:
      region: "us-east-1"
endpoints:
  ep1:
    enabled: true
    path: "/webhooks/ep1"
    destinations:
      - destination: "jobs"
        target: "orders-queue"
        create: true
        sqs_options:
          visibility_timeout: "30s"
    auth:
      type: "no_auth"
      scheme: "no_auth"
  ep2:
    enabled: true
    path: "/webhooks/ep2"
    destinations:
      - destination: "jobs"
        target: "orders-queue"
        create: true
        sqs_options:
          visibility_timeout: "60s"
    auth:
      type: "no_auth"
      scheme: "no_auth"
`
	_, err := Load(writeConfig(t, yaml))
	if err == nil {
		t.Fatal("expected validation error for divergent SQS settings across endpoints")
	}
	if !strings.Contains(err.Error(), "cannot differ between endpoints that share an sqs target") {
		t.Fatalf("expected sqs divergence error, got: %v", err)
	}
}

// FIX 7+8: SQS option range validation
func TestLoadRejectsSQSSubSecondVisibilityTimeout(t *testing.T) {
	yaml := strings.Replace(validSQSYAML(), "create: true", "create: true\n        sqs_options:\n          visibility_timeout: \"500ms\"", 1)
	_, err := Load(writeConfig(t, yaml))
	if err == nil {
		t.Fatal("expected sub-second visibility_timeout to be rejected")
	}
	if !strings.Contains(err.Error(), "visibility_timeout") {
		t.Fatalf("expected visibility_timeout in error, got: %v", err)
	}
}

func TestLoadRejectsSQSRetentionBelowMinimum(t *testing.T) {
	yaml := strings.Replace(validSQSYAML(), "create: true", "create: true\n        sqs_options:\n          message_retention: \"30s\"", 1)
	_, err := Load(writeConfig(t, yaml))
	if err == nil {
		t.Fatal("expected retention below 60s to be rejected")
	}
	if !strings.Contains(err.Error(), "message_retention") {
		t.Fatalf("expected message_retention in error, got: %v", err)
	}
}

func TestLoadRejectsSQSVisibilityAboveMaximum(t *testing.T) {
	yaml := strings.Replace(validSQSYAML(), "create: true", "create: true\n        sqs_options:\n          visibility_timeout: \"13h\"", 1)
	_, err := Load(writeConfig(t, yaml))
	if err == nil {
		t.Fatal("expected visibility_timeout above 12h to be rejected")
	}
	if !strings.Contains(err.Error(), "visibility_timeout") {
		t.Fatalf("expected visibility_timeout in error, got: %v", err)
	}
}

func TestLoadAcceptsValidSQSOptions(t *testing.T) {
	yaml := strings.Replace(validSQSYAML(), "create: true", "create: true\n        sqs_options:\n          visibility_timeout: \"30s\"\n          message_retention: \"4h\"", 1)
	_, err := Load(writeConfig(t, yaml))
	if err != nil {
		t.Fatalf("expected valid SQS options to be accepted: %v", err)
	}
}

// FIX 9: SNS subject validation
func TestLoadRejectsSNSSubjectTooLong(t *testing.T) {
	long := strings.Repeat("a", 101)
	yaml := strings.Replace(validSNSYAML(), "create: true", "create: true\n        sns_options:\n          subject: \""+long+"\"", 1)
	_, err := Load(writeConfig(t, yaml))
	if err == nil {
		t.Fatal("expected subject longer than 100 chars to be rejected")
	}
	if !strings.Contains(err.Error(), "subject") {
		t.Fatalf("expected subject in error, got: %v", err)
	}
}

func TestLoadRejectsSNSSubjectWithNewline(t *testing.T) {
	yaml := strings.Replace(validSNSYAML(), "create: true", "create: true\n        sns_options:\n          subject: \"hello\\nworld\"", 1)
	_, err := Load(writeConfig(t, yaml))
	if err == nil {
		t.Fatal("expected subject with newline to be rejected")
	}
	if !strings.Contains(err.Error(), "subject") {
		t.Fatalf("expected subject in error, got: %v", err)
	}
}

func TestLoadAcceptsValidSNSSubject(t *testing.T) {
	yaml := strings.Replace(validSNSYAML(), "create: true", "create: true\n        sns_options:\n          subject: \"Order Confirmed\"", 1)
	_, err := Load(writeConfig(t, yaml))
	if err != nil {
		t.Fatalf("expected valid SNS subject to be accepted: %v", err)
	}
}

// FIX 5: SNS FIFO + subject validation
func TestLoadRejectsSNSFIFOWithSubject(t *testing.T) {
	yaml := `
server:
  addr: ":8080"
  max_body_bytes: 1048576
destinations:
  alerts:
    type: "sns"
    sns:
      region: "us-east-1"
endpoints:
  ep1:
    enabled: true
    path: "/webhooks/ep1"
    destinations:
      - destination: "alerts"
        target: "orders.fifo"
        create: true
        sns_options:
          subject: "Order event"
        fifo:
          group_id:
            source: "endpoint"
          dedup:
            content_based_fallback: true
    auth:
      type: "no_auth"
      scheme: "no_auth"
`
	_, err := Load(writeConfig(t, yaml))
	if err == nil {
		t.Fatal("expected FIFO SNS target with subject to be rejected")
	}
	if !strings.Contains(err.Error(), "subject is not supported on FIFO SNS topics") {
		t.Fatalf("expected FIFO SNS subject error, got: %v", err)
	}
}

func TestLoadOnExistingConflictDefaultsToReconcile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	yaml := `
server:
  addr: ":8080"
  max_body_bytes: 1048576
destinations:
  jobs:
    type: sqs
    sqs:
      region: us-east-1
endpoints:
  e:
    enabled: true
    path: /p
    destinations:
      - destination: jobs
        target: q
    auth:
      type: no_auth
      scheme: no_auth
`
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Destinations["jobs"].OnExistingConflict != "reconcile" {
		t.Fatalf("default = %q, want reconcile", cfg.Destinations["jobs"].OnExistingConflict)
	}
}

func TestValidateRejectsInvalidOnExistingConflict(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	yaml := `
server:
  addr: ":8080"
  max_body_bytes: 1048576
destinations:
  jobs:
    type: sqs
    on_existing_conflict: nonsense
    sqs:
      region: us-east-1
endpoints:
  e:
    enabled: true
    path: /p
    destinations:
      - destination: jobs
        target: q
    auth: { type: no_auth, scheme: no_auth }
`
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid on_existing_conflict")
	}
	if !strings.Contains(err.Error(), "on_existing_conflict") {
		t.Fatalf("error did not mention on_existing_conflict: %v", err)
	}
}

func TestValidateRejectsOnExistingConflictOnRabbitMQ(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	yaml := `
server:
  addr: ":8080"
  max_body_bytes: 1048576
destinations:
  primary:
    type: rabbitmq
    on_existing_conflict: warn
    rabbitmq:
      url: amqp://x
      exchange: e
      exchange_type: direct
endpoints:
  e:
    enabled: true
    path: /p
    destinations:
      - destination: primary
        target: q
    auth: { type: no_auth, scheme: no_auth }
`
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "on_existing_conflict") {
		t.Fatalf("expected on_existing_conflict-on-rabbitmq error, got %v", err)
	}
}

// ---- Redis config tests ----

func validRedisStreamYAML() string {
	return `
server:
  addr: ":8080"
  max_body_bytes: 1048576
destinations:
  events:
    type: redis_stream
    redis:
      mode: single
      url: "redis://localhost:6379"
      username: "redisuser"
      password: "redispass"
      db: 1
      key_prefix: "hookway"
      tls:
        enabled: true
        ca_cert: ""
        client_cert: ""
        client_key: ""
        server_name: ""
        insecure_skip_verify: false
endpoints:
  ep1:
    enabled: true
    path: "/webhooks/ep1"
    destinations:
      - destination: events
        target: webhooks-stream
        create: true
        redis_options:
          max_len: 100000
          consumer_group: "workers"
    auth:
      type: no_auth
      scheme: no_auth
`
}

// TestRedisStreamSingleModeLoadsCorrectly verifies a valid redis_stream single-mode config round-trips.
func TestRedisStreamSingleModeLoadsCorrectly(t *testing.T) {
	cfg, err := Load(writeConfig(t, validRedisStreamYAML()))
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	dest := cfg.Destinations["events"]
	if dest.Type != "redis_stream" {
		t.Fatalf("type = %q, want redis_stream", dest.Type)
	}
	r := dest.Redis
	if r.Mode != "single" {
		t.Fatalf("mode = %q, want single", r.Mode)
	}
	if r.URL != "redis://localhost:6379" {
		t.Fatalf("url = %q", r.URL)
	}
	if r.Username != "redisuser" {
		t.Fatalf("username = %q", r.Username)
	}
	if r.Password != "redispass" {
		t.Fatalf("password = %q", r.Password)
	}
	if r.DB != 1 {
		t.Fatalf("db = %d, want 1", r.DB)
	}
	if r.KeyPrefix != "hookway" {
		t.Fatalf("key_prefix = %q", r.KeyPrefix)
	}
	if !r.TLS.Enabled {
		t.Fatalf("tls.enabled = %v, want true", r.TLS.Enabled)
	}

	ep := cfg.Endpoints["ep1"]
	d := ep.Destinations[0]
	if d.RedisOptions.MaxLen != 100000 {
		t.Fatalf("max_len = %d, want 100000", d.RedisOptions.MaxLen)
	}
	if d.RedisOptions.ConsumerGroup != "workers" {
		t.Fatalf("consumer_group = %q, want workers", d.RedisOptions.ConsumerGroup)
	}
}

// TestRedisModeDefaultsToSingle verifies that omitting mode entirely defaults to "single".
func TestRedisModeDefaultsToSingle(t *testing.T) {
	yaml := strings.Replace(validRedisStreamYAML(), "      mode: single\n", "", 1)
	cfg, err := Load(writeConfig(t, yaml))
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if got := cfg.Destinations["events"].Redis.Mode; got != "single" {
		t.Fatalf("mode = %q, want single (defaulted)", got)
	}
}

// TestRedisModeNotDefaultedForNonRedisDestination verifies a non-redis destination
// keeps a zero RedisConfig (empty Mode) and is not defaulted to "single".
func TestRedisModeNotDefaultedForNonRedisDestination(t *testing.T) {
	cfg, err := Load(writeConfig(t, validSQSYAML()))
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if got := cfg.Destinations["jobs"].Redis.Mode; got != "" {
		t.Fatalf("non-redis destination Redis.Mode = %q, want empty", got)
	}
	if !reflect.DeepEqual(cfg.Destinations["jobs"].Redis, RedisConfig{}) {
		t.Fatalf("non-redis destination has non-zero RedisConfig: %#v", cfg.Destinations["jobs"].Redis)
	}
}

// TestRedisInvalidModeRejected verifies that an unsupported mode is rejected.
func TestRedisInvalidModeRejected(t *testing.T) {
	yaml := strings.Replace(validRedisStreamYAML(), "mode: single", "mode: replication", 1)
	_, err := Load(writeConfig(t, yaml))
	if err == nil {
		t.Fatal("expected error for invalid redis mode")
	}
	if !strings.Contains(err.Error(), "mode") {
		t.Fatalf("expected mode in error, got: %v", err)
	}
}

// TestRedisSingleWithoutURLRejected verifies that single mode without url is rejected.
func TestRedisSingleWithoutURLRejected(t *testing.T) {
	yaml := strings.Replace(validRedisStreamYAML(), "url: \"redis://localhost:6379\"", "url: \"\"", 1)
	_, err := Load(writeConfig(t, yaml))
	if err == nil {
		t.Fatal("expected error for single mode without url")
	}
	if !strings.Contains(err.Error(), "url") {
		t.Fatalf("expected url in error, got: %v", err)
	}
}

// TestRedisClusterWithoutAddrsRejected verifies that cluster mode without addrs is rejected.
func TestRedisClusterWithoutAddrsRejected(t *testing.T) {
	yaml := strings.Replace(validRedisStreamYAML(), "mode: single\n      url: \"redis://localhost:6379\"", "mode: cluster", 1)
	_, err := Load(writeConfig(t, yaml))
	if err == nil {
		t.Fatal("expected error for cluster mode without addrs")
	}
	if !strings.Contains(err.Error(), "addrs") {
		t.Fatalf("expected addrs in error, got: %v", err)
	}
}

// TestRedisSentinelWithoutMasterNameRejected verifies sentinel mode without master_name is rejected.
func TestRedisSentinelWithoutMasterNameRejected(t *testing.T) {
	yaml := strings.Replace(validRedisStreamYAML(),
		"mode: single\n      url: \"redis://localhost:6379\"",
		"mode: sentinel\n      addrs:\n        - \"sentinel1:26379\"", 1)
	_, err := Load(writeConfig(t, yaml))
	if err == nil {
		t.Fatal("expected error for sentinel mode without master_name")
	}
	if !strings.Contains(err.Error(), "master_name") {
		t.Fatalf("expected master_name in error, got: %v", err)
	}
}

// TestRedisConsumerGroupOnKVRejected verifies that consumer_group is only valid for redis_stream.
func TestRedisConsumerGroupOnKVRejected(t *testing.T) {
	yaml := strings.Replace(validRedisStreamYAML(), "type: redis_stream", "type: redis_kv", 1)
	// add ttl since redis_kv requires it conceptually but we're testing consumer_group rejection
	yaml = strings.Replace(yaml,
		"redis_options:\n          max_len: 100000\n          consumer_group: \"workers\"",
		"redis_options:\n          consumer_group: \"workers\"", 1)
	_, err := Load(writeConfig(t, yaml))
	if err == nil {
		t.Fatal("expected error for consumer_group on redis_kv")
	}
	if !strings.Contains(err.Error(), "consumer_group") {
		t.Fatalf("expected consumer_group in error, got: %v", err)
	}
}

// TestRedisTTLOnStreamRejected verifies that ttl is not valid for redis_stream.
func TestRedisTTLOnStreamRejected(t *testing.T) {
	yaml := strings.Replace(validRedisStreamYAML(),
		"redis_options:\n          max_len: 100000\n          consumer_group: \"workers\"",
		"redis_options:\n          max_len: 100000\n          ttl: \"24h\"", 1)
	_, err := Load(writeConfig(t, yaml))
	if err == nil {
		t.Fatal("expected error for ttl on redis_stream")
	}
	if !strings.Contains(err.Error(), "ttl") {
		t.Fatalf("expected ttl in error, got: %v", err)
	}
}

func TestConvertDestinationPostgres(t *testing.T) {
	raw := rawDestinationConfig{Type: "postgres"}
	raw.Postgres.URL = "postgres://u:p@localhost:5432/db?sslmode=disable"
	raw.Postgres.Table = "webhook_events"
	raw.Postgres.MaxConns = 8
	// schema omitted -> should default to "public"

	dest, issues := convertDestination("destinations.warehouse", raw)
	if len(issues) != 0 {
		t.Fatalf("unexpected issues: %#v", issues)
	}
	if dest.Type != "postgres" {
		t.Fatalf("type = %q", dest.Type)
	}
	if dest.Postgres.URL == "" || dest.Postgres.Table != "webhook_events" {
		t.Fatalf("postgres config not mapped: %#v", dest.Postgres)
	}
	if dest.Postgres.Schema != "public" {
		t.Fatalf("schema default = %q, want \"public\"", dest.Postgres.Schema)
	}
	if dest.Postgres.MaxConns != 8 {
		t.Fatalf("max_conns = %d", dest.Postgres.MaxConns)
	}
}

func TestConvertDestinationPostgresRejectsOnExistingConflict(t *testing.T) {
	raw := rawDestinationConfig{Type: "postgres", OnExistingConflict: "reconcile"}
	raw.Postgres.URL = "postgres://localhost/db"
	raw.Postgres.Table = "events"
	_, issues := convertDestination("destinations.warehouse", raw)
	if !hasIssue(issues, "destinations.warehouse.on_existing_conflict") {
		t.Fatalf("expected on_existing_conflict to be rejected for postgres; got %#v", issues)
	}
}

func TestValidatePostgresRequiresURLAndTable(t *testing.T) {
	c := Config{
		Destinations: map[string]DestinationConfig{
			"warehouse": {Type: "postgres", Postgres: PostgresConfig{Schema: "bad-schema!", Table: "", MaxConns: -1}},
		},
		Endpoints: map[string]EndpointConfig{
			"e": {Enabled: true, Path: "/e", Destinations: []EndpointDestinationConfig{
				{Destination: "warehouse", Target: "orders"},
			}},
		},
	}
	issues := c.ValidationIssues()
	for _, f := range []string{
		"destinations.warehouse.postgres.url",
		"destinations.warehouse.postgres.table",
		"destinations.warehouse.postgres.schema",
		"destinations.warehouse.postgres.max_conns",
	} {
		if !hasIssue(issues, f) {
			t.Fatalf("missing validation issue for %q; got %#v", f, issues)
		}
	}
}

func TestValidatePostgresValidTablePasses(t *testing.T) {
	c := Config{
		Destinations: map[string]DestinationConfig{
			"warehouse": {Type: "postgres", Postgres: PostgresConfig{URL: "postgres://localhost/db", Schema: "public", Table: "webhook_events"}},
		},
		Endpoints: map[string]EndpointConfig{
			"e": {Enabled: true, Path: "/e", Destinations: []EndpointDestinationConfig{
				{Destination: "warehouse", Target: "orders"},
			}},
		},
	}
	for _, iss := range c.ValidationIssues() {
		if strings.HasPrefix(iss.Field, "destinations.warehouse.postgres") {
			t.Fatalf("unexpected postgres validation issue: %#v", iss)
		}
	}
}

// TestRedisOptionsOnSQSRejected verifies that redis_options is not valid for sqs destinations.
func TestRedisOptionsOnSQSRejected(t *testing.T) {
	cfg := Config{
		Server: ServerConfig{Addr: ":8080", MaxBodyBytes: 1024},
		Destinations: map[string]DestinationConfig{
			"jobs": {Type: "sqs", SQS: SQSConfig{Region: "us-east-1"}},
		},
		Endpoints: map[string]EndpointConfig{
			"e": {Enabled: true, Path: "/p", Auth: AuthConfig{Type: "no_auth"},
				Destinations: []EndpointDestinationConfig{{
					Destination:  "jobs",
					Target:       "orders-queue",
					RedisOptions: RedisEndpointOptions{MaxLen: 1000},
				}}},
		},
	}
	issues := cfg.ValidationIssues()
	if !hasIssue(issues, "endpoints.e.destinations[0].redis_options") {
		t.Fatalf("expected redis_options-on-sqs error, got %#v", issues)
	}
}
