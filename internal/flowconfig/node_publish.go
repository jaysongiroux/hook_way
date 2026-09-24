package flowconfig

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/jaysongiroux/hook_way/internal/graph"
	"github.com/jaysongiroux/hook_way/internal/vars"
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

func newPublishNode(id string, opts PublishOptions, resolver PublisherResolver) (graph.Node, error) {
	tpl, err := vars.Parse(opts.Target)
	if err != nil {
		return nil, err
	}
	return publishNode{id: id, opts: opts, template: tpl, resolver: resolver}, nil
}

func (n publishNode) DeliveryTarget() string { return n.opts.Target }

// PreparePublish applies a publish node's options to its input envelope
// exactly as the node does before handing it to the publisher: it renders the
// target and message attributes from the envelope's variables and sets the
// payload mode. Failure capture uses it to rebuild the message a failed
// publish sent from the node's recorded input.
func PreparePublish(opts PublishOptions, in graph.Envelope) (target string, prepared graph.Envelope, err error) {
	tpl, err := vars.Parse(opts.Target)
	if err != nil {
		return "", in, err
	}
	return preparePublish(opts, tpl, in)
}

// preparePublish returns the rendered target even when a later step fails, so
// the node can report it on the failed delivery.
func preparePublish(opts PublishOptions, tpl vars.Template, env graph.Envelope) (target string, prepared graph.Envelope, err error) {
	rendered, err := tpl.Render(env.Vars)
	if err != nil {
		return "", env, err
	}
	if len(opts.MessageAttributes) > 0 {
		attrs, attrErr := renderMessageAttributes(opts.MessageAttributes, env.Vars)
		if attrErr != nil {
			return rendered, env, attrErr
		}
		env.MessageAttributes = attrs
	}
	switch opts.Payload {
	case "", "envelope":
		env.PayloadMode = "envelope"
	case "object":
		env.PayloadMode = "object"
	}
	return rendered, env, nil
}

type publishingNodeKey struct{}

// PublishingNode returns the id of the publish node that issued a publish,
// from the context the node hands its publisher, or "".
func PublishingNode(ctx context.Context) string {
	id, _ := ctx.Value(publishingNodeKey{}).(string)
	return id
}

func (n publishNode) Run(ctx context.Context, in graph.Payload) (graph.Payload, error) {
	env, ok := in.(graph.Envelope)
	if !ok {
		err := fmt.Errorf("publish %q: expected Envelope, got %T", n.id, in)
		return graph.Terminal{Deliveries: []graph.Delivery{{NodeID: n.id, Target: n.opts.Target, Err: err}}}, graph.NodeError{
			NodeID: n.id, Status: http.StatusInternalServerError, Err: err,
		}
	}
	rendered, prepared, err := preparePublish(n.opts, n.template, env)
	if err != nil {
		term := graph.Terminal{Deliveries: []graph.Delivery{{NodeID: n.id, Target: n.opts.Target, RenderedTarget: rendered, Err: err}}}
		return term, graph.NodeError{NodeID: n.id, Status: http.StatusBadRequest, Err: err}
	}
	env = prepared
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
	if err := pub.Publish(context.WithValue(ctx, publishingNodeKey{}, n.id), n.opts.Destination, rendered, env); err != nil {
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
