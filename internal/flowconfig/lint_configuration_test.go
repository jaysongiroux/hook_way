package flowconfig

import (
	"strings"
	"testing"

	"github.com/jaysongiroux/hook_way/internal/config"
)

func TestConfigurationPolicyWarnings(t *testing.T) {
	for _, tc := range []struct {
		name, before, after, field string
	}{
		{"unlimited admission", "max_body_bytes: 1024}", "max_body_bytes: 1024, max_inflight_requests: -1}", "server.max_inflight_requests"},
		{"all IPv4 proxies", "destinations:", "security: {trusted_proxies: [0.0.0.0/0]}\ndestinations:", "security.trusted_proxies[0]"},
		{"all IPv6 proxies", "destinations:", "security: {trusted_proxies: ['::/0']}\ndestinations:", "security.trusted_proxies[0]"},
		{"any peer proxy trust", "destinations:", "security: {trusted_proxy_headers: true}\ndestinations:", "security.trusted_proxies"},
		{"sample auth", "secret: s,", "secret: change-me,", "flows.demo.nodes[3].secret"},
		{"sample outbound auth", "delivery_mode: sync", "delivery_mode: sync, auth: {type: hmac, secret: change-me}", "destinations.primary.webhook.auth.secret"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := strings.Replace(cleanLintYAML, tc.before, tc.after, 1)
			issues := ValidateBytes([]byte(src))
			if len(issues) != 1 || issues[0].Field != tc.field || issues[0].EffectiveSeverity() != config.SeverityWarning || issues[0].Suggestion == "" {
				t.Fatalf("issues = %#v, want one actionable warning at %s", issues, tc.field)
			}
		})
	}
}

func TestConfigurationPolicyPreservesLocalAndAuthoringChoices(t *testing.T) {
	for _, value := range []string{"s", "provider-issued", "${WEBHOOK_SECRET}", "$WEBHOOK_SECRET"} {
		src := strings.Replace(cleanLintYAML, "secret: s,", "secret: '"+value+"',", 1)
		src = strings.Replace(src, "https://example.test", "http://localhost:8081", 1)
		src = strings.Replace(src, "destinations:", "security: {trusted_proxies: ['127.0.0.1', '10.0.0.0/8', '::1/128']}\ndestinations:", 1)
		if issues := ValidateBytes([]byte(src)); len(issues) != 0 {
			t.Fatalf("valid local configuration with secret %q rejected: %#v", value, issues)
		}
	}
}

func TestSampleReplayCredentialWarningsDoNotEchoValues(t *testing.T) {
	cfg := mustLoad(t, cleanLintYAML)
	cfg.Replay.Configured = true
	cfg.Replay.CaptureToken = "dev-capture-token-0123456789abcdef"
	cfg.Replay.Control.Token = "dev-control-token-0123456789abcdef"
	issues := configurationPolicyIssues(cfg)
	if len(issues) != 2 || issues[0].Field != "replay.capture_token" || issues[1].Field != "replay.control.token" {
		t.Fatalf("issues = %#v", issues)
	}
	for _, issue := range issues {
		if issue.EffectiveSeverity() != config.SeverityWarning || strings.Contains(issue.Message+issue.Suggestion, cfg.Replay.CaptureToken) || strings.Contains(issue.Message+issue.Suggestion, cfg.Replay.Control.Token) {
			t.Fatalf("unexpected severity or leaked sample credential: %#v", issue)
		}
	}
}
