package capture

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"maps"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jaysongiroux/hook_way/internal/flowconfig"
	"github.com/jaysongiroux/hook_way/internal/graph"
	"github.com/jaysongiroux/hook_way/internal/logging"
	"github.com/jaysongiroux/hook_way/internal/replaywire"
	"github.com/jaysongiroux/hook_way/internal/transform"
)

// InspectionMetrics keeps spot-check drops separate from delivery-failure drops.
type InspectionMetrics interface {
	InspectionUploaded(flow string)
	InspectionDropped(flow, reason string)
}

var errInspectionTooLarge = errors.New("inspection exceeds max_record_bytes")

type inspectionBuffer struct {
	bytes.Buffer
	limit int64
}

func (b *inspectionBuffer) Write(p []byte) (int, error) {
	if int64(b.Len()+len(p)) > b.limit {
		return 0, errInspectionTooLarge
	}
	return b.Buffer.Write(p)
}

func inspectionBody(env graph.Envelope, limit int64) (Payload, error) {
	// BodyFormat distinguishes parsed JSON null from an absent raw body.
	if env.BodyFormat == "raw" || (env.Body == nil && env.BodyFormat != "json") {
		if int64(len(env.V.Raw.Body)) > limit {
			return Payload{}, errInspectionTooLarge
		}
		return Payload{Bytes: env.V.Raw.Body, ContentType: env.V.Raw.Head.Header.Get("Content-Type")}, nil
	}
	switch env.Body.(type) {
	case transform.Document, *transform.Document:
		p, err := RenderInputBody(env)
		if int64(len(p.Bytes)) > limit {
			return Payload{}, errInspectionTooLarge
		}
		return p, err
	default:
		buf := &inspectionBuffer{limit: limit}
		if err := json.NewEncoder(buf).Encode(env.Body); err != nil {
			return Payload{}, err
		}
		return Payload{Bytes: bytes.TrimSuffix(buf.Bytes(), []byte("\n")), ContentType: "application/json"}, nil
	}
}

// FromInspection freezes only display data at the node boundary. No raw
// original envelope, upstream reply, credentials, or executable state is stored.
func (b *Builder) FromInspection(flow, nodeID string, head graph.RequestHead, env graph.Envelope) Record {
	at := b.now().UTC()
	m := replaywire.RecordMetadata{ID: uuid.Must(uuid.NewV7()).String(), Kind: replaywire.KindInspection,
		Flow: flow, NodeID: nodeID, NodeType: "inspect", EventID: head.EventID,
		RequestID: head.RequestID, TraceID: head.TraceID, ReceivedAt: at, OccurredAt: at,
		NodeConfigHash: NodeConfigHash(b.nodes[flow][nodeID]), CoreVersion: b.version, CoreInstance: b.instance,
		NotRetryableReason: replaywire.NotRetryableInspectionOnly}
	if ce, ok := env.Extra.(flowconfig.CapturedEvent); ok {
		m.IdempotencyKey = ce.Event.IdempotencyKey
		if !ce.Event.ReceivedAt.IsZero() {
			m.ReceivedAt = ce.Event.ReceivedAt.UTC()
		}
	}
	sanitize(&m)
	rec := Record{Metadata: m}
	unavailable := func(err error) Record {
		if errors.Is(err, errInspectionTooLarge) {
			rec.Metadata.PayloadUnavailableReason = replaywire.NotRetryableTooLarge
		} else {
			rec.Metadata.PayloadUnavailableReason = replaywire.NotRetryableUnsupported
		}
		return rec
	}
	body, err := inspectionBody(env, b.maxBytes)
	if err != nil {
		return unavailable(err)
	}
	encoding, content := "utf8", ""
	if utf8.Valid(body.Bytes) && !bytes.ContainsRune(body.Bytes, 0) {
		content = string(body.Bytes)
	} else {
		encoding, content = "base64", base64.StdEncoding.EncodeToString(body.Bytes)
	}
	deny := append(append([]string(nil), b.inspectionDenylist...), b.inspectionAuthHeaders...)
	doc := replaywire.InspectionPayload{Version: replaywire.InspectionPayloadVersion,
		Body:      replaywire.InspectionBody{ContentType: body.ContentType, Encoding: encoding, Content: content},
		Headers:   logging.FilterHeaders(env.V.Raw.Head.Header, b.inspectionAllowlist, deny),
		Variables: maps.Clone(env.Vars), Method: head.Method, Path: head.Path}
	if doc.Variables == nil {
		doc.Variables = map[string]string{}
	}
	buf := &inspectionBuffer{limit: b.maxBytes}
	if err := json.NewEncoder(buf).Encode(doc); err != nil {
		return unavailable(err)
	}
	rec.Payload = bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
	rec.Metadata.Payload = &replaywire.ArtifactInfo{Size: int64(len(rec.Payload)), SHA256: digest(rec.Payload), ContentType: "application/json", Filename: "inspection.json"}
	return rec
}

func (r *Request) OnInspection(nodeID string, env graph.Envelope) {
	if r == nil || !r.c.inspectEnabled[r.flow] {
		return
	}
	u := r.c.uploader
	p := pending{flow: r.flow, nodeID: nodeID, eventID: r.head.EventID, inspection: true,
		estimate: 2*min(envelopeEstimate(env), r.c.builder.maxBytes) + estimateOverhead}
	if !u.reserve(p) {
		return
	}
	// Release reservations even when a serialization implementation panics.
	reserved := true
	defer func() {
		if reserved {
			u.release(p.estimate)
			u.drop(p, DropRejected, nil)
		}
	}()
	rec := r.c.builder.FromInspection(r.flow, nodeID, r.head, env)
	size := 2*rec.size() + estimateOverhead
	u.mu.Lock()
	if u.bytes+size-p.estimate > maxPendingBytes {
		u.mu.Unlock()
		u.release(p.estimate)
		reserved = false
		u.drop(p, DropBufferFull, nil)
		return
	}
	u.bytes += size - p.estimate
	u.metrics.SetCaptureBufferBytes(u.bytes)
	u.mu.Unlock()
	p.estimate = size
	p.build = func() (Record, bool) { return rec, true }
	r.mu.Lock()
	r.inspections = append(r.inspections, p)
	r.mu.Unlock()
	reserved = false
}
