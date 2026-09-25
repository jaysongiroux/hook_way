package httpapi

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/jaysongiroux/hook_way/internal/capture"
	"github.com/jaysongiroux/hook_way/internal/control"
	"github.com/jaysongiroux/hook_way/internal/deliverysnapshot"
	"github.com/jaysongiroux/hook_way/internal/flowconfig"
	"github.com/jaysongiroux/hook_way/internal/graph"
	"github.com/jaysongiroux/hook_way/internal/queue"
	"github.com/jaysongiroux/hook_way/internal/replaywire"
)

// Redrive implements control.Redriver: it runs one captured failure again
// under the current configuration (D5) with inline delivery, so every
// reported outcome is real. It never captures, dead-letters, or runs
// compensations (I21): the control path begins no capture request, inline
// deliveries never dead-letter, and RunFrom returns no compensations.
func (s *FlowServer) Redrive(ctx context.Context, req replaywire.DeliverRequest, snap deliverysnapshot.Snapshot) (control.Result, error) {
	fc, nc, err := s.checkRedrive(req)
	if err != nil {
		return control.Result{}, err
	}
	res := control.Result{NodeConfigHash: capture.NodeConfigHash(nc)}
	if req.DestinationID != "" {
		res.DestinationHash = capture.DestinationHash(s.cfg.Destinations[req.DestinationID])
	}
	ctx = queue.WithInlineDelivery(ctx)
	if req.Kind == replaywire.KindPublishOnly {
		return s.redrivePublish(ctx, req, snap, res)
	}
	return s.redriveResume(ctx, req, fc, snap, res)
}

// checkRedrive applies the configuration block reasons, in the spec's order.
func (s *FlowServer) checkRedrive(req replaywire.DeliverRequest) (graph.FlowConfig, graph.NodeConfig, error) {
	fc, ok := s.cfg.Flows[req.Flow]
	if !ok {
		return fc, graph.NodeConfig{}, control.Blockf(replaywire.BlockFlowMissing, "flow %q is not configured", req.Flow)
	}
	if mf := s.flows[req.Flow]; mf.broken || mf.flow == nil {
		return fc, graph.NodeConfig{}, control.Blockf(replaywire.BlockFlowBroken, "flow %q does not compile", req.Flow)
	}
	i := slices.IndexFunc(fc.Nodes, func(nc graph.NodeConfig) bool { return nc.ID == req.NodeID })
	if i < 0 {
		return fc, graph.NodeConfig{}, control.Blockf(replaywire.BlockNodeMissing, "flow %q has no node %q", req.Flow, req.NodeID)
	}
	nc := fc.Nodes[i]
	if nc.Type != req.NodeType {
		return fc, nc, control.Blockf(replaywire.BlockNodeTypeChanged, "node %q is now a %s, not a %s", req.NodeID, nc.Type, req.NodeType)
	}
	if req.DestinationID == "" && req.Kind != replaywire.KindPublishOnly {
		return fc, nc, nil
	}
	opts, err := flowconfig.PublishNodeOptions(nc)
	dest, ok := s.cfg.Destinations[opts.Destination]
	if err != nil || !ok || opts.Destination != req.DestinationID || dest.Type != req.DestinationType {
		return fc, nc, control.Blockf(replaywire.BlockDestinationChanged, "node %q now publishes to %q (%s), not %q (%s)",
			req.NodeID, opts.Destination, dest.Type, req.DestinationID, req.DestinationType)
	}
	return fc, nc, nil
}

func (s *FlowServer) redriveResume(ctx context.Context, req replaywire.DeliverRequest, fc graph.FlowConfig, snap deliverysnapshot.Snapshot, res control.Result) (control.Result, error) {
	env, err := capture.EnvelopeFromSnapshot(*snap.Envelope)
	if err != nil {
		return res, fmt.Errorf("%w: %w", control.ErrMalformedSnapshot, err)
	}
	var col redriveCollector
	opts := graph.RunOptions{
		PublishTimeout:    time.Duration(req.DeadlineMS) * time.Millisecond,
		FanoutConcurrency: s.cfg.Server.FanoutConcurrencyLimit(),
		OnNode:            col.node,
		OnFailure:         col.failure,
	}
	term, runErr := s.flows[req.Flow].flow.RunFrom(ctx, req.NodeID, env, opts)
	res.Nodes = col.outcomes(term)
	resume, ran := col.outcome(res.Nodes, req.NodeID)
	if !ran {
		return res, fmt.Errorf("the resume node %q did not run: %w", req.NodeID, runErr)
	}
	b := s.capture.Builder()
	if b == nil || !fc.CapturesFailures() || (resume != replaywire.NodeDelivered && resume != replaywire.NodeHandled) {
		return res, nil
	}
	// Children keep the original request's identity: the envelope's head
	// carries its event, request, and trace ids.
	b = b.Limited(req.MaxRecordBytes)
	for _, ev := range col.failures {
		if ev.NodeID == req.NodeID {
			continue
		}
		if rec, ok := b.FromFailure(req.Flow, env.V.Raw.Head, req.InboundStatus, ev); ok {
			res.Children = append(res.Children, rec)
		}
	}
	return res, nil
}

func (s *FlowServer) redrivePublish(ctx context.Context, req replaywire.DeliverRequest, snap deliverysnapshot.Snapshot, res control.Result) (control.Result, error) {
	msg, err := capture.MessageFromSnapshot(*snap.Message)
	if err != nil {
		return res, fmt.Errorf("%w: %w", control.ErrMalformedSnapshot, err)
	}
	pubErr := s.publisher.Publish(ctx, req.DestinationID, req.RenderedTarget, msg)
	if errors.Is(pubErr, queue.ErrTargetNotConfigured) {
		return res, control.Blockf(replaywire.BlockTargetNotConfigured, "destination %q has no target %q", req.DestinationID, req.RenderedTarget)
	}
	o := replaywire.NodeOutcome{NodeID: req.NodeID, Outcome: replaywire.NodeDelivered, RenderedTarget: capture.CleanText(req.RenderedTarget), Attempts: 1}
	if pubErr != nil {
		o.Outcome, o.ErrorClass, o.ErrorMessage, o.Attempts = replaywire.NodeFailed, capture.ErrorClass(pubErr), capture.ErrorMessage(pubErr), capture.Attempts(pubErr)
	}
	res.Nodes = []replaywire.NodeOutcome{o}
	return res, nil
}

// redriveCollector gathers node outcomes and unhandled failures from one
// RunFrom. Fan-out reports from several goroutines.
type redriveCollector struct {
	mu       sync.Mutex
	order    []string
	events   map[string]graph.NodeEvent
	failed   map[string]bool
	failures []graph.FailureEvent
}

func (c *redriveCollector) node(ev graph.NodeEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.events == nil {
		c.events = map[string]graph.NodeEvent{}
	}
	if _, seen := c.events[ev.NodeID]; !seen {
		c.order = append(c.order, ev.NodeID)
	}
	c.events[ev.NodeID] = ev
}

func (c *redriveCollector) failure(ev graph.FailureEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.failed == nil {
		c.failed = map[string]bool{}
	}
	c.failed[ev.NodeID] = true
	c.failures = append(c.failures, ev)
}

// outcomes reports each node that ran — delivered, failed, or handled (it
// failed and an on_error path took over) — then each destination the run
// skipped as not_run.
func (c *redriveCollector) outcomes(term graph.Terminal) []replaywire.NodeOutcome {
	c.mu.Lock()
	defer c.mu.Unlock()
	rendered := map[string]string{}
	var skipped []string
	for _, d := range term.Deliveries {
		if d.RenderedTarget != "" {
			rendered[d.NodeID] = d.RenderedTarget
		}
		if d.Unattempted {
			skipped = append(skipped, d.NodeID)
		}
	}
	out := make([]replaywire.NodeOutcome, 0, len(c.order)+len(skipped))
	for _, id := range c.order {
		ev := c.events[id]
		o := replaywire.NodeOutcome{NodeID: id, Outcome: replaywire.NodeDelivered, RenderedTarget: capture.CleanText(rendered[id]), Attempts: 1}
		if ev.Err != nil {
			o.Outcome = replaywire.NodeHandled
			if c.failed[id] {
				o.Outcome = replaywire.NodeFailed
			}
			o.ErrorClass, o.ErrorMessage, o.Attempts = capture.ErrorClass(ev.Err), capture.ErrorMessage(ev.Err), capture.Attempts(ev.Err)
		}
		out = append(out, o)
	}
	slices.Sort(skipped)
	for _, id := range slices.Compact(skipped) {
		if _, ran := c.events[id]; !ran {
			out = append(out, replaywire.NodeOutcome{NodeID: id, Outcome: replaywire.NodeNotRun})
		}
	}
	return out
}

func (c *redriveCollector) outcome(nodes []replaywire.NodeOutcome, id string) (string, bool) {
	for _, n := range nodes {
		if n.NodeID == id && n.Outcome != replaywire.NodeNotRun {
			return n.Outcome, true
		}
	}
	return "", false
}
