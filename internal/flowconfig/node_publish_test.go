package flowconfig

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jaysongiroux/hook_way/internal/graph"
)

type fakePublisher struct {
	lastTarget string
	lastEnv    graph.Envelope
	err        error
}

func (f *fakePublisher) Publish(_ context.Context, _, renderedTarget string, env graph.Envelope) error {
	f.lastTarget = renderedTarget
	f.lastEnv = env
	return f.err
}

type staticResolver struct{ pub Publisher }

func (s staticResolver) Resolve(string) (Publisher, error) { return s.pub, nil }

func mustPublishNode(t *testing.T, body string, resolver PublisherResolver) graph.Node {
	t.Helper()
	reg, err := NewRegistryWithOptions(RegistryOptions{Publishers: resolver})
	if err != nil {
		t.Fatal(err)
	}
	spec, _ := reg.Lookup("publish")
	node, err := spec.New(nodeConfigFromYAML(t, "publish", body))
	if err != nil {
		t.Fatal(err)
	}
	return node
}

func TestPublishLabelsWithTemplateNotRenderedTarget(t *testing.T) {
	fake := &fakePublisher{}
	node := mustPublishNode(t, "destination: primary\ntarget: \"{route}-queue\"\n",
		staticResolver{pub: fake})

	env := graph.Envelope{Vars: map[string]string{"route": "env-4711"}}
	out, err := node.Run(context.Background(), env)
	if err != nil {
		t.Fatalf("Run() = %v", err)
	}

	if fake.lastTarget != "env-4711-queue" {
		t.Errorf("published to %q, want the RENDERED target env-4711-queue", fake.lastTarget)
	}
	term := out.(graph.Terminal)
	if term.Deliveries[0].Target != "{route}-queue" {
		t.Errorf("Delivery.Target = %q, want the TEMPLATE {route}-queue: "+
			"metric labels must be bounded by config, not data (R3)",
			term.Deliveries[0].Target)
	}
}

func TestPublishRendersMessageAttributes(t *testing.T) {
	fake := &fakePublisher{}
	node := mustPublishNode(t, "destination: primary\ntarget: t\nmessage_attributes:\n  event: '{kind}'\n  fields_changed: {data_type: String.Array, value: '{diff}'}\n",
		staticResolver{pub: fake})
	_, err := node.Run(context.Background(), graph.Envelope{Vars: map[string]string{
		"kind": "opened",
		"diff": `["status"]`,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if fake.lastEnv.MessageAttributes["event"].DataType != "String" || fake.lastEnv.MessageAttributes["event"].Value != "opened" {
		t.Fatalf("event = %+v", fake.lastEnv.MessageAttributes["event"])
	}
	if fake.lastEnv.MessageAttributes["fields_changed"].DataType != "String.Array" {
		t.Fatalf("fields_changed = %+v", fake.lastEnv.MessageAttributes["fields_changed"])
	}
}

func TestPublishRecordsFailure(t *testing.T) {
	fake := &fakePublisher{err: errors.New("broker down")}
	node := mustPublishNode(t, "destination: primary\ntarget: github-webhooks\n",
		staticResolver{pub: fake})
	out, err := node.Run(context.Background(), graph.Envelope{})
	if err == nil {
		t.Fatal("expected publish error")
	}
	var ne graph.NodeError
	if !errors.As(err, &ne) || ne.Status != 503 {
		t.Errorf("status = %+v, want 503", err)
	}
	term := out.(graph.Terminal)
	if term.Deliveries[0].Err == nil {
		t.Fatal("delivery error not recorded")
	}
}

func TestPublishPreservesTypedPublisherStatus(t *testing.T) {
	fake := &fakePublisher{err: graph.NodeError{Status: 400, Err: errors.New("invalid payload")}}
	node := mustPublishNode(t, "destination: primary\ntarget: github-webhooks\n",
		staticResolver{pub: fake})
	_, err := node.Run(context.Background(), graph.Envelope{})
	var nodeErr graph.NodeError
	if !errors.As(err, &nodeErr) || nodeErr.Status != 400 || nodeErr.NodeID != "node" {
		t.Fatalf("error = %+v, want node node status 400", err)
	}
}

func TestPublishSetsObjectPayloadMode(t *testing.T) {
	fake := &fakePublisher{}
	node := mustPublishNode(t, "destination: archive\ntarget: uploads\npayload: object\n",
		staticResolver{pub: fake})
	_, err := node.Run(context.Background(), graph.Envelope{
		Body: map[string]any{"content": "YQ==", "filename": "a.bin"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if fake.lastEnv.PayloadMode != "object" {
		t.Fatalf("PayloadMode = %q, want object", fake.lastEnv.PayloadMode)
	}
}

func TestPublishRejectsUnknownPayload(t *testing.T) {
	reg, err := NewRegistryWithOptions(RegistryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	spec, _ := reg.Lookup("publish")
	_, err = spec.New(nodeConfigFromYAML(t, "publish", "destination: primary\ntarget: t\npayload: blob\n"))
	if err == nil || !strings.Contains(err.Error(), "payload") {
		t.Fatalf("err = %v, want payload rejection", err)
	}
}
