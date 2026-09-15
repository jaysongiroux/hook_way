package graph

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"
)

// NodeEvent contains only config-bounded identity, suitable for metric labels.
type NodeEvent struct {
	NodeID, NodeType string
	Duration         time.Duration
	Err              error
}

type RunOptions struct {
	PublishTimeout time.Duration
	// FanoutConcurrency is reserved for skip_failed. stop_on_failure is sequential.
	FanoutConcurrency int
	// OnNode runs once after each attempted node, including failures and panics.
	// An observer panic is isolated: instrumentation must not alter delivery.
	OnNode func(NodeEvent)
}

// DeliveryTarget is optional destination metadata. Return the configured template,
// never the request's rendered target. It also describes unattempted destinations.
type DeliveryTarget interface{ DeliveryTarget() string }

// Run executes successors in edge order, stopping on the first failure. Nodes are
// shared between requests; all per-request state belongs to this execution.
// Terminal remains populated on error. Run never invokes compensation itself.
func (f *Flow) Run(ctx context.Context, in Payload, opts RunOptions) (term Terminal, err error) {
	if opts.PublishTimeout <= 0 {
		return term, errors.New("graph: RunOptions.PublishTimeout must be positive")
	}
	if f == nil || f.nodes[f.root] == nil {
		return term, errors.New("graph: uncompiled flow")
	}
	root := f.nodes[f.root]
	if in == nil {
		return term, errors.New("graph: nil root payload")
	}
	if _, err := concretePort(in); err != nil {
		return term, err
	}
	if in.PortType() != root.spec.Out {
		return term, fmt.Errorf("graph: trigger %q produces %s but was seeded with %s", f.root, root.spec.Out, in.PortType())
	}
	seed, err := clonePayload(in)
	if err != nil {
		return term, err
	}
	e := execution{flow: f, opts: opts, visited: map[string]bool{}, suppressed: map[string]bool{}}
	err = e.node(ctx, f.root, seed)
	if err != nil {
		// Node order is stable and unattempted destinations never masquerade as success.
		for _, id := range f.order {
			b := f.nodes[id]
			if b.spec.Detached && !e.visited[id] && !e.suppressed[id] {
				e.term.Deliveries = append(e.term.Deliveries, e.delivery(b, nil, true))
			}
		}
	}
	slices.Reverse(e.term.Compensations)
	return e.term, err
}

type execution struct {
	flow                *Flow
	opts                RunOptions
	term                Terminal
	visited, suppressed map[string]bool
}

func (e *execution) delivery(b *bound, err error, unattempted bool) Delivery {
	d := Delivery{NodeID: b.id, Err: err, Unattempted: unattempted}
	d.Target = b.target
	return d
}
func (e *execution) suppress(id string) {
	for _, edge := range e.flow.out[id] {
		e.suppressed[edge.To] = true
		e.suppress(edge.To)
	}
}
func (e *execution) node(ctx context.Context, id string, in Payload) (err error) {
	b := e.flow.nodes[id]
	var saved Payload
	comp, isComp := b.node.(Compensator)
	if isComp {
		saved, err = clonePayload(in)
		if err != nil {
			return fmt.Errorf("node %q compensation input: %w", id, err)
		}
	}
	e.visited[id] = true
	nctx := ctx
	cancel := func() {}
	if b.spec.Detached {
		nctx, cancel = context.WithTimeout(context.WithoutCancel(ctx), e.opts.PublishTimeout)
	}
	started := time.Now()
	out, runErr := invoke(nctx, b.node, in)
	cancel()
	err = runErr
	terminal, isTerminal := out.(Terminal)
	if err == nil {
		_, payloadErr := concretePort(out)
		switch {
		case out == nil:
			err = errors.New("node returned nil payload")
		case payloadErr != nil:
			err = payloadErr
		case isTerminal: // Explicit successful branch stop is valid at any phase.
		case out.PortType() != b.spec.Out:
			err = fmt.Errorf("returned %s, declared output %s", out.PortType(), b.spec.Out)
		}
	}
	if isTerminal {
		if terminal.Response != nil && !b.spec.Detached {
			if e.term.Response != nil {
				err = errors.Join(err, errors.New("more than one response produced"))
			} else {
				e.term.Response = terminal.Response
			}
		}
		if b.spec.Detached {
			for _, outcome := range terminal.Deliveries {
				// Identity, configured target, and attempted state are executor-owned.
				// A destination controls only its outcome error.
				d := e.delivery(b, outcome.Err, false)
				if d.Err == nil && runErr != nil {
					d.Err = runErr
				}
				e.term.Deliveries = append(e.term.Deliveries, d)
				if outcome.Err != nil && runErr == nil {
					err = errors.Join(err, d.Err)
				}
			}
		}
		// D12: only executor-collected Compensator instances are authorized.
		// Node-returned payloads cannot inject release callbacks.
	}
	if b.spec.Detached && (!isTerminal || len(terminal.Deliveries) == 0) {
		e.term.Deliveries = append(e.term.Deliveries, e.delivery(b, err, false))
	}
	observe(e.opts.OnNode, NodeEvent{NodeID: id, NodeType: b.spec.Type, Duration: time.Since(started), Err: err})
	// A successful reservation must be released even when its output is malformed.
	if runErr == nil && isComp {
		e.term.Compensations = append(e.term.Compensations, Compensation{NodeID: id, Node: comp, Input: saved})
	}
	if err != nil {
		if handled := e.followOnError(ctx, id, in); handled == nil {
			return nil
		} else if !errors.Is(handled, errNoOnError) {
			return handled
		}
		return fmt.Errorf("node %q (%s): %w", id, b.spec.Type, err)
	}
	if isTerminal {
		e.suppress(id)
		return nil
	}
	return e.followSuccessors(ctx, id, out)
}

func concretePort(p Payload) (PortType, error) {
	switch p.(type) {
	case RequestHead, RawRequest, Verified, Envelope, Terminal:
		return p.PortType(), nil
	default:
		return PortNone, fmt.Errorf("graph: unsupported payload %T; payloads must be concrete values", p)
	}
}
func invoke(ctx context.Context, n Node, in Payload) (out Payload, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panicked: %v", r)
		}
	}()
	return n.Run(ctx, in)
}
func observe(fn func(NodeEvent), ev NodeEvent) {
	if fn == nil {
		return
	}
	defer func() { _ = recover() }()
	fn(ev)
}

func selectedBranch(out Payload) string {
	if env, ok := out.(Envelope); ok {
		return env.Branch
	}
	return ""
}

var errNoOnError = errors.New("graph: no on_error successor")

func (e *execution) followOnError(ctx context.Context, id string, in Payload) error {
	var edges []Edge
	for _, edge := range e.flow.out[id] {
		if edge.FromPort == "on_error" {
			edges = append(edges, edge)
		}
	}
	if len(edges) == 0 {
		return errNoOnError
	}
	for _, edge := range edges {
		next := in
		if len(edges) > 1 {
			cloned, err := clonePayload(in)
			if err != nil {
				return fmt.Errorf("node %q on_error: %w", id, err)
			}
			next = cloned
		}
		if err := e.node(ctx, edge.To, next); err != nil {
			return err
		}
	}
	return nil
}

func (e *execution) followSuccessors(ctx context.Context, id string, out Payload) error {
	successors := successEdges(e.flow.out[id])
	if selected := selectedBranch(out); selected != "" || labelledSuccessors(successors) {
		var matched []Edge
		for _, edge := range successors {
			if edge.FromPort == selected || (selected == "" && edge.FromPort == "") {
				matched = append(matched, edge)
			}
		}
		if len(matched) == 0 {
			b := e.flow.nodes[id]
			return fmt.Errorf("node %q (%s): no successor for port %q", id, b.spec.Type, selected)
		}
		successors = matched
	}
	if e.flow.skipFailed && allDetached(e.flow, successors) {
		return e.followSkipFailed(ctx, id, out, successors)
	}
	for _, edge := range successors {
		next := out
		var err error
		if len(successors) > 1 {
			next, err = clonePayload(out)
			if err != nil {
				return fmt.Errorf("node %q fan-out: %w", id, err)
			}
		}
		if err = e.node(ctx, edge.To, next); err != nil {
			return err
		}
	}
	return nil
}

func (e *execution) followSkipFailed(ctx context.Context, id string, out Payload, successors []Edge) error {
	var firstErr error
	var ok int
	for _, edge := range successors {
		next := out
		var err error
		if len(successors) > 1 {
			next, err = clonePayload(out)
			if err != nil {
				return fmt.Errorf("node %q fan-out: %w", id, err)
			}
		}
		if err = e.node(ctx, edge.To, next); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		ok++
	}
	if ok == 0 {
		return firstErr
	}
	return nil
}

func successEdges(edges []Edge) []Edge {
	var out []Edge
	for _, e := range edges {
		if e.FromPort != "on_error" {
			out = append(out, e)
		}
	}
	return out
}

func allDetached(f *Flow, edges []Edge) bool {
	if len(edges) == 0 {
		return false
	}
	for _, e := range edges {
		if !f.nodes[e.To].spec.Detached {
			return false
		}
	}
	return true
}

func labelledSuccessors(edges []Edge) bool {
	for _, e := range edges {
		if e.FromPort != "" && e.FromPort != "on_error" {
			return true
		}
	}
	return false
}

// Compensator must derive its reservation from Input, never shared node state.
type Compensator interface {
	Compensate(context.Context, Payload, error) error
}
type Compensation struct {
	NodeID string
	Node   Compensator
	Input  Payload
}

func NeedsCompensation(term Terminal, err error) bool {
	if err != nil {
		return true
	}
	for _, d := range term.Deliveries {
		if d.Err != nil {
			return true
		}
	}
	return false
}

// RunCompensations consumes the reverse execution order returned by Run. Context
// detachment retains request values, with one deadline bounding the release batch.
func RunCompensations(ctx context.Context, term Terminal, reason error, timeout time.Duration) error {
	if len(term.Compensations) == 0 {
		return nil
	}
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()
	var errs []error
	for _, c := range term.Compensations {
		errs = append(errs, runOneCompensation(cctx, c, reason))
	}
	return errors.Join(errs...)
}
func runOneCompensation(ctx context.Context, c Compensation, reason error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("compensation for node %q panicked: %v", c.NodeID, r)
		}
	}()
	if err := c.Node.Compensate(ctx, c.Input, reason); err != nil {
		return fmt.Errorf("compensation for node %q: %w", c.NodeID, err)
	}
	return nil
}
