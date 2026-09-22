package flowconfig

import (
	"context"
	"fmt"
	"net/http"

	"github.com/jaysongiroux/hook_way/internal/filter"
	"github.com/jaysongiroux/hook_way/internal/graph"
)

// Port names for the two branches. Both must be wired: the compiler requires
// exactly one successor per declared port, so an ignored branch is an explicit
// edge to a terminal node rather than a dangling handle.
const (
	ifElsePortTrue  = "true"
	ifElsePortFalse = "false"
)

type ifElseNode struct {
	id    string
	chain filter.Chain
}

func newIfElseNode(id string, opts IfElseOptions) graph.Node {
	return ifElseNode{id: id, chain: compileConditionSet(opts.set())}
}

func (n ifElseNode) OutPorts() []string {
	return []string{ifElsePortTrue, ifElsePortFalse}
}

func (n ifElseNode) Run(_ context.Context, in graph.Payload) (graph.Payload, error) {
	env, ok := in.(graph.Envelope)
	if !ok {
		return nil, graph.NodeError{
			NodeID: n.id, Status: http.StatusInternalServerError,
			Err: fmt.Errorf("if_else %q: expected Envelope, got %T", n.id, in),
		}
	}
	env.Branch = ifElsePortFalse
	if matched, _ := n.chain.Match(eventHeaders(env), env.Body); matched {
		env.Branch = ifElsePortTrue
	}
	return env, nil
}
