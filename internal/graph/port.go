// Package graph implements Hookway's typed-port node graph: the compiler that
// turns a flow config into an executable DAG, and the executor that runs it.
//
// Each node declares the payload type it consumes and the one it produces.
// Edges are type-checked when the config loads, and the type lattice is chosen
// so that incompatible request phases are compile errors. Presence, filter
// ordering and executor invariants need additional checks. See docs/invariants.md.
package graph

import "fmt"

// PortType identifies the payload type carried on a graph edge. The values
// are the phases of a request, in order.
type PortType uint8

const (
	// PortNone is the input port of a trigger: it consumes nothing.
	PortNone PortType = iota
	// PortRequestHead carries method, path, query, headers, source IP and hop
	// count -- and deliberately NO body, so that gate nodes operating on it
	// provably run before the body is buffered.
	PortRequestHead
	// PortRawRequest carries a RequestHead plus raw, unverified body bytes.
	PortRawRequest
	// PortVerified carries a RawRequest whose signature has been checked
	// against the raw bytes.
	PortVerified
	// PortEnvelope carries a Verified payload plus the parsed body and
	// resolved variables. This is what publishes.
	PortEnvelope
	// PortTerminal marks a leaf: a response was written or a delivery
	// completed, and nothing flows onward.
	PortTerminal
)

func (p PortType) String() string {
	switch p {
	case PortNone:
		return "None"
	case PortRequestHead:
		return "RequestHead"
	case PortRawRequest:
		return "RawRequest"
	case PortVerified:
		return "Verified"
	case PortEnvelope:
		return "Envelope"
	case PortTerminal:
		return "Terminal"
	default:
		return fmt.Sprintf("PortType(%d)", uint8(p))
	}
}

// Accepts reports whether an input port of type p may take an edge from an
// output port of type out.
//
// This is STRICT EQUALITY, and deliberately so. PortRawRequest contains a
// RequestHead, so widening this to an assignability relation is the obvious
// refactor -- and it would let a gate node be wired downstream of read_body,
// which destroys invariants I1-I4. Do not "simplify" this. See
// TestRawRequestIsNotARequestHead.
func (p PortType) Accepts(out PortType) bool { return p == out }
