package config

import (
	"strings"
	"testing"
	"time"
)

func TestDecodeSharedYAMLUsesDiskShapeAndDefaults(t *testing.T) {
	const src = `
server:
  addr: ":8081"
  read_timeout: 2s
  max_body_bytes: 2048
  max_inflight_requests: 0
security:
  api_key: key
  trusted_proxy_headers: true
  trusted_proxies: [10.0.0.0/8]
  request_id_header: X-Request-ID
  redacted_header_denylist: [Authorization]
  forwarded_header_allowlist: [Content-Type]
  loop_protection: {enabled: false, max_hops: 7}
metrics:
  enabled: true
  path: /metrics
  addr: ":9090"
  runtime_metrics: false
logging:
  level: debug
destinations:
  outgoing:
    type: webhook
    webhook:
      url: https://example.test/hooks
flows: {}
`
	shared, issues := DecodeSharedYAML([]byte(src), "test", "flows")
	if len(issues) != 0 {
		t.Fatalf("DecodeSharedYAML issues = %#v", issues)
	}
	if shared.Server.Addr != ":8081" || shared.Server.ReadTimeout != 2*time.Second {
		t.Fatalf("server = %#v", shared.Server)
	}
	if shared.Server.WriteTimeout != 10*time.Second || shared.Server.IdleTimeout != time.Minute {
		t.Fatalf("server duration defaults = %#v", shared.Server)
	}
	if shared.Server.MaxInflightRequests != 256 {
		t.Fatalf("max inflight = %d, want default 256", shared.Server.MaxInflightRequests)
	}
	if !shared.Security.TrustedProxyHeaders || shared.Security.RequestIDHeader != "X-Request-ID" || shared.Security.LoopMaxHops() != 7 {
		t.Fatalf("security = %#v", shared.Security)
	}
	if shared.Security.LoopProtectionEnabled() {
		t.Fatal("loop protection enabled, want explicit false")
	}
	if !shared.Metrics.Enabled || shared.Metrics.Path != "/metrics" || shared.Metrics.RuntimeMetricsEnabled() {
		t.Fatalf("metrics = %#v", shared.Metrics)
	}
	if shared.Logging.Level != "debug" {
		t.Fatalf("logging = %#v", shared.Logging)
	}
	webhook := shared.Destinations["outgoing"].Webhook
	if webhook.URL != "https://example.test/hooks" || webhook.Timeout != 10*time.Second {
		t.Fatalf("webhook = %#v", webhook)
	}
}

func TestDecodeSharedYAMLReportsDurationConversionIssues(t *testing.T) {
	const src = `
server:
  addr: ":8080"
  read_timeout: soon
  max_body_bytes: 1024
destinations:
  outgoing:
    type: webhook
    webhook:
      url: https://example.test/hooks
      timeout: later
`
	_, issues := DecodeSharedYAML([]byte(src), "test")
	joined := sharedIssuesText(issues)
	for _, field := range []string{"server.read_timeout", "destinations.outgoing.webhook.timeout"} {
		if !strings.Contains(joined, field) {
			t.Errorf("issues %q do not include %s", joined, field)
		}
	}
}

func TestDecodeSharedYAMLRejectsUnknownFieldsAndHonorsAllowedExtras(t *testing.T) {
	tests := []struct {
		name  string
		src   string
		field string
	}{
		{"top level", "server: {addr: ':8080'}\nflows: {}\ntyop: true\n", "tyop"},
		{"nested server", "server: {addr: ':8080', max_body_bytes: 1, tyop: true}\nflows: {}\n", "server.tyop"},
		{"nested destination", "server: {addr: ':8080', max_body_bytes: 1}\ndestinations: {outgoing: {type: webhook, webhook: {url: https://example.test, tyop: true}}}\nflows: {}\n", "destinations.outgoing.webhook.tyop"},
		{"global rate limit is not shared", "server: {addr: ':8080'}\nflows: {}\nrate_limit: {enabled: true}\n", "rate_limit"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, issues := DecodeSharedYAML([]byte(tc.src), "candidate", "flows")
			if !hasIssueField(issues, tc.field) {
				t.Fatalf("issues = %q, want field %s", sharedIssuesText(issues), tc.field)
			}
			for _, issue := range issues {
				if issue.Severity != SeverityError {
					t.Errorf("%s severity = %q, want %q", issue.Field, issue.Severity, SeverityError)
				}
			}
		})
	}

	_, issues := DecodeSharedYAML([]byte("server: {addr: ':8080'}\nflows: {anything: true}\n"), "candidate", "flows")
	if len(issues) != 0 {
		t.Fatalf("allowed extra was decoded instead of ignored: %#v", issues)
	}
}

func TestDecodeSharedYAMLAggregatesUnknownDurationAndSemanticIssues(t *testing.T) {
	const src = `
server:
  addr: ":8080"
  tyop: true
  read_timeout: soon
  max_body_bytes: 0
destinations:
  outgoing:
    type: webhook
    webhook:
      tyop: true
      url: ftp://example.test
flows: {}
`
	shared, issues := DecodeSharedYAML([]byte(src), "candidate", "flows")
	issues = append(issues, shared.ValidationIssues()...)
	for _, field := range []string{
		"server.tyop",
		"destinations.outgoing.webhook.tyop",
		"server.read_timeout",
		"server.max_body_bytes",
		"destinations.outgoing.webhook.url",
	} {
		if !hasIssueField(issues, field) {
			t.Errorf("issues %q do not include %s", sharedIssuesText(issues), field)
		}
	}
	for _, issue := range issues {
		if issue.Severity != SeverityError {
			t.Errorf("%s severity = %q, want %q", issue.Field, issue.Severity, SeverityError)
		}
	}
}

func TestSharedValidationIssuesSetExplicitErrorSeverity(t *testing.T) {
	issues := SharedConfig{}.ValidationIssues()
	if len(issues) == 0 {
		t.Fatal("expected shared validation issues")
	}
	for _, issue := range issues {
		if issue.Severity != SeverityError {
			t.Fatalf("%s severity = %q, want explicit %q", issue.Field, issue.Severity, SeverityError)
		}
	}
}

func TestSharedValidationAllowsNoDestinationsAndValidatesConfiguredOnes(t *testing.T) {
	shared := SharedConfig{Server: ServerConfig{
		Addr: ":8080", ReadTimeout: time.Second, WriteTimeout: 10 * time.Second,
		IdleTimeout: time.Minute, ShutdownTimeout: 10 * time.Second,
		PublishTimeout: 10 * time.Second, MaxBodyBytes: 1024,
		MaxInflightRequests: 256, ReadinessInterval: 10 * time.Second,
		ReadinessTimeout: 5 * time.Second,
	}}
	for _, issue := range shared.ValidationIssues() {
		if issue.Field == "destinations" || strings.HasPrefix(issue.Field, "endpoints") {
			t.Fatalf("respond-only shared config was rejected: %#v", issue)
		}
	}

	shared.Destinations = map[string]DestinationConfig{
		"bad": {Type: "webhook", Webhook: WebhookConfig{URL: "ftp://example.test"}},
	}
	if !strings.Contains(sharedIssuesText(shared.ValidationIssues()), "destinations.bad.webhook.url") {
		t.Fatalf("configured destination was not validated: %#v", shared.ValidationIssues())
	}
}

func TestValidationIssueEffectiveSeverityDefaultsToError(t *testing.T) {
	if got := (ValidationIssue{}).EffectiveSeverity(); got != SeverityError {
		t.Fatalf("empty severity = %q, want %q", got, SeverityError)
	}
	if got := (ValidationIssue{Severity: SeverityWarning}).EffectiveSeverity(); got != SeverityWarning {
		t.Fatalf("warning severity = %q", got)
	}
}

func sharedIssuesText(issues []ValidationIssue) string {
	var b strings.Builder
	for _, issue := range issues {
		b.WriteString(issue.Field)
		b.WriteString(": ")
		b.WriteString(issue.Message)
		b.WriteByte('\n')
	}
	return b.String()
}

func hasIssueField(issues []ValidationIssue, field string) bool {
	for _, issue := range issues {
		if issue.Field == field {
			return true
		}
	}
	return false
}

// The embedded replay store's block is gone; its fields are simply unknown.
func TestDecodeSharedYAMLRejectsTheOldReplayShape(t *testing.T) {
	src := `
server: {addr: ":8080"}
replay:
  enabled: true
flows: {}
`
	_, issues := DecodeSharedYAML([]byte(src), "test", "flows")
	for _, issue := range issues {
		if issue.Severity == SeverityError && issue.Field == "replay.enabled" {
			return
		}
	}
	t.Fatalf("issues = %#v, want an unknown-field error for replay.enabled", issues)
}
