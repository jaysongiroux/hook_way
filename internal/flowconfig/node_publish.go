package flowconfig

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/jasongiroux/hookway/internal/graph"
	"github.com/jasongiroux/hookway/internal/vars"
)

// Publisher delivers one rendered destination target. Implemented by the
// serving adapter over queue.Publisher; tests supply a fake. Defined here so
// flowconfig does not import the queue package (WASM validator).
type Publisher interface {
	Publish(ctx context.Context, destinationID, renderedTarget string, env graph.Envelope) error
}

// PublisherResolver hands a publish node its destination at construction.
type PublisherResolver interface {
	Resolve(destinationID string) (Publisher, error)
}

type publishNode struct {
	id       string
	opts     PublishOptions
	template vars.Template
	resolver PublisherResolver
}

func newPublishNode(id string, opts PublishOptions, resolver PublisherResolver) graph.Node {
	tpl, err := vars.Parse(opts.Target)
	if err != nil {
		tpl, _ = vars.Parse("")
	}
	return publishNode{id: id, opts: opts, template: tpl, resolver: resolver}
}

func (n publishNode) DeliveryTarget() string { return n.opts.Target }

func (n publishNode) Run(ctx context.Context, in graph.Payload) (graph.Payload, error) {
	env, ok := in.(graph.Envelope)
	if !ok {
		err := fmt.Errorf("publish %q: expected Envelope, got %T", n.id, in)
		return graph.Terminal{Deliveries: []graph.Delivery{{NodeID: n.id, Target: n.opts.Target, Err: err}}}, graph.NodeError{
			NodeID: n.id, Status: http.StatusInternalServerError, Err: err,
		}
	}
	rendered, err := n.template.Render(env.Vars)
	if err != nil {
		term := graph.Terminal{Deliveries: []graph.Delivery{{NodeID: n.id, Target: n.opts.Target, Err: err}}}
		return term, graph.NodeError{NodeID: n.id, Status: http.StatusBadRequest, Err: err}
	}
	if len(n.opts.MessageAttributes) > 0 {
		attrs, attrErr := renderMessageAttributes(n.opts.MessageAttributes, env.Vars)
		if attrErr != nil {
			term := graph.Terminal{Deliveries: []graph.Delivery{{NodeID: n.id, Target: n.opts.Target, RenderedTarget: rendered, Err: attrErr}}}
			return term, graph.NodeError{NodeID: n.id, Status: http.StatusBadRequest, Err: attrErr}
		}
		env.MessageAttributes = attrs
	}
	switch n.opts.Payload {
	case "", "envelope":
		env.PayloadMode = "envelope"
	case "object":
		env.PayloadMode = "object"
	}
	if n.resolver == nil {
		err := fmt.Errorf("publish %q: no publisher resolver", n.id)
		term := graph.Terminal{Deliveries: []graph.Delivery{{NodeID: n.id, Target: n.opts.Target, RenderedTarget: rendered, Err: err}}}
		return term, graph.NodeError{NodeID: n.id, Status: http.StatusServiceUnavailable, Err: err}
	}
	pub, err := n.resolver.Resolve(n.opts.Destination)
	if err != nil {
		term := graph.Terminal{Deliveries: []graph.Delivery{{NodeID: n.id, Target: n.opts.Target, RenderedTarget: rendered, Err: err}}}
		return term, graph.NodeError{NodeID: n.id, Status: http.StatusServiceUnavailable, Err: err}
	}
	if err := pub.Publish(ctx, n.opts.Destination, rendered, env); err != nil {
		term := graph.Terminal{Deliveries: []graph.Delivery{{NodeID: n.id, Target: n.opts.Target, RenderedTarget: rendered, Err: err}}}
		var nodeErr graph.NodeError
		if errors.As(err, &nodeErr) {
			nodeErr.NodeID = n.id
			return term, nodeErr
		}
		return term, graph.NodeError{NodeID: n.id, Status: http.StatusServiceUnavailable, Err: err}
	}
	return graph.Terminal{Deliveries: []graph.Delivery{{NodeID: n.id, Target: n.opts.Target, RenderedTarget: rendered}}}, nil
}
