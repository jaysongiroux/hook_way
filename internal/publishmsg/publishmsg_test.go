package publishmsg

import (
	"encoding/base64"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/jaysongiroux/hook_way/internal/flowconfig"
	"github.com/jaysongiroux/hook_way/internal/graph"
	"github.com/jaysongiroux/hook_way/internal/queue"
	"github.com/jaysongiroux/hook_way/internal/transform"
	"github.com/jaysongiroux/hook_way/internal/webhook"
)

func capturedEnvelope() graph.Envelope {
	received := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	return graph.Envelope{
		Body:       map[string]any{"event": "created"},
		BodyFormat: "json",
		Vars:       map[string]string{"route": "orders"},
		Extra: flowconfig.CapturedEvent{Event: webhook.Envelope{
			SchemaVersion: "1.0", ID: "evt-1", RequestID: "req-1", Endpoint: "/webhooks/orders",
			ReceivedAt: received, Method: http.MethodPost, Path: "/webhooks/orders",
			Headers:     http.Header{"Content-Type": {"application/json"}},
			Body:        map[string]any{"event": "original"},
			Auth:        webhook.AuthInfo{Type: "no_auth"},
			Variables:   map[string]string{"stale": "yes"},
			RawBody:     []byte(`{"event":"original"}`),
			OrigHeaders: http.Header{"X-Signature": {"abc"}},
			Hops:        2,
		}},
	}
}

func TestRawPublicationEncodesBytesDeferredByProxyIntake(t *testing.T) {
	for _, raw := range [][]byte{[]byte(`{"sample":true}`), {0xff, 0x00, 0xa5}} {
		env := capturedEnvelope()
		env.Body, env.BodyFormat = nil, "raw"
		event := env.Extra.(flowconfig.CapturedEvent)
		event.Event.Body, event.Event.RawBodyBase64, event.Event.RawBody = nil, nil, raw
		env.Extra = event
		msg, err := FromEnvelope(env, "primary", "sample")
		if err != nil {
			t.Fatal(err)
		}
		if msg.Body != nil || msg.RawBodyBase64 == nil || *msg.RawBodyBase64 != base64.StdEncoding.EncodeToString(raw) {
			t.Fatal("raw publication lost its bytes")
		}
		if env.Extra.(flowconfig.CapturedEvent).Event.RawBodyBase64 != nil {
			t.Fatal("publisher changed captured ingress")
		}
	}
}

func TestFromEnvelopeUsesTheCapturedEvent(t *testing.T) {
	env := capturedEnvelope()
	msg, err := FromEnvelope(env, "primary", "orders-queue")
	if err != nil {
		t.Fatal(err)
	}
	if msg.ID != "evt-1" || msg.DestinationQueueID != "primary" || msg.Queue != "orders-queue" {
		t.Fatalf("identity = %q %q %q", msg.ID, msg.DestinationQueueID, msg.Queue)
	}
	if !msg.ReceivedAt.Equal(time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("ReceivedAt = %v, want the captured event's", msg.ReceivedAt)
	}
	if !reflect.DeepEqual(msg.Body, env.Body) || !reflect.DeepEqual(msg.Variables, env.Vars) {
		t.Fatalf("Body/Variables must come from the branch envelope: %#v %#v", msg.Body, msg.Variables)
	}
	if string(msg.RawBody) != `{"event":"original"}` || msg.OrigHeaders.Get("X-Signature") != "abc" || msg.Hops != 2 {
		t.Fatalf("proxy fields not copied: %q %v %d", msg.RawBody, msg.OrigHeaders, msg.Hops)
	}
	if msg.PayloadMode != "envelope" {
		t.Fatalf("PayloadMode = %q, want envelope", msg.PayloadMode)
	}
}

func TestFromEnvelopeSerializesXMLBodies(t *testing.T) {
	doc, err := transform.ParseXMLWithLimits([]byte("<order><id>7</id></order>"), transform.XMLLimits{})
	if err != nil {
		t.Fatal(err)
	}
	env := capturedEnvelope()
	env.Body, env.BodyFormat = doc, "xml"
	msg, err := FromEnvelope(env, "primary", "q")
	if err != nil {
		t.Fatal(err)
	}
	want, err := transform.SerializeXML(doc)
	if err != nil {
		t.Fatal(err)
	}
	if msg.Body != nil || msg.RawBodyBase64 == nil || *msg.RawBodyBase64 != base64.StdEncoding.EncodeToString(want) {
		t.Fatalf("XML body = %#v / %v, want nil body and base64 of the serialized document", msg.Body, msg.RawBodyBase64)
	}
}

func TestFromEnvelopeObjectMode(t *testing.T) {
	env := capturedEnvelope()
	env.PayloadMode = "object"
	env.Body = map[string]any{
		"content":      base64.StdEncoding.EncodeToString([]byte("hello")),
		"filename":     "a.txt",
		"content_type": "text/plain",
	}
	msg, err := FromEnvelope(env, "archive", "uploads")
	if err != nil {
		t.Fatal(err)
	}
	if msg.PayloadMode != "object" || string(msg.ObjectBytes) != "hello" || msg.ObjectFilename != "a.txt" || msg.ObjectContentType != "text/plain" {
		t.Fatalf("object fields = %q %q %q %q", msg.PayloadMode, msg.ObjectBytes, msg.ObjectFilename, msg.ObjectContentType)
	}
}

func TestFromEnvelopeCopiesMessageAttributes(t *testing.T) {
	env := capturedEnvelope()
	env.MessageAttributes = map[string]graph.MessageAttribute{"tenant": {DataType: "String", Value: "acme"}}
	msg, err := FromEnvelope(env, "queue", "q")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]queue.MessageAttribute{"tenant": {DataType: "String", StringValue: "acme"}}
	if !reflect.DeepEqual(msg.MessageAttributes, want) {
		t.Fatalf("MessageAttributes = %#v, want %#v", msg.MessageAttributes, want)
	}
}

func TestObjectPayloadRawFallbackRequiresRawBodyFormat(t *testing.T) {
	rawBase64 := base64.StdEncoding.EncodeToString([]byte("null"))
	msg := queue.Message{RawBody: []byte("null"), RawBodyBase64: &rawBase64}
	env := graph.Envelope{BodyFormat: "json", PayloadMode: "object"}
	if _, err := attachObjectPayload(msg, env); err == nil {
		t.Fatal("JSON null was accepted as a raw object payload")
	}
}
