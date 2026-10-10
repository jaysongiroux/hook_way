package graph

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
)

var ErrAdmissionRecovery = errors.New("outbound recovery after protection denial")

// CompileError aggregates every problem found in one flow, so a single
// `hookway validate` run reports all of them rather than the first.
type CompileError struct {
	Flow     string
	Problems []string
	Causes   []error
	// ProblemFields associates a problem-list index with its original node ID.
	// A missing key means a flow-wide/structural issue. IDs are values, not
	// dotted paths: callers can resolve them through the original node list.
	ProblemFields map[int]string
}

func (e *CompileError) Unwrap() []error { return e.Causes }

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
//  7. every leaf produces PortTerminal, or is a SuccessContinuation with
//     zero success edges (optional single continuation after publish)
//  8. the graph is acyclic
//
// Check 5 enforces phase compatibility, preventing gates after read_body and
// Envelope creation without an auth transition in the trusted registry. Check 7
// rejects dangling branches. Types do not enforce gate presence, I8 filter
// ordering or I9 capture timing; these need policy/topology and executor checks.
func Compile(name string, fc FlowConfig, reg *Registry) (*Flow, error) {
	return compile(name, fc, reg, "")
}

// CompileChild compiles an explicitly seeded Envelope subflow. All ordinary
// edge, reachability, terminal and cycle checks still apply; triggers are forbidden.
func CompileChild(name string, fc FlowConfig, entry string, reg *Registry) (*Flow, error) {
	if !ValidID(entry) {
		return nil, fmt.Errorf("graph: child entry is required")
	}
	return compile(name, fc, reg, entry)
}
func compile(name string, fc FlowConfig, reg *Registry, entry string) (*Flow, error) {
	ce := &CompileError{Flow: name}
	ce.Problems = append(ce.Problems, ValidateResponses(fc.Responses)...)
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
	invalidNodes := false
	for _, nc := range fc.Nodes {
		if !validID(nc.ID) {
			invalidNodes = true
			ce.addNodeProblem(nc.ID, fmt.Sprintf("node id %q is empty or invalid", nc.ID))
		}
		if seenIDs[nc.ID] {
			invalidNodes = true
			ce.addNodeProblem(nc.ID, fmt.Sprintf("duplicate node id %q", nc.ID))
			continue
		}
		seenIDs[nc.ID] = true
		if nc.Type == "" {
			invalidNodes = true
			ce.addNodeProblem(nc.ID, fmt.Sprintf("node %q has no type", nc.ID))
			continue
		}
		spec, ok := reg.Lookup(nc.Type)
		if !ok {
			known := reg.Types()
			sort.Strings(known)
			invalidNodes = true
			ce.addNodeProblem(nc.ID,
				fmt.Sprintf("node %q: unknown node type %q (known types: %s)",
					nc.ID, nc.Type, strings.Join(known, ", ")))
			continue
		}
		node, err := construct(spec, nc)
		if err != nil {
			ce.Causes = append(ce.Causes, err)
			invalidNodes = true
			ce.addNodeProblem(nc.ID, fmt.Sprintf("node %q (%s): %v", nc.ID, nc.Type, err))
			continue
		}
		if spec.ResolvePorts != nil {
			spec.In, spec.Out, err = spec.ResolvePorts(nc)
			if err != nil || spec.In == PortNone || spec.Out == PortNone || spec.In >= PortTerminal || spec.Out >= PortTerminal {
				invalidNodes = true
				ce.addNodeProblem(nc.ID, "invalid configured node ports")
				continue
			}
		}
		target, err := configuredTarget(node)
		if err != nil {
			invalidNodes = true
			ce.Causes = append(ce.Causes, err)
			ce.addNodeProblem(nc.ID, fmt.Sprintf("node %q: %v", nc.ID, err))
			continue
		}
		f.nodes[nc.ID] = &bound{id: nc.ID, spec: spec, node: node, target: target}
		f.order = append(f.order, nc.ID)
	}

	// Structural checks require a completely bound graph. Failed nodes would
	// otherwise appear missing and produce misleading edge/reachability/leaf
	// errors. Still report independent raw edge syntax errors and every primary
	// node problem from this pass, alongside response/policy diagnostics.
	if invalidNodes {
		edges, err := ParseEdges(fc.Edges)
		if err != nil {
			ce.Problems = appendErrorProblems(ce.Problems, err)
		}
		// Missing declarations are independent authoring errors. Compare with
		// the original IDs, never the successfully constructed subset.
		seenEdges := map[Edge]bool{}
		for _, edge := range edges {
			if seenEdges[edge] {
				ce.Problems = append(ce.Problems, fmt.Sprintf("duplicate edge %s", edge))
				continue
			}
			seenEdges[edge] = true
			for _, id := range []string{edge.From, edge.To} {
				if !seenIDs[id] {
					ce.Problems = append(ce.Problems, fmt.Sprintf("edge %s: unknown node %q", edge, id))
				}
			}
		}
		return nil, ce
	}
	// Admission denial may render a local response, but its recovery path must
	// never dispatch work that bypasses the failed protection decision. Inspect
	// raw edges here as well: later checks still report malformed/unknown edges.
	allEdges, _ := ParseEdges(fc.Edges)
	adj := map[string][]string{}
	for _, edge := range allEdges {
		adj[edge.From] = append(adj[edge.From], edge.To)
	}
	for _, edge := range allEdges {
		if edge.FromPort != "on_error" || f.nodes[edge.From] == nil {
			continue
		}
		if _, guarded := f.nodes[edge.From].node.(AdmissionGuard); !guarded {
			continue
		}
		visited := map[string]bool{}
		var check func(string)
		check = func(id string) {
			if visited[id] {
				return
			}
			visited[id] = true
			b := f.nodes[id]
			if b == nil {
				return
			}
			_, outbound := b.node.(SideEffectTracker)
			_, child := b.node.(ChildExecutionNode)
			if b.spec.Detached || outbound || child {
				ce.Causes = append(ce.Causes, ErrAdmissionRecovery)
				ce.addNodeProblem(edge.From, fmt.Sprintf("protection on_error recovery must produce a local response; outbound node %q cannot bypass admission", id))
				return
			}
			for _, next := range adj[id] {
				check(next)
			}
		}
		check(edge.To)
	}

	// (3): exactly one trigger.
	var roots []string
	for _, id := range f.order {
		if f.nodes[id].spec.In == PortNone {
			roots = append(roots, id)
		}
	}
	switch {
	case entry != "":
		if len(roots) != 0 {
			ce.Problems = append(ce.Problems, "child flows cannot contain triggers")
		}
		if b := f.nodes[entry]; b == nil || b.spec.In != PortEnvelope {
			ce.Problems = append(ce.Problems, "child entry must consume Envelope")
		} else {
			f.root = entry
		}
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
	if entry != "" && inbound[entry] != 0 {
		ce.Problems = append(ce.Problems, "child entry cannot have incoming edges")
	}
	for _, id := range f.order {
		if inbound[id] > 1 && !f.nodes[id].spec.AllowMultipleInputs {
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

	// (7): leaves must be Terminal (or a SuccessContinuation with no success
	// edge), and non-terminals need a success path unless they are a
	// SuccessContinuation leaf that only has on_error edges.
	for _, id := range f.order {
		spec := f.nodes[id].spec
		if len(f.out[id]) == 0 && spec.Out != PortTerminal && !spec.SuccessContinuation && !spec.AllowLeaf {
			ce.Problems = append(ce.Problems, fmt.Sprintf(
				"node %q (%s) has no outgoing edge but produces %s, not Terminal: "+
					"events reaching it would be silently dropped",
				id, spec.Type, spec.Out))
			continue
		}
		if spec.Out == PortTerminal {
			continue
		}
		hasSuccess := false
		for _, e := range f.out[id] {
			if e.FromPort != "on_error" {
				hasSuccess = true
				break
			}
		}
		if !hasSuccess && len(f.out[id]) > 0 && !spec.SuccessContinuation && !spec.AllowLeaf {
			ce.Problems = append(ce.Problems, fmt.Sprintf(
				"node %q (%s) has only on_error successors; a success path is required",
				id, spec.Type))
		}
	}

	// SuccessContinuation: at most one unlabeled success edge, and respond
	// must not be reachable along that path (respond writes the caller
	// connection and is not detached).
	for _, id := range f.order {
		if !f.nodes[id].spec.SuccessContinuation {
			continue
		}
		success := successEdges(f.out[id])
		if len(success) > 1 {
			ce.Problems = append(ce.Problems, fmt.Sprintf(
				"node %q (%s) has %d success edges; SuccessContinuation allows at most one",
				id, f.nodes[id].spec.Type, len(success)))
		}
		for _, edge := range success {
			if edge.FromPort != "" {
				ce.Problems = append(ce.Problems, fmt.Sprintf(
					"node %q (%s): SuccessContinuation success edge must be unlabeled, got port %q",
					id, f.nodes[id].spec.Type, edge.FromPort))
			}
		}
		if respondID := f.successReachableType(id, "respond"); respondID != "" {
			ce.Problems = append(ce.Problems, fmt.Sprintf(
				"node %q (%s): respond node %q is reachable via a success continuation; respond is not detached",
				id, f.nodes[id].spec.Type, respondID))
		}
		if batchID := f.successReachableType(id, "batch"); batchID != "" {
			ce.Problems = append(ce.Problems, fmt.Sprintf(
				"node %q (%s): batch response node %q is reachable via a success continuation; batch is not detached",
				id, f.nodes[id].spec.Type, batchID))
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

func (e *CompileError) addNodeProblem(id, problem string) {
	if e.ProblemFields == nil {
		e.ProblemFields = make(map[int]string)
	}
	e.ProblemFields[len(e.Problems)] = id
	e.Problems = append(e.Problems, problem)
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

// successReachableType walks only unlabeled success edges from id and returns
// the first node whose type matches, or "" if none. on_error is not followed.
func (f *Flow) successReachableType(id, nodeType string) string {
	seen := map[string]bool{}
	var walk func(string) string
	walk = func(cur string) string {
		for _, e := range successEdges(f.out[cur]) {
			if seen[e.To] {
				continue
			}
			seen[e.To] = true
			if f.nodes[e.To].spec.Type == nodeType {
				return e.To
			}
			if found := walk(e.To); found != "" {
				return found
			}
		}
		return ""
	}
	return walk(id)
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
