package config

import (
	"strings"
	"testing"
)

func TestWebhookPrivateTargetExceptionRequiresTemplatedAuthority(t *testing.T) {
	for _, tc := range []struct {
		url          string
		allow, valid bool
	}{
		{"http://{tenant}-api.example.test/events", true, true},
		{"http://127.0.0.1/events", false, true},
		{"http://localhost/{tenant}/events", false, true},
		{"http://127.0.0.1/events", true, false},
		{"http://localhost/{tenant}/events", true, false},
		{"http://${WEBHOOK_HOST}/{route}", false, true},
		{"http://${WEBHOOK_HOST}/{route}", true, false},
	} {
		doc := "server: {addr: ':8080', max_body_bytes: 1024}\ndestinations:\n  notify:\n    type: webhook\n    webhook:\n      url: '" + tc.url + "'\n      auth: {type: none}\n"
		if tc.allow {
			doc += "      allow_private_targets: true\n"
		}
		cfg, issues := DecodeSharedYAML([]byte(doc), "test")
		issues = append(issues, cfg.ValidationIssues()...)
		if (len(issues) == 0) != tc.valid {
			t.Fatalf("url=%s, allow=%v, issues=%#v", tc.url, tc.allow, issues)
		}
		if cfg.Destinations["notify"].Webhook.AllowPrivateTargets != tc.allow {
			t.Fatal("authored private-target permission was lost during conversion")
		}
		if !tc.valid && !strings.Contains(ConfigValidationError{Issues: issues}.Error(), "webhook.allow_private_targets") {
			t.Fatal("missing corrective exception field")
		}
	}
}
