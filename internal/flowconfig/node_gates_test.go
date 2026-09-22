package flowconfig

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/jaysongiroux/hook_way/internal/graph"
)

func TestLoopGuardRejectsAtMaxHops(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	spec, _ := reg.Lookup("loop_guard")
	node, err := spec.New(nodeConfigFromYAML(t, "loop_guard", "max_hops: 2\n"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := node.Run(context.Background(), graph.RequestHead{Hops: 1}); err != nil {
		t.Fatalf("hops 1: %v", err)
	}
	_, err = node.Run(context.Background(), graph.RequestHead{Hops: 2})
	var ne graph.NodeError
	if err == nil || !errors.As(err, &ne) || ne.Status != http.StatusBadRequest {
		t.Fatalf("hops 2 = %v, want 400 NodeError", err)
	}
}

func TestIPAllowlistRejectsUnknownIP(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	spec, _ := reg.Lookup("ip_allowlist")
	node, err := spec.New(nodeConfigFromYAML(t, "ip_allowlist", "cidrs: [10.0.0.0/8]\n"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := node.Run(context.Background(), graph.RequestHead{SourceIP: "10.1.2.3"}); err != nil {
		t.Fatal(err)
	}
	_, err = node.Run(context.Background(), graph.RequestHead{SourceIP: "203.0.113.7"})
	var ne graph.NodeError
	if err == nil || !errors.As(err, &ne) || ne.Status != http.StatusForbidden {
		t.Fatalf("denied = %v, want 403 NodeError", err)
	}
}

func TestRateLimitIndependentKeysAndBurst(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	spec, _ := reg.Lookup("rate_limit")
	node, err := spec.New(nodeConfigFromYAML(t, "rate_limit", "rpm: 60\nburst: 1\nkey_by: source_ip\n"))
	if err != nil {
		t.Fatal(err)
	}
	a := graph.RequestHead{SourceIP: "1.1.1.1"}
	b := graph.RequestHead{SourceIP: "2.2.2.2"}
	if _, err := node.Run(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	_, err = node.Run(context.Background(), a)
	var ne graph.NodeError
	if err == nil || !errors.As(err, &ne) || ne.Status != http.StatusTooManyRequests {
		t.Fatalf("second from same IP = %v, want 429", err)
	}
	if _, err := node.Run(context.Background(), b); err != nil {
		t.Fatalf("other IP should have its own bucket: %v", err)
	}
}

func TestGatesPassCompliantRequestThrough(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	head := graph.RequestHead{Method: http.MethodPost, SourceIP: "10.0.0.9", Hops: 0}
	for _, tc := range []struct{ typ, yaml string }{
		{"loop_guard", "max_hops: 3\n"},
		{"ip_allowlist", "cidrs: [10.0.0.0/8]\n"},
		{"rate_limit", "rpm: 60\nburst: 10\n"},
	} {
		spec, _ := reg.Lookup(tc.typ)
		node, err := spec.New(nodeConfigFromYAML(t, tc.typ, tc.yaml))
		if err != nil {
			t.Fatal(err)
		}
		out, err := node.Run(context.Background(), head)
		if err != nil {
			t.Fatalf("%s: %v", tc.typ, err)
		}
		got := out.(graph.RequestHead)
		if got.SourceIP != head.SourceIP || got.Hops != head.Hops {
			t.Errorf("%s mutated head: %+v", tc.typ, got)
		}
	}
}
