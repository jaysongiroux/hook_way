package flowconfig

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"time"

	"github.com/jasongiroux/hookway/internal/codenode"
	"github.com/jasongiroux/hookway/internal/graph"
	hooklogging "github.com/jasongiroux/hookway/internal/logging"
	"github.com/jasongiroux/hookway/internal/transform"
)

const (
	defaultCodeTimeout    = time.Second
	defaultCodeStack      = 1000
	defaultCodeOutputSize = 1 << 20
)

type codeNode struct {
	id                  string
	rt                  *codenode.Runtime
	allowlist, denylist []string
}

func newCodeNode(id string, opts CodeOptions) (graph.Node, error) {
	timeout := defaultCodeTimeout
	if opts.Timeout != "" {
		d, err := time.ParseDuration(opts.Timeout)
		if err != nil {
			return nil, err
		}
		timeout = d
	}
	stack := defaultCodeStack
	if opts.MaxCallStack > 0 {
		stack = opts.MaxCallStack
	}
	maxOut := defaultCodeOutputSize
	if opts.MaxOutputBytes > 0 {
		maxOut = int(opts.MaxOutputBytes)
	}
	rt, err := codenode.NewRuntime(opts.Script, codenode.Limits{
		Timeout:        timeout,
		MaxCallStack:   stack,
		MaxInputBytes:  defaultCodeOutputSize,
		MaxOutputBytes: maxOut,
	})
	if err != nil {
		return nil, err
	}
	return codeNode{id: id, rt: rt}, nil
}

func (n codeNode) Run(ctx context.Context, in graph.Payload) (graph.Payload, error) {
	env, ok := in.(graph.Envelope)
	if !ok {
		return nil, graph.NodeError{
			NodeID: n.id, Status: http.StatusInternalServerError,
			Err: fmt.Errorf("code %q: expected Envelope, got %T", n.id, in),
		}
	}
	if _, isXML := env.Body.(transform.Document); isXML {
		return nil, graph.NodeError{
			NodeID: n.id, Status: http.StatusBadRequest,
			Err: fmt.Errorf("code %q: XML bodies are not a script input; use transform xml_prune", n.id),
		}
	}
	if _, isXML := env.Body.(*transform.Document); isXML {
		return nil, graph.NodeError{
			NodeID: n.id, Status: http.StatusBadRequest,
			Err: fmt.Errorf("code %q: XML bodies are not a script input; use transform xml_prune", n.id),
		}
	}
	body, err := cloneJSONValue(env.Body)
	if err != nil {
		return nil, graph.NodeError{NodeID: n.id, Status: http.StatusBadRequest, Err: fmt.Errorf("code %q: body: %w", n.id, err)}
	}
	input := map[string]any{
		"body":    body,
		"vars":    maps.Clone(env.Vars),
		"headers": scriptHeaders(env.V.Raw.Head.Header, n.allowlist, n.denylist),
	}
	out, err := n.rt.Run(ctx, input)
	if err != nil {
		status := http.StatusBadRequest
		return nil, graph.NodeError{NodeID: n.id, Status: status, Err: fmt.Errorf("code %q: %w", n.id, err)}
	}
	result, ok := out.(map[string]any)
	if !ok {
		return nil, graph.NodeError{
			NodeID: n.id, Status: http.StatusBadRequest,
			Err: fmt.Errorf("code %q: script must return an object {body, vars}, got %T", n.id, out),
		}
	}
	if body, exists := result["body"]; exists {
		env.Body = body
	}
	if varsVal, exists := result["vars"]; exists {
		varsOut, err := asStringMap(varsVal)
		if err != nil {
			return nil, graph.NodeError{NodeID: n.id, Status: http.StatusBadRequest, Err: fmt.Errorf("code %q: %w", n.id, err)}
		}
		env.Vars = varsOut
	}
	return env, nil
}

func scriptHeaders(h http.Header, allowlist, denylist []string) map[string]string {
	filtered := hooklogging.FilterHeaders(h, allowlist, denylist)
	out := make(map[string]string, len(filtered))
	for k, vs := range filtered {
		if len(vs) > 0 {
			out[k] = vs[0]
		}
	}
	return out
}

func cloneJSONValue(v any) (any, error) {
	if v == nil {
		return nil, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func asStringMap(v any) (map[string]string, error) {
	switch m := v.(type) {
	case map[string]string:
		return maps.Clone(m), nil
	case map[string]any:
		out := make(map[string]string, len(m))
		for k, val := range m {
			out[k] = stringify(val)
		}
		return out, nil
	case nil:
		return map[string]string{}, nil
	default:
		return nil, fmt.Errorf("vars must be an object, got %T", v)
	}
}
