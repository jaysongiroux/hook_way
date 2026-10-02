package capture

import (
	"fmt"
	"reflect"
	"unicode/utf8"

	"github.com/jaysongiroux/hook_way/internal/deliverymeta"
	"github.com/jaysongiroux/hook_way/internal/deliverysnapshot"
	"github.com/jaysongiroux/hook_way/internal/flowconfig"
	"github.com/jaysongiroux/hook_way/internal/graph"
	"github.com/jaysongiroux/hook_way/internal/queue"
	"github.com/jaysongiroux/hook_way/internal/webhook"
)

// SnapshotFromEnvelope converts a node's input envelope into its snapshot
// form. The result shares env's maps and slices; encode it before anything
// can change env (nodes never do: I19).
func SnapshotFromEnvelope(env graph.Envelope) (deliverysnapshot.Envelope, error) {
	body, err := valueFromAny(env.Body, 0)
	if err != nil {
		return deliverysnapshot.Envelope{}, fmt.Errorf("body: %w", err)
	}
	out := deliverysnapshot.Envelope{
		Publish: publishToSnapshot(env.Publish),
		Verified: deliverysnapshot.Verified{
			Raw:      deliverysnapshot.RawRequest{Head: headToSnapshot(env.V.Raw.Head), Body: env.V.Raw.Body},
			Identity: env.V.Identity,
		},
		Body:              body,
		BodyFormat:        env.BodyFormat,
		Vars:              env.Vars,
		Branch:            env.Branch,
		MessageAttributes: graphAttrsToSnapshot(env.MessageAttributes),
		PayloadMode:       env.PayloadMode,
	}
	if env.Upstream != nil {
		out.Upstream = &deliverysnapshot.Upstream{Status: env.Upstream.Status, Header: env.Upstream.Header, Body: env.Upstream.Body, Values: env.Upstream.Values, SelectionError: env.Upstream.SelectionError}
	}
	switch extra := env.Extra.(type) {
	case nil:
	case flowconfig.CapturedEvent:
		event, err := webhookToSnapshot(extra.Event)
		if err != nil {
			return deliverysnapshot.Envelope{}, fmt.Errorf("captured event: %w", err)
		}
		out.CapturedEvent = &deliverysnapshot.CapturedEvent{Event: event}
	default:
		return deliverysnapshot.Envelope{}, fmt.Errorf("%w: envelope extra of type %T", ErrUnsupported, env.Extra)
	}
	if path, bad := firstInvalidUTF8(reflect.ValueOf(out), "envelope"); bad {
		return deliverysnapshot.Envelope{}, fmt.Errorf("%w: %s is not valid UTF-8", ErrUnsupported, path)
	}
	return out, nil
}

// EnvelopeFromSnapshot reverses SnapshotFromEnvelope.
func EnvelopeFromSnapshot(s deliverysnapshot.Envelope) (graph.Envelope, error) {
	body, err := anyFromValue(s.Body, 0)
	if err != nil {
		return graph.Envelope{}, fmt.Errorf("body: %w", err)
	}
	out := graph.Envelope{
		Publish: publishFromSnapshot(s.Publish),
		V: graph.Verified{
			Raw:      graph.RawRequest{Head: headFromSnapshot(s.Verified.Raw.Head), Body: s.Verified.Raw.Body},
			Identity: s.Verified.Identity,
		},
		Body:              body,
		BodyFormat:        s.BodyFormat,
		Vars:              s.Vars,
		Branch:            s.Branch,
		MessageAttributes: graphAttrsFromSnapshot(s.MessageAttributes),
		PayloadMode:       s.PayloadMode,
	}
	if s.Upstream != nil {
		out.Upstream = &graph.UpstreamResponse{Status: s.Upstream.Status, Header: s.Upstream.Header, Body: s.Upstream.Body, Values: s.Upstream.Values, SelectionError: s.Upstream.SelectionError}
	}
	if s.CapturedEvent != nil {
		event, err := webhookFromSnapshot(s.CapturedEvent.Event)
		if err != nil {
			return graph.Envelope{}, fmt.Errorf("captured event: %w", err)
		}
		out.Extra = flowconfig.CapturedEvent{Event: event}
	}
	return out, nil
}

// SnapshotFromMessage converts a prepared message into its snapshot form.
func SnapshotFromMessage(msg queue.Message) (deliverysnapshot.Message, error) {
	query, err := valueFromAny(msg.Query, 0)
	if err != nil {
		return deliverysnapshot.Message{}, fmt.Errorf("query: %w", err)
	}
	body, err := valueFromAny(msg.Body, 0)
	if err != nil {
		return deliverysnapshot.Message{}, fmt.Errorf("body: %w", err)
	}
	auth, err := authToSnapshot(msg.Auth)
	if err != nil {
		return deliverysnapshot.Message{}, err
	}
	out := deliverysnapshot.Message{
		Publish:       publishToSnapshot(msg.Publish),
		SchemaVersion: msg.SchemaVersion, ID: msg.ID, IdempotencyKey: msg.IdempotencyKey,
		RequestID: msg.RequestID, TraceID: msg.TraceID, Traceparent: msg.Traceparent,
		Endpoint: msg.Endpoint, DestinationQueueID: msg.DestinationQueueID, Queue: msg.Queue,
		ReceivedAt: msg.ReceivedAt, Method: msg.Method, Path: msg.Path, Headers: msg.Headers,
		Query: query, Body: body, RawBodyBase64: msg.RawBodyBase64, SourceIP: msg.SourceIP, Auth: auth,
		Variables: msg.Variables, RawBody: msg.RawBody, OrigHeaders: msg.OrigHeaders, Hops: msg.Hops,
		MessageAttributes: queueAttrsToSnapshot(msg.MessageAttributes),
		PayloadMode:       msg.PayloadMode, ObjectBytes: msg.ObjectBytes,
		ObjectContentType: msg.ObjectContentType, ObjectFilename: msg.ObjectFilename,
	}
	if msg.DeadLetter != nil {
		dl := *msg.DeadLetter
		out.DeadLetter = &deliverysnapshot.DeadLetterMeta{Destination: dl.Destination, Target: dl.Target, Outcome: dl.Outcome, Attempts: dl.Attempts, FailedAt: dl.FailedAt}
	}
	if path, bad := firstInvalidUTF8(reflect.ValueOf(out), "message"); bad {
		return deliverysnapshot.Message{}, fmt.Errorf("%w: %s is not valid UTF-8", ErrUnsupported, path)
	}
	return out, nil
}

// firstInvalidUTF8 returns the path of a string reachable from v that is not
// valid UTF-8. encoding/json would rewrite such a string to U+FFFD, so a
// redrive would send different header bytes or route on different variables.
// Body strings never reach here: valueFromAny carries them as bytes.
func firstInvalidUTF8(v reflect.Value, path string) (string, bool) {
	switch v.Kind() {
	case reflect.String:
		if !utf8.ValidString(v.String()) {
			return path, true
		}
	case reflect.Pointer, reflect.Interface:
		if !v.IsNil() {
			return firstInvalidUTF8(v.Elem(), path)
		}
	case reflect.Struct:
		for i := range v.NumField() {
			if f := v.Type().Field(i); f.IsExported() {
				if p, bad := firstInvalidUTF8(v.Field(i), path+"."+f.Name); bad {
					return p, true
				}
			}
		}
	case reflect.Map:
		iter := v.MapRange()
		for iter.Next() {
			key := fmt.Sprintf("%s[%q]", path, fmt.Sprint(iter.Key()))
			if p, bad := firstInvalidUTF8(iter.Key(), key+" (key)"); bad {
				return p, true
			}
			if p, bad := firstInvalidUTF8(iter.Value(), key); bad {
				return p, true
			}
		}
	case reflect.Slice, reflect.Array:
		if v.Type().Elem().Kind() == reflect.Uint8 {
			return "", false
		}
		for i := range v.Len() {
			if p, bad := firstInvalidUTF8(v.Index(i), fmt.Sprintf("%s[%d]", path, i)); bad {
				return p, true
			}
		}
	}
	return "", false
}

// MessageFromSnapshot reverses SnapshotFromMessage.
func MessageFromSnapshot(s deliverysnapshot.Message) (queue.Message, error) {
	query, err := mapFromValue(s.Query)
	if err != nil {
		return queue.Message{}, fmt.Errorf("query: %w", err)
	}
	body, err := anyFromValue(s.Body, 0)
	if err != nil {
		return queue.Message{}, fmt.Errorf("body: %w", err)
	}
	out := queue.Message{
		Publish:       publishFromSnapshot(s.Publish),
		SchemaVersion: s.SchemaVersion, ID: s.ID, IdempotencyKey: s.IdempotencyKey,
		RequestID: s.RequestID, TraceID: s.TraceID, Traceparent: s.Traceparent,
		Endpoint: s.Endpoint, DestinationQueueID: s.DestinationQueueID, Queue: s.Queue,
		ReceivedAt: s.ReceivedAt, Method: s.Method, Path: s.Path, Headers: s.Headers,
		Query: query, Body: body, RawBodyBase64: s.RawBodyBase64, SourceIP: s.SourceIP,
		Variables: s.Variables, RawBody: s.RawBody, OrigHeaders: s.OrigHeaders, Hops: s.Hops,
		MessageAttributes: queueAttrsFromSnapshot(s.MessageAttributes),
		PayloadMode:       s.PayloadMode, ObjectBytes: s.ObjectBytes,
		ObjectContentType: s.ObjectContentType, ObjectFilename: s.ObjectFilename,
	}
	if s.Auth != nil {
		out.Auth = webhook.AuthInfo{Type: s.Auth.Type, Scheme: s.Auth.Scheme, Verified: s.Auth.Verified}
	}
	if s.DeadLetter != nil {
		dl := *s.DeadLetter
		out.DeadLetter = &queue.DeadLetterMeta{Destination: dl.Destination, Target: dl.Target, Outcome: dl.Outcome, Attempts: dl.Attempts, FailedAt: dl.FailedAt}
	}
	return out, nil
}

func headToSnapshot(h graph.RequestHead) deliverysnapshot.RequestHead {
	return deliverysnapshot.RequestHead{
		Method: h.Method, Path: h.Path, PathVars: h.PathVars, Query: h.Query, Header: h.Header,
		SourceIP: h.SourceIP, Hops: h.Hops, RequestID: h.RequestID, TraceID: h.TraceID,
		Traceparent: h.Traceparent, EventID: h.EventID,
	}
}

func headFromSnapshot(h deliverysnapshot.RequestHead) graph.RequestHead {
	return graph.RequestHead{
		Method: h.Method, Path: h.Path, PathVars: h.PathVars, Query: h.Query, Header: h.Header,
		SourceIP: h.SourceIP, Hops: h.Hops, RequestID: h.RequestID, TraceID: h.TraceID,
		Traceparent: h.Traceparent, EventID: h.EventID,
	}
}

func webhookToSnapshot(e webhook.Envelope) (deliverysnapshot.WebhookEnvelope, error) {
	query, err := valueFromAny(e.Query, 0)
	if err != nil {
		return deliverysnapshot.WebhookEnvelope{}, fmt.Errorf("query: %w", err)
	}
	body, err := valueFromAny(e.Body, 0)
	if err != nil {
		return deliverysnapshot.WebhookEnvelope{}, fmt.Errorf("body: %w", err)
	}
	return deliverysnapshot.WebhookEnvelope{
		SchemaVersion: e.SchemaVersion, ID: e.ID, IdempotencyKey: e.IdempotencyKey,
		RequestID: e.RequestID, TraceID: e.TraceID, Traceparent: e.Traceparent,
		Endpoint: e.Endpoint, DestinationQueueID: e.DestinationQueueID, Queue: e.Queue,
		ReceivedAt: e.ReceivedAt, Method: e.Method, Path: e.Path, Headers: e.Headers,
		Query: query, Body: body, RawBodyBase64: e.RawBodyBase64, SourceIP: e.SourceIP,
		Auth:      deliverysnapshot.AuthInfo{Type: e.Auth.Type, Scheme: e.Auth.Scheme, Verified: e.Auth.Verified},
		Variables: e.Variables, RawBody: e.RawBody, OrigHeaders: e.OrigHeaders, Hops: e.Hops,
	}, nil
}

func webhookFromSnapshot(s deliverysnapshot.WebhookEnvelope) (webhook.Envelope, error) {
	query, err := mapFromValue(s.Query)
	if err != nil {
		return webhook.Envelope{}, fmt.Errorf("query: %w", err)
	}
	body, err := anyFromValue(s.Body, 0)
	if err != nil {
		return webhook.Envelope{}, fmt.Errorf("body: %w", err)
	}
	return webhook.Envelope{
		SchemaVersion: s.SchemaVersion, ID: s.ID, IdempotencyKey: s.IdempotencyKey,
		RequestID: s.RequestID, TraceID: s.TraceID, Traceparent: s.Traceparent,
		Endpoint: s.Endpoint, DestinationQueueID: s.DestinationQueueID, Queue: s.Queue,
		ReceivedAt: s.ReceivedAt, Method: s.Method, Path: s.Path, Headers: s.Headers,
		Query: query, Body: body, RawBodyBase64: s.RawBodyBase64, SourceIP: s.SourceIP,
		Auth:      webhook.AuthInfo{Type: s.Auth.Type, Scheme: s.Auth.Scheme, Verified: s.Auth.Verified},
		Variables: s.Variables, RawBody: s.RawBody, OrigHeaders: s.OrigHeaders, Hops: s.Hops,
	}, nil
}

// mapFromValue decodes a value that must be a map[string]any (possibly nil).
func mapFromValue(v *deliverysnapshot.Value) (map[string]any, error) {
	decoded, err := anyFromValue(v, 0)
	if err != nil {
		return nil, err
	}
	if decoded == nil {
		return nil, nil
	}
	m, ok := decoded.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%w: want a map, got %T", ErrUnsupported, decoded)
	}
	return m, nil
}

func authToSnapshot(a any) (*deliverysnapshot.AuthInfo, error) {
	switch x := a.(type) {
	case nil:
		return nil, nil
	case webhook.AuthInfo:
		return &deliverysnapshot.AuthInfo{Type: x.Type, Scheme: x.Scheme, Verified: x.Verified}, nil
	default:
		return nil, fmt.Errorf("%w: message auth of type %T", ErrUnsupported, a)
	}
}

func graphAttrsToSnapshot(in map[string]graph.MessageAttribute) map[string]deliverysnapshot.MessageAttribute {
	if in == nil {
		return nil
	}
	out := make(map[string]deliverysnapshot.MessageAttribute, len(in))
	for k, a := range in {
		out[k] = deliverysnapshot.MessageAttribute{DataType: a.DataType, Value: a.Value}
	}
	return out
}

func graphAttrsFromSnapshot(in map[string]deliverysnapshot.MessageAttribute) map[string]graph.MessageAttribute {
	if in == nil {
		return nil
	}
	out := make(map[string]graph.MessageAttribute, len(in))
	for k, a := range in {
		out[k] = graph.MessageAttribute{DataType: a.DataType, Value: a.Value}
	}
	return out
}

func queueAttrsToSnapshot(in map[string]queue.MessageAttribute) map[string]deliverysnapshot.MessageAttribute {
	if in == nil {
		return nil
	}
	out := make(map[string]deliverysnapshot.MessageAttribute, len(in))
	for k, a := range in {
		out[k] = deliverysnapshot.MessageAttribute{DataType: a.DataType, Value: a.StringValue}
	}
	return out
}

func queueAttrsFromSnapshot(in map[string]deliverysnapshot.MessageAttribute) map[string]queue.MessageAttribute {
	if in == nil {
		return nil
	}
	out := make(map[string]queue.MessageAttribute, len(in))
	for k, a := range in {
		out[k] = queue.MessageAttribute{DataType: a.DataType, StringValue: a.Value}
	}
	return out
}

func publishToSnapshot(p *deliverymeta.Publish) *deliverysnapshot.Publish {
	if p == nil {
		return nil
	}
	out := &deliverysnapshot.Publish{NodeID: p.NodeID, Destination: p.Destination, Target: p.Target, WireBody: p.WireBody, Body: p.Body, GroupID: p.GroupID, DelaySeconds: p.DelaySeconds, Subject: p.Subject, MessageStructure: p.MessageStructure}
	if p.FIFO != nil {
		out.FIFO = &deliverysnapshot.FIFO{GroupID: p.FIFO.GroupID, DeduplicationID: p.FIFO.DeduplicationID, ContentBased: p.FIFO.ContentBased}
	}
	if p.Retry != nil {
		out.Retry = &deliverysnapshot.Retry{MaxAttempts: p.Retry.MaxAttempts, InitialBackoff: p.Retry.InitialBackoff, MaxBackoff: p.Retry.MaxBackoff}
	}
	return out
}
func publishFromSnapshot(p *deliverysnapshot.Publish) *deliverymeta.Publish {
	if p == nil {
		return nil
	}
	out := &deliverymeta.Publish{NodeID: p.NodeID, Destination: p.Destination, Target: p.Target, WireBody: p.WireBody, Body: p.Body, GroupID: p.GroupID, DelaySeconds: p.DelaySeconds, Subject: p.Subject, MessageStructure: p.MessageStructure}
	if p.FIFO != nil {
		out.FIFO = &deliverymeta.FIFO{GroupID: p.FIFO.GroupID, DeduplicationID: p.FIFO.DeduplicationID, ContentBased: p.FIFO.ContentBased}
	}
	if p.Retry != nil {
		out.Retry = &deliverymeta.Retry{MaxAttempts: p.Retry.MaxAttempts, InitialBackoff: p.Retry.InitialBackoff, MaxBackoff: p.Retry.MaxBackoff}
	}
	return out
}
