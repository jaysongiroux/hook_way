package flowconfig

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/jasongiroux/hookway/internal/graph"
	"github.com/jasongiroux/hookway/internal/vars"
)

type respondNode struct {
	id   string
	opts RespondOptions
	body vars.Template
}

func newRespondNode(id string, opts RespondOptions) graph.Node {
	tpl, err := vars.Parse(opts.Body)
	if err != nil {
		tpl, _ = vars.Parse("")
	}
	return respondNode{id: id, opts: opts, body: tpl}
}

func (n respondNode) Run(_ context.Context, in graph.Payload) (graph.Payload, error) {
	env, ok := in.(graph.Envelope)
	if !ok {
		return nil, graph.NodeError{
			NodeID: n.id, Status: http.StatusInternalServerError,
			Err: fmt.Errorf("respond %q: expected Envelope, got %T", n.id, in),
		}
	}
	if n.opts.Passthrough {
		if env.Upstream == nil {
			return graph.Terminal{}, graph.NodeError{
				NodeID: n.id, Status: http.StatusBadGateway,
				Err: fmt.Errorf("respond %q: passthrough requires an upstream response", n.id),
			}
		}
		return graph.Terminal{Response: &graph.Response{
			Status: env.Upstream.Status,
			Header: filterPassthroughHeaders(env.Upstream.Header),
			Body:   slices.Clone(env.Upstream.Body),
		}}, nil
	}
	body := n.opts.Body
	if n.opts.Body != "" {
		if rendered, err := n.body.Render(env.Vars); err == nil {
			body = rendered
		} else if strings.Contains(n.opts.Body, "{") {
			return graph.Terminal{}, graph.NodeError{NodeID: n.id, Status: http.StatusBadRequest, Err: err}
		}
	}
	hdr := http.Header{}
	for k, v := range n.opts.Headers {
		hdr.Set(k, v)
	}
	return graph.Terminal{Response: &graph.Response{
		Status: n.opts.Status,
		Header: hdr,
		Body:   []byte(body),
	}}, nil
}

func filterPassthroughHeaders(src http.Header) http.Header {
	out := http.Header{}
	for k, vs := range src {
		canon := http.CanonicalHeaderKey(k)
		if isHopByHop(canon) || canon == "Set-Cookie" {
			continue
		}
		out[canon] = append([]string(nil), vs...)
	}
	return out
}
