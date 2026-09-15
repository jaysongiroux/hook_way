package flowconfig

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/jasongiroux/hookway/internal/graph"
)

func TestFilterDropReturnsTerminalWithoutError(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	spec, _ := reg.Lookup("filter")
	node, err := spec.New(nodeConfigFromYAML(t, "filter",
		"conditions: [{source: body, path: event, operator: eq, value: created}]\non_no_match: drop\n"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := node.Run(context.Background(), graph.Envelope{Body: map[string]any{"event": "deleted"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := out.(graph.Terminal); !ok {
		t.Fatalf("drop produced %T, want Terminal", out)
	}
}

func TestFilterRejectReturns422(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	spec, _ := reg.Lookup("filter")
	node, err := spec.New(nodeConfigFromYAML(t, "filter",
		"conditions: [{source: body, path: event, operator: eq, value: created}]\non_no_match: reject\n"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = node.Run(context.Background(), graph.Envelope{Body: map[string]any{"event": "deleted"}})
	var ne graph.NodeError
	if err == nil || !errors.As(err, &ne) || ne.Status != http.StatusUnprocessableEntity {
		t.Fatalf("reject = %v, want 422 NodeError", err)
	}
}

func TestFilterPassThroughOnMatch(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	spec, _ := reg.Lookup("filter")
	node, err := spec.New(nodeConfigFromYAML(t, "filter",
		"conditions: [{source: body, path: event, operator: eq, value: created}]\n"))
	if err != nil {
		t.Fatal(err)
	}
	in := graph.Envelope{Body: map[string]any{"event": "created"}}
	out, err := node.Run(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if out.(graph.Envelope).Body.(map[string]any)["event"] != "created" {
		t.Fatalf("out = %#v", out)
	}
}

func TestBrokenFilterChainFailsClosed(t *testing.T) {
	node := newFilterNode("f", FilterOptions{
		Conditions: []FilterConditionOptions{{Source: "not-a-source", Path: "x", Operator: "eq", Value: "y"}},
	})
	out, err := node.Run(context.Background(), graph.Envelope{Body: map[string]any{"x": "y"}})
	if err != nil {
		t.Fatalf("fail-closed drop should not error, got %v", err)
	}
	if _, ok := out.(graph.Terminal); !ok {
		t.Fatalf("fail-closed should drop (Terminal), got %T", out)
	}
}
