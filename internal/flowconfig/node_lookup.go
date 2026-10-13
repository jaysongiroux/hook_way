package flowconfig

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"strings"

	"github.com/jaysongiroux/hook_way/internal/graph"
	"github.com/jaysongiroux/hook_way/internal/vars"
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

func newLookupNode(id string, opts LookupOptions, resolve LookupFunc) (graph.Node, error) {
	var tpl vars.Template
	if !isBodyPathKey(opts.Key) {
		parsed, err := vars.Parse(opts.Key)
		if err != nil {
			return nil, err
		}
		tpl = parsed
	}
	return &lookupNode{id: id, opts: opts, resolve: resolve, key: tpl}, nil
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
		return nil, graph.NodeError{NodeID: n.id, Status: http.StatusServiceUnavailable, Err: fmt.Errorf("lookup %q: provider unavailable", n.id)}
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
	// Copy before writing: nodes never mutate their input (I19).
	vars := maps.Clone(env.Vars)
	if vars == nil {
		vars = map[string]string{}
	}
	name := n.opts.Name
	if name == "" {
		name = n.id
	}
	vars[name] = value
	env.Vars = vars
	return env, nil
}

func (n *lookupNode) lookupKey(env graph.Envelope) (string, error) {
	if isBodyPathKey(n.opts.Key) {
		path := strings.TrimSuffix(strings.TrimPrefix(n.opts.Key, "{body."), "}")
		if v, ok := dotted(env.Body, path); ok {
			return fmt.Sprint(v), nil
		}
		return "", fmt.Errorf("lookup key %q is unbound", n.opts.Key)
	}
	values := map[string]string{}
	for k, v := range env.Vars {
		values[k] = v
	}
	if rendered, err := n.key.Render(values); err == nil {
		return rendered, nil
	} else {
		return "", err
	}
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
