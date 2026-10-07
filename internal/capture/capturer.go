package capture

import (
	"context"
	"log/slog"
	"sync"

	"github.com/jaysongiroux/hook_way/internal/flowconfig"
	"github.com/jaysongiroux/hook_way/internal/graph"
	"github.com/jaysongiroux/hook_way/internal/queue"
	"github.com/jaysongiroux/hook_way/internal/tlsfiles"
)

// estimateOverhead is the memory a failure is charged, beyond its bodies,
// until its record is built.
const estimateOverhead = 64 << 10

// envelopeEstimate is the memory a queued failure keeps alive until its
// record is built: every body its envelope references — the request's raw
// bytes, an http_request node's upstream reply, the captured event's copies —
// three times over, for the parsed body tree and the record built from it.
func envelopeEstimate(env graph.Envelope) int64 {
	raw := env.V.Raw.Body
	n := int64(len(raw))
	if env.Upstream != nil {
		n += int64(len(env.Upstream.Body))
	}
	if ce, ok := env.Extra.(flowconfig.CapturedEvent); ok {
		if !sameBytes(ce.Event.RawBody, raw) { // usually the request's own bytes
			n += int64(len(ce.Event.RawBody))
		}
		if ce.Event.RawBodyBase64 != nil {
			n += int64(len(*ce.Event.RawBodyBase64))
		}
	}
	return 3*n + estimateOverhead
}

func sameBytes(a, b []byte) bool {
	return len(a) == len(b) && (len(a) == 0 || &a[0] == &b[0])
}

// messageEstimate is envelopeEstimate for an async delivery's message.
func messageEstimate(msg queue.Message) int64 {
	n := int64(len(msg.RawBody) + len(msg.ObjectBytes))
	if msg.RawBodyBase64 != nil {
		n += int64(len(*msg.RawBodyBase64))
	}
	return 3*n + estimateOverhead
}

// Options configures the capture pipeline.
type Options struct {
	Config       flowconfig.Config // the replay block, flows, and destinations
	Metrics      UploaderMetrics
	Logger       *slog.Logger
	CoreVersion  string
	CoreInstance string
}

// Capturer uploads acknowledged failures and explicitly requested inspections
// to the replay service. Uploads never block a request or change a
// response or readiness (docs/development/invariants.md I15).
type Capturer struct {
	builder        *Builder
	uploader       *Uploader
	enabled        map[string]bool
	inspectEnabled map[string]bool
}

func New(o Options) (*Capturer, error) {
	r := o.Config.Replay
	up, err := NewUploader(UploaderOptions{URL: r.URL, Token: r.CaptureToken, TLS: tlsfiles.Files(r.TLS), Metrics: o.Metrics, Logger: o.Logger})
	if err != nil {
		return nil, err
	}
	enabled := make(map[string]bool, len(o.Config.Flows))
	for name, fc := range o.Config.Flows {
		enabled[name] = fc.CapturesFailures()
	}
	inspectEnabled := make(map[string]bool)
	for name, flow := range o.Config.Flows {
		for _, node := range flow.Nodes {
			if node.Type == "inspect" {
				inspectEnabled[name] = true
			}
		}
	}
	b := NewBuilder(BuilderOptions{Flows: o.Config.Flows, Credentials: o.Config.Credentials, HeaderAllowlist: o.Config.Security.ForwardedHeaderAllowlist, HeaderDenylist: o.Config.Security.RedactedHeaderDenylist, Destinations: o.Config.Destinations,
		MaxRecordBytes: r.MaxRecordBytes, CoreVersion: o.CoreVersion, CoreInstance: o.CoreInstance})
	return &Capturer{builder: b, uploader: up, enabled: enabled, inspectEnabled: inspectEnabled}, nil
}

// Builder returns the capturer's record builder, or nil for a nil capturer.
// Redrives use it to build child records.
func (c *Capturer) Builder() *Builder {
	if c == nil {
		return nil
	}
	return c.builder
}

// Close drains pending uploads within a bounded wait.
func (c *Capturer) Close() error { return c.uploader.Close() }

// Request collects one inbound request's failures and inspections until its final status is
// known.
type Request struct {
	c           *Capturer
	flow        string
	head        graph.RequestHead
	settled     *queue.Settled
	mu          sync.Mutex
	events      []graph.FailureEvent
	inspections []pending
}

// Begin starts collecting for a request, or returns nil when the flow does
// not capture failures or inspections. Every Request method accepts a nil receiver.
func (c *Capturer) Begin(flow string, head graph.RequestHead) *Request {
	if c == nil || (!c.enabled[flow] && !c.inspectEnabled[flow]) {
		return nil
	}
	return &Request{c: c, flow: flow, head: head, settled: queue.NewSettled()}
}

// OnFailure is the executor's RunOptions.OnFailure hook. It only records;
// building happens on the uploader, after the response.
func (r *Request) OnFailure(ev graph.FailureEvent) {
	if r == nil || !r.c.enabled[r.flow] {
		return
	}
	r.mu.Lock()
	r.events = append(r.events, ev)
	r.mu.Unlock()
}

// WithOrigin marks a publish so an async webhook delivery that fails for good
// can be captured.
func (r *Request) WithOrigin(ctx context.Context, nodeID string) context.Context {
	if r == nil || !r.c.enabled[r.flow] {
		return ctx
	}
	return queue.WithOrigin(ctx, queue.Origin{Flow: r.flow, NodeID: nodeID, Settled: r.settled})
}

// Settle records the request's final status. Only a 2xx keeps the failures:
// the sender was told the event was accepted, so core owns its delivery
// (spec D1); any other status leaves the retry to the sender.
func (r *Request) Settle(status int) {
	if r == nil {
		return
	}
	r.mu.Lock()
	events := r.events
	r.events = nil
	inspections := r.inspections
	r.inspections = nil
	r.mu.Unlock()
	if acknowledged(status) {
		for _, ev := range events {
			r.c.submitFailure(r.flow, r.head, status, ev)
		}
	}
	for _, p := range inspections {
		buildInspection := p.build
		p.build = func() (Record, bool) {
			rec, ok := buildInspection()
			rec.Metadata.InboundStatus = status
			return rec, ok
		}
		r.c.uploader.submitReserved(p)
	}
	r.settled.Resolve(status)
}

func acknowledged(status int) bool { return status >= 200 && status < 300 }

func (c *Capturer) submitFailure(flow string, head graph.RequestHead, status int, ev graph.FailureEvent) {
	env, _ := ev.Input.(graph.Envelope)
	p := pending{flow: flow, nodeID: ev.NodeID, eventID: head.EventID, estimate: envelopeEstimate(env)}
	if p.estimate > maxPendingBytes {
		// It can never fit the buffer: send its metadata as too_large,
		// built now so nothing keeps the bodies alive.
		rec, ok := c.builder.TooLargeFromFailure(flow, head, status, ev)
		if !ok {
			return
		}
		p.estimate, p.build = estimateOverhead, func() (Record, bool) { return rec, true }
	} else {
		p.build = func() (Record, bool) { return c.builder.FromFailure(flow, head, status, ev) }
	}
	c.uploader.submit(p)
}

// OnTerminalFailure is the queue factory's hook for async webhook deliveries
// no dead-letter destination accepted. It never blocks: the failure waits for
// its request's final status, then is kept only if that status was a 2xx.
func (c *Capturer) OnTerminalFailure(tf queue.TerminalFailure) {
	if c == nil || tf.Origin.Settled == nil {
		return
	}
	tf.Origin.Settled.AfterSettle(func(status int) {
		if !acknowledged(status) {
			return
		}
		p := pending{flow: tf.Origin.Flow, nodeID: tf.Origin.NodeID, eventID: tf.Message.ID, estimate: messageEstimate(tf.Message)}
		if p.estimate > maxPendingBytes {
			rec := c.builder.TooLargeFromTerminal(tf, status)
			p.estimate, p.build = estimateOverhead, func() (Record, bool) { return rec, true }
		} else {
			p.build = func() (Record, bool) { return c.builder.FromTerminal(tf, status), true }
		}
		c.uploader.submit(p)
	})
}
