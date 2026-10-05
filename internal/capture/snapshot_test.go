package capture

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jaysongiroux/hook_way/internal/deliverymeta"
	"github.com/jaysongiroux/hook_way/internal/deliverysnapshot"
	"github.com/jaysongiroux/hook_way/internal/flowconfig"
	"github.com/jaysongiroux/hook_way/internal/graph"
	"github.com/jaysongiroux/hook_way/internal/jsoncanon"
	"github.com/jaysongiroux/hook_way/internal/queue"
	"github.com/jaysongiroux/hook_way/internal/webhook"
)

func richEnvelope() graph.Envelope {
	b64 := "eyJhIjoxfQ=="
	return graph.Envelope{
		Publish: richPublish(),
		V: graph.Verified{
			Raw: graph.RawRequest{
				Head: graph.RequestHead{
					Method: http.MethodPost, Path: "/webhooks/orders",
					PathVars: map[string]string{"route": "orders"},
					Query:    url.Values{"q": {"1", "2"}},
					Header:   http.Header{"Content-Type": {"application/json"}, "X-Signature": {"sig"}},
					SourceIP: "10.0.0.1", Hops: 1, RequestID: "req", TraceID: "trace",
					Traceparent: "00-abc-def-01", EventID: "evt",
				},
				Body: []byte(`{"a":1}`),
			},
			Identity: "hmac",
		},
		Body:       map[string]any{"a": 1.0, "size": int64(9), "tags": []string{"x"}},
		BodyFormat: "json",
		Vars:       map[string]string{"route": "orders"},
		Upstream:   &graph.UpstreamResponse{Values: map[string]string{"created_id": "sample-1"}, SelectionError: "missing_field", Status: 202, Header: http.Header{"X-Up": {"1"}}, Body: []byte("up")},
		Extra: flowconfig.CapturedEvent{Event: webhook.Envelope{
			SchemaVersion: "1.0", ID: "evt", IdempotencyKey: "idem-evt", RequestID: "req", TraceID: "trace", Traceparent: "00-abc-def-01",
			Endpoint: "/webhooks/orders", DestinationQueueID: "orders-dest", Queue: "orders-queue", ReceivedAt: time.Date(2026, 9, 23, 12, 0, 0, 123, time.UTC),
			Method: http.MethodPost, Path: "/webhooks/orders",
			Headers:       http.Header{"Content-Type": {"application/json"}},
			Query:         map[string]any{"q": []any{"1", "2"}},
			Body:          map[string]any{"a": 1.0},
			RawBodyBase64: &b64, SourceIP: "10.0.0.1",
			Auth:        webhook.AuthInfo{Type: "hmac", Scheme: "hmac_sha256_hex", Verified: true},
			Variables:   map[string]string{"route": "orders"},
			RawBody:     []byte(`{"a":1}`),
			OrigHeaders: http.Header{"X-Signature": {"sig"}},
			Hops:        1,
		}},
		Branch:            "created",
		MessageAttributes: map[string]graph.MessageAttribute{"tenant": {DataType: "String", Value: "acme"}},
		PayloadMode:       "envelope",
	}
}

func roundTripEnvelope(t *testing.T, env graph.Envelope) graph.Envelope {
	t.Helper()
	dto, err := SnapshotFromEnvelope(env)
	if err != nil {
		t.Fatal(err)
	}
	data, err := deliverysnapshot.Encode(deliverysnapshot.Snapshot{Kind: deliverysnapshot.KindResume, Envelope: &dto})
	if err != nil {
		t.Fatal(err)
	}
	s, err := deliverysnapshot.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	out, err := EnvelopeFromSnapshot(*s.Envelope)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestEnvelopeRoundTripIsExact(t *testing.T) {
	env := richEnvelope()
	if got := roundTripEnvelope(t, env); !reflect.DeepEqual(got, env) {
		t.Fatalf("round trip changed the envelope:\n got  %#v\n want %#v", got, env)
	}
}

func TestSnapshotDropsRequestLocalRepresentation(t *testing.T) {
	env := richEnvelope()
	prepared, err := jsoncanon.Prepare(context.Background(), env.V.Raw.Body)
	if err != nil {
		t.Fatal(err)
	}
	env.V.OriginalJSON = prepared
	got := roundTripEnvelope(t, env)
	if got.V.OriginalJSON != nil {
		t.Fatal("request-local metadata escaped into snapshot")
	}
	env.V.OriginalJSON = nil
	if !reflect.DeepEqual(got, env) {
		t.Fatal("dropping transient representation changed persisted delivery contract")
	}
}

func TestSnapshotDropsRequestLocalQuotaIdentityAndGuardOwnership(t *testing.T) {
	env := richEnvelope()
	env.V.CredentialID = "credential"
	env.V.Subject = "subject"
	env.V.Tenant = "tenant"
	env.V.Issuer = "issuer"
	env.V.TrustScope = "trust"
	env.V.Reservations = map[string]graph.GuardReservation{"guard": {Key: "private-key", Owner: "private-owner"}}
	got := roundTripEnvelope(t, env)
	if got.V.CredentialID != "" || got.V.Subject != "" || got.V.Tenant != "" || got.V.Issuer != "" || got.V.TrustScope != "" || got.V.Reservations != nil {
		t.Fatal("request-local admission identity/ownership escaped into redrive")
	}
	env.V.CredentialID, env.V.Subject, env.V.Tenant, env.V.Issuer, env.V.TrustScope = "", "", "", "", ""
	env.V.Reservations = nil
	if !reflect.DeepEqual(got, env) {
		t.Fatal("dropping admission metadata changed persisted delivery contract")
	}
}

func TestZeroEnvelopeRoundTripsToZero(t *testing.T) {
	if got := roundTripEnvelope(t, graph.Envelope{}); !reflect.DeepEqual(got, graph.Envelope{}) {
		t.Fatalf("round trip = %#v, want the zero Envelope", got)
	}
}

func TestEnvelopeRejectsUnknownExtra(t *testing.T) {
	if _, err := SnapshotFromEnvelope(graph.Envelope{Extra: struct{}{}}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("SnapshotFromEnvelope = %v, want ErrUnsupported", err)
	}
}

func richMessage() queue.Message {
	b64 := "cmF3"
	return queue.Message{
		Publish:       richPublish(),
		SchemaVersion: "1.0", ID: "evt", IdempotencyKey: "idem", RequestID: "req", TraceID: "trace", Traceparent: "00-a-b-01",
		Endpoint: "/w", DestinationQueueID: "hook", Queue: "orders",
		ReceivedAt: time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC),
		Method:     http.MethodPost, Path: "/w",
		Headers:       http.Header{"A": {"b"}},
		Query:         map[string]any{"q": []string{"1"}},
		Body:          map[string]any{"n": int64(7)},
		RawBodyBase64: &b64, SourceIP: "10.0.0.1",
		Auth:              webhook.AuthInfo{Type: "hmac", Scheme: "hmac_sha256_hex", Verified: true},
		Variables:         map[string]string{"k": "v"},
		RawBody:           []byte("raw"),
		OrigHeaders:       http.Header{"X-Sig": {"s"}},
		Hops:              2,
		DeadLetter:        &queue.DeadLetterMeta{Destination: "hook", Target: "orders", Outcome: "5xx", Attempts: 3, FailedAt: time.Date(2026, 9, 23, 12, 1, 0, 0, time.UTC)},
		MessageAttributes: map[string]queue.MessageAttribute{"a": {DataType: "String", StringValue: "b"}},
		PayloadMode:       "object", ObjectBytes: []byte{0, 1}, ObjectContentType: "application/octet-stream", ObjectFilename: "f.bin",
	}
}

func roundTripMessage(t *testing.T, msg queue.Message) queue.Message {
	t.Helper()
	dto, err := SnapshotFromMessage(msg)
	if err != nil {
		t.Fatal(err)
	}
	data, err := deliverysnapshot.Encode(deliverysnapshot.Snapshot{Kind: deliverysnapshot.KindPublishOnly, Message: &dto})
	if err != nil {
		t.Fatal(err)
	}
	s, err := deliverysnapshot.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	out, err := MessageFromSnapshot(*s.Message)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestMessageRoundTripIsExact(t *testing.T) {
	msg := richMessage()
	if got := roundTripMessage(t, msg); !reflect.DeepEqual(got, msg) {
		t.Fatalf("round trip changed the message:\n got  %#v\n want %#v", got, msg)
	}
}

func TestMessageWithoutAuthStaysNil(t *testing.T) {
	msg := richMessage()
	msg.Auth = nil
	if got := roundTripMessage(t, msg); got.Auth != nil {
		t.Fatalf("Auth = %#v, want nil", got.Auth)
	}
}

func TestMessageRejectsUnknownAuthType(t *testing.T) {
	msg := richMessage()
	msg.Auth = map[string]string{"type": "x"}
	if _, err := SnapshotFromMessage(msg); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("SnapshotFromMessage = %v, want ErrUnsupported", err)
	}
}

// JSON cannot carry bytes that are not UTF-8: encoding/json rewrites them to
// U+FFFD, which would redrive different header bytes or path variables.
// Outside the body such values fail closed; capture then keeps a
// metadata-only record instead of a lossy snapshot.
func TestSnapshotRejectsInvalidUTF8OutsideTheBody(t *testing.T) {
	envCases := map[string]func(*graph.Envelope){
		"header value": func(e *graph.Envelope) { e.V.Raw.Head.Header = http.Header{"X-Custom": {"caf\xe9"}} },
		"variable":     func(e *graph.Envelope) { e.Vars = map[string]string{"route": "caf\xe9"} },
		"path":         func(e *graph.Envelope) { e.V.Raw.Head.Path = "/webhooks/caf\xe9" },
		"body map key": func(e *graph.Envelope) { e.Body = map[string]any{"caf\xe9": "x"} },
		"event headers": func(e *graph.Envelope) {
			ce := e.Extra.(flowconfig.CapturedEvent)
			ce.Event.OrigHeaders = http.Header{"X-Sig": {"\xff"}}
			e.Extra = ce
		},
	}
	for name, mutate := range envCases {
		t.Run("envelope "+name, func(t *testing.T) {
			env := richEnvelope()
			mutate(&env)
			if _, err := SnapshotFromEnvelope(env); !errors.Is(err, ErrUnsupported) || !strings.Contains(err.Error(), "UTF-8") {
				t.Fatalf("SnapshotFromEnvelope = %v, want an ErrUnsupported UTF-8 error", err)
			}
		})
	}
	t.Run("message header", func(t *testing.T) {
		msg := richMessage()
		msg.OrigHeaders = http.Header{"X-Sig": {"\xff"}}
		if _, err := SnapshotFromMessage(msg); !errors.Is(err, ErrUnsupported) || !strings.Contains(err.Error(), "UTF-8") {
			t.Fatalf("SnapshotFromMessage = %v, want an ErrUnsupported UTF-8 error", err)
		}
	})
}

func richPublish() *deliverymeta.Publish {
	delay := int32(10)
	return &deliverymeta.Publish{GroupID: "standard-group", DelaySeconds: &delay, Subject: "sample", MessageStructure: "json", NodeID: "out", Destination: "broker", Target: "events.fifo", WireBody: "json_js", Body: []byte(`{"k":1}`), FIFO: &deliverymeta.FIFO{GroupID: "group", DeduplicationID: "dedup", ContentBased: true}, Retry: &deliverymeta.Retry{MaxAttempts: 3, InitialBackoff: 100, MaxBackoff: 200}}
}
