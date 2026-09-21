package deploy

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type prometheusRule struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Spec       struct {
		Groups []struct {
			Name  string `yaml:"name"`
			Rules []struct {
				Alert string `yaml:"alert"`
				Expr  string `yaml:"expr"`
				For   string `yaml:"for"`
			} `yaml:"rules"`
		} `yaml:"groups"`
	} `yaml:"spec"`
}

func TestAlertsArePrometheusRuleWithRequiredExprs(t *testing.T) {
	raw, err := os.ReadFile("alerts.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var rule prometheusRule
	if err := yaml.Unmarshal(raw, &rule); err != nil {
		t.Fatal(err)
	}
	if rule.Kind != "PrometheusRule" {
		t.Fatalf("kind = %q, want PrometheusRule", rule.Kind)
	}
	want := map[string]string{
		"HookwayPanics":                    "hookway_panics_total",
		"HookwayCompensationFailures":      "hookway_compensation_failed_total",
		"HookwaySecretRefreshFailures":     "hookway_secret_refresh_failures_total",
		"HookwayWebhookDrops":              "hookway_webhook_dropped_total",
		"HookwayWebhookDeadLetterFailures": "hookway_webhook_dead_letter_failures_total",
		"HookwayDestinationNotReady":       "hookway_destination_ready == 0",
		"HookwayPublishErrorRatio":         `hookway_publish_total{status="error"}`,
	}
	wantFor := map[string]string{
		"HookwayDestinationNotReady": "5m",
		"HookwayPublishErrorRatio":   "10m",
	}
	found := map[string]string{}
	for _, g := range rule.Spec.Groups {
		for _, r := range g.Rules {
			found[r.Alert] = r.Expr
			if wantDur, ok := wantFor[r.Alert]; ok && r.For != wantDur {
				t.Errorf("alert %s for = %q, want %q", r.Alert, r.For, wantDur)
			}
		}
	}
	for alert, needle := range want {
		expr, ok := found[alert]
		if !ok {
			t.Errorf("missing alert %s", alert)
			continue
		}
		if !strings.Contains(expr, needle) {
			t.Errorf("alert %s expr %q does not contain %q", alert, expr, needle)
		}
	}
	if !strings.Contains(found["HookwayPublishErrorRatio"], ">= 20") {
		t.Errorf("publish error ratio must require >= 20 attempts: %s", found["HookwayPublishErrorRatio"])
	}
}
