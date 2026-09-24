package flowconfig

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"strings"
	"time"

	"github.com/jaysongiroux/hook_way/internal/graph"
	"github.com/jaysongiroux/hook_way/internal/transform"
)

type transformNode struct {
	id     string
	opts   TransformOptions
	logger *slog.Logger
}

func newTransformNode(id string, opts TransformOptions, logger *slog.Logger) graph.Node {
	return transformNode{id: id, opts: opts, logger: logger}
}

func (n transformNode) Run(_ context.Context, in graph.Payload) (graph.Payload, error) {
	env, ok := in.(graph.Envelope)
	if !ok {
		return nil, graph.NodeError{
			NodeID: n.id, Status: http.StatusInternalServerError,
			Err: fmt.Errorf("transform %q: expected Envelope, got %T", n.id, in),
		}
	}
	switch n.opts.Kind {
	case "changed_fields":
		return n.changedFields(env)
	case "field_map":
		return n.fieldMap(env)
	case "xml_prune":
		return n.xmlPrune(env)
	default:
		return nil, graph.NodeError{
			NodeID: n.id, Status: http.StatusInternalServerError,
			Err: fmt.Errorf("%w: transform kind %s", errNotImplemented, n.opts.Kind),
		}
	}
}

func (n transformNode) xmlPrune(env graph.Envelope) (graph.Payload, error) {
	doc, ok := xmlDocument(env.Body)
	if !ok {
		return nil, graph.NodeError{
			NodeID: n.id, Status: http.StatusBadRequest,
			Err: fmt.Errorf("transform %q: envelope body is not XML", n.id),
		}
	}
	log := func(rule string, index, before, after int, d time.Duration) {
		if n.logger == nil {
			return
		}
		n.logger.Info("xml_prune rule",
			"rule", rule,
			"index", index,
			"elements_before", before,
			"elements_after", after,
			"duration", d,
		)
	}
	next, err := transform.Prune(doc, n.opts.Rules, log)
	if err != nil {
		return nil, graph.NodeError{NodeID: n.id, Status: http.StatusInternalServerError, Err: err}
	}
	env.Body = next
	return env, nil
}

func xmlDocument(body any) (transform.Document, bool) {
	switch d := body.(type) {
	case transform.Document:
		return d, d.Root != nil
	case *transform.Document:
		if d == nil || d.Root == nil {
			return transform.Document{}, false
		}
		return *d, true
	default:
		return transform.Document{}, false
	}
}

func (n transformNode) changedFields(env graph.Envelope) (graph.Payload, error) {
	prev, ok := lookupEnvelope(env, n.opts.From)
	if !ok {
		return nil, graph.NodeError{NodeID: n.id, Status: http.StatusBadRequest, Err: fmt.Errorf("transform %q: from %q not found", n.id, n.opts.From)}
	}
	curr, ok := lookupEnvelope(env, n.opts.To)
	if !ok {
		return nil, graph.NodeError{NodeID: n.id, Status: http.StatusBadRequest, Err: fmt.Errorf("transform %q: to %q not found", n.id, n.opts.To)}
	}
	encoded, err := transform.ChangedFieldsJSON(prev, curr)
	if err != nil {
		return nil, graph.NodeError{NodeID: n.id, Status: http.StatusInternalServerError, Err: err}
	}
	name := n.opts.Name
	if name == "" {
		name = "changed_fields"
	}
	// Copy before writing: nodes never mutate their input (I19).
	vars := maps.Clone(env.Vars)
	if vars == nil {
		vars = map[string]string{}
	}
	vars[name] = encoded
	env.Vars = vars
	return env, nil
}

func (n transformNode) fieldMap(env graph.Envelope) (graph.Payload, error) {
	body := env.Body
	// Copy before writing: nodes never mutate their input (I19).
	varsOut := maps.Clone(env.Vars)
	if varsOut == nil {
		varsOut = map[string]string{}
	}
	for _, m := range n.opts.Mappings {
		val, ok := lookupEnvelope(env, m.From)
		if !ok {
			return nil, graph.NodeError{NodeID: n.id, Status: http.StatusBadRequest, Err: fmt.Errorf("transform %q: mapping from %q not found", n.id, m.From)}
		}
		target := m.To
		switch {
		case strings.HasPrefix(target, "vars."):
			varsOut[strings.TrimPrefix(target, "vars.")] = stringify(val)
		case strings.HasPrefix(target, "body."):
			next, err := transform.SetPath(body, strings.TrimPrefix(target, "body."), val)
			if err != nil {
				return nil, graph.NodeError{NodeID: n.id, Status: http.StatusBadRequest, Err: err}
			}
			body = next
		default:
			next, err := transform.SetPath(body, target, val)
			if err != nil {
				return nil, graph.NodeError{NodeID: n.id, Status: http.StatusBadRequest, Err: err}
			}
			body = next
		}
	}
	env.Body = body
	env.Vars = varsOut
	return env, nil
}

func lookupEnvelope(env graph.Envelope, path string) (any, bool) {
	path = strings.TrimSpace(path)
	switch {
	case path == "body":
		return env.Body, env.Body != nil
	case strings.HasPrefix(path, "body."):
		return transform.GetPath(env.Body, strings.TrimPrefix(path, "body."))
	case strings.HasPrefix(path, "vars."):
		v, ok := env.Vars[strings.TrimPrefix(path, "vars.")]
		return v, ok
	default:
		return transform.GetPath(env.Body, path)
	}
}

func stringify(v any) string {
	switch s := v.(type) {
	case string:
		return s
	case nil:
		return ""
	default:
		b, err := json.Marshal(s)
		if err != nil {
			return fmt.Sprint(v)
		}
		return string(b)
	}
}
