package config

import (
	"os"
	"path/filepath"
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
	if cfg.MessageQueues["billing"].Type != "rabbitmq" {
		t.Fatalf("message queue type = %q", cfg.MessageQueues["billing"].Type)
	}
	if cfg.MessageQueues["billing"].RabbitMQ.URL != "amqp://guest:guest@localhost:5672/" {
		t.Fatalf("RabbitMQ URL not expanded: %q", cfg.MessageQueues["billing"].RabbitMQ.URL)
	}
	destinations := cfg.Endpoints["paddle_billing"].Destinations
	if len(destinations) != 1 || destinations[0].DestinationQueueID != "billing" {
		t.Fatalf("endpoint destinations = %#v", destinations)
	}
	if cfg.Endpoints["paddle_billing"].Path != "/webhooks/paddle/billing" {
		t.Fatalf("endpoint path = %q", cfg.Endpoints["paddle_billing"].Path)
	}
	if destinations[0].Queue != "billing-events" {
		t.Fatalf("endpoint queue = %q", destinations[0].Queue)
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
	if !cfg.MessageQueues["billing"].RabbitMQ.DeadLetter.Enabled {
		t.Fatal("dead-letter config not parsed")
	}
	if cfg.MessageQueues["billing"].RabbitMQ.QueueDefaults.MessageTTL != 24*time.Hour {
		t.Fatalf("queue default message ttl = %s", cfg.MessageQueues["billing"].RabbitMQ.QueueDefaults.MessageTTL)
	}
	if cfg.MessageQueues["billing"].RabbitMQ.QueueDefaults.MaxLength != 100000 {
		t.Fatalf("queue default max length = %d", cfg.MessageQueues["billing"].RabbitMQ.QueueDefaults.MaxLength)
	}
	if cfg.MessageQueues["billing"].RabbitMQ.QueueDefaults.Overflow != "reject-publish" {
		t.Fatalf("queue default overflow = %q", cfg.MessageQueues["billing"].RabbitMQ.QueueDefaults.Overflow)
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
	if cfg.MessageQueues["billing"].RabbitMQ.DeadLetter.Exchange != "hookway.webhooks.dlx" {
		t.Fatalf("dead-letter exchange = %q", cfg.MessageQueues["billing"].RabbitMQ.DeadLetter.Exchange)
	}
	if !destinations[0].CreateQueue {
		t.Fatal("endpoint create_queue not parsed")
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
      - destination_queue_id: "archive"
        queue: "billing-archive-events"
        create_queue: false
        queue_options:
          message_ttl: "6h"`, 1)
	body = strings.Replace(body, `message_queues:
  billing:`, `message_queues:
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
	if got[0].DestinationQueueID != "billing" || got[0].Queue != "billing-events" || !got[0].CreateQueue || got[0].QueueOptions.MessageTTL != 72*time.Hour {
		t.Fatalf("first destination = %#v", got[0])
	}
	if got[1].DestinationQueueID != "archive" || got[1].Queue != "billing-archive-events" || got[1].CreateQueue || got[1].QueueOptions.MessageTTL != 6*time.Hour {
		t.Fatalf("second destination = %#v", got[1])
	}
}

func TestLoadRejectsInvalidConfig(t *testing.T) {
	tests := map[string]string{
		"missing message queue type": replace(validYAML(), "type: \"rabbitmq\"", "type: \"\""),
		"unsupported queue type":     replace(validYAML(), "type: \"rabbitmq\"", "type: \"sqs\""),
		"invalid message queue id":   replace(validYAML(), "billing:", "bad queue:"),
		"missing destinations":       strings.Replace(validYAML(), "    destinations:\n      - destination_queue_id: \"billing\"", "    destinations: []\n    #", 1),
		"missing destination id":     replace(validYAML(), "destination_queue_id: \"billing\"", "destination_queue_id: \"\""),
		"unknown fanout destination": replace(validYAML(), "destination_queue_id: \"billing\"", "destination_queue_id: \"missing\""),
		"duplicate fanout destination route": strings.Replace(validYAML(), `          dead_letter_routing_key: "paddle.dead"`, `          dead_letter_routing_key: "paddle.dead"
      - destination_queue_id: "billing"
        queue: "billing-events"
        create_queue: true
        queue_options:
          message_ttl: "72h"
          max_length: 500000
          dead_letter_routing_key: "paddle.dead"`, 1),
		"missing rabbitmq url":     replace(validYAML(), "url: \"${RABBITMQ_URL}\"", "url: \"\""),
		"invalid endpoint":         replace(validYAML(), "paddle_billing:", "bad/endpoint:"),
		"missing endpoint path":    replace(validYAML(), "path: \"/webhooks/paddle/billing\"", "path: \"\""),
		"invalid endpoint path":    replace(validYAML(), "path: \"/webhooks/paddle/billing\"", "path: \"webhooks/paddle/billing\""),
		"invalid queue":            replace(validYAML(), "queue: \"billing-events\"", "queue: \"bad queue\""),
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
		"invalid endpoint rate limit":  replace(validYAML(), "requests_per_minute: 60", "requests_per_minute: 0"),
		"invalid replay ttl":           replace(validYAML(), "ttl: \"10m\"", "ttl: \"soon\""),
		"invalid replay max entries":   replace(validYAML(), "max_entries: 50000", "max_entries: 0"),
		"invalid ip allowlist":         replace(validYAML(), "- \"203.0.113.10\"", "- \"not an ip\""),
		"invalid redacted header":      replace(validYAML(), "- \"Authorization\"", "- \"\""),
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
      - destination_queue_id: "billing"
        queue: "billing-child-events"
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
      - destination_queue_id: "billing"
        queue: "billing-archive-events"
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
	body = replace(body, "queue: \"billing-events\"", "queue: \"bad queue\"")
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
		"message_queues.billing.type",
		"metrics.path",
		"endpoints.paddle_billing.path",
		"endpoints.paddle_billing.destinations[0].queue",
		"endpoints.paddle_billing.auth.secret",
		"endpoints.paddle_billing.auth.signature_header",
		"security.redacted_header_denylist",
		"message_queues.billing.rabbitmq.dead_letter.exchange",
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

func validHeaderExactYAML() string {
	return `
server:
  addr: ":8080"
  max_body_bytes: 1048576
message_queues:
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
      - destination_queue_id: "billing"
        queue: "revenuecat-events"
        create_queue: true
    auth:
      type: "header_exact"
      scheme: "exact"
      secret: "${REVENUECAT_WEBHOOK_AUTH}"
      signature_header: "Authorization"
      remove_bearer: true
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
message_queues:
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
      - destination_queue_id: "billing"
        queue: "billing-events"
        create_queue: true
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

func replace(s, old, new string) string {
	return strings.ReplaceAll(s, old, new)
}
