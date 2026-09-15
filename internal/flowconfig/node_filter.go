package flowconfig

import (
	"context"
	"fmt"
	"net/http"

	"github.com/jasongiroux/hookway/internal/filter"
	"github.com/jasongiroux/hookway/internal/graph"
)

type filterNode struct {
	id        string
	chain     filter.Chain
	onNoMatch string
}

func newFilterNode(id string, opts FilterOptions) graph.Node {
	specs := make([]filter.Spec, len(opts.Conditions))
	for i, c := range opts.Conditions {
		specs[i] = filter.Spec{Source: c.Source, Path: c.Path, Operator: c.Operator, Value: c.Value}
	}
	chain, issues := filter.Compile(specs)
	if len(issues) > 0 {
		chain = filter.MatchNothing()
	}
	onNoMatch := opts.OnNoMatch
	if onNoMatch == "" {
		onNoMatch = "drop"
	}
	return filterNode{id: id, chain: chain, onNoMatch: onNoMatch}
}

func (n filterNode) Run(_ context.Context, in graph.Payload) (graph.Payload, error) {
	env, ok := in.(graph.Envelope)
	if !ok {
		return nil, graph.NodeError{
			NodeID: n.id, Status: http.StatusInternalServerError,
			Err: fmt.Errorf("filter %q: expected Envelope, got %T", n.id, in),
		}
	}
	headers := env.V.Raw.Head.Header
	if cap, ok := env.Extra.(CapturedEvent); ok && cap.Event.OrigHeaders != nil {
		headers = cap.Event.OrigHeaders
	}
	okMatch, _ := n.chain.Match(headers, env.Body)
	if okMatch {
		return env, nil
	}
	if n.onNoMatch == "reject" {
		return nil, graph.NodeError{
			NodeID: n.id, Status: http.StatusUnprocessableEntity,
			Err: fmt.Errorf("filtered"),
		}
	}
	// Silent drop: successful Terminal with no deliveries keeps the replay
	// reservation (the event was consumed) and returns 202 to the sender.
	return graph.Terminal{}, nil
}
