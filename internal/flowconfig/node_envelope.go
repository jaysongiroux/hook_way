package flowconfig

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"time"

	"github.com/jaysongiroux/hook_way/internal/graph"
	"github.com/jaysongiroux/hook_way/internal/transform"
	"github.com/jaysongiroux/hook_way/internal/webhook"
)

// CapturedEvent is the webhook envelope stored on graph.Envelope.Extra so
// publish can reuse identity and metadata without putting a struct in Body
// (filters read Body as parsed JSON).
type CapturedEvent struct {
	Event webhook.Envelope
}

func (c CapturedEvent) CloneBody() (any, error) {
	b, err := json.Marshal(c.Event)
	if err != nil {
		return nil, err
	}
	var event webhook.Envelope
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	if err := d.Decode(&event); err != nil {
		return nil, err
	}
	event.RawBody = slices.Clone(c.Event.RawBody)
	event.OrigHeaders = c.Event.OrigHeaders.Clone()
	event.Hops = c.Event.Hops
	return CapturedEvent{Event: event}, nil
}

type envelopeNode struct {
	id            string
	format        string
	multipart     multipartLimits
	xml           transform.XMLLimits
	allowlist     []string
	denylist      []string
	queryDenylist []string
}

func newEnvelopeNode(id string, opts EnvelopeOptions, allowlist, denylist []string) graph.Node {
	format := opts.BodyFormat
	if format == "" {
		format = "json"
	}
	return envelopeNode{
		id: id, format: format, multipart: opts.Multipart.limits(),
		xml:       transform.XMLLimits{MaxBytes: opts.XML.MaxBytes, MaxDepth: opts.XML.MaxDepth, MaxNodes: opts.XML.MaxNodes},
		allowlist: allowlist, denylist: denylist,
	}
}

func (n envelopeNode) Run(_ context.Context, in graph.Payload) (graph.Payload, error) {
	verified, ok := in.(graph.Verified)
	if !ok {
		return nil, graph.NodeError{
			NodeID: n.id, Status: http.StatusInternalServerError,
			Err: fmt.Errorf("envelope %q: expected Verified, got %T", n.id, in),
		}
	}
	head := verified.Raw.Head
	req := &http.Request{
		Method: head.Method,
		URL:    &url.URL{Path: head.Path, RawQuery: head.Query.Encode()},
		Header: head.Header,
	}
	event, err := webhook.BuildEnvelopeWithOptions(req, webhook.EnvelopeOptions{
		SkipBodyParsing: n.format != "json",
		Endpoint:        head.Path,
		RawBody:         verified.Raw.Body,
		ReceivedAt:      time.Now(),
		SourceIP:        head.SourceIP,
		RequestID:       head.RequestID,
		TraceID:         head.TraceID,
		Traceparent:     head.Traceparent,
		AuthType:        verified.Identity,
		AuthScheme:      verified.Identity,
		HeaderDenylist:  n.denylist,
		HeaderAllowlist: n.allowlist,
		Hops:            head.Hops,
	})
	if err != nil {
		return nil, graph.NodeError{
			NodeID: n.id, Status: http.StatusInternalServerError, Err: err,
		}
	}
	for _, key := range n.queryDenylist {
		if _, exists := event.Query[key]; exists {
			event.Query[key] = []string{"redacted"}
		}
	}
	if head.EventID != "" {
		event.ID = head.EventID
	}

	out := graph.Envelope{
		V:          verified,
		BodyFormat: n.format,
		Vars:       map[string]string{},
		Extra:      CapturedEvent{Event: event},
	}
	for k, v := range head.PathVars {
		out.Vars[k] = v
	}
	event.Variables = out.Vars

	switch n.format {
	case "raw":
		event.Body = nil
		out.Extra = CapturedEvent{Event: event}
		return out, nil
	case "multipart":
		// The signature was already verified against the raw bytes (I6), so
		// the parser only ever sees a trusted body. Bounds come from the
		// node's multipart block; both failures carry their own status so a
		// 413 reads differently from a 400 in the logs.
		parsed, status, err := parseMultipart(head.Header.Get("Content-Type"), verified.Raw.Body, n.multipart)
		if err != nil {
			return nil, graph.NodeError{NodeID: n.id, Status: status, Err: fmt.Errorf("envelope %q: %w", n.id, err)}
		}
		// event.Body stays the captured JSON view (nil here, since a multipart
		// body is not JSON); the parsed parts go on the branch body, which is
		// what filter, code, and publish read.
		event.Body = parsed
		out.Body = parsed
		out.Extra = CapturedEvent{Event: event}
		return out, nil
	case "xml":
		doc, err := transform.ParseXMLWithLimits(verified.Raw.Body, n.xml)
		if err != nil {
			status := http.StatusBadRequest
			if errors.Is(err, transform.ErrXMLLimit) {
				status = http.StatusRequestEntityTooLarge
			}
			return nil, graph.NodeError{NodeID: n.id, Status: status, Err: err}
		}
		event.Body = nil
		out.Body = doc
		out.Extra = CapturedEvent{Event: event}
		return out, nil
	default:
		out.Body = event.Body
		out.Extra = CapturedEvent{Event: event}
		return out, nil
	}
}
