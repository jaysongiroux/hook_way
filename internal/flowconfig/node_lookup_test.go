package flowconfig

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/jasongiroux/hookway/internal/graph"
)

func TestLookupWritesVarsAndCachesViaResolver(t *testing.T) {
	var calls atomic.Int32
	resolve := func(_ context.Context, key string) (string, bool, error) {
		calls.Add(1)
		if key == "abc" {
			return "env-1", true, nil
		}
		return "", false, nil
	}
	reg, err := NewRegistryWithOptions(RegistryOptions{
		Lookups: func(LookupOptions) LookupFunc { return resolve },
	})
	if err != nil {
		t.Fatal(err)
	}
	spec, _ := reg.Lookup("lookup")
	node, err := spec.New(nodeConfigFromYAML(t, "lookup", "provider: dynamodb\nkey: '{id}'\nname: route\n"))
	if err != nil {
		t.Fatal(err)
	}
	in := graph.Envelope{Vars: map[string]string{"id": "abc"}}
	out, err := node.Run(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if out.(graph.Envelope).Vars["route"] != "env-1" {
		t.Fatalf("vars = %#v", out.(graph.Envelope).Vars)
	}
	if _, err := node.Run(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	// The node does not cache; the injected resolver is the cache seam.
	if calls.Load() != 2 {
		t.Fatalf("calls = %d (resolver itself is the cache in serving)", calls.Load())
	}
}

func TestLookupBodyKeyAndFallback(t *testing.T) {
	resolve := func(_ context.Context, key string) (string, bool, error) {
		return "", false, nil
	}
	node := newLookupNode("l", LookupOptions{Key: "{body.id}", Name: "route", Fallback: "shared"}, resolve)
	out, err := node.Run(context.Background(), graph.Envelope{Body: map[string]any{"id": "x"}})
	if err != nil {
		t.Fatal(err)
	}
	if out.(graph.Envelope).Vars["route"] != "shared" {
		t.Fatalf("fallback = %#v", out)
	}
	_, err = newLookupNode("l", LookupOptions{Key: "{body.id}", Name: "route"}, resolve).
		Run(context.Background(), graph.Envelope{Body: map[string]any{"id": "x"}})
	if err == nil {
		t.Fatal("miss without fallback should error")
	}
	if errors.Is(err, errNotImplemented) {
		t.Fatal(err)
	}
}

func TestLookupCacheBehaviourViaCountingProvider(t *testing.T) {
	var calls atomic.Int32
	cached := map[string]string{}
	resolve := func(_ context.Context, key string) (string, bool, error) {
		if v, ok := cached[key]; ok {
			return v, true, nil
		}
		calls.Add(1)
		cached[key] = "v"
		return "v", true, nil
	}
	node := newLookupNode("l", LookupOptions{Key: "{id}", Name: "route"}, resolve)
	in := graph.Envelope{Vars: map[string]string{"id": "k"}}
	if _, err := node.Run(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if _, err := node.Run(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("provider calls = %d, want 1 (cache in resolver)", calls.Load())
	}
}
