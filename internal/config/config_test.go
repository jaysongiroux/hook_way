package config

import (
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

// loadSharedYAML mirrors the production load path (flowconfig.Load): strict
// ${VAR} expansion, a strict decode of the shared process-scoped sections, then
// shared validation. It returns the issues wrapped in ConfigValidationError so
// tests can assert on the same operator-facing error text the CLI prints.
func loadSharedYAML(t *testing.T, body string) (SharedConfig, error) {
	t.Helper()
	expanded, missing := ExpandEnvStrict(body)
	if len(missing) > 0 {
		return SharedConfig{}, ConfigValidationError{Issues: MissingEnvIssues(missing)}
	}
	shared, issues := DecodeSharedYAML([]byte(expanded), "config")
	issues = append(issues, shared.ValidationIssues()...)
	if len(issues) > 0 {
		return SharedConfig{}, ConfigValidationError{Issues: issues}
	}
	return shared, nil
}

// sharedIssues validates raw YAML bytes without touching the process
// environment, the way the editor / `validate --json` path does: ${VAR} stays
// an opaque literal so validating untrusted bytes cannot leak secrets.
func sharedIssues(body string) []ValidationIssue {
	shared, issues := DecodeSharedYAML([]byte(body), "config")
	return append(issues, shared.ValidationIssues()...)
}

// validSharedYAML is a complete, valid process-scoped config document: the
// sections flowconfig decodes through config.DecodeSharedYAML. Flows are
// authored separately and are not part of this package's contract.
func validSharedYAML() string {
	return `
server:
  addr: ":8080"
  read_timeout: "5s"
  write_timeout: "10s"
  idle_timeout: "60s"
  shutdown_timeout: "10s"
  publish_timeout: "10s"
  max_body_bytes: 1048576
  fanout_concurrency: 4
security:
  trusted_proxy_headers: false
  request_id_header: "X-Request-ID"
  redacted_header_denylist:
    - "Authorization"
    - "Cookie"
    - "X-Signature"
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
`
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

func TestLoadExpandsEnvironmentAndValidates(t *testing.T) {
	t.Setenv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/")
	cfg, err := loadSharedYAML(t, validSharedYAML())
	if err != nil {
		t.Fatalf("load returned error: %v", err)
	}
	if cfg.Destinations["billing"].Type != "rabbitmq" {
		t.Fatalf("message queue type = %q", cfg.Destinations["billing"].Type)
	}
	if cfg.Destinations["billing"].RabbitMQ.URL != "amqp://guest:guest@localhost:5672/" { // #nosec G101 -- expanded test fixture
		t.Fatalf("RabbitMQ URL not expanded: %q", cfg.Destinations["billing"].RabbitMQ.URL)
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
	if cfg.Destinations["billing"].RabbitMQ.DeadLetter.Exchange != "hookway.webhooks.dlx" {
		t.Fatalf("dead-letter exchange = %q", cfg.Destinations["billing"].RabbitMQ.DeadLetter.Exchange)
	}
}

func TestLoadRejectsInvalidConfig(t *testing.T) {
	tests := map[string]string{
		"missing destination type":     replace(validSharedYAML(), "type: \"rabbitmq\"", "type: \"\""),
		"invalid destination id":       replace(validSharedYAML(), "  billing:", "  bad queue:"),
		"missing rabbitmq url":         replace(validSharedYAML(), "url: \"${RABBITMQ_URL}\"", "url: \"\""),
		"invalid metrics path":         replace(validSharedYAML(), "path: \"/metrics\"", "path: \"metrics\""),
		"invalid dead letter exchange": replace(validSharedYAML(), "exchange: \"hookway.webhooks.dlx\"", "exchange: \"bad exchange\""),
		"invalid dead letter queue":    replace(validSharedYAML(), "queue: \"hookway.webhooks.dlq\"", "queue: \"bad queue\""),
		"invalid queue default ttl":    replace(validSharedYAML(), "message_ttl: \"24h\"", "message_ttl: \"forever\""),
		"invalid queue default length": replace(validSharedYAML(), "max_length: 100000", "max_length: -1"),
		"invalid queue overflow":       replace(validSharedYAML(), "overflow: \"reject-publish\"", "overflow: \"drop-head\""),
		"invalid redacted header":      replace(validSharedYAML(), "- \"Authorization\"", "- \"\""),
		"invalid trusted proxy":        validSharedYAML() + "  trusted_proxies:\n    - \"not an ip\"\n",
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			t.Setenv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/")
			if _, err := loadSharedYAML(t, body); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestLoadRejectsTopLevelTrustedProxies(t *testing.T) {
	t.Setenv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/")
	body := replace(validSharedYAML(), "  request_id_header: \"X-Request-ID\"",
		"  request_id_header: \"X-Request-ID\"\n  trusted_proxies:\n    - \"10.0.0.0/8\"\n    - \"203.0.113.10\"")
	cfg, err := loadSharedYAML(t, body)
	if err != nil {
		t.Fatalf("valid trusted_proxies rejected: %v", err)
	}
	if len(cfg.Security.TrustedProxies) != 2 {
		t.Fatalf("trusted proxies = %#v", cfg.Security.TrustedProxies)
	}
}

func TestLoadRejectsOldMessageQueueShape(t *testing.T) {
	t.Setenv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/")
	body := `
server:
  addr: ":8080"
  max_body_bytes: 1048576
message_queue:
  type: "rabbitmq"
  rabbitmq:
    url: "${RABBITMQ_URL}"
`
	_, err := loadSharedYAML(t, body)
	if err == nil {
		t.Fatal("expected old message_queue shape to be rejected")
	}
	if !strings.Contains(err.Error(), "unknown or invalid YAML field") {
		t.Fatalf("unexpected error: %s", err)
	}
}

// TestLoadRejectsV1EndpointsBlock pins the v2 contract: the removed v1
// `endpoints:` schema is reported as an unknown top-level field rather than
// silently ignored.
func TestLoadRejectsV1EndpointsBlock(t *testing.T) {
	t.Setenv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/")
	body := validSharedYAML() + `
endpoints:
  paddle_billing:
    enabled: true
    path: "/webhooks/paddle/billing"
`
	_, err := loadSharedYAML(t, body)
	if err == nil {
		t.Fatal("expected the v1 endpoints block to be rejected")
	}
	if !strings.Contains(err.Error(), "endpoints") || !strings.Contains(err.Error(), "unknown or invalid YAML field") {
		t.Fatalf("unexpected error: %s", err)
	}
}

func TestLoadReturnsHelpfulValidationErrors(t *testing.T) {
	t.Setenv("RABBITMQ_URL", "")
	body := replace(validSharedYAML(), "addr: \":8080\"", "addr: \"\"")
	body = replace(body, "max_body_bytes: 1048576", "max_body_bytes: 0")
	body = replace(body, "type: \"rabbitmq\"", "type: \"\"")
	body = replace(body, "path: \"/metrics\"", "path: \"metrics\"")
	body = replace(body, "- \"Authorization\"", "- \"\"")
	body = replace(body, "exchange: \"hookway.webhooks.dlx\"", "exchange: \"bad exchange\"")

	_, err := loadSharedYAML(t, body)
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
		"security.redacted_header_denylist",
		"destinations.billing.rabbitmq.dead_letter.exchange",
		"Suggestion:",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("error missing %q:\n%s", want, text)
		}
	}
}

func TestLoadReportsInvalidDurationWithFieldPath(t *testing.T) {
	t.Setenv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/")
	body := replace(validSharedYAML(), "read_timeout: \"5s\"", "read_timeout: \"soon\"")
	_, err := loadSharedYAML(t, body)
	if err == nil {
		t.Fatal("expected duration error")
	}
	if !strings.Contains(err.Error(), "server.read_timeout") || !strings.Contains(err.Error(), "valid Go duration") {
		t.Fatalf("unhelpful error: %s", err)
	}
}

func TestLoadReportsUnknownFieldsAndYAMLSyntax(t *testing.T) {
	t.Setenv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/")
	_, err := loadSharedYAML(t, replace(validSharedYAML(), "server:", "server:\n  typo: true"))
	if err == nil {
		t.Fatal("expected unknown field error")
	}
	if !strings.Contains(err.Error(), "unknown or invalid YAML field") || !strings.Contains(err.Error(), "typo") {
		t.Fatalf("unhelpful unknown field error: %s", err)
	}

	_, err = loadSharedYAML(t, "server:\n  addr: [")
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

func TestConfigPathFallsBackToEnvAndErrors(t *testing.T) {
	t.Setenv("HOOKWAY_CONFIG", "env.yaml")
	if path, err := ResolvePath(""); err != nil || path != "env.yaml" {
		t.Fatalf("ResolvePath(\"\") = %q, %v", path, err)
	}
	_ = os.Unsetenv("HOOKWAY_CONFIG")
	if _, err := ResolvePath(""); err == nil {
		t.Fatal("expected an error when neither --config nor HOOKWAY_CONFIG is set")
	}
}

// endpointConfigFor builds a Config carrying one enabled endpoint. Endpoint
// validation is reachable only through validationIssues; flows are validated by
// internal/flowconfig.
func endpointConfigFor(destinations map[string]DestinationConfig, endpoint EndpointConfig) Config {
	return Config{
		Server:       ServerConfig{Addr: ":8080", MaxBodyBytes: 1024},
		Destinations: destinations,
		Endpoints:    map[string]EndpointConfig{"e": endpoint},
	}
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
	}
	issues := validationIssues(c)
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
	body := `
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
`
	cfg, err := loadSharedYAML(t, body)
	if err != nil {
		t.Fatalf("load returned error: %v", err)
	}
	if cfg.Destinations["jobs"].Type != "sqs" || cfg.Destinations["jobs"].SQS.Region != "us-east-1" {
		t.Fatalf("bad sqs destination: %#v", cfg.Destinations["jobs"])
	}
	if cfg.Destinations["alerts"].Type != "sns" || cfg.Destinations["alerts"].SNS.Endpoint != "http://localhost:4566" {
		t.Fatalf("bad sns destination: %#v", cfg.Destinations["alerts"])
	}
}

func TestValidateRejectsSQSOptionsOnNonSQSDestination(t *testing.T) {
	cfg := endpointConfigFor(
		map[string]DestinationConfig{
			"primary": {Type: "rabbitmq", RabbitMQ: RabbitMQConfig{URL: "amqp://x", Exchange: "e", ExchangeType: "direct"}},
		},
		EndpointConfig{Enabled: true, Path: "/p", Auth: AuthConfig{Type: "no_auth"},
			Destinations: []EndpointDestinationConfig{{
				Destination: "primary", Target: "q",
				SQSOptions: SQSEndpointOptions{VisibilityTimeout: time.Second},
			}}},
	)
	if issues := validationIssues(cfg); !hasIssue(issues, "endpoints.e.destinations[0].sqs_options") {
		t.Fatalf("expected sqs_options-on-rabbitmq error, got %#v", issues)
	}
}

func TestValidateRejectsFIFOBlockOnNonFifoTarget(t *testing.T) {
	cfg := endpointConfigFor(
		map[string]DestinationConfig{"jobs": {Type: "sqs", SQS: SQSConfig{Region: "us-east-1"}}},
		EndpointConfig{Enabled: true, Path: "/p", Auth: AuthConfig{Type: "no_auth"},
			Destinations: []EndpointDestinationConfig{{
				Destination: "jobs", Target: "plain-queue",
				FIFO: &FIFOConfig{GroupID: FIFOGroupIDConfig{Source: "endpoint"}},
			}}},
	)
	if issues := validationIssues(cfg); !hasIssue(issues, "endpoints.e.destinations[0].fifo") {
		t.Fatalf("expected fifo-on-non-fifo-target error, got %#v", issues)
	}
}

// TestValidateRejectsFIFOWithoutContentBasedFallback asserts that a FIFO target
// with content_based_fallback: false fails validation, while content_based_fallback: true passes.
func TestValidateRejectsFIFOWithoutContentBasedFallback(t *testing.T) {
	dests := map[string]DestinationConfig{"jobs": {Type: "sqs", SQS: SQSConfig{Region: "us-east-1"}}}
	fifoField := "endpoints.e.destinations[0].fifo.dedup.content_based_fallback"
	mk := func(contentBased bool) Config {
		return endpointConfigFor(dests, EndpointConfig{Enabled: true, Path: "/p", Auth: AuthConfig{Type: "no_auth"},
			Destinations: []EndpointDestinationConfig{{
				Destination: "jobs", Target: "billing-jobs.fifo",
				FIFO: &FIFOConfig{
					GroupID: FIFOGroupIDConfig{Source: "endpoint"},
					Dedup:   FIFODedupConfig{ContentBasedFallback: contentBased},
				},
			}}})
	}
	if issues := validationIssues(mk(false)); !hasIssue(issues, fifoField) {
		t.Fatalf("expected content_based_fallback error, got %#v", issues)
	}
	if issues := validationIssues(mk(true)); hasIssue(issues, fifoField) {
		t.Fatalf("expected no content_based_fallback error with true, got %#v", issues)
	}
}

func TestValidateSQSOptionRanges(t *testing.T) {
	dests := map[string]DestinationConfig{"jobs": {Type: "sqs", SQS: SQSConfig{Region: "us-east-1"}}}
	mk := func(opts SQSEndpointOptions) Config {
		return endpointConfigFor(dests, EndpointConfig{Enabled: true, Path: "/p", Auth: AuthConfig{Type: "no_auth"},
			Destinations: []EndpointDestinationConfig{{Destination: "jobs", Target: "orders-queue", SQSOptions: opts}}})
	}
	cases := []struct {
		name  string
		opts  SQSEndpointOptions
		field string
	}{
		{"sub-second visibility timeout", SQSEndpointOptions{VisibilityTimeout: 500 * time.Millisecond}, "endpoints.e.destinations[0].sqs_options.visibility_timeout"},
		{"visibility timeout above maximum", SQSEndpointOptions{VisibilityTimeout: 13 * time.Hour}, "endpoints.e.destinations[0].sqs_options.visibility_timeout"},
		{"retention below minimum", SQSEndpointOptions{MessageRetention: 30 * time.Second}, "endpoints.e.destinations[0].sqs_options.message_retention"},
		{"retention above maximum", SQSEndpointOptions{MessageRetention: 24 * 15 * time.Hour}, "endpoints.e.destinations[0].sqs_options.message_retention"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if issues := validationIssues(mk(tc.opts)); !hasIssue(issues, tc.field) {
				t.Fatalf("expected issue on %s, got %#v", tc.field, issues)
			}
		})
	}
	valid := mk(SQSEndpointOptions{VisibilityTimeout: 30 * time.Second, MessageRetention: 4 * time.Hour})
	for _, iss := range validationIssues(valid) {
		if strings.Contains(iss.Field, "sqs_options") {
			t.Fatalf("valid sqs_options rejected: %#v", iss)
		}
	}
}

func TestValidateSNSSubject(t *testing.T) {
	dests := map[string]DestinationConfig{"alerts": {Type: "sns", SNS: SNSConfig{Region: "us-east-1"}}}
	mk := func(target, subject string, fifo *FIFOConfig) Config {
		return endpointConfigFor(dests, EndpointConfig{Enabled: true, Path: "/p", Auth: AuthConfig{Type: "no_auth"},
			Destinations: []EndpointDestinationConfig{{
				Destination: "alerts", Target: target,
				SNSOptions: SNSEndpointOptions{Subject: subject},
				FIFO:       fifo,
			}}})
	}
	field := "endpoints.e.destinations[0].sns_options.subject"
	if issues := validationIssues(mk("orders-topic", strings.Repeat("a", 101), nil)); !hasIssue(issues, field) {
		t.Fatalf("expected subject longer than 100 chars to be rejected, got %#v", issues)
	}
	if issues := validationIssues(mk("orders-topic", "hello\nworld", nil)); !hasIssue(issues, field) {
		t.Fatalf("expected subject with newline to be rejected, got %#v", issues)
	}
	for _, iss := range validationIssues(mk("orders-topic", "Order Confirmed", nil)) {
		if iss.Field == field {
			t.Fatalf("valid subject rejected: %#v", iss)
		}
	}
	fifoCfg := mk("orders.fifo", "Order event", &FIFOConfig{
		GroupID: FIFOGroupIDConfig{Source: "endpoint"},
		Dedup:   FIFODedupConfig{ContentBasedFallback: true},
	})
	issues := validationIssues(fifoCfg)
	if !hasIssue(issues, field) {
		t.Fatalf("expected FIFO SNS target with subject to be rejected, got %#v", issues)
	}
	if !strings.Contains(ConfigValidationError{Issues: issues}.Error(), "subject is not supported on FIFO SNS topics") {
		t.Fatalf("expected FIFO SNS subject error text, got %#v", issues)
	}
}

func TestValidateRejectsSQSDivergentSettingsAcrossEndpoints(t *testing.T) {
	route := func(vt time.Duration) EndpointConfig {
		return EndpointConfig{Enabled: true, Path: "/p", Auth: AuthConfig{Type: "no_auth"},
			Destinations: []EndpointDestinationConfig{{
				Destination: "jobs", Target: "orders-queue", Create: true,
				SQSOptions: SQSEndpointOptions{VisibilityTimeout: vt},
			}}}
	}
	ep1 := route(30 * time.Second)
	ep2 := route(60 * time.Second)
	ep2.Path = "/p2"
	cfg := Config{
		Server:       ServerConfig{Addr: ":8080", MaxBodyBytes: 1024},
		Destinations: map[string]DestinationConfig{"jobs": {Type: "sqs", SQS: SQSConfig{Region: "us-east-1"}}},
		Endpoints:    map[string]EndpointConfig{"ep1": ep1, "ep2": ep2},
	}
	err := ConfigValidationError{Issues: validationIssues(cfg)}
	if !strings.Contains(err.Error(), "cannot differ between endpoints that share an sqs target") {
		t.Fatalf("expected sqs divergence error, got: %v", err)
	}
}

func TestLoadOnExistingConflictDefaultsToReconcile(t *testing.T) {
	body := `
server:
  addr: ":8080"
  max_body_bytes: 1048576
destinations:
  jobs:
    type: sqs
    sqs:
      region: us-east-1
`
	cfg, err := loadSharedYAML(t, body)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Destinations["jobs"].OnExistingConflict != "reconcile" {
		t.Fatalf("default = %q, want reconcile", cfg.Destinations["jobs"].OnExistingConflict)
	}
}

func TestValidateRejectsInvalidOnExistingConflict(t *testing.T) {
	body := `
server:
  addr: ":8080"
  max_body_bytes: 1048576
destinations:
  jobs:
    type: sqs
    on_existing_conflict: nonsense
    sqs:
      region: us-east-1
`
	_, err := loadSharedYAML(t, body)
	if err == nil {
		t.Fatal("expected error for invalid on_existing_conflict")
	}
	if !strings.Contains(err.Error(), "on_existing_conflict") {
		t.Fatalf("error did not mention on_existing_conflict: %v", err)
	}
}

func TestValidateRejectsOnExistingConflictOnRabbitMQ(t *testing.T) {
	body := `
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
`
	_, err := loadSharedYAML(t, body)
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
`
}

// TestRedisStreamSingleModeLoadsCorrectly verifies a valid redis_stream single-mode config round-trips.
func TestRedisStreamSingleModeLoadsCorrectly(t *testing.T) {
	cfg, err := loadSharedYAML(t, validRedisStreamYAML())
	if err != nil {
		t.Fatalf("load returned error: %v", err)
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
}

// TestRedisModeDefaultsToSingle verifies that omitting mode entirely defaults to "single".
func TestRedisModeDefaultsToSingle(t *testing.T) {
	body := strings.Replace(validRedisStreamYAML(), "      mode: single\n", "", 1)
	cfg, err := loadSharedYAML(t, body)
	if err != nil {
		t.Fatalf("load returned error: %v", err)
	}
	if got := cfg.Destinations["events"].Redis.Mode; got != "single" {
		t.Fatalf("mode = %q, want single (defaulted)", got)
	}
}

// TestRedisModeNotDefaultedForNonRedisDestination verifies a non-redis destination
// keeps a zero RedisConfig (empty Mode) and is not defaulted to "single".
func TestRedisModeNotDefaultedForNonRedisDestination(t *testing.T) {
	body := `
server:
  addr: ":8080"
  max_body_bytes: 1048576
destinations:
  jobs:
    type: "sqs"
    sqs:
      region: "us-east-1"
`
	cfg, err := loadSharedYAML(t, body)
	if err != nil {
		t.Fatalf("load returned error: %v", err)
	}
	if got := cfg.Destinations["jobs"].Redis.Mode; got != "" {
		t.Fatalf("non-redis destination Redis.Mode = %q, want empty", got)
	}
	if !reflect.DeepEqual(cfg.Destinations["jobs"].Redis, RedisConfig{}) {
		t.Fatalf("non-redis destination has non-zero RedisConfig: %#v", cfg.Destinations["jobs"].Redis)
	}
}

func TestRedisConnectionValidation(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"invalid mode", strings.Replace(validRedisStreamYAML(), "mode: single", "mode: replication", 1), "mode"},
		{"single without url", strings.Replace(validRedisStreamYAML(), "url: \"redis://localhost:6379\"", "url: \"\"", 1), "url"},
		{"cluster without addrs", strings.Replace(validRedisStreamYAML(), "mode: single\n      url: \"redis://localhost:6379\"", "mode: cluster", 1), "addrs"},
		{"sentinel without master_name", strings.Replace(validRedisStreamYAML(),
			"mode: single\n      url: \"redis://localhost:6379\"",
			"mode: sentinel\n      addrs:\n        - \"sentinel1:26379\"", 1), "master_name"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadSharedYAML(t, tc.body)
			if err == nil {
				t.Fatalf("expected error mentioning %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected %q in error, got: %v", tc.want, err)
			}
		})
	}
}

func TestValidateRedisRouteOptions(t *testing.T) {
	mk := func(destType string, opts RedisEndpointOptions) Config {
		return endpointConfigFor(
			map[string]DestinationConfig{"events": {Type: destType, Redis: RedisConfig{Mode: "single", URL: "redis://localhost:6379"}}},
			EndpointConfig{Enabled: true, Path: "/p", Auth: AuthConfig{Type: "no_auth"},
				Destinations: []EndpointDestinationConfig{{Destination: "events", Target: "webhooks-stream", RedisOptions: opts}}})
	}
	cases := []struct {
		name     string
		destType string
		opts     RedisEndpointOptions
		field    string
	}{
		{"consumer_group on kv", "redis_kv", RedisEndpointOptions{ConsumerGroup: "workers", TTL: time.Hour}, "endpoints.e.destinations[0].redis_options.consumer_group"},
		{"ttl on stream", "redis_stream", RedisEndpointOptions{MaxLen: 100000, TTL: 24 * time.Hour}, "endpoints.e.destinations[0].redis_options.ttl"},
		{"max_len on kv", "redis_kv", RedisEndpointOptions{MaxLen: 10}, "endpoints.e.destinations[0].redis_options.max_len"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if issues := validationIssues(mk(tc.destType, tc.opts)); !hasIssue(issues, tc.field) {
				t.Fatalf("expected issue on %s, got %#v", tc.field, issues)
			}
		})
	}
	valid := mk("redis_stream", RedisEndpointOptions{MaxLen: 100000, ConsumerGroup: "workers"})
	for _, iss := range validationIssues(valid) {
		if strings.Contains(iss.Field, "redis_options") {
			t.Fatalf("valid redis_options rejected: %#v", iss)
		}
	}
}

// TestRedisOptionsOnSQSRejected verifies that redis_options is not valid for sqs destinations.
func TestRedisOptionsOnSQSRejected(t *testing.T) {
	cfg := endpointConfigFor(
		map[string]DestinationConfig{"jobs": {Type: "sqs", SQS: SQSConfig{Region: "us-east-1"}}},
		EndpointConfig{Enabled: true, Path: "/p", Auth: AuthConfig{Type: "no_auth"},
			Destinations: []EndpointDestinationConfig{{
				Destination:  "jobs",
				Target:       "orders-queue",
				RedisOptions: RedisEndpointOptions{MaxLen: 1000},
			}}},
	)
	if issues := validationIssues(cfg); !hasIssue(issues, "endpoints.e.destinations[0].redis_options") {
		t.Fatalf("expected redis_options-on-sqs error, got %#v", issues)
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
	}
	issues := validationIssues(c)
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
	}
	for _, iss := range validationIssues(c) {
		if strings.HasPrefix(iss.Field, "destinations.warehouse.postgres") {
			t.Fatalf("unexpected postgres validation issue: %#v", iss)
		}
	}
}

func TestConvertDestinationMongo(t *testing.T) {
	raw := rawDestinationConfig{Type: "mongodb"}
	raw.Mongo.URI = "mongodb://localhost:27017"
	raw.Mongo.Database = "webhooks"
	raw.Mongo.MaxPoolSize = 20

	dest, issues := convertDestination("destinations.archive", raw)
	if len(issues) != 0 {
		t.Fatalf("unexpected issues: %#v", issues)
	}
	if dest.Type != "mongodb" {
		t.Fatalf("type = %q", dest.Type)
	}
	if dest.Mongo.URI == "" || dest.Mongo.Database != "webhooks" || dest.Mongo.MaxPoolSize != 20 {
		t.Fatalf("mongo config not mapped: %#v", dest.Mongo)
	}
}

func TestConvertDestinationMongoRejectsOnExistingConflict(t *testing.T) {
	raw := rawDestinationConfig{Type: "mongodb", OnExistingConflict: "reconcile"}
	raw.Mongo.URI = "mongodb://localhost:27017"
	raw.Mongo.Database = "webhooks"
	_, issues := convertDestination("destinations.archive", raw)
	if !hasIssue(issues, "destinations.archive.on_existing_conflict") {
		t.Fatalf("expected on_existing_conflict rejected for mongodb; got %#v", issues)
	}
}

func TestValidateMongoRequiresURIAndDatabase(t *testing.T) {
	c := Config{
		Destinations: map[string]DestinationConfig{
			"archive": {Type: "mongodb", Mongo: MongoConfig{URI: "", Database: "bad/name"}},
		},
	}
	issues := validationIssues(c)
	for _, f := range []string{
		"destinations.archive.mongodb.uri",
		"destinations.archive.mongodb.database",
	} {
		if !hasIssue(issues, f) {
			t.Fatalf("missing validation issue for %q; got %#v", f, issues)
		}
	}
}

func TestValidateMongoValidPasses(t *testing.T) {
	c := Config{
		Destinations: map[string]DestinationConfig{
			"archive": {Type: "mongodb", Mongo: MongoConfig{URI: "mongodb://localhost:27017", Database: "webhooks"}},
		},
	}
	for _, iss := range validationIssues(c) {
		if strings.HasPrefix(iss.Field, "destinations.archive.mongodb") {
			t.Fatalf("unexpected mongodb validation issue: %#v", iss)
		}
	}
}

func TestValidateMongoRequiresDatabase(t *testing.T) {
	c := Config{
		Destinations: map[string]DestinationConfig{
			"archive": {Type: "mongodb", Mongo: MongoConfig{URI: "mongodb://localhost:27017", Database: ""}},
		},
	}
	if issues := validationIssues(c); !hasIssue(issues, "destinations.archive.mongodb.database") {
		t.Fatalf("missing validation issue for destinations.archive.mongodb.database; got %#v", issues)
	}
}

func TestValidateMongoMTLSBothOrNeither(t *testing.T) {
	c := Config{
		Destinations: map[string]DestinationConfig{
			"archive": {Type: "mongodb", Mongo: MongoConfig{
				URI:      "mongodb://localhost:27017",
				Database: "webhooks",
				TLS:      MongoTLSConfig{ClientCert: "/path/to/cert.pem", ClientKey: ""},
			}},
		},
	}
	if issues := validationIssues(c); !hasIssue(issues, "destinations.archive.mongodb.tls.client_cert") {
		t.Fatalf("missing validation issue for destinations.archive.mongodb.tls.client_cert; got %#v", issues)
	}
}

func TestLoadParsesKafkaDestination(t *testing.T) {
	body := `
server:
  addr: ":8080"
  max_body_bytes: 1048576
destinations:
  events:
    type: kafka
    kafka:
      brokers: ["broker-1:9092", "broker-2:9092"]
      acks: "all"
      compression: "snappy"
      publish_timeout: "7s"
      sasl:
        mechanism: "scram-sha-256"
        username: "user"
        password: "pass"
      tls:
        enabled: true
        ca_cert: "/etc/hookway/ca.pem"
      retry:
        max_attempts: 5
        initial_backoff: "200ms"
        max_backoff: "3s"
`
	cfg, err := loadSharedYAML(t, body)
	if err != nil {
		t.Fatalf("load returned error: %v", err)
	}
	k := cfg.Destinations["events"].Kafka
	if len(k.Brokers) != 2 || k.Brokers[0] != "broker-1:9092" {
		t.Fatalf("bad brokers: %#v", k.Brokers)
	}
	if k.Acks != "all" || k.Compression != "snappy" {
		t.Fatalf("bad acks/compression: %q %q", k.Acks, k.Compression)
	}
	if k.PublishTimeout != 7*time.Second {
		t.Fatalf("bad publish_timeout: %v", k.PublishTimeout)
	}
	if k.SASL.Mechanism != "scram-sha-256" || k.SASL.Username != "user" || k.SASL.Password != "pass" {
		t.Fatalf("bad sasl: %#v", k.SASL)
	}
	if !k.TLS.Enabled || k.TLS.CACert != "/etc/hookway/ca.pem" {
		t.Fatalf("bad tls: %#v", k.TLS)
	}
	if k.Retry.MaxAttempts != 5 || k.Retry.InitialBackoff != 200*time.Millisecond || k.Retry.MaxBackoff != 3*time.Second {
		t.Fatalf("bad retry: %#v", k.Retry)
	}
}

func TestLoadDefaultsKafkaAcksAndCompression(t *testing.T) {
	cfg, err := loadSharedYAML(t, kafkaConfigBody("      brokers: [\"broker-1:9092\"]"))
	if err != nil {
		t.Fatalf("load returned error: %v", err)
	}
	k := cfg.Destinations["events"].Kafka
	if k.Acks != "all" {
		t.Fatalf("acks default: got %q want \"all\"", k.Acks)
	}
	if k.Compression != "none" {
		t.Fatalf("compression default: got %q want \"none\"", k.Compression)
	}
	if k.PublishTimeout != 5*time.Second {
		t.Fatalf("publish_timeout default: got %v want 5s", k.PublishTimeout)
	}
}

func TestLoadRejectsInvalidKafkaDestination(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string // substring of the field path in the error
	}{
		{"no brokers", kafkaConfigBody(`      acks: "all"`), "destinations.events.kafka.brokers"},
		{"bad acks", kafkaConfigBody("      brokers: [\"b:9092\"]\n      acks: \"maybe\""), "destinations.events.kafka.acks"},
		{"bad compression", kafkaConfigBody("      brokers: [\"b:9092\"]\n      compression: \"brotli\""), "destinations.events.kafka.compression"},
		{"sasl missing credentials", kafkaConfigBody("      brokers: [\"b:9092\"]\n      sasl:\n        mechanism: \"plain\""), "destinations.events.kafka.sasl"},
		{"bad sasl mechanism", kafkaConfigBody("      brokers: [\"b:9092\"]\n      sasl:\n        mechanism: \"kerberos\""), "destinations.events.kafka.sasl.mechanism"},
		{"half mTLS", kafkaConfigBody("      brokers: [\"b:9092\"]\n      tls:\n        enabled: true\n        client_cert: \"/c.pem\""), "destinations.events.kafka.tls.client_cert"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadSharedYAML(t, tc.body)
			if err == nil {
				t.Fatalf("expected validation error containing %q, got nil", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not mention %q", err.Error(), tc.want)
			}
		})
	}
}

func TestValidateKafkaRouteOptions(t *testing.T) {
	mk := func(destType string, opts KafkaEndpointOptions) Config {
		dest := DestinationConfig{Type: destType}
		switch destType {
		case "kafka":
			dest.Kafka = KafkaConfig{Brokers: []string{"b:9092"}, Acks: "all", Compression: "none"}
		default:
			dest.RabbitMQ = RabbitMQConfig{URL: "amqp://x", Exchange: "e", ExchangeType: "direct"}
		}
		return endpointConfigFor(
			map[string]DestinationConfig{"events": dest},
			EndpointConfig{Enabled: true, Path: "/p", Auth: AuthConfig{Type: "no_auth"},
				Destinations: []EndpointDestinationConfig{{Destination: "events", Target: "billing-events", KafkaOptions: opts}}})
	}
	cases := []struct {
		name     string
		destType string
		opts     KafkaEndpointOptions
		want     string
	}{
		{"kafka_options on non-kafka dest", "rabbitmq", KafkaEndpointOptions{PartitionKey: KafkaPartitionKeyConfig{Source: "endpoint"}}, "kafka_options"},
		{"partition_key static without value", "kafka", KafkaEndpointOptions{PartitionKey: KafkaPartitionKeyConfig{Source: "static"}}, "kafka_options.partition_key.value"},
		{"partition_key body without path", "kafka", KafkaEndpointOptions{PartitionKey: KafkaPartitionKeyConfig{Source: "body"}}, "kafka_options.partition_key.path"},
		{"partition_key bad source", "kafka", KafkaEndpointOptions{PartitionKey: KafkaPartitionKeyConfig{Source: "random"}}, "kafka_options.partition_key.source"},
		{"negative partitions", "kafka", KafkaEndpointOptions{Partitions: -1}, "kafka_options.partitions"},
		{"negative replication_factor", "kafka", KafkaEndpointOptions{ReplicationFactor: -1}, "kafka_options.replication_factor"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ConfigValidationError{Issues: validationIssues(mk(tc.destType, tc.opts))}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not mention %q", err.Error(), tc.want)
			}
		})
	}
	valid := mk("kafka", KafkaEndpointOptions{
		PartitionKey: KafkaPartitionKeyConfig{Source: "body", Path: "account.id", Fallback: "endpoint"},
		Partitions:   6, ReplicationFactor: 3,
	})
	for _, iss := range validationIssues(valid) {
		if strings.Contains(iss.Field, "kafka_options") {
			t.Fatalf("valid kafka_options rejected: %#v", iss)
		}
	}
}

// kafkaConfigBody builds a shared config with one kafka destination. destLines
// are extra indented lines under `kafka:`.
func kafkaConfigBody(destLines string) string {
	return `
server:
  addr: ":8080"
  max_body_bytes: 1048576
destinations:
  events:
    type: kafka
    kafka:
` + destLines + "\n"
}

func TestConvertDestinationWebhook(t *testing.T) {
	raw := rawDestinationConfig{Type: "webhook"}
	raw.Webhook.URL = "https://api.example.com/hooks"
	raw.Webhook.Timeout = "10s"
	raw.Webhook.MaxQueue = 500
	raw.Webhook.Retry.MaxAttempts = 3
	raw.Webhook.Retry.InitialBackoff = "200ms"
	raw.Webhook.Retry.MaxBackoff = "5s"
	raw.Webhook.Auth.Type = "hmac"
	raw.Webhook.Auth.Secret = "s3cr3t"

	dest, issues := convertDestination("destinations.notify", raw)
	if len(issues) != 0 {
		t.Fatalf("unexpected issues: %#v", issues)
	}
	if dest.Type != "webhook" || dest.Webhook.URL != "https://api.example.com/hooks" {
		t.Fatalf("webhook not mapped: %#v", dest.Webhook)
	}
	if dest.Webhook.Timeout != 10*time.Second || dest.Webhook.MaxQueue != 500 {
		t.Fatalf("scalar fields not mapped: %#v", dest.Webhook)
	}
	if dest.Webhook.Retry.MaxAttempts != 3 || dest.Webhook.Retry.InitialBackoff != 200*time.Millisecond {
		t.Fatalf("retry not mapped: %#v", dest.Webhook.Retry)
	}
	if dest.Webhook.Auth.Type != "hmac" || dest.Webhook.Auth.Secret != "s3cr3t" {
		t.Fatalf("auth not mapped: %#v", dest.Webhook.Auth)
	}
}

func TestConvertDestinationRabbitMQRetry(t *testing.T) {
	raw := rawDestinationConfig{Type: "rabbitmq"}
	raw.RabbitMQ.URL = "amqp://x"
	raw.RabbitMQ.Exchange = "e"
	raw.RabbitMQ.ExchangeType = "direct"
	raw.RabbitMQ.Retry.MaxAttempts = 5
	raw.RabbitMQ.Retry.InitialBackoff = "200ms"
	raw.RabbitMQ.Retry.MaxBackoff = "5s"

	dest, issues := convertDestination("destinations.billing", raw)
	if len(issues) != 0 {
		t.Fatalf("unexpected issues: %#v", issues)
	}
	if dest.RabbitMQ.Retry.MaxAttempts != 5 {
		t.Fatalf("max_attempts not mapped: %#v", dest.RabbitMQ.Retry)
	}
	if dest.RabbitMQ.Retry.InitialBackoff != 200*time.Millisecond {
		t.Fatalf("initial_backoff not mapped: %#v", dest.RabbitMQ.Retry)
	}
	if dest.RabbitMQ.Retry.MaxBackoff != 5*time.Second {
		t.Fatalf("max_backoff not mapped: %#v", dest.RabbitMQ.Retry)
	}
}

func TestConvertDestinationRabbitMQRetryUnsetPreservesZero(t *testing.T) {
	raw := rawDestinationConfig{Type: "rabbitmq"}
	raw.RabbitMQ.URL = "amqp://x"
	raw.RabbitMQ.Exchange = "e"
	raw.RabbitMQ.ExchangeType = "direct"

	dest, issues := convertDestination("destinations.billing", raw)
	if len(issues) != 0 {
		t.Fatalf("unexpected issues: %#v", issues)
	}
	if dest.RabbitMQ.Retry != (RetryPolicyConfig{}) {
		t.Fatalf("unset retry should stay zero so the queue layer applies its own defaults: %#v", dest.RabbitMQ.Retry)
	}
}

func TestValidateRabbitMQRetry(t *testing.T) {
	mk := func(r RetryPolicyConfig) Config {
		return Config{
			Server: ServerConfig{Addr: ":8080", MaxBodyBytes: 1024},
			Destinations: map[string]DestinationConfig{"billing": {Type: "rabbitmq", RabbitMQ: RabbitMQConfig{
				URL: "amqp://x", Exchange: "e", ExchangeType: "direct", Retry: r,
			}}},
		}
	}
	if iss := validationIssues(mk(RetryPolicyConfig{MaxAttempts: 3, InitialBackoff: 100 * time.Millisecond, MaxBackoff: time.Second})); len(iss) != 0 {
		t.Fatalf("valid retry rejected: %#v", iss)
	}
	if iss := validationIssues(mk(RetryPolicyConfig{MaxAttempts: -1})); !hasIssue(iss, "destinations.billing.rabbitmq.retry.max_attempts") {
		t.Fatalf("negative max_attempts not rejected: %#v", iss)
	}
	if iss := validationIssues(mk(RetryPolicyConfig{InitialBackoff: -time.Second})); !hasIssue(iss, "destinations.billing.rabbitmq.retry.initial_backoff") {
		t.Fatalf("negative initial_backoff not rejected: %#v", iss)
	}
	if iss := validationIssues(mk(RetryPolicyConfig{MaxBackoff: -time.Second})); !hasIssue(iss, "destinations.billing.rabbitmq.retry.max_backoff") {
		t.Fatalf("negative max_backoff not rejected: %#v", iss)
	}
	if iss := validationIssues(mk(RetryPolicyConfig{InitialBackoff: 5 * time.Second, MaxBackoff: time.Second})); !hasIssue(iss, "destinations.billing.rabbitmq.retry.initial_backoff") {
		t.Fatalf("initial > max not rejected: %#v", iss)
	}
}

func TestValidateWebhook(t *testing.T) {
	mk := func(w WebhookConfig) Config {
		return Config{
			Destinations: map[string]DestinationConfig{"notify": {Type: "webhook", Webhook: w}},
		}
	}
	// missing url + bad auth type
	iss := validationIssues(mk(WebhookConfig{Auth: WebhookAuthConfig{Type: "bogus"}}))
	for _, f := range []string{"destinations.notify.webhook.url", "destinations.notify.webhook.auth.type"} {
		if !hasIssue(iss, f) {
			t.Fatalf("missing issue %q; got %#v", f, iss)
		}
	}
	// api_key requires header+value
	iss = validationIssues(mk(WebhookConfig{URL: "https://x", Auth: WebhookAuthConfig{Type: "api_key"}}))
	if !hasIssue(iss, "destinations.notify.webhook.auth.header") || !hasIssue(iss, "destinations.notify.webhook.auth.value") {
		t.Fatalf("api_key required fields not enforced; got %#v", iss)
	}
	// hmac requires secret
	iss = validationIssues(mk(WebhookConfig{URL: "https://x", Auth: WebhookAuthConfig{Type: "hmac"}}))
	if !hasIssue(iss, "destinations.notify.webhook.auth.secret") {
		t.Fatalf("hmac secret not enforced; got %#v", iss)
	}
	// valid none passes
	iss = validationIssues(mk(WebhookConfig{URL: "https://x", Auth: WebhookAuthConfig{Type: "none"}}))
	for _, i := range iss {
		if strings.HasPrefix(i.Field, "destinations.notify.webhook") {
			t.Fatalf("unexpected webhook issue: %#v", i)
		}
	}

	// bad hmac scheme (e.g. "sha1") must produce a scheme validation issue
	iss = validationIssues(mk(WebhookConfig{URL: "https://x", Auth: WebhookAuthConfig{Type: "hmac", Secret: "s3cr3t", Scheme: "sha1"}}))
	if !hasIssue(iss, "destinations.notify.webhook.auth.scheme") {
		t.Fatalf("bad hmac scheme not rejected; got %#v", iss)
	}
}

func TestLoadParsesLoggingLevel(t *testing.T) {
	t.Setenv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/")
	for _, level := range []string{"debug", "info", "warn", "error", ""} {
		t.Run("level="+level, func(t *testing.T) {
			body := validSharedYAML()
			if level != "" {
				body += "logging:\n  level: \"" + level + "\"\n"
			}
			cfg, err := loadSharedYAML(t, body)
			if err != nil {
				t.Fatalf("load returned error: %v", err)
			}
			if cfg.Logging.Level != level {
				t.Fatalf("Logging.Level = %q, want %q", cfg.Logging.Level, level)
			}
		})
	}
}

func TestValidationRejectsInvalidLoggingLevel(t *testing.T) {
	cfg := Config{Logging: LoggingConfig{Level: "trace"}}
	if !hasIssue(validationIssues(cfg), "logging.level") {
		t.Fatalf("invalid logging.level not rejected: %#v", validationIssues(cfg))
	}
}

func TestValidationAcceptsEmptyOrValidLoggingLevel(t *testing.T) {
	for _, level := range []string{"", "debug", "info", "warn", "error"} {
		cfg := Config{Logging: LoggingConfig{Level: level}}
		if hasIssue(validationIssues(cfg), "logging.level") {
			t.Fatalf("valid logging.level %q rejected: %#v", level, validationIssues(cfg))
		}
	}
}

func TestValidationRejectsInvalidLoggingHeaderAllowlist(t *testing.T) {
	cfg := Config{Logging: LoggingConfig{HeaderAllowlist: []string{"ok", ""}}}
	if !hasIssue(validationIssues(cfg), "logging.header_allowlist[1]") {
		t.Fatalf("empty logging.header_allowlist entry not rejected: %#v", validationIssues(cfg))
	}
}

func TestValidationRejectsInvalidForwardedHeaderAllowlist(t *testing.T) {
	cfg := Config{Security: SecurityConfig{ForwardedHeaderAllowlist: []string{"X-Request-ID", "bad header"}}}
	if !hasIssue(validationIssues(cfg), "security.forwarded_header_allowlist[1]") {
		t.Fatalf("invalid forwarded_header_allowlist entry not rejected: %#v", validationIssues(cfg))
	}
}

func TestLoadParsesLoggingHeaderAllowlist(t *testing.T) {
	t.Setenv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/")
	body := validSharedYAML() + "logging:\n  header_allowlist:\n    - Content-Type\n    - X-Request-ID\n"
	cfg, err := loadSharedYAML(t, body)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Logging.HeaderAllowlist) != 2 || cfg.Logging.HeaderAllowlist[0] != "Content-Type" {
		t.Fatalf("HeaderAllowlist = %#v", cfg.Logging.HeaderAllowlist)
	}
}

func TestExpandEnvLenientExpandsKnownAndReportsMissing(t *testing.T) {
	t.Setenv("KNOWN_VAR", "known-value")
	_ = os.Unsetenv("DEFINITELY_UNSET_VAR")

	expanded, missing := ExpandEnvLenient("known=${KNOWN_VAR} missing=${DEFINITELY_UNSET_VAR}")

	if !strings.Contains(expanded, "known=known-value") {
		t.Fatalf("expanded = %q, want known var substituted", expanded)
	}
	if !strings.Contains(expanded, "missing=${DEFINITELY_UNSET_VAR}") {
		t.Fatalf("expanded = %q, want missing var left as opaque literal (not emptied)", expanded)
	}
	if len(missing) != 1 || missing[0] != "DEFINITELY_UNSET_VAR" {
		t.Fatalf("missing = %#v, want [DEFINITELY_UNSET_VAR]", missing)
	}
}

func TestExpandEnvLenientNoMissingReturnsEmptySlice(t *testing.T) {
	t.Setenv("KNOWN_VAR", "known-value")
	_, missing := ExpandEnvLenient("known=${KNOWN_VAR}")
	if len(missing) != 0 {
		t.Fatalf("missing = %#v, want none", missing)
	}
}
