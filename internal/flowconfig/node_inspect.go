package flowconfig

import (
	"context"
	"fmt"
	"net/http"

	"github.com/jaysongiroux/hook_way/internal/graph"
)

type inspectionObserverKey struct{}

// WithInspectionObserver attaches the inbound request's view-only capture hook.
// Redrives do not install this hook, so an inspect node is a pass-through there.
func WithInspectionObserver(ctx context.Context, observer func(string, graph.Envelope)) context.Context {
	return context.WithValue(ctx, inspectionObserverKey{}, observer)
}

type inspectNode struct{ id string }

func newInspectNode(id string, _ InspectOptions) graph.Node { return inspectNode{id: id} }
func (n inspectNode) Run(ctx context.Context, in graph.Payload) (graph.Payload, error) {
	env, ok := in.(graph.Envelope)
	if !ok {
		return nil, graph.NodeError{NodeID: n.id, Status: http.StatusInternalServerError, Err: fmt.Errorf("inspect %q: expected Envelope, got %T", n.id, in)}
	}
	if observer, ok := ctx.Value(inspectionObserverKey{}).(func(string, graph.Envelope)); ok && observer != nil {
		// An observation failure must never change webhook processing.
		func() { defer func() { _ = recover() }(); observer(n.id, env) }()
	}
	return env, nil
}
