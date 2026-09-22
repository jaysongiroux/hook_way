package graph

import (
	"context"
	"sync"
	"testing"
)

// recordingNode appends its id to a shared log when it runs, so tests can
// assert execution order and concurrency. The mutex matters: fan-out runs
// siblings in parallel and the race detector is on in CI (make test-race).
type recordingNode struct {
	id  string
	mu  *sync.Mutex
	log *[]string
	err error
	out Payload
}

func (n *recordingNode) Run(_ context.Context, in Payload) (Payload, error) {
	n.mu.Lock()
	*n.log = append(*n.log, n.id)
	n.mu.Unlock()
	if n.err != nil {
		return nil, n.err
	}
	if n.out != nil {
		return n.out, nil
	}
	return in, nil
}

// recorder builds recordingNodes that share one log.
type recorder struct {
	mu  sync.Mutex
	log []string
}

func (r *recorder) node(id string) *recordingNode {
	return &recordingNode{id: id, mu: &r.mu, log: &r.log}
}

func (r *recorder) entries() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.log...)
}

// passthroughSpec builds a NodeSpec whose node returns the payload the port
// signature promises, so compile-time and run-time types agree.
func passthroughSpec(name string, in, out PortType) NodeSpec {
	return NodeSpec{
		Type: name, In: in, Out: out,
		New: func(nc NodeConfig) (Node, error) {
			return &staticNode{id: nc.ID, out: zeroPayload(out)}, nil
		},
	}
}

type staticNode struct {
	id  string
	out Payload
}

func (n *staticNode) Run(_ context.Context, _ Payload) (Payload, error) { return n.out, nil }

// zeroPayload returns an empty payload of the given port type.
func zeroPayload(p PortType) Payload {
	switch p {
	case PortRequestHead:
		return RequestHead{}
	case PortRawRequest:
		return RawRequest{}
	case PortVerified:
		return Verified{}
	case PortEnvelope:
		return Envelope{}
	default:
		return Terminal{}
	}
}

// testRegistry provides one node type per transition in the real lattice, so
// compile tests exercise the same shapes production configs will.
func testRegistry(t *testing.T) *Registry {
	t.Helper()
	r := NewRegistry()
	specs := []NodeSpec{
		passthroughSpec("trigger", PortNone, PortRequestHead),
		passthroughSpec("gate", PortRequestHead, PortRequestHead),
		passthroughSpec("read_body", PortRequestHead, PortRawRequest),
		passthroughSpec("auth", PortRawRequest, PortVerified),
		passthroughSpec("envelope", PortVerified, PortEnvelope),
		passthroughSpec("process", PortEnvelope, PortEnvelope),
		passthroughSpec("respond", PortEnvelope, PortTerminal),
	}
	dest := NodeSpec{
		Type: "dest", In: PortEnvelope, Out: PortEnvelope,
		Detached: true, SuccessContinuation: true,
		New: func(nc NodeConfig) (Node, error) {
			// Runtime return stays Terminal (delivery record); declared Out is
			// Envelope so SuccessContinuation edges type-check.
			return &staticNode{id: nc.ID, out: Terminal{}}, nil
		},
	}
	specs = append(specs, dest)

	for _, s := range specs {
		if err := r.Register(s); err != nil {
			t.Fatalf("Register(%s) = %v", s.Type, err)
		}
	}
	return r
}
