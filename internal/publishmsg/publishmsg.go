// Package publishmsg builds the queue.Message a destination receives from the
// graph.Envelope a publish node hands its publisher. Serving uses it to
// publish; failure capture uses it to rebuild the message a failed publish
// sent, so the two can never drift.
package publishmsg

import (
	"encoding/base64"
	"fmt"
	"time"

	"github.com/jaysongiroux/hook_way/internal/flowconfig"
	"github.com/jaysongiroux/hook_way/internal/graph"
	"github.com/jaysongiroux/hook_way/internal/queue"
	"github.com/jaysongiroux/hook_way/internal/transform"
	"github.com/jaysongiroux/hook_way/internal/webhook"
)

// FromEnvelope converts env, as prepared by a publish node, into the message
// published to destID and target. Identity and redacted request metadata
// come from the captured event; the body and variables come from the branch
// after its transforms.
func FromEnvelope(env graph.Envelope, destID, target string) (msg queue.Message, err error) {
	defer func() {
		if err != nil {
			return
		}
		msg.Body = env.Body
		msg.Variables = env.Vars
		var doc *transform.Document
		switch body := env.Body.(type) {
		case transform.Document:
			doc = &body
		case *transform.Document:
			doc = body
		}
		if doc != nil {
			var raw []byte
			raw, err = transform.SerializeXML(*doc)
			if err != nil {
				return
			}
			encoded := base64.StdEncoding.EncodeToString(raw)
			msg.Body, msg.RawBodyBase64 = nil, &encoded
		} else if env.Body != nil {
			msg.RawBodyBase64 = nil
		}
		msg, err = attachObjectPayload(msg, env)
	}()
	if cap, ok := env.Extra.(flowconfig.CapturedEvent); ok {
		msg := envelopeToMessage(cap.Event)
		msg.DestinationQueueID = destID
		msg.Queue = target
		if env.Vars != nil {
			msg.Variables = env.Vars
		}
		return attachMessageAttributes(msg, env), nil
	}
	head := env.V.Raw.Head
	query := map[string]any{}
	for k, v := range head.Query {
		query[k] = append([]string(nil), v...)
	}
	var rawB64 *string
	if env.Body == nil && len(env.V.Raw.Body) > 0 {
		encoded := base64.StdEncoding.EncodeToString(env.V.Raw.Body)
		rawB64 = &encoded
	}
	return attachMessageAttributes(queue.Message{
		SchemaVersion: "1.0", ID: head.EventID, RequestID: head.RequestID, TraceID: head.TraceID,
		Traceparent: head.Traceparent, Endpoint: head.Path, DestinationQueueID: destID, Queue: target,
		ReceivedAt: time.Now().UTC(), Method: head.Method, Path: head.Path, Headers: head.Header,
		Query: query, Body: env.Body, RawBodyBase64: rawB64, SourceIP: head.SourceIP,
		Variables: env.Vars, RawBody: env.V.Raw.Body, OrigHeaders: head.Header, Hops: head.Hops,
	}, env), nil
}

func attachObjectPayload(msg queue.Message, env graph.Envelope) (queue.Message, error) {
	switch env.PayloadMode {
	case "", "envelope":
		msg.PayloadMode = "envelope"
		return msg, nil
	case "object":
		// Use the original request's Content-Type for raw bodies. msg.Headers is
		// intentionally redacted/allowlisted and may omit Content-Type entirely.
		var rawBody []byte
		var rawBodyBase64 *string
		var rawContentType string
		if env.BodyFormat == "raw" {
			rawBody = msg.RawBody
			rawBodyBase64 = msg.RawBodyBase64
			rawContentType = env.V.Raw.Head.Header.Get("Content-Type")
		}
		data, contentType, filename, err := queue.ObjectFromBody(
			env.Body, rawBody, rawBodyBase64, rawContentType,
		)
		if err != nil {
			return msg, err
		}
		msg.PayloadMode = "object"
		msg.ObjectBytes = data
		msg.ObjectContentType = contentType
		msg.ObjectFilename = filename
		return msg, nil
	default:
		return msg, fmt.Errorf("unknown publish payload mode %q", env.PayloadMode)
	}
}

func attachMessageAttributes(msg queue.Message, env graph.Envelope) queue.Message {
	if len(env.MessageAttributes) == 0 {
		return msg
	}
	msg.MessageAttributes = make(map[string]queue.MessageAttribute, len(env.MessageAttributes))
	for k, v := range env.MessageAttributes {
		msg.MessageAttributes[k] = queue.MessageAttribute{DataType: v.DataType, StringValue: v.Value}
	}
	return msg
}

// envelopeToMessage copies every event-level field from env into a
// queue.Message, leaving DestinationQueueID/Queue at env's own endpoint-level
// values. FromEnvelope calls it for a captured event and then
// overrides those two fields with the per-route destination and target.
func envelopeToMessage(env webhook.Envelope) queue.Message {
	return queue.Message{
		SchemaVersion: env.SchemaVersion, ID: env.ID, IdempotencyKey: env.IdempotencyKey, RequestID: env.RequestID, TraceID: env.TraceID,
		Traceparent: env.Traceparent,
		Endpoint:    env.Endpoint, DestinationQueueID: env.DestinationQueueID, Queue: env.Queue, ReceivedAt: env.ReceivedAt,
		Method: env.Method, Path: env.Path, Headers: env.Headers, Query: env.Query,
		Body: env.Body, RawBodyBase64: env.RawBodyBase64, SourceIP: env.SourceIP, Auth: env.Auth,
		Variables: env.Variables,
		RawBody:   env.RawBody, OrigHeaders: env.OrigHeaders, Hops: env.Hops,
	}
}
