package capture

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/jaysongiroux/hook_way/internal/config"
	"github.com/jaysongiroux/hook_way/internal/deliverysnapshot"
	"github.com/jaysongiroux/hook_way/internal/flowconfig"
	"github.com/jaysongiroux/hook_way/internal/graph"
	"github.com/jaysongiroux/hook_way/internal/publishmsg"
	"github.com/jaysongiroux/hook_way/internal/queue"
	"github.com/jaysongiroux/hook_way/internal/replaywire"
)

// maxTextLen bounds metadata text, matching replaywire.Validate.
const maxTextLen = 1024

// Record is one failure ready to upload. Snapshot and Payload are nil for a
// metadata-only record.
type Record struct {
	Metadata replaywire.FailureMetadata
	Snapshot []byte
	Payload  []byte
}

func (r Record) size() int64 { return int64(len(r.Snapshot) + len(r.Payload)) }

// MetadataOnly returns r without its artifacts, marked not retryable for
// reason.
func (r Record) MetadataOnly(reason string) Record {
	m := r.Metadata
	m.Retryable, m.NotRetryableReason, m.Snapshot, m.Payload = false, reason, nil, nil
	return Record{Metadata: m}
}

// BuilderOptions configures a Builder.
type BuilderOptions struct {
	Flows          map[string]graph.FlowConfig
	Destinations   map[string]config.DestinationConfig
	MaxRecordBytes int64
	CoreVersion    string
	CoreInstance   string
	Now            func() time.Time
}

// Builder turns failures into records.
type Builder struct {
	nodes    map[string]map[string]graph.NodeConfig
	dests    map[string]config.DestinationConfig
	maxBytes int64
	version  string
	instance string
	now      func() time.Time
}

func NewBuilder(o BuilderOptions) *Builder {
	b := &Builder{nodes: map[string]map[string]graph.NodeConfig{}, dests: o.Destinations, maxBytes: o.MaxRecordBytes,
		version: o.CoreVersion, instance: o.CoreInstance, now: o.Now}
	if b.now == nil {
		b.now = time.Now
	}
	for flow, fc := range o.Flows {
		byID := make(map[string]graph.NodeConfig, len(fc.Nodes))
		for _, nc := range fc.Nodes {
			byID[nc.ID] = nc
		}
		b.nodes[flow] = byID
	}
	return b
}

// Limited returns a copy of b whose per-artifact limit is also bounded by
// maxBytes (the caller's own limit, for example replay's). maxBytes <= 0
// keeps b's limit.
func (b *Builder) Limited(maxBytes int64) *Builder {
	c := *b
	if maxBytes > 0 && maxBytes < c.maxBytes {
		c.maxBytes = maxBytes
	}
	return &c
}

// FromFailure builds a resume record for a node that failed in a request
// that finished with status. ok is false when the node failed before the
// envelope existed: such a failure always fails the request, so it is never
// captured.
func (b *Builder) FromFailure(flow string, head graph.RequestHead, status int, ev graph.FailureEvent) (Record, bool) {
	return b.fromFailure(flow, head, status, ev, false)
}

// TooLargeFromFailure is FromFailure without artifacts: the metadata-only
// too_large record for a failure too large to hold until it is built. It
// reads the envelope only for identity and the publish target, so it is
// cheap enough to build before the bodies are released.
func (b *Builder) TooLargeFromFailure(flow string, head graph.RequestHead, status int, ev graph.FailureEvent) (Record, bool) {
	return b.fromFailure(flow, head, status, ev, true)
}

func (b *Builder) fromFailure(flow string, head graph.RequestHead, status int, ev graph.FailureEvent, metadataOnly bool) (Record, bool) {
	env, ok := ev.Input.(graph.Envelope)
	if !ok {
		return Record{}, false
	}
	nc := b.nodes[flow][ev.NodeID]
	m := b.base(flow, ev.NodeID, nc, ev.NodeType, status, ev.Err)
	m.Kind = replaywire.KindResume
	m.EventID, m.RequestID, m.TraceID = head.EventID, head.RequestID, head.TraceID
	m.ReceivedAt = m.FailedAt
	if ce, ok := env.Extra.(flowconfig.CapturedEvent); ok {
		m.IdempotencyKey = ce.Event.IdempotencyKey
		if !ce.Event.ReceivedAt.IsZero() {
			m.ReceivedAt = ce.Event.ReceivedAt.UTC()
		}
	}
	if metadataOnly {
		if ev.NodeType == "publish" {
			_, _, _ = b.publishTarget(&m, nc, env)
		}
		return b.tooLarge(m), true
	}
	var payload Payload
	var err error
	if ev.NodeType == "publish" {
		payload, err = b.publishPayload(&m, nc, env)
	} else {
		payload, err = RenderInputBody(env)
	}
	snapshot, snapErr := encodeEnvelope(env)
	return b.finish(m, snapshot, payload, errors.Join(err, snapErr)), true
}

// FromTerminal builds a publish_only record for an async webhook delivery
// that failed for good, in a request that finished with status.
func (b *Builder) FromTerminal(tf queue.TerminalFailure, status int) Record {
	return b.fromTerminal(tf, status, false)
}

// TooLargeFromTerminal is FromTerminal without artifacts; see
// TooLargeFromFailure.
func (b *Builder) TooLargeFromTerminal(tf queue.TerminalFailure, status int) Record {
	return b.fromTerminal(tf, status, true)
}

func (b *Builder) fromTerminal(tf queue.TerminalFailure, status int, metadataOnly bool) Record {
	nc := b.nodes[tf.Origin.Flow][tf.Origin.NodeID]
	m := b.base(tf.Origin.Flow, tf.Origin.NodeID, nc, "publish", status, tf.Err)
	m.Kind = replaywire.KindPublishOnly
	if !tf.FailedAt.IsZero() {
		m.FailedAt = tf.FailedAt.UTC()
	}
	m.ErrorClass, m.Attempts = classFromOutcome(tf.Outcome), max(tf.Attempts, 1)
	msg := tf.Message
	m.EventID, m.RequestID, m.TraceID, m.IdempotencyKey = msg.ID, msg.RequestID, msg.TraceID, msg.IdempotencyKey
	m.ReceivedAt = msg.ReceivedAt.UTC()
	if m.ReceivedAt.IsZero() {
		m.ReceivedAt = m.FailedAt
	}
	dest := b.dests[tf.DestinationID]
	m.DestinationID, m.DestinationType, m.Target, m.RenderedTarget = tf.DestinationID, dest.Type, tf.Target, tf.RenderedTarget
	m.DestinationHash = DestinationHash(dest)
	if metadataOnly {
		return b.tooLarge(m)
	}
	payload, err := RenderPayload(dest, msg)
	snapshot, snapErr := encodeMessage(msg)
	return b.finish(m, snapshot, payload, errors.Join(err, snapErr))
}

func (b *Builder) base(flow, nodeID string, nc graph.NodeConfig, nodeType string, status int, err error) replaywire.FailureMetadata {
	return replaywire.FailureMetadata{
		ID: uuid.Must(uuid.NewV7()).String(), Flow: flow, NodeID: nodeID, NodeType: nodeType,
		FailedAt: b.now().UTC(), InboundStatus: status,
		ErrorClass: ErrorClass(err), ErrorMessage: ErrorMessage(err), Attempts: attempts(err),
		NodeConfigHash: NodeConfigHash(nc), CoreVersion: b.version, CoreInstance: b.instance,
	}
}

// publishPayload renders what the failed publish node sent, exactly as the
// node prepares it, and records the destination on m.
func (b *Builder) publishPayload(m *replaywire.FailureMetadata, nc graph.NodeConfig, env graph.Envelope) (Payload, error) {
	opts, prepared, err := b.publishTarget(m, nc, env)
	if err != nil {
		return Payload{}, err
	}
	dest := b.dests[opts.Destination]
	msg, err := publishmsg.FromEnvelope(prepared, opts.Destination, m.RenderedTarget)
	if err != nil {
		return Payload{}, err
	}
	return RenderPayload(dest, msg)
}

// publishTarget records the failed publish node's destination and rendered
// target on m, exactly as the node prepares them, and returns the prepared
// envelope.
func (b *Builder) publishTarget(m *replaywire.FailureMetadata, nc graph.NodeConfig, env graph.Envelope) (flowconfig.PublishOptions, graph.Envelope, error) {
	opts, err := flowconfig.PublishNodeOptions(nc)
	if err != nil {
		return opts, env, err
	}
	dest := b.dests[opts.Destination]
	m.DestinationID, m.DestinationType, m.Target = opts.Destination, dest.Type, opts.Target
	m.DestinationHash = DestinationHash(dest)
	target, prepared, err := flowconfig.PreparePublish(opts, env)
	m.RenderedTarget = target
	return opts, prepared, err
}

// tooLarge finishes m as a metadata-only too_large record.
func (b *Builder) tooLarge(m replaywire.FailureMetadata) Record {
	m.NotRetryableReason = replaywire.NotRetryableTooLarge
	sanitize(&m)
	return Record{Metadata: m}
}

func encodeEnvelope(env graph.Envelope) ([]byte, error) {
	dto, err := SnapshotFromEnvelope(env)
	if err != nil {
		return nil, err
	}
	return deliverysnapshot.Encode(deliverysnapshot.Snapshot{Kind: deliverysnapshot.KindResume, Envelope: &dto})
}

func encodeMessage(msg queue.Message) ([]byte, error) {
	dto, err := SnapshotFromMessage(msg)
	if err != nil {
		return nil, err
	}
	return deliverysnapshot.Encode(deliverysnapshot.Snapshot{Kind: deliverysnapshot.KindPublishOnly, Message: &dto})
}

func digest(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// finish attaches the artifacts, or makes the record metadata-only when
// they cannot be built exactly (unsupported) or exceed max_record_bytes
// (too_large).
func (b *Builder) finish(m replaywire.FailureMetadata, snapshot []byte, payload Payload, err error) Record {
	rec := Record{}
	switch {
	case err != nil:
		m.NotRetryableReason = replaywire.NotRetryableUnsupported
	case int64(len(snapshot)) > b.maxBytes || int64(len(payload.Bytes)) > b.maxBytes:
		m.NotRetryableReason = replaywire.NotRetryableTooLarge
	default:
		if payload.Bytes == nil {
			payload.Bytes = []byte{}
		}
		m.Retryable = true
		m.Snapshot = &replaywire.ArtifactInfo{Size: int64(len(snapshot)), SHA256: digest(snapshot), Version: deliverysnapshot.Version}
		m.Payload = &replaywire.ArtifactInfo{Size: int64(len(payload.Bytes)), SHA256: digest(payload.Bytes),
			ContentType: cleanText(payload.ContentType, 255), Filename: cleanFilename(payload.Filename)}
		rec.Snapshot, rec.Payload = snapshot, payload.Bytes
	}
	sanitize(&m)
	rec.Metadata = m
	return rec
}

// sanitize makes every text field storable. Request ids, trace ids, and
// idempotency keys come from the sender; targets can carry request data.
func sanitize(m *replaywire.FailureMetadata) {
	for _, f := range []*string{&m.Flow, &m.NodeID, &m.NodeType, &m.DestinationID, &m.DestinationType, &m.Target,
		&m.RenderedTarget, &m.EventID, &m.RequestID, &m.TraceID, &m.IdempotencyKey, &m.NodeConfigHash,
		&m.DestinationHash, &m.CoreVersion, &m.CoreInstance} {
		*f = cleanText(*f, maxTextLen)
	}
}

// cleanFilename keeps a download name that is a bare, printable file name.
func cleanFilename(name string) string {
	name = cleanText(path.Base(strings.ReplaceAll(name, `\`, "/")), 255)
	if name == "." || name == ".." || name == "/" {
		return ""
	}
	return name
}
