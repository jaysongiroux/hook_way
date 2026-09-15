package metrics

import (
	"fmt"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// TestNodeMetricCardinalityIsBoundedByConfig guards scalability rule R3.
// The lookup node exists to make targets data-dependent, so a rendered
// target as a label would give a 5,000-environment routing table 5,000
// label values per metric. Only config-derived values may be labels.
func TestNodeMetricCardinalityIsBoundedByConfig(t *testing.T) {
	m := New()
	for i := 0; i < 100; i++ {
		renderedTarget := fmt.Sprintf("env-%d-applicationServiceQueue", i)
		_ = renderedTarget // belongs in logs and span attributes, never labels
		m.Node("sf-router", "lookup", "lookup", "ok", time.Millisecond)
		m.Node("sf-router", "out", "publish", "ok", 2*time.Millisecond)
	}

	families, err := m.Registry().Gather()
	if err != nil {
		t.Fatal(err)
	}
	var series int
	for _, mf := range families {
		if mf.GetName() != "hookway_node_total" {
			continue
		}
		series = len(mf.Metric)
		for _, metric := range mf.Metric {
			for _, lp := range metric.Label {
				switch lp.GetName() {
				case "flow", "node_id", "node_type", "outcome":
				default:
					t.Errorf("unbounded or unexpected label %q=%q", lp.GetName(), lp.GetValue())
				}
				if lp.GetName() == "target" || lp.GetName() == "queue" {
					t.Errorf("rendered target leaked into labels: %s=%s", lp.GetName(), lp.GetValue())
				}
			}
		}
	}
	if series != 2 {
		t.Fatalf("hookway_node_total series = %d after 100 distinct lookup results, want 2 (one per node); cardinality grew with data", series)
	}

	desc := make(chan *prometheus.Desc, 8)
	m.nodeTotal.Describe(desc)
	close(desc)
}
