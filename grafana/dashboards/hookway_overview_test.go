package dashboards

import (
	"encoding/json"
	"os"
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

