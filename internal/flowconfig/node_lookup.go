package flowconfig

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/jasongiroux/hookway/internal/graph"
	"github.com/jasongiroux/hookway/internal/vars"
)

// LookupFunc resolves a routing key. Serving injects the cached lookup
// provider; tests inject a fake. Defined here so flowconfig does not import
// internal/lookup (WASM).
type LookupFunc func(ctx context.Context, key string) (value string, found bool, err error)

type lookupNode struct {
	id      string
	opts    LookupOptions
	resolve LookupFunc
	key     vars.Template
}

func newLookupNode(id string, opts LookupOptions, resolve LookupFunc) graph.Node {
	tpl, _ := vars.Parse(opts.Key)
	return &lookupNode{id: id, opts: opts, resolve: resolve, key: tpl}
}

func (n *lookupNode) Run(ctx context.Context, in graph.Payload) (graph.Payload, error) {
	env, ok := in.(graph.Envelope)
	if !ok {
		return nil, graph.NodeError{
			NodeID: n.id, Status: http.StatusInternalServerError,
			Err: fmt.Errorf("lookup %q: expected Envelope, got %T", n.id, in),
		}
	}
	if n.resolve == nil {
		return nil, graph.NodeError{
			NodeID: n.id, Status: http.StatusInternalServerError,
			Err: fmt.Errorf("lookup %q: no provider", n.id),
		}
	}
	key, err := n.lookupKey(env)
	if err != nil {
		return nil, graph.NodeError{NodeID: n.id, Status: http.StatusBadRequest, Err: err}
	}
	value, found, err := n.resolve(ctx, key)
	if err != nil {
		return nil, graph.NodeError{NodeID: n.id, Status: http.StatusBadRequest, Err: err}
	}
	if !found {
		if n.opts.Fallback != "" {
			value = n.opts.Fallback
		} else {
			return nil, graph.NodeError{
				NodeID: n.id, Status: http.StatusBadRequest,
				Err: fmt.Errorf("lookup %q: key %q was not found", n.id, key),
			}
		}
	}
	if env.Vars == nil {
		env.Vars = map[string]string{}
	}
	name := n.opts.Name
	if name == "" {
		name = n.id
	}
	env.Vars[name] = value
	return env, nil
}

func (n *lookupNode) lookupKey(env graph.Envelope) (string, error) {
	values := map[string]string{}
	for k, v := range env.Vars {
		values[k] = v
	}
	if rendered, err := n.key.Render(values); err == nil {
		return rendered, nil
	}
	if strings.HasPrefix(n.opts.Key, "{body.") && strings.HasSuffix(n.opts.Key, "}") {
		path := strings.TrimSuffix(strings.TrimPrefix(n.opts.Key, "{body."), "}")
		if v, ok := dotted(env.Body, path); ok {
			return fmt.Sprint(v), nil
		}
	}
	return "", fmt.Errorf("lookup key %q is unbound", n.opts.Key)
}

func dotted(body any, path string) (any, bool) {
	cur := body
	for _, part := range strings.Split(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = m[part]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}
