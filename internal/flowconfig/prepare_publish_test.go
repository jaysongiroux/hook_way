package flowconfig

import (
	"context"
	"reflect"
	"testing"

	"github.com/jaysongiroux/hook_way/internal/graph"
)

type preparedCall struct {
	dest, target string
	env          graph.Envelope
}

type preparingResolver struct{ calls []preparedCall }

func (r *preparingResolver) Resolve(string) (Publisher, error) { return r, nil }

func (r *preparingResolver) Publish(_ context.Context, dest, target string, env graph.Envelope) error {
	r.calls = append(r.calls, preparedCall{dest, target, env})
	return nil
}

// PreparePublish must reproduce exactly what the publish node hands its
// publisher, or failure capture would render a different payload than the one
// that failed.
func TestPreparePublishMatchesWhatThePublishNodeSends(t *testing.T) {
	body := "destination: primary\ntarget: '{route}-queue'\nmessage_attributes: {tenant: '{route}'}\npayload: envelope\n"
	resolver := &preparingResolver{}
	node := mustCatalogNode(t, "publish", body, RegistryOptions{Publishers: resolver})
	in := graph.Envelope{Body: map[string]any{"event": "created"}, Vars: map[string]string{"route": "orders"}}
	if _, err := node.Run(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	opts, err := PublishNodeOptions(nodeConfigFromYAML(t, "publish", body))
	if err != nil {
		t.Fatal(err)
	}
	target, prepared, err := PreparePublish(opts, in)
	if err != nil {
		t.Fatal(err)
	}
	got := resolver.calls[0]
	if target != got.target || !reflect.DeepEqual(prepared, got.env) {
		t.Fatalf("PreparePublish = %q %#v, publish node sent %q %#v", target, prepared, got.target, got.env)
	}
	if prepared.PayloadMode != "envelope" || prepared.MessageAttributes["tenant"].Value != "orders" {
		t.Fatalf("prepared = %#v", prepared)
	}
}

func TestPreparePublishReportsUnrenderableTargets(t *testing.T) {
	opts := PublishOptions{Destination: "primary", Target: "{missing}-queue"}
	target, _, err := PreparePublish(opts, graph.Envelope{Vars: map[string]string{}})
	if err == nil || target != "" {
		t.Fatalf("PreparePublish = %q, %v; want an error and no target", target, err)
	}
}

type contextResolver struct{ node string }

func (r *contextResolver) Resolve(string) (Publisher, error) { return r, nil }

func (r *contextResolver) Publish(ctx context.Context, _, _ string, _ graph.Envelope) error {
	r.node = PublishingNode(ctx)
	return nil
}

// Failure capture names the node behind an async webhook delivery from the
// publish context.
func TestPublishCarriesThePublishingNodeOnTheContext(t *testing.T) {
	r := &contextResolver{}
	node := mustCatalogNode(t, "publish", "destination: q\ntarget: t\n", RegistryOptions{Publishers: r})
	if _, err := node.Run(context.Background(), graph.Envelope{Vars: map[string]string{}}); err != nil {
		t.Fatal(err)
	}
	if r.node != "node" {
		t.Fatalf("PublishingNode = %q, want the publish node's id", r.node)
	}
	if PublishingNode(context.Background()) != "" {
		t.Fatal("a bare context names a node")
	}
}
