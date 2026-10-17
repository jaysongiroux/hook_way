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
	DropReason       string
	Dropped          bool
	Duration         time.Duration
	Err              error
}

// FailureEvent reports one node whose error no on_error path handled. It fires
// once per failed invocation, at the node where the error originated. Ancestors
// that merely propagate the error never fire. Input is the payload the node received. It
// is not copied: nodes never mutate their input (I19), so it still holds what
// the node was given.
type FailureEvent struct {
	NodeID   string
	NodeType string
	Input    Payload
	Err      error
}

type RunOptions struct {
	// Cancellable keeps invocation work on its caller deadline. Webhooks default to detached delivery.
	Cancellable    bool
	PublishTimeout time.Duration
	// FanoutConcurrency bounds concurrent destination publishes for
	// skip_failed fan-out, matching the publishRoutesSkipFailed worker pool.
	// stop_on_failure stays sequential: it must not attempt a destination
	// after an earlier one failed. <= 0 means 8.
	FanoutConcurrency int
	// OnNode runs once after each attempted invocation, including failures and panics.
	// An observer panic is isolated: instrumentation must not alter delivery.
	OnNode func(NodeEvent)
	// OnFailure runs for each node whose error no on_error path handled, at
	// the node where the error originated. Under skip_failed it can be called
	// from several fan-out goroutines at once, so it must be safe for
	// concurrent use. An observer panic is isolated, like OnNode.
	OnFailure func(FailureEvent)
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
	ctx, effects := effectContext(ctx)
	e := &execution{flow: f, opts: opts, visited: map[string]bool{}, suppressed: map[string]bool{}}
	err = e.node(ctx, f.root, seed, "")
	// skip_failed may acknowledge a partially failed run. Record blocked paths
	// even then; successful and deliberately suppressed paths add no outcomes.
	e.recordUnattempted(f.root)
	slices.Reverse(e.term.Compensations)
	e.term.SideEffects = effects.Load()
	return e.term, err
}

// RunFrom executes the flow starting at nodeID with in as that node's input.
// It is the redrive entry point: nodes upstream of nodeID and sibling
// branches never run, and compensations are never returned (a redrive holds
// no sender reservation to release). on_error paths, success continuations,
// skip_failed fan-out, OnNode, and OnFailure behave as in Run. Unattempted
// destinations are reported only among nodes reachable from nodeID.
//
// The timing differs from Run on purpose: one budget bounds the whole resumed
// run, PublishTimeout capped at ctx's deadline, so a redrive never outlives
// the time its caller will wait. Like Run's publish budget it is detached from
// cancellation (I10): a caller that disconnects mid-run does not abort a
// delivery already under way. A ctx that has already ended runs nothing.
func (f *Flow) RunFrom(ctx context.Context, nodeID string, in Payload, opts RunOptions) (term Terminal, err error) {
	if opts.PublishTimeout <= 0 {
		return term, errors.New("graph: RunOptions.PublishTimeout must be positive")
	}
	if f == nil || f.nodes == nil {
		return term, errors.New("graph: uncompiled flow")
	}
	b, ok := f.nodes[nodeID]
	if !ok {
		return term, fmt.Errorf("graph: flow %q has no node %q", f.name, nodeID)
	}
	if in == nil {
		return term, errors.New("graph: nil payload")
	}
	if _, err := concretePort(in); err != nil {
		return term, err
	}
	// Resuming is for nodes after the envelope exists. Earlier nodes are
	// gates (replay_guard, rate limits, auth) that must never rerun.
	if b.spec.In != PortEnvelope {
		return term, fmt.Errorf("graph: RunFrom starts only at a node that consumes an Envelope; %q consumes %s", nodeID, b.spec.In)
	}
	env, ok := in.(Envelope)
	if !ok {
		return term, fmt.Errorf("graph: node %q expects %s but was given %s", nodeID, b.spec.In, in.PortType())
	}
	// Branch selects a switch node's output port; it belongs to the run that
	// set it, not to a resumed one.
	env.Branch = ""
	in = env
	if err := ctx.Err(); err != nil {
		return term, err
	}
	seed, err := clonePayload(in)
	if err != nil {
		return term, err
	}
	deadline := time.Now().Add(opts.PublishTimeout)
	if callerDeadline, ok := ctx.Deadline(); ok && callerDeadline.Before(deadline) {
		deadline = callerDeadline
	}
	bctx, cancel := context.WithDeadline(context.WithoutCancel(ctx), deadline)
	defer cancel()
	// Marking the context as already inside a publish budget makes every
	// detached node reuse this one instead of starting a fresh PublishTimeout.
	bctx = context.WithValue(bctx, publishBudgetKey{}, struct{}{})
	bctx, effects := effectContext(bctx)
	e := &execution{flow: f, opts: opts, visited: map[string]bool{}, suppressed: map[string]bool{}}
	err = e.node(bctx, nodeID, seed, "")
	e.recordUnattempted(nodeID)
	e.term.Compensations = nil
	e.term.SideEffects = effects.Load()
	return e.term, err
}

type execution struct {
	flow *Flow
	opts RunOptions

	// mu guards every field below. skip_failed fan-out runs sibling
	// destination publishes concurrently, so all per-request state is
	// touched from multiple goroutines. stop_on_failure is sequential and
	// pays only an uncontended lock.
	mu   sync.Mutex
	term Terminal
	// Keys are execution paths, not node IDs. Suppression marks a path prefix.
	visited, suppressed map[string]bool
}

func (e *execution) markVisited(path string) {
	e.mu.Lock()
	e.visited[path] = true
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

// executionPath identifies an arrival by its complete edge path. Node IDs may
// contain punctuation, but not NUL, so the separators cannot collide with IDs.
func executionPath(parent string, edge Edge) string {
	return parent + "\x00" + edge.String()
}

// recordUnattempted runs after workers have joined. Traverse paths, rather than
// deduplicating nodes: a completed arrival must not hide a blocked sibling.
// Suppressed prefixes prune paths the request deliberately did not select.
func (e *execution) recordUnattempted(root string) {
	var walk func(string, string)
	walk = func(id, path string) {
		if e.suppressed[path] {
			return
		}
		b := e.flow.nodes[id]
		if b.spec.Detached && !e.visited[path] {
			e.term.Deliveries = append(e.term.Deliveries, e.delivery(b, nil, true))
		}
		for _, edge := range e.flow.out[id] {
			walk(edge.To, executionPath(path, edge))
		}
	}
	walk(root, "")
}

func (e *execution) suppress(id, path string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, edge := range e.flow.out[id] {
		e.suppressed[executionPath(path, edge)] = true
	}
}

func (e *execution) suppressBranch(path string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.suppressed[path] = true
}

func (e *execution) node(ctx context.Context, id string, in Payload, path string) (err error) {
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
	e.markVisited(path)
	if e.opts.Cancellable && ctx.Err() != nil {
		return ctx.Err()
	}
	nctx := ctx
	cancel := func() {}
	ownsBudget := false
	if b.spec.Detached && !e.opts.Cancellable {
		if ctx.Value(publishBudgetKey{}) != nil {
			// Already inside a publish success chain: reuse the shared budget.
			nctx = ctx
		} else {
			var stop context.CancelFunc
			nctx, stop = context.WithTimeout(context.WithoutCancel(ctx), e.opts.PublishTimeout)
			nctx = context.WithValue(nctx, publishBudgetKey{}, struct{}{})
			// Defer keeps the budget open through a success continuation;
			// an error path also cancels early so on_error sees the request ctx.
			defer stop()
			cancel = stop
			ownsBudget = true
		}
	}
	started := time.Now()
	if b.spec.Detached {
		if _, tracked := b.node.(SideEffectTracker); !tracked {
			MarkSideEffect(nctx)
		}
	}
	out, runErr := invoke(nctx, b.node, in, e.opts)
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
		if terminal.Result != nil {
			e.mu.Lock()
			if e.term.Result != nil {
				err = errors.Join(err, errors.New("more than one invocation result produced"))
			} else {
				e.term.Result = &Result{JSON: append([]byte(nil), terminal.Result.JSON...)}
			}
			e.mu.Unlock()
		}
		if terminal.SideEffects {
			MarkSideEffect(nctx)
		}
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
				d.NoEffect = outcome.NoEffect
				if d.Err == nil && runErr != nil {
					d.Err = runErr
				}
				e.addDelivery(d)
				if outcome.Err != nil && runErr == nil {
					err = errors.Join(err, d.Err)
				}
			}
		}
		if _, childRunner := b.node.(ChildExecutionNode); childRunner && !b.spec.Detached {
			for _, d := range terminal.Deliveries {
				e.addDelivery(d)
			}
		}
		// D12: only executor-collected Compensator instances are authorized.
		// Node-returned payloads cannot inject release callbacks.
	}
	if b.spec.Detached && (!isTerminal || len(terminal.Deliveries) == 0) {
		e.addDelivery(e.delivery(b, err, false))
	}
	observe(e.opts.OnNode, NodeEvent{NodeID: id, NodeType: b.spec.Type, Duration: time.Since(started), Err: err, Target: b.target, Dropped: isTerminal && (b.spec.Type == "filter" || b.spec.Type == "sample") && err == nil, DropReason: terminal.DropReason})
	// A successful reservation must be released even when its output is malformed.
	if runErr == nil && isComp {
		if capture, ok := comp.(CompensationInput); ok {
			captured, captureErr := capture.CompensationInput(in, out)
			if captureErr == nil {
				saved, captureErr = clonePayload(captured)
			}
			if captureErr != nil {
				return fmt.Errorf("node %q compensation ownership: %w", id, captureErr)
			}
		}
		e.addCompensation(Compensation{NodeID: id, Node: comp, Input: saved})
	}
	if err != nil {
		// Cancel an owned budget before on_error so a root publish timeout
		// still recovers on the request context (I10). Mid-chain nodes do
		// not own the budget; their on_error sees the exhausted deadline.
		if ownsBudget {
			cancel()
		}
		var denied NodeError
		if errors.As(err, &denied) && denied.AbortRecovery {
			observeFailure(e.opts.OnFailure, FailureEvent{NodeID: id, NodeType: b.spec.Type, Input: in, Err: err})
			return fmt.Errorf("node %q (%s): %w", id, b.spec.Type, err)
		}
		if handled := e.followOnError(ctx, id, errorInput, path); handled == nil {
			return nil
		} else if !errors.Is(handled, errNoOnError) {
			return handled
		}
		// A destination that fails for any reason -- including a panic
		// inside the publisher, which arrives here as a bare error -- is a
		// retryable delivery failure. Without this, a panicking publisher
		// would surface as 500 and lose the 503-so-the-sender-retries
		// contract.
		var ne NodeError
		if b.spec.Detached && !errors.As(err, &ne) {
			err = NodeError{NodeID: id, Status: 503, Err: err}
		}
		observeFailure(e.opts.OnFailure, FailureEvent{NodeID: id, NodeType: b.spec.Type, Input: in, Err: err})
		return fmt.Errorf("node %q (%s): %w", id, b.spec.Type, err)
	}
	if isTerminal {
		successors := successEdges(e.flow.out[id])
		if b.spec.SuccessContinuation && len(successors) > 0 {
			// Suppress only on_error: the success edge runs with the input
			// envelope on the detached budget, after Publish returned nil.
			for _, edge := range e.flow.out[id] {
				if edge.FromPort == "on_error" {
					e.suppressBranch(executionPath(path, edge))
				}
			}
			next, cloneErr := clonePayload(in)
			if cloneErr != nil {
				return fmt.Errorf("node %q success continuation: %w", id, cloneErr)
			}
			return e.followSuccessors(nctx, id, next, path)
		}
		e.suppress(id, path)
		return nil
	}
	return e.followSuccessors(ctx, id, out, path)
}

// publishBudgetKey marks a context as owning (or sharing) one publish_timeout
// budget for a SuccessContinuation chain.
type publishBudgetKey struct{}

func concretePort(p Payload) (PortType, error) {
	switch p.(type) {
	case RequestHead, RawRequest, Verified, Envelope, Terminal:
		return p.PortType(), nil
	default:
		return PortNone, fmt.Errorf("graph: unsupported payload %T; payloads must be concrete values", p)
	}
}

// ChildExecutionNode runs typed child graphs and returns their executor-owned
// outcomes. It is a trusted registry extension, never a script callback.
type ChildExecutionNode interface {
	RunChildren(context.Context, Payload, RunOptions) (Payload, error)
}

func invoke(ctx context.Context, n Node, in Payload, options ...RunOptions) (out Payload, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = PanicError{Value: r}
		}
	}()
	if child, ok := n.(ChildExecutionNode); ok && len(options) > 0 {
		return child.RunChildren(ctx, in, options[0])
	}
	return n.Run(ctx, in)
}
func observe(fn func(NodeEvent), ev NodeEvent) {
	if fn == nil {
		return
	}
	defer func() { _ = recover() }()
	fn(ev)
}

func observeFailure(fn func(FailureEvent), ev FailureEvent) {
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

func (e *execution) followOnError(ctx context.Context, id string, in Payload, path string) error {
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
		e.suppressBranch(executionPath(path, edge))
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
		if err := e.node(ctx, edge.To, next, executionPath(path, edge)); err != nil {
			return err
		}
	}
	return nil
}

func (e *execution) followSuccessors(ctx context.Context, id string, out Payload, path string) error {
	for _, edge := range e.flow.out[id] {
		if edge.FromPort == "on_error" {
			e.suppressBranch(executionPath(path, edge))
		}
	}
	successors := successEdges(e.flow.out[id])
	if selected := selectedBranch(out); selected != "" || labelledSuccessors(successors) {
		var matched []Edge
		for _, edge := range successors {
			if edge.FromPort == selected || (selected == "" && edge.FromPort == "") {
				matched = append(matched, edge)
			} else {
				e.suppressBranch(executionPath(path, edge))
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
		return e.followSkipFailed(ctx, id, out, successors, path)
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
		if err = e.node(ctx, edge.To, next, executionPath(path, edge)); err != nil {
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
// Only reached when every successor is detached (see followSuccessors). Each
// worker runs a destination and its continuation on an isolated publish budget.
// All shared execution state they touch goes through execution's mutex.
func (e *execution) followSkipFailed(ctx context.Context, id string, out Payload, successors []Edge, path string) error {
	if len(successors) == 1 {
		if err := e.node(ctx, successors[0].To, out, executionPath(path, successors[0])); err != nil {
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
				errs[i] = e.node(ctx, successors[i].To, payloads[i], executionPath(path, successors[i]))
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

// CompensationInput lets a successful reservation capture request-local
// ownership returned by Run, instead of reconstructing an unowned key.
type CompensationInput interface {
	CompensationInput(Payload, Payload) (Payload, error)
}
type Compensation struct {
	NodeID string
	Node   Compensator
	Input  Payload
}

func NeedsCompensation(term Terminal, err error) bool {
	if potentialSideEffects(term) {
		return false
	}
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
	if potentialSideEffects(term) {
		return nil
	}
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

// RunChild executes a compiled Envelope child under its parent's cancellation
// and deadline. Unlike redrive, batch delivery must stop when its caller ends.
func (f *Flow) RunChild(ctx context.Context, in Envelope, opts RunOptions) (Terminal, error) {
	if f == nil || f.nodes[f.root] == nil || f.nodes[f.root].spec.In != PortEnvelope {
		return Terminal{}, errors.New("graph: uncompiled child flow")
	}
	if opts.PublishTimeout <= 0 {
		return Terminal{}, errors.New("graph: child publish timeout must be positive")
	}
	ctx, cancel := context.WithTimeout(ctx, opts.PublishTimeout)
	defer cancel()
	ctx = context.WithValue(ctx, publishBudgetKey{}, struct{}{})
	ctx, effects := effectContext(ctx)
	e := &execution{flow: f, opts: opts, visited: map[string]bool{}, suppressed: map[string]bool{}}
	if err := ctx.Err(); err != nil {
		e.recordUnattempted(f.root)
		return e.term, err
	}
	seed, err := clonePayload(in)
	if err != nil {
		return Terminal{}, err
	}
	err = e.node(ctx, f.root, seed, "")
	// skip_failed may acknowledge a partially failed run. Record blocked paths
	// even then; successful and deliberately suppressed paths add no outcomes.
	e.recordUnattempted(f.root)
	e.term.SideEffects = effects.Load()
	return e.term, err
}
