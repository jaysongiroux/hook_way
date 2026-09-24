package dashboards

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"
)

type dashboard struct {
	Panels []struct {
		Title       string `json:"title"`
		FieldConfig struct {
			Defaults struct {
				Min  *float64 `json:"min"`
				Unit string   `json:"unit"`
			} `json:"defaults"`
		} `json:"fieldConfig"`
		Targets []struct {
			Expr string `json:"expr"`
		} `json:"targets"`
	} `json:"panels"`
	Templating struct {
		List []struct {
			Name       string `json:"name"`
			Definition string `json:"definition"`
		} `json:"list"`
	} `json:"templating"`
}

func loadDashboard(t *testing.T) dashboard {
	t.Helper()
	raw, err := os.ReadFile("hookway-overview.json")
	if err != nil {
		t.Fatal(err)
	}
	var got dashboard
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestSparseTrafficPanelsShowIntervalCounts(t *testing.T) {
	d := loadDashboard(t)
	want := map[string]bool{
		"HTTP Requests":      false,
		"Messages Published": false,
		"Publish Attempts":   false,
	}
	for _, panel := range d.Panels {
		if _, ok := want[panel.Title]; !ok {
			continue
		}
		want[panel.Title] = true
		if panel.FieldConfig.Defaults.Min == nil || *panel.FieldConfig.Defaults.Min != 0 {
			t.Errorf("panel %q minimum must be 0", panel.Title)
		}
		if panel.FieldConfig.Defaults.Unit != "short" {
			t.Errorf("panel %q unit = %q, want short", panel.Title, panel.FieldConfig.Defaults.Unit)
		}
		for _, target := range panel.Targets {
			if !strings.Contains(target.Expr, "increase(") {
				t.Errorf("panel %q query must use increase for visible sparse counts: %s", panel.Title, target.Expr)
			}
		}
	}
	for title, found := range want {
		if !found {
			t.Errorf("missing panel %q", title)
		}
	}
}

func TestRouteFiltersUseTopologyMetrics(t *testing.T) {
	d := loadDashboard(t)
	for _, variable := range d.Templating.List {
		if variable.Name != "destination" && variable.Name != "queue" {
			continue
		}
		if !strings.Contains(variable.Definition, "hookway_endpoint_info") {
			t.Errorf("variable %q must use per-route endpoint topology: %s", variable.Name, variable.Definition)
		}
	}
}

func TestRequestLevelPanelsDoNotUseRouteFilters(t *testing.T) {
	d := loadDashboard(t)
	want := map[string]bool{"HTTP Requests": false, "Request Duration": false}
	for _, panel := range d.Panels {
		if _, ok := want[panel.Title]; !ok {
			continue
		}
		want[panel.Title] = true
		for _, target := range panel.Targets {
			if strings.Contains(target.Expr, "destination_queue_id=~") || strings.Contains(target.Expr, "queue=~") {
				t.Errorf("request-level panel %q must not filter by fan-out route labels: %s", panel.Title, target.Expr)
			}
		}
	}
	for title, found := range want {
		if !found {
			t.Errorf("missing panel %q", title)
		}
	}
}

func TestNodeDurationPanelUsesBoundedNodeMetric(t *testing.T) {
	d := loadDashboard(t)
	found := false
	for _, panel := range d.Panels {
		if panel.Title != "Node Duration" {
			continue
		}
		found = true
		if panel.FieldConfig.Defaults.Unit != "s" {
			t.Errorf("Node Duration unit = %q, want s", panel.FieldConfig.Defaults.Unit)
		}
		for _, target := range panel.Targets {
			if !strings.Contains(target.Expr, "hookway_node_duration_seconds") {
				t.Errorf("Node Duration query must use hookway_node_duration_seconds: %s", target.Expr)
			}
			if strings.Contains(target.Expr, "queue=") || strings.Contains(target.Expr, "target=") {
				t.Errorf("Node Duration must not label by data-dependent queue/target: %s", target.Expr)
			}
		}
	}
	if !found {
		t.Fatal("missing panel \"Node Duration\"")
	}
}

func TestDashboardHasRequiredPanelsAndVariables(t *testing.T) {
	d := loadDashboard(t)
	wantPanels := []string{
		"In-flight Requests",
		"Goroutines",
		"Heap Alloc",
		"RabbitMQ Reconnects",
		"HTTP Requests",
		"Auth Failures",
		"Messages Published",
		"Request Duration",
		"Pre-publish Rejections",
		"Filtered Deliveries",
		"Flows",
		"Destinations",
		"Publishing",
		"Publish Duration",
		"Publish Attempts",
		"Webhook Deliveries by Outcome",
		"Webhook Retries",
		"Webhook Dropped",
		"CPU Usage",
		"Memory",
		"Webhook Dead-Letters",
		"Webhook Dead-Letter Failures (event lost)",
		"Webhook Queue Depth vs Capacity",
		"RabbitMQ Unroutable (lost)",
		"RabbitMQ DLX Publishes",
		"Redis Pub/Sub Dropped (no subscriber)",
		"Panics by Location",
		"Destination Readiness",
		"Build Info",
		"Node Duration",
		"Node Errors",
		"Node Error Ratio",
		"Lookup Outcomes",
		"Lookup Latency",
		"Compensation Failures",
		"Secret Refresh Failures",
	}
	have := map[string]bool{}
	for _, panel := range d.Panels {
		have[panel.Title] = true
	}
	for _, title := range wantPanels {
		if !have[title] {
			t.Errorf("missing panel %q", title)
		}
	}
	if have["RabbitMQ Publishing"] {
		t.Error("row title still says RabbitMQ Publishing; use generic Publishing")
	}

	wantVars := []string{"datasource", "job", "instance", "endpoint", "destination", "queue"}
	haveVar := map[string]bool{}
	for _, v := range d.Templating.List {
		haveVar[v.Name] = true
	}
	for _, name := range wantVars {
		if !haveVar[name] {
			t.Errorf("missing template variable %q", name)
		}
	}
}

func TestNewReliabilityPanelsUseBoundedMetrics(t *testing.T) {
	d := loadDashboard(t)
	wantExpr := map[string]string{
		"Node Errors":             "hookway_node_total",
		"Node Error Ratio":        "hookway_node_total",
		"Lookup Outcomes":         `node_type="lookup"`,
		"Lookup Latency":          "hookway_node_duration_seconds",
		"Compensation Failures":   "hookway_compensation_failed_total",
		"Secret Refresh Failures": "hookway_secret_refresh_failures_total",
	}
	found := map[string]bool{}
	for _, panel := range d.Panels {
		needle, ok := wantExpr[panel.Title]
		if !ok {
			continue
		}
		found[panel.Title] = true
		joined := ""
		for _, target := range panel.Targets {
			joined += target.Expr
			if strings.Contains(target.Expr, "queue=") || strings.Contains(target.Expr, "target=") {
				t.Errorf("panel %q must not label by data-dependent queue/target: %s", panel.Title, target.Expr)
			}
		}
		if !strings.Contains(joined, needle) {
			t.Errorf("panel %q missing %q in expr %s", panel.Title, needle, joined)
		}
	}
	for title := range wantExpr {
		if !found[title] {
			t.Errorf("missing panel %q", title)
		}
	}
}

// Every hookway_* series a panel or variable queries must be declared in
// internal/metrics/metrics.go, or the panel silently shows "No data" after a
// metric is removed or renamed.
func TestDashboardQueriesOnlyDeclaredMetrics(t *testing.T) {
	src, err := os.ReadFile("../../internal/metrics/metrics.go")
	if err != nil {
		t.Fatal(err)
	}
	declared := map[string]bool{}
	for _, m := range regexp.MustCompile(`"(hookway_[a-z0-9_]+)"`).FindAllStringSubmatch(string(src), -1) {
		declared[m[1]] = true
	}
	name := regexp.MustCompile(`hookway_[a-z0-9_]+`)
	check := func(where, expr string) {
		for _, used := range name.FindAllString(expr, -1) {
			base := used
			for _, suffix := range []string{"_bucket", "_count", "_sum"} {
				if trimmed := strings.TrimSuffix(used, suffix); trimmed != used && declared[trimmed] {
					base = trimmed
				}
			}
			if !declared[base] {
				t.Errorf("%s queries %s, which internal/metrics/metrics.go does not declare", where, used)
			}
		}
	}
	d := loadDashboard(t)
	for _, panel := range d.Panels {
		for _, target := range panel.Targets {
			check("panel "+panel.Title, target.Expr)
		}
	}
	for _, v := range d.Templating.List {
		check("variable "+v.Name, v.Definition)
	}
}

func TestCaptureAndRedrivePanels(t *testing.T) {
	d := loadDashboard(t)
	want := map[string]string{
		"Captured failures":  "hookway_capture_total",
		"Capture drops":      "hookway_capture_dropped_total",
		"Capture buffer":     "hookway_capture_buffer_bytes",
		"Redrives":           "hookway_control_runs_total",
		"Redrives in flight": "hookway_control_inflight",
	}
	for _, p := range d.Panels {
		needle, ok := want[p.Title]
		if !ok {
			continue
		}
		joined := ""
		for _, target := range p.Targets {
			joined += target.Expr
		}
		if !strings.Contains(joined, needle) {
			t.Errorf("panel %q does not query %s", p.Title, needle)
		}
		delete(want, p.Title)
	}
	for title := range want {
		t.Errorf("missing panel %q", title)
	}
}
