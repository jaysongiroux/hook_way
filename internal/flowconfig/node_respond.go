package flowconfig

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/jaysongiroux/hook_way/internal/graph"
	"github.com/jaysongiroux/hook_way/internal/vars"
)

type respondNode struct {
	id   string
	opts RespondOptions
	body vars.Template
}

func newRespondNode(id string, opts RespondOptions) (graph.Node, error) {
	tpl, err := vars.Parse(opts.Body)
	if err != nil {
		return nil, err
	}
	return respondNode{id: id, opts: opts, body: tpl}, nil
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
		header := filterPassthroughHeadersWithOptions(env.Upstream.Header, n.opts)
		body := responseBody(env.V.Raw.Head.Method, env.Upstream.Status, slices.Clone(env.Upstream.Body))
		relayContentLength(header, env.V.Raw.Head.Method, env.Upstream.Status, len(env.Upstream.Body))
		return graph.Terminal{Response: &graph.Response{
			Status: env.Upstream.Status,
			Header: header,
			Body:   body,
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
		Body:   responseBody(env.V.Raw.Head.Method, n.opts.Status, []byte(body)),
	}}, nil
}

func responseBody(method string, status int, body []byte) []byte {
	if method == http.MethodHead {
		return nil
	}
	return bodyForStatus(status, body)
}

func relayContentLength(header http.Header, method string, status, size int) {
	if status == http.StatusNoContent {
		header.Del("Content-Length")
		return
	}
	if method == http.MethodHead || status == http.StatusNotModified {
		// A representation length supplied by upstream remains meaningful even
		// though HEAD and 304 carry no response body.
		if values := header.Values("Content-Length"); len(values) > 0 {
			n, err := strconv.ParseInt(values[0], 10, 64)
			if len(values) != 1 || err != nil || n < 0 {
				header.Del("Content-Length")
			}
		}
		return
	}
	header.Set("Content-Length", strconv.Itoa(size))
}

func bodyForStatus(status int, body []byte) []byte {
	if status == http.StatusNoContent || status == http.StatusNotModified {
		return nil
	}
	return body
}

func filterPassthroughHeadersWithOptions(src http.Header, opts RespondOptions) http.Header {
	out := http.Header{}
	allowed := map[string]bool{}
	for _, name := range opts.ForwardHeaders {
		allowed[http.CanonicalHeaderKey(name)] = true
	}
	nominated := connectionHeaders(src)
	for k, vs := range src {
		canon := http.CanonicalHeaderKey(k)
		if nominated[canon] || (isHopByHop(canon) && canon != "Content-Length") || (canon == "Set-Cookie" && !opts.ForwardSetCookie) || (len(allowed) > 0 && !allowed[canon]) {
			continue
		}
		out[canon] = append([]string(nil), vs...)
	}
	return out
}
