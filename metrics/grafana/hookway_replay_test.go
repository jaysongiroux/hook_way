package dashboards

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func loadNamed(t *testing.T, file string) dashboard {
	t.Helper()
	raw, err := os.ReadFile(filepath.Clean(file))
	if err != nil {
		t.Fatal(err)
	}
	var d dashboard
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestReplayDashboard(t *testing.T) {
	d := loadNamed(t, "hookway-replay.json")
	want := map[string]string{
		"State sample age":                 "hookway_replay_state_last_success_timestamp_seconds",
		"Configured automatic GC interval": "hookway_replay_gc_interval_seconds",
		"Store failures by cause":          "hookway_replay_store_failures_total",
		"Suppressed diagnostic records":    "hookway_replay_logs_suppressed_total",
		"Records by kind and state":        "hookway_replay_records",
		"Usage of retention limits":        "hookway_replay_capacity_bytes",
		"Ingest outcomes":                  "hookway_replay_captures_total",
		"Runs queued and running":          "hookway_replay_runs",
		"Oldest queued run":                "hookway_replay_oldest_queued_run_seconds",
		"Runs finished":                    "hookway_replay_runs_total",
		"Executor calls":                   "hookway_replay_executor_calls_total",
		"Workers busy":                     "hookway_replay_workers_busy",
		"Leases expired, late results":     "hookway_replay_leases_expired_total",
		"GC deletions":                     "hookway_replay_gc_deleted_records_total",
		"Since last successful GC":         "hookway_replay_gc_last_success_timestamp_seconds",
		"Database":                         "hookway_replay_db_up",
		"API requests":                     "hookway_replay_http_requests_total",
		"Auth failures":                    "hookway_replay_auth_failures_total",
		"Payload downloads":                "hookway_replay_payload_downloads_total",
	}
	src, err := os.ReadFile("../../internal/replay/metrics/metrics.go")
	if err != nil {
		t.Fatal(err)
	}
	declared := map[string]bool{}
	for _, m := range regexp.MustCompile(`"(hookway_replay_[a-z0-9_]+)"`).FindAllStringSubmatch(string(src), -1) {
		declared[m[1]] = true
	}
	name := regexp.MustCompile(`hookway_[a-z0-9_]+`)
	sampled := regexp.MustCompile(`sum\s*(by\s*\([^)]*\)\s*)?\(\s*hookway_replay_(records|runs\{|runs\)|usage_|capacity_|oldest_queued|db_up|schema_version)`)
	found := map[string]bool{}
	for _, p := range d.Panels {
		joined := ""
		for _, target := range p.Targets {
			joined += target.Expr + "\n"
			for _, used := range name.FindAllString(target.Expr, -1) {
				base := used
				for _, suffix := range []string{"_bucket", "_count", "_sum"} {
					if trimmed := strings.TrimSuffix(used, suffix); trimmed != used && declared[trimmed] {
						base = trimmed
					}
				}
				if !declared[base] {
					t.Errorf("panel %q queries %s, which internal/replay/metrics/metrics.go does not declare", p.Title, used)
				}
			}
			if sampled.MatchString(target.Expr) {
				t.Errorf("panel %q sums a sampled gauge across replicas (use max): %s", p.Title, target.Expr)
			}
		}
		if p.Title == "Records by kind and state" && !strings.Contains(joined, "max by (job, kind, delivery_status, dismissed)") {
			t.Error("record gauge must preserve kind, delivery state and dismissal across replicas")
		}
		if (p.Title == "Database" || p.Title == "State sample age" || p.Title == "Since last successful GC") && (strings.Contains(joined, "max(") || strings.Contains(joined, "min(") || strings.Contains(joined, "max by") || strings.Contains(joined, "min by")) {
			t.Errorf("panel %q hides the affected replica: %s", p.Title, joined)
		}
		if needle, ok := want[p.Title]; ok {
			found[p.Title] = true
			if !strings.Contains(joined, needle) {
				t.Errorf("panel %q does not query %s: %s", p.Title, needle, joined)
			}
		}
	}
	for title := range want {
		if !found[title] {
			t.Errorf("missing panel %q", title)
		}
	}
	vars := map[string]bool{}
	for _, v := range d.Templating.List {
		vars[v.Name] = true
	}
	if !vars["datasource"] || !vars["job"] {
		t.Errorf("variables = %v, want datasource and job", vars)
	}
}
