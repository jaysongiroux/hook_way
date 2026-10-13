package capture

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/jaysongiroux/hook_way/internal/deliverysnapshot"
	"github.com/jaysongiroux/hook_way/internal/flowconfig"
	"github.com/jaysongiroux/hook_way/internal/graph"
	"github.com/jaysongiroux/hook_way/internal/replaywire"
	"gopkg.in/yaml.v3"
)

type sharedPublisher struct {
	mu     sync.Mutex
	replay bool
	bodies []any
}

func (p *sharedPublisher) Resolve(string) (flowconfig.Publisher, error) { return p, nil }
func (p *sharedPublisher) Publish(ctx context.Context, _, _ string, env graph.Envelope) error {
	if flowconfig.PublishingNode(ctx) != "out" {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.replay {
		return errors.New("shared destination unavailable")
	}
	p.bodies = append(p.bodies, env.Body)
	return nil
}

func TestSharedDestinationFailuresCaptureAndReplayIndependently(t *testing.T) {
	srv, uploads := fakeReplay(t, 201)
	metrics := newFakeMetrics()
	capturer := newTestCapturer(t, srv.URL, "", metrics)
	var fc graph.FlowConfig
	if err := yaml.Unmarshal([]byte(`
delivery_failure_policy: skip_failed
nodes:
 - {id: split, type: transform, kind: strip_fields, fields: [unused]}
 - {id: left, type: publish, destination: queue, target: first}
 - {id: right, type: publish, destination: queue, target: second}
 - {id: accepted, type: publish, destination: queue, target: accepted}
 - {id: shape_left, type: transform, kind: strip_fields, fields: [b]}
 - {id: shape_right, type: transform, kind: strip_fields, fields: [a]}
 - {id: out, type: publish, destination: queue, target: "orders-{tenant}"}
edges: [split>left, split>right, split>accepted, left>shape_left, right>shape_right, shape_left>out, shape_right>out]
`), &fc); err != nil {
		t.Fatal(err)
	}
	publisher := &sharedPublisher{}
	registry, err := flowconfig.NewRegistryWithOptions(flowconfig.RegistryOptions{Publishers: publisher})
	if err != nil {
		t.Fatal(err)
	}
	flow, err := graph.CompileChild("orders", fc, "split", registry)
	if err != nil {
		t.Fatal(err)
	}
	env := testEnvelope()
	env.Body = map[string]any{"a": 1.0, "b": 2.0}
	req := capturer.Begin("orders", testHead)
	term, err := flow.RunChild(t.Context(), env, graph.RunOptions{PublishTimeout: time.Second, FanoutConcurrency: 3, OnFailure: req.OnFailure})
	if err != nil || len(term.Deliveries) != 5 {
		t.Fatal(term, err)
	}
	req.Settle(202)
	waitUntil(t, "two shared-node uploads", func() bool { return metrics.count("uploaded", "orders") == 2 })
	records := uploads()
	if len(records) != 2 || records[0].meta.ID == records[1].meta.ID {
		t.Fatal(records)
	}
	seen := map[string]bool{}
	for _, rec := range records {
		if rec.meta.NodeID != "out" || !rec.meta.Retryable {
			t.Fatal(rec.meta)
		}
		snapshot, err := deliverysnapshot.Decode(rec.parts[replaywire.PartSnapshot])
		if err != nil {
			t.Fatal(err)
		}
		restored, err := EnvelopeFromSnapshot(*snapshot.Envelope)
		if err != nil {
			t.Fatal(err)
		}
		body := restored.Body.(map[string]any)
		if len(body) != 1 {
			t.Fatal("lost branch-local transform", body)
		}
		for field := range body {
			seen[field] = true
		}
	}
	if !seen["a"] || !seen["b"] {
		t.Fatal(seen)
	}
	if reflect.DeepEqual(records[0].parts[replaywire.PartPayload], records[1].parts[replaywire.PartPayload]) {
		t.Fatal("captured identical payloads for different arrivals")
	}
	saved, err := deliverysnapshot.Decode(records[0].parts[replaywire.PartSnapshot])
	if err != nil {
		t.Fatal(err)
	}
	restored, err := EnvelopeFromSnapshot(*saved.Envelope)
	if err != nil {
		t.Fatal(err)
	}
	publisher.replay = true // all original workers have joined
	replay, err := flow.RunFrom(t.Context(), "out", restored, graph.RunOptions{PublishTimeout: time.Second})
	if err != nil || len(replay.Deliveries) != 1 || len(publisher.bodies) != 1 || !reflect.DeepEqual(publisher.bodies[0], restored.Body) {
		t.Fatal(replay, publisher.bodies, err)
	}
}
