package graph

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"
)

// NodeEvent contains only config-bounded identity, suitable for metric labels.
type NodeEvent struct {
	NodeID, NodeType string
	Target           string
	Dropped          bool
	Duration         time.Duration
	Err              error
}

type RunOptions struct {
	PublishTimeout time.Duration
	// FanoutConcurrency bounds concurrent destination publishes for
	// skip_failed fan-out, matching the publishRoutesSkipFailed worker pool.
	// stop_on_failure stays sequential: it must not attempt a destination
	// after an earlier one failed. <= 0 means 8.
	FanoutConcurrency int
	// OnNode runs once after each attempted node, including failures and panics.
	// An observer panic is isolated: instrumentation must not alter delivery.
	OnNode func(NodeEvent)
	// BeforeDetached runs once before the first detached node's publish-timeout
	// context is created. Used for replay capture INSERT (I9). Panics are isolated.
	BeforeDetached func(Payload)
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
	e := &execution{flow: f, opts: opts, visited: map[string]bool{}, suppressed: map[string]bool{}}
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
	flow *Flow
	opts RunOptions

	// mu guards every field below. skip_failed fan-out runs sibling
	// destination publishes concurrently, so all per-request state is
	// touched from multiple goroutines. stop_on_failure is sequential and
	// pays only an uncontended lock.
	mu                  sync.Mutex
	term                Terminal
	visited, suppressed map[string]bool
	beforeDetached      sync.Once
}

func (e *execution) markVisited(id string) {
	e.mu.Lock()
	e.visited[id] = true
	e.mu.Unlock()
}

func (e *execution) addDelivery(d Delivery) {
	e.mu.Lock()
	e.term.Deliveries = append(e.term.Deliveries, d)
	e.mu.Unlock()
}

func (e *execution) addCompensation(c Compensation) {
	e.mu.Lock()
	e.term.Compensations = append(e.term.Compensations, c)
	e.mu.Unlock()
}

// setResponse records a respond node's reply, reporting an error if one was
// already recorded. At most one response may be written per request.
func (e *execution) setResponse(r *Response) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.term.Response != nil {
		return errors.New("more than one response produced")
	}
	e.term.Response = r
	return nil
}

func (e *execution) fanoutLimit() int {
	if e.opts.FanoutConcurrency <= 0 {
		return 8
	}
	return e.opts.FanoutConcurrency
}

func (e *execution) delivery(b *bound, err error, unattempted bool) Delivery {
	d := Delivery{NodeID: b.id, Err: err, Unattempted: unattempted}
	d.Target = b.target
	return d
}
func (e *execution) suppress(id string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.suppressLocked(id)
}

func (e *execution) suppressBranch(id string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.suppressed[id] = true
	e.suppressLocked(id)
}

func (e *execution) suppressLocked(id string) {
	for _, edge := range e.flow.out[id] {
		e.suppressed[edge.To] = true
		e.suppressLocked(edge.To)
	}
}
func (e *execution) node(ctx context.Context, id string, in Payload) (err error) {
	b := e.flow.nodes[id]
	errorInput := in
	for _, edge := range e.flow.out[id] {
		if edge.FromPort == "on_error" {
			errorInput, err = clonePayload(in)
			if err != nil {
				return fmt.Errorf("node %q error input: %w", id, err)
			}
			break
		}
	}
	var saved Payload
	comp, isComp := b.node.(Compensator)
	if isComp {
		saved, err = clonePayload(in)
		if err != nil {
			return fmt.Errorf("node %q compensation input: %w", id, err)
		}
	}
	e.markVisited(id)
	nctx := ctx
	cancel := func() {}
	if b.spec.Detached {
		e.runBeforeDetached(in)
		nctx, cancel = context.WithTimeout(context.WithoutCancel(ctx), e.opts.PublishTimeout)
	}
	started := time.Now()
	out, runErr := invoke(nctx, b.node, in)
	cancel()
	var pe PanicError
	if errors.As(runErr, &pe) {
		pe.NodeID = id
		runErr = pe
	}
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
			if setErr := e.setResponse(terminal.Response); setErr != nil {
				err = errors.Join(err, setErr)
			}
		}
		if b.spec.Detached {
			for _, outcome := range terminal.Deliveries {
				// Identity, configured target, and attempted state are executor-owned.
				// A destination controls only its outcome error.
				d := e.delivery(b, outcome.Err, false)
				d.RenderedTarget = outcome.RenderedTarget
				if d.Err == nil && runErr != nil {
					d.Err = runErr
				}
				e.addDelivery(d)
				if outcome.Err != nil && runErr == nil {
					err = errors.Join(err, d.Err)
				}
			}
		}
		// D12: only executor-collected Compensator instances are authorized.
		// Node-returned payloads cannot inject release callbacks.
	}
	if b.spec.Detached && (!isTerminal || len(terminal.Deliveries) == 0) {
		e.addDelivery(e.delivery(b, err, false))
	}
	observe(e.opts.OnNode, NodeEvent{NodeID: id, NodeType: b.spec.Type, Duration: time.Since(started), Err: err, Target: b.target, Dropped: isTerminal && b.spec.Type == "filter" && err == nil})
	// A successful reservation must be released even when its output is malformed.
	if runErr == nil && isComp {
		e.addCompensation(Compensation{NodeID: id, Node: comp, Input: saved})
	}
	if err != nil {
		if handled := e.followOnError(ctx, id, errorInput); handled == nil {
			return nil
		} else if !errors.Is(handled, errNoOnError) {
			return handled
		}
		// A destination that fails for any reason -- including a panic
		// inside the publisher, which arrives here as a bare error -- is a
		// retryable delivery failure. Without this, a panicking publisher
		// would surface as 500 and lose the 503-so-the-sender-retries
		// contract (gaps doc C1).
		var ne NodeError
		if b.spec.Detached && !errors.As(err, &ne) {
			err = NodeError{NodeID: id, Status: 503, Err: err}
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
			err = PanicError{Value: r}
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

func (e *execution) runBeforeDetached(in Payload) {
	if e.opts.BeforeDetached == nil {
		return
	}
	// All publishers must wait for the single capture, including its panic
	// recovery, before starting their individual publish timeout (I9/I15).
	e.beforeDetached.Do(func() {
		defer func() { _ = recover() }()
		e.opts.BeforeDetached(in)
	})
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
	for _, edge := range successEdges(e.flow.out[id]) {
		e.suppressBranch(edge.To)
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
	for _, edge := range e.flow.out[id] {
		if edge.FromPort == "on_error" {
			e.suppressBranch(edge.To)
		}
	}
	successors := successEdges(e.flow.out[id])
	if selected := selectedBranch(out); selected != "" || labelledSuccessors(successors) {
		var matched []Edge
		for _, edge := range successors {
			if edge.FromPort == selected || (selected == "" && edge.FromPort == "") {
				matched = append(matched, edge)
			} else {
				e.suppressBranch(edge.To)
			}
		}
		if len(matched) == 0 {
			b := e.flow.nodes[id]
			return fmt.Errorf("node %q (%s): no successor for port %q", id, b.spec.Type, selected)
		}
		successors = matched
		// Selection belongs to this node, not the rest of the selected path.
		if env, ok := out.(Envelope); ok {
			env.Branch = ""
			out = env
		}
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

// followSkipFailed attempts every destination and succeeds when at least one
// does. Destinations run concurrently, bounded by RunOptions.FanoutConcurrency,
// matching the publishRoutesSkipFailed worker pool: with skip_failed the whole
// point is that no destination gates another, so serialising them would make
// request latency the SUM of destination latencies instead of the max.
//
// Only reached when every successor is detached (see followSuccessors), so the
// nodes run here are destination publishes: terminal, non-recursing, and
// already isolated from the request context. All shared state they touch goes
// through execution's mutex.
func (e *execution) followSkipFailed(ctx context.Context, id string, out Payload, successors []Edge) error {
	if len(successors) == 1 {
		if err := e.node(ctx, successors[0].To, out); err != nil {
			return err
		}
		return nil
	}

	// Clone per destination before fanning out: a publisher must never observe
	// a payload another destination is mutating.
	payloads := make([]Payload, len(successors))
	for i := range successors {
		next, err := clonePayload(out)
		if err != nil {
			return fmt.Errorf("node %q fan-out: %w", id, err)
		}
		payloads[i] = next
	}

	errs := make([]error, len(successors))
	limit := min(e.fanoutLimit(), len(successors))
	jobs := make(chan int)
	var wg sync.WaitGroup
	for range limit {
		wg.Go(func() {
			// Each worker writes only errs[i] for the indices it pulls, so
			// the slice needs no lock; execution's own state is guarded by
			// e.mu inside node.
			for i := range jobs {
				errs[i] = e.node(ctx, successors[i].To, payloads[i])
			}
		})
	}
	for i := range successors {
		jobs <- i
	}
	close(jobs)
	wg.Wait()

	// Report a real failure over a cancellation. The same rule
	// (publishRoutesSkipFailed): when one destination times out, its siblings
	// can fail with a context error that would otherwise mask the root cause
	// in logs and in the replay record.
	var firstErr error
	ok := 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case firstErr == nil, isContextErr(firstErr) && !isContextErr(err):
			firstErr = err
		}
	}
	if ok == 0 {
		return firstErr
	}
	return nil
}

func isContextErr(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
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
