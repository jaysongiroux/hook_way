package graph

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// CompileError aggregates every problem found in one flow, so a single
// `hookway validate` run reports all of them rather than the first.
type CompileError struct {
	Flow     string
	Problems []string
}

func (e *CompileError) Error() string {
	return fmt.Sprintf("flow %q: %d problem(s):\n  - %s",
		e.Flow, len(e.Problems), strings.Join(e.Problems, "\n  - "))
}

// bound is a node config paired with its spec and constructed instance.
type bound struct {
	id     string
	spec   NodeSpec
	node   Node
	target string
}

// Flow is a compiled, executable graph. It is immutable after Compile and safe
// for concurrent use across requests.
type Flow struct {
	name       string
	root       string
	nodes      map[string]*bound
	out        map[string][]Edge
	order      []string // deterministic node order, for stable error and metric output
	skipFailed bool
}

func (f *Flow) Name() string { return f.name }
func (f *Flow) Root() string { return f.root }

func (f *Flow) NodeIDs() []string { return append([]string(nil), f.order...) }

// Compile turns a flow config into an executable graph, or reports every
// structural problem it finds. Checks, in order:
//
//  1. every node type is registered
//  2. node ids are unique
//  3. exactly one trigger (In == PortNone) exists
//  4. every edge endpoint names a real node
//  5. every edge is type-compatible: to.In.Accepts(from.Out). on_error is
//     different: the error path carries the failing node's input, so the
//     target must Accepts(from.In).
//  6. every non-root node is reachable from the root
//  7. every leaf produces PortTerminal
//  8. the graph is acyclic
//
// Check 5 enforces phase compatibility, preventing gates after read_body and
// Envelope creation without an auth transition in the trusted registry. Check 7
// rejects dangling branches. Types do not enforce gate presence, I8 filter
// ordering or I9 capture timing; these need policy/topology and executor checks.
func Compile(name string, fc FlowConfig, reg *Registry) (*Flow, error) {
	ce := &CompileError{Flow: name}
	f := &Flow{name: name, nodes: map[string]*bound{}, out: map[string][]Edge{}}
	if reg == nil {
		ce.Problems = append(ce.Problems, "node registry is nil")
		return nil, ce
	}
	switch strings.TrimSpace(fc.DeliveryFailurePolicy) {
	case "", "stop_on_failure":
	case "skip_failed":
		f.skipFailed = true
	default:
		ce.Problems = append(ce.Problems, fmt.Sprintf("delivery_failure_policy %q is not supported (use stop_on_failure or skip_failed)", fc.DeliveryFailurePolicy))
	}

	// (1) and (2): track identity independently from successful construction.
	seenIDs := map[string]bool{}
	for _, nc := range fc.Nodes {
		if !validID(nc.ID) {
			ce.Problems = append(ce.Problems, fmt.Sprintf("node id %q is empty or invalid", nc.ID))
		}
		if seenIDs[nc.ID] {
			ce.Problems = append(ce.Problems, fmt.Sprintf("duplicate node id %q", nc.ID))
			continue
		}
		seenIDs[nc.ID] = true
		if nc.Type == "" {
			ce.Problems = append(ce.Problems, fmt.Sprintf("node %q has no type", nc.ID))
			continue
		}
		spec, ok := reg.Lookup(nc.Type)
		if !ok {
			known := reg.Types()
			sort.Strings(known)
			ce.Problems = append(ce.Problems,
				fmt.Sprintf("node %q: unknown node type %q (known types: %s)",
					nc.ID, nc.Type, strings.Join(known, ", ")))
			continue
		}
		node, err := construct(spec, nc)
		if err != nil {
			ce.Problems = append(ce.Problems, fmt.Sprintf("node %q (%s): %v", nc.ID, nc.Type, err))
			continue
		}
		target, err := configuredTarget(node)
		if err != nil {
			ce.Problems = append(ce.Problems, fmt.Sprintf("node %q: %v", nc.ID, err))
			continue
		}
		f.nodes[nc.ID] = &bound{id: nc.ID, spec: spec, node: node, target: target}
		f.order = append(f.order, nc.ID)
	}

	// (3): exactly one trigger.
	var roots []string
	for _, id := range f.order {
		if f.nodes[id].spec.In == PortNone {
			roots = append(roots, id)
		}
	}
	switch {
	case len(roots) == 0:
		ce.Problems = append(ce.Problems, "no trigger node (none has an input port of None)")
	case len(roots) > 1:
		sort.Strings(roots)
		ce.Problems = append(ce.Problems,
			fmt.Sprintf("more than one trigger node: %s", strings.Join(roots, ", ")))
	default:
		f.root = roots[0]
	}

	// (4) and (5): endpoints exist and are type-compatible.
	edges, err := ParseEdges(fc.Edges)
	if err != nil {
		ce.Problems = appendErrorProblems(ce.Problems, err)
	}
	inbound := map[string]int{}
	seenEdges := map[Edge]bool{}
	for _, e := range edges {
		if seenEdges[e] {
			ce.Problems = append(ce.Problems, fmt.Sprintf("duplicate edge %s", e))
			continue
		}
		seenEdges[e] = true
		from, fromOK := f.nodes[e.From]
		to, toOK := f.nodes[e.To]
		if !fromOK {
			ce.Problems = append(ce.Problems, fmt.Sprintf("edge %s: unknown node %q", e, e.From))
		}
		if !toOK {
			ce.Problems = append(ce.Problems, fmt.Sprintf("edge %s: unknown node %q", e, e.To))
			continue
		}
		if !fromOK {
			continue
		}
		if e.FromPort != "" && e.FromPort != "on_error" {
			declared := map[string]bool{}
			if d, ok := from.node.(PortDeclarer); ok {
				for _, p := range d.OutPorts() {
					declared[p] = true
				}
			}
			if len(declared) == 0 {
				ce.Problems = append(ce.Problems, fmt.Sprintf("edge %s: labelled outputs are not supported on %s", e, from.spec.Type))
			} else if !declared[e.FromPort] {
				ce.Problems = append(ce.Problems, fmt.Sprintf("edge %s: port %q is not declared by %q", e, e.FromPort, e.From))
			}
		}
		if e.FromPort == "on_error" {
			if from.spec.In == PortNone {
				ce.Problems = append(ce.Problems, fmt.Sprintf("edge %s: trigger nodes cannot declare on_error", e))
			} else if !to.spec.In.Accepts(from.spec.In) {
				ce.Problems = append(ce.Problems, fmt.Sprintf(
					"edge %s: on_error type mismatch -- %q consumes %s so the error path carries %s, but %q consumes %s",
					e, e.From, from.spec.In, from.spec.In, e.To, to.spec.In))
				continue
			}
		} else if from.spec.Out == PortTerminal {
			ce.Problems = append(ce.Problems, fmt.Sprintf("edge %s: Terminal node cannot have successors", e))
		} else if !to.spec.In.Accepts(from.spec.Out) {
			ce.Problems = append(ce.Problems, fmt.Sprintf(
				"edge %s: type mismatch -- %q (%s) produces %s but %q (%s) consumes %s",
				e, e.From, from.spec.Type, from.spec.Out, e.To, to.spec.Type, to.spec.In))
			continue
		}
		f.out[e.From] = append(f.out[e.From], e)
		inbound[e.To]++
	}
	for _, id := range f.order {
		if inbound[id] > 1 {
			ce.Problems = append(ce.Problems, fmt.Sprintf("node %q has multiple incoming edges: reconvergence needs explicit merge or exclusive-branch semantics", id))
		}
	}

	// (6): reachability from the root.
	if f.root != "" {
		seen := map[string]bool{}
		f.walk(f.root, seen)
		for _, id := range f.order {
			if id != f.root && !seen[id] {
				ce.Problems = append(ce.Problems,
					fmt.Sprintf("node %q is unreachable from trigger %q", id, f.root))
			}
		}
	}

	// (7): leaves must be Terminal, and non-terminals need a success path.
	for _, id := range f.order {
		if len(f.out[id]) == 0 && f.nodes[id].spec.Out != PortTerminal {
			ce.Problems = append(ce.Problems, fmt.Sprintf(
				"node %q (%s) has no outgoing edge but produces %s, not Terminal: "+
					"events reaching it would be silently dropped",
				id, f.nodes[id].spec.Type, f.nodes[id].spec.Out))
			continue
		}
		if f.nodes[id].spec.Out == PortTerminal {
			continue
		}
		hasSuccess := false
		for _, e := range f.out[id] {
			if e.FromPort != "on_error" {
				hasSuccess = true
				break
			}
		}
		if !hasSuccess && len(f.out[id]) > 0 {
			ce.Problems = append(ce.Problems, fmt.Sprintf(
				"node %q (%s) has only on_error successors; a success path is required",
				id, f.nodes[id].spec.Type))
		}
	}

	// A selector emits exactly one successor per declared label. Fan-out
	// belongs on the selected path, not on the selector's labelled output.
	for _, id := range f.order {
		selector, ok := f.nodes[id].node.(PortDeclarer)
		if !ok {
			continue
		}
		counts := map[string]int{}
		for _, edge := range f.out[id] {
			if edge.FromPort == "on_error" {
				continue
			}
			counts[edge.FromPort]++
		}
		if counts[""] > 0 {
			ce.Problems = append(ce.Problems, fmt.Sprintf("node %q requires labelled successors", id))
		}
		for _, port := range selector.OutPorts() {
			if counts[port] != 1 {
				ce.Problems = append(ce.Problems, fmt.Sprintf("node %q port %q requires exactly one successor", id, port))
			}
		}
	}

	// (8): acyclicity.
	if cycle := f.findCycle(); cycle != nil {
		ce.Problems = append(ce.Problems, fmt.Sprintf("cycle: %s", strings.Join(cycle, " -> ")))
	}

	if len(ce.Problems) > 0 {
		return nil, ce
	}
	return f, nil
}

// appendErrorProblems flattens errors.Join so each independently actionable
// diagnostic remains a separate CompileError problem and renders as its own
// bullet with an accurate problem count.
func appendErrorProblems(problems []string, err error) []string {
	if err == nil {
		return problems
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		for _, child := range joined.Unwrap() {
			problems = appendErrorProblems(problems, child)
		}
		return problems
	}
	return append(problems, err.Error())
}

func configuredTarget(node Node) (target string, err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("destination metadata panicked: %v", p)
		}
	}()
	if n, ok := node.(DeliveryTarget); ok {
		target = n.DeliveryTarget()
	}
	return target, nil
}

// Constructors are extension points; a bad one must fail the load, not crash it.
func construct(spec NodeSpec, config NodeConfig) (node Node, err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("constructor panicked: %v", p)
		}
	}()
	node, err = spec.New(config)
	if err == nil && nilNode(node) {
		err = fmt.Errorf("constructor returned nil node")
	}
	return node, err
}

func nilNode(node Node) bool {
	if node == nil {
		return true
	}
	v := reflect.ValueOf(node)
	switch v.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Func, reflect.Interface, reflect.Chan:
		return v.IsNil()
	default:
		return false
	}
}

func (f *Flow) walk(id string, seen map[string]bool) {
	for _, e := range f.out[id] {
		if seen[e.To] {
			continue
		}
		seen[e.To] = true
		f.walk(e.To, seen)
	}
}

// findCycle returns one cycle as a node path, or nil. Iterative-free DFS with
// three colours: unvisited, on the current stack, done.
func (f *Flow) findCycle() []string {
	const (
		white = 0
		grey  = 1
		black = 2
	)
	colour := map[string]int{}
	var stack []string
	var cycle []string

	var visit func(string) bool
	visit = func(id string) bool {
		colour[id] = grey
		stack = append(stack, id)
		for _, e := range f.out[id] {
			switch colour[e.To] {
			case grey:
				for i, s := range stack {
					if s == e.To {
						cycle = append(append([]string(nil), stack[i:]...), e.To)
						return true
					}
				}
				cycle = []string{e.To, e.To}
				return true
			case white:
				if visit(e.To) {
					return true
				}
			}
		}
		stack = stack[:len(stack)-1]
		colour[id] = black
		return false
	}

	for _, id := range f.order {
		if colour[id] == white && visit(id) {
			return cycle
		}
	}
	return nil
}
