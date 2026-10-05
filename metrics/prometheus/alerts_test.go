package prometheus

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type prometheusRules struct {
	Groups []struct {
		Name  string `yaml:"name"`
		Rules []struct {
			Alert       string            `yaml:"alert"`
			Expr        string            `yaml:"expr"`
			For         string            `yaml:"for"`
			Labels      map[string]string `yaml:"labels"`
			Annotations map[string]string `yaml:"annotations"`
		} `yaml:"rules"`
	} `yaml:"groups"`
}

func TestAlertsArePrometheusRulesWithRequiredExprs(t *testing.T) {
	raw, err := os.ReadFile("alerts.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var rule prometheusRules
	decoder := yaml.NewDecoder(strings.NewReader(string(raw)))
	decoder.KnownFields(true)
	if err := decoder.Decode(&rule); err != nil {
		t.Fatal(err)
	}
	if len(rule.Groups) != 2 {
		t.Fatalf("groups = %d, want runtime and replay groups", len(rule.Groups))
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
	for _, g := range rule.Groups {
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

// Every alert expression must reference only declared hookway_* metrics.
func TestAlertsQueryOnlyDeclaredMetrics(t *testing.T) {
	var src []byte
	for _, f := range []string{"../../internal/metrics/metrics.go", "../../internal/metrics/protection.go", "../../internal/replay/metrics/metrics.go"} {
		raw, err := os.ReadFile(filepath.Clean(f))
		if err != nil {
			t.Fatal(err)
		}
		src = append(src, raw...)
	}
	declared := map[string]bool{}
	for _, m := range regexp.MustCompile(`"(hookway_[a-z0-9_]+)"`).FindAllStringSubmatch(string(src), -1) {
		declared[m[1]] = true
	}
	raw, err := os.ReadFile("alerts.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var rule prometheusRules
	if err := yaml.Unmarshal(raw, &rule); err != nil {
		t.Fatal(err)
	}
	name := regexp.MustCompile(`hookway_[a-z0-9_]+`)
	for _, group := range rule.Groups {
		for _, r := range group.Rules {
			for _, used := range name.FindAllString(r.Expr, -1) {
				base := used
				for _, suffix := range []string{"_bucket", "_count", "_sum"} {
					if trimmed := strings.TrimSuffix(used, suffix); trimmed != used && declared[trimmed] {
						base = trimmed
					}
				}
				if !declared[base] {
					t.Errorf("alert %s queries %s, which neither metrics source declares", r.Alert, used)
				}
			}
		}
	}
}

func TestReplayAlerts(t *testing.T) {
	raw, err := os.ReadFile("alerts.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var rule prometheusRules
	if err := yaml.Unmarshal(raw, &rule); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"HookwayCaptureDrops":             "hookway_capture_dropped_total",
		"HookwayReplayUsageHigh":          "hookway_replay_capacity_bytes",
		"HookwayReplayCapacityRejections": `hookway_replay_captures_total{outcome="capacity"}`,
		"HookwayReplayQueuedRunStale":     "hookway_replay_oldest_queued_run_seconds",
		"HookwayReplayUnknownRecords":     `hookway_replay_records{status="unknown"}`,
		"HookwayReplayRunFailureRatio":    `hookway_replay_runs_total{status="failed"}`,
		"HookwayReplayDatabaseDown":       "hookway_replay_db_up",
		"HookwayReplayGCStalled":          "hookway_replay_gc_last_success_timestamp_seconds",
	}
	found := map[string]string{}
	for _, g := range rule.Groups {
		for _, r := range g.Rules {
			found[r.Alert] = r.Expr
		}
	}
	// Every replica samples the shared database, so summing those gauges
	// multiplies them by the replica count.
	sampled := regexp.MustCompile(`sum\s*(by\s*\([^)]*\)\s*)?\(\s*hookway_replay_(records|runs|usage_|capacity_|oldest_queued|db_up|schema_version)`)
	for alert, needle := range want {
		expr, ok := found[alert]
		if !ok {
			t.Errorf("missing alert %s", alert)
			continue
		}
		if !strings.Contains(expr, needle) {
			t.Errorf("alert %s expr %q does not contain %q", alert, expr, needle)
		}
		if sampled.MatchString(expr) {
			t.Errorf("alert %s sums a sampled replay gauge across replicas: %s", alert, expr)
		}
	}
	if !strings.Contains(found["HookwayReplayUsageHigh"], "hookway_replay_capacity_records") {
		t.Error("the usage alert must watch the record limit as well as bytes")
	}
	if !strings.Contains(found["HookwayReplayRunFailureRatio"], ">= 10") {
		t.Errorf("the failure ratio must require >= 10 finished runs: %s", found["HookwayReplayRunFailureRatio"])
	}
}
