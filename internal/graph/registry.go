package graph

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// ErrDuplicateNodeType is returned when two NodeSpecs claim the same Type.
var ErrDuplicateNodeType = errors.New("graph: duplicate node type")

// Node is one step in a flow. Run transforms the inbound payload; returning an
// error aborts this branch and is reported per the fan-out failure policy.
//
// Implementations must be safe for concurrent use: one Node instance is built
// at config load and shared across every request. Successors currently run
// sequentially under stop_on_failure; different requests run concurrently.
// Keep per-request state in the Payload, never on the Node.
type Node interface {
	Run(ctx context.Context, in Payload) (Payload, error)
}

// PortDeclarer is implemented by nodes that expose labelled output ports
// (switch). Compile rejects labelled edges that are not in OutPorts.
type PortDeclarer interface {
	OutPorts() []string
}

// NodeConfig is one entry from a flow's `nodes:` list. Options holds the
// type-specific fields, decoded by the NodeSpec's New function.
type NodeConfig struct {
	ID      string
	Type    string
	Options yaml.Node
}

// NodeSpec declares a node type's port signature and how to build one.
type NodeSpec struct {
	// Type is the string used in config, e.g. "read_body".
	Type string
	// In is the payload this node consumes; PortNone marks a trigger.
	In PortType
	// Out is the payload it produces.
	Out PortType
	// Detached marks a destination: the executor runs it on a context
	// detached from the inbound request, bounded by publish_timeout, so a
	// sender disconnect cannot cancel work already committed to delivery
	// (I10).
	//
	// This is NOT inferrable from Out == PortTerminal: `respond` is Terminal
	// too and must stay on the request context, because it writes to the
	// caller's connection.
	Detached bool
	// New builds a node from its config. Returning an error fails the load.
	New func(NodeConfig) (Node, error)
}

// Registry maps node type names to their specs.
type Registry struct {
	specs map[string]NodeSpec
}

func NewRegistry() *Registry {
	return &Registry{specs: map[string]NodeSpec{}}
}

// Register adds a node type. It rejects incomplete specs and duplicates rather
// than overwriting, so a typo in a node type name fails the build instead of
// silently shadowing a real node (fail-closed, matching I11/I12).
func (r *Registry) Register(s NodeSpec) error {
	switch {
	case strings.TrimSpace(s.Type) == "":
		return errors.New("graph: NodeSpec.Type is empty")
	case s.New == nil:
		return fmt.Errorf("graph: NodeSpec %q has no New function", s.Type)
	case s.Out == PortNone:
		return fmt.Errorf("graph: NodeSpec %q declares Out=None; only In may be None", s.Type)
	case s.In > PortTerminal || s.Out > PortTerminal || s.In == PortTerminal:
		return fmt.Errorf("graph: NodeSpec %q has invalid ports %s -> %s", s.Type, s.In, s.Out)
	case s.Detached && s.Out != PortTerminal:
		return fmt.Errorf("graph: detached destination %q must produce Terminal", s.Type)
	}
	if r.specs == nil {
		r.specs = make(map[string]NodeSpec)
	}
	if _, exists := r.specs[s.Type]; exists {
		return fmt.Errorf("%w: %q", ErrDuplicateNodeType, s.Type)
	}
	r.specs[s.Type] = s
	return nil
}

// Lookup returns the spec for a node type.
func (r *Registry) Lookup(nodeType string) (NodeSpec, bool) {
	s, ok := r.specs[nodeType]
	return s, ok
}

// Types returns every registered type name, for error messages and for the
// config-UI schema endpoint.
func (r *Registry) Types() []string {
	out := make([]string, 0, len(r.specs))
	for t := range r.specs {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}
