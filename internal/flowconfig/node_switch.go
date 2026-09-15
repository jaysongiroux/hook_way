package flowconfig

import (
	"context"
	"fmt"
	"net/http"

	"github.com/jasongiroux/hookway/internal/filter"
	"github.com/jasongiroux/hookway/internal/graph"
)

type switchNode struct {
	id      string
	opts    SwitchOptions
	chains  []filter.Chain
	ports   []string
	defPort string
}

func newSwitchNode(id string, opts SwitchOptions) graph.Node {
	n := switchNode{id: id, opts: opts, defPort: opts.Default}
	for _, c := range opts.Cases {
		ch, issues := filter.Compile([]filter.Spec{{
			Source: c.When.Source, Path: c.When.Path, Operator: c.When.Operator, Value: c.When.Value,
		}})
		if len(issues) > 0 {
			ch = filter.MatchNothing()
		}
		n.chains = append(n.chains, ch)
		n.ports = append(n.ports, c.Port)
	}
	return n
}

func (n switchNode) OutPorts() []string {
	out := append([]string(nil), n.ports...)
	if n.defPort != "" {
		out = append(out, n.defPort)
	}
	return out
}

func (n switchNode) Run(_ context.Context, in graph.Payload) (graph.Payload, error) {
	env, ok := in.(graph.Envelope)
	if !ok {
		return nil, graph.NodeError{
			NodeID: n.id, Status: http.StatusInternalServerError,
			Err: fmt.Errorf("switch %q: expected Envelope, got %T", n.id, in),
		}
	}
	headers := env.V.Raw.Head.Header
	for i, ch := range n.chains {
		if ok, _ := ch.Match(headers, env.Body); ok {
			env.Branch = n.ports[i]
			return env, nil
		}
	}
	if n.defPort != "" {
		env.Branch = n.defPort
		return env, nil
	}
	return nil, graph.NodeError{
		NodeID: n.id, Status: http.StatusBadRequest,
		Err: fmt.Errorf("switch %q: no case matched and no default", n.id),
	}
}
