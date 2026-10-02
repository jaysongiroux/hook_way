package capture

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/jaysongiroux/hook_way/internal/config"
	"github.com/jaysongiroux/hook_way/internal/deliverysnapshot"
	"github.com/jaysongiroux/hook_way/internal/flowconfig"
	"github.com/jaysongiroux/hook_way/internal/graph"
	"github.com/jaysongiroux/hook_way/internal/queue"
	"github.com/jaysongiroux/hook_way/internal/replaywire"
	"github.com/jaysongiroux/hook_way/internal/webhook"
)

var fixedNow = time.Date(2026, 9, 23, 12, 0, 5, 0, time.UTC)

func testBuilder(t *testing.T, maxBytes int64) *Builder {
	t.Helper()
	var fc graph.FlowConfig
	if err := yaml.Unmarshal([]byte(`
nodes:
  - {id: out, type: publish, destination: queue, target: "orders-{tenant}"}
  - {id: shape, type: code, script: "return {body: input.body}"}
  - {id: hook, type: publish, destination: notify, target: inbound}
edges: []
`), &fc); err != nil {
		t.Fatal(err)
	}
	return NewBuilder(BuilderOptions{
		Flows: map[string]graph.FlowConfig{"orders": fc},
		Destinations: map[string]config.DestinationConfig{
			"queue":  {Type: "sqs"},
			"notify": {Type: "webhook", Webhook: config.WebhookConfig{URL: "https://hooks.example.com"}},
		},
		MaxRecordBytes: maxBytes, CoreVersion: "v1.2.3", CoreInstance: "core-0", Now: func() time.Time { return fixedNow },
	})
}

func testEnvelope() graph.Envelope {
	return graph.Envelope{
		V:    graph.Verified{Raw: graph.RawRequest{Head: graph.RequestHead{Method: http.MethodPost, Path: "/hooks"}, Body: []byte(`{"a":1}`)}},
		Body: map[string]any{"a": 1.0}, BodyFormat: "json", Vars: map[string]string{"tenant": "acme"},
		Extra: flowconfig.CapturedEvent{Event: webhook.Envelope{ID: "evt-1", IdempotencyKey: "idem-1",
			ReceivedAt: time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC), Variables: map[string]string{"tenant": "acme"}}},
	}
}

var testHead = graph.RequestHead{EventID: "evt-1", RequestID: "req-1", TraceID: "trace-1"}

func sha(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func TestFromFailureBuildsAPublishRecord(t *testing.T) {
	b := testBuilder(t, 1<<20)
	failure := &queue.DeliveryError{Outcome: "5xx", Attempts: 3, Err: errors.New("sqs: 503")}
	rec, ok := b.FromFailure("orders", testHead, 202, graph.FailureEvent{NodeID: "out", NodeType: "publish", Input: testEnvelope(), Err: failure})
	if !ok {
		t.Fatal("record not built")
	}
	m := rec.Metadata
	if err := m.Validate(); err != nil {
		t.Fatalf("metadata invalid: %v", err)
	}
	want := replaywire.FailureMetadata{
		ID: m.ID, Kind: replaywire.KindResume, Flow: "orders", NodeID: "out", NodeType: "publish",
		DestinationID: "queue", DestinationType: "sqs", Target: "orders-{tenant}", RenderedTarget: "orders-acme",
		EventID: "evt-1", RequestID: "req-1", TraceID: "trace-1", IdempotencyKey: "idem-1",
		ReceivedAt: time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC), FailedAt: fixedNow, InboundStatus: 202,
		ErrorClass: replaywire.ErrorClass5xx, ErrorMessage: "sqs: 503", Attempts: 3,
		NodeConfigHash: m.NodeConfigHash, DestinationHash: m.DestinationHash, CoreVersion: "v1.2.3", CoreInstance: "core-0",
		Retryable: true,
		Snapshot:  &replaywire.ArtifactInfo{Size: int64(len(rec.Snapshot)), SHA256: sha(rec.Snapshot), Version: deliverysnapshot.Version},
		Payload:   &replaywire.ArtifactInfo{Size: int64(len(rec.Payload)), SHA256: sha(rec.Payload), ContentType: "application/json"},
	}
	if m.NodeConfigHash == "" || m.DestinationHash == "" {
		t.Fatalf("hashes missing: %+v", m)
	}
	if !reflect.DeepEqual(m, want) {
		t.Fatalf("metadata = %+v %+v %+v\nwant       %+v %+v %+v", m, *m.Snapshot, *m.Payload, want, *want.Snapshot, *want.Payload)
	}
	if !strings.Contains(string(rec.Payload), `"queue":"orders-acme"`) {
		t.Fatalf("payload is not the rendered envelope: %s", rec.Payload)
	}
	s, err := deliverysnapshot.Decode(rec.Snapshot)
	if err != nil || s.Kind != deliverysnapshot.KindResume || s.Envelope == nil {
		t.Fatalf("snapshot = %+v, %v", s, err)
	}
}

func TestFromFailureUsesTheInputBodyForOtherNodes(t *testing.T) {
	b := testBuilder(t, 1<<20)
	rec, ok := b.FromFailure("orders", testHead, 202, graph.FailureEvent{NodeID: "shape", NodeType: "code", Input: testEnvelope(), Err: errors.New("boom")})
	if !ok || rec.Metadata.DestinationID != "" || string(rec.Payload) != `{"a":1}` || rec.Metadata.ErrorClass != replaywire.ErrorClassOther {
		t.Fatalf("record = %+v payload %s", rec.Metadata, rec.Payload)
	}
}

func TestFromFailureSkipsFailuresBeforeTheEnvelope(t *testing.T) {
	b := testBuilder(t, 1<<20)
	if _, ok := b.FromFailure("orders", testHead, 202, graph.FailureEvent{NodeID: "auth", NodeType: "auth", Input: graph.RawRequest{}}); ok {
		t.Fatal("a pre-envelope failure produced a record")
	}
}

func TestFromFailureMakesOversizedRecordsMetadataOnly(t *testing.T) {
	b := testBuilder(t, 16)
	rec, _ := b.FromFailure("orders", testHead, 202, graph.FailureEvent{NodeID: "out", NodeType: "publish", Input: testEnvelope(), Err: errors.New("x")})
	m := rec.Metadata
	if m.Retryable || m.NotRetryableReason != replaywire.NotRetryableTooLarge || m.Snapshot != nil || m.Payload != nil || rec.Snapshot != nil || rec.Payload != nil {
		t.Fatalf("record = %+v", m)
	}
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestFromFailureMakesUnsnapshottableRecordsMetadataOnly(t *testing.T) {
	b := testBuilder(t, 1<<20)
	env := testEnvelope()
	env.Body = map[string]any{"n": struct{}{}} // not a body type the executor produces
	rec, _ := b.FromFailure("orders", testHead, 202, graph.FailureEvent{NodeID: "shape", NodeType: "code", Input: env, Err: errors.New("x")})
	if rec.Metadata.Retryable || rec.Metadata.NotRetryableReason != replaywire.NotRetryableUnsupported {
		t.Fatalf("record = %+v", rec.Metadata)
	}
}

func TestFromTerminalBuildsAPublishOnlyRecord(t *testing.T) {
	b := testBuilder(t, 1<<20)
	msg := queue.Message{ID: "evt-9", RequestID: "req-9", IdempotencyKey: "idem-9", ReceivedAt: time.Date(2026, 9, 23, 11, 0, 0, 0, time.UTC), Body: map[string]any{"a": 1.0}, PayloadMode: "envelope"}
	failedAt := time.Date(2026, 9, 23, 11, 0, 30, 0, time.UTC)
	rec := b.FromTerminal(queue.TerminalFailure{DestinationID: "notify", Target: "inbound", RenderedTarget: "inbound", Message: msg,
		Outcome: "4xx", Attempts: 1, Err: errors.New("404"), FailedAt: failedAt, Origin: queue.Origin{Flow: "orders", NodeID: "hook"}}, 202)
	m := rec.Metadata
	if err := m.Validate(); err != nil {
		t.Fatalf("metadata invalid: %v", err)
	}
	if m.Kind != replaywire.KindPublishOnly || m.Flow != "orders" || m.NodeID != "hook" || m.DestinationType != "webhook" ||
		m.RenderedTarget != "inbound" || m.EventID != "evt-9" || m.ErrorClass != replaywire.ErrorClass4xx || !m.FailedAt.Equal(failedAt) || !m.Retryable {
		t.Fatalf("metadata = %+v", m)
	}
	s, err := deliverysnapshot.Decode(rec.Snapshot)
	if err != nil || s.Kind != deliverysnapshot.KindPublishOnly || s.Message == nil || s.Message.ID != "evt-9" {
		t.Fatalf("snapshot = %+v, %v", s, err)
	}
}

func TestBuilderSanitizesUntrustedText(t *testing.T) {
	b := testBuilder(t, 1<<20)
	head := graph.RequestHead{EventID: "evt-1", RequestID: "req\x00\x1b[31m" + strings.Repeat("r", 2000), TraceID: "caf\xe9"}
	longErr := errors.New(strings.Repeat("e\x00", replaywire.MaxErrorMessageLen))
	env := testEnvelope()
	env.Body = map[string]any{"content": "aGk=", "filename": "../../etc/pass\x07wd", "content_type": "text/plain\r\nX: y"}
	env.PayloadMode = "object"
	rec, _ := b.FromFailure("orders", head, 202, graph.FailureEvent{NodeID: "shape", NodeType: "code", Input: env, Err: longErr})
	if err := rec.Metadata.Validate(); err != nil {
		t.Fatalf("sanitized metadata is still invalid: %v", err)
	}
}

func graphEvent() graph.FailureEvent {
	return graph.FailureEvent{NodeID: "out", NodeType: "publish", Input: testEnvelope(), Err: errors.New("sqs: 503")}
}

func TestBrokerFailureSnapshotFreezesPublication(t *testing.T) {
	var fc graph.FlowConfig
	if err := yaml.Unmarshal([]byte(`nodes:
  - id: out
    type: publish
    destination: broker
    target: events.fifo
    wire_body: json_js
    fifo:
      group_id: {source: json_js_sha256, hash_length: 10}
      deduplication_id: {source: json_js_sha256, hash_length: 10}
    message_attributes:
      changed: {data_type: String.Array, value: '["state"]'}
`), &fc); err != nil {
		t.Fatal(err)
	}
	builder := NewBuilder(BuilderOptions{Flows: map[string]graph.FlowConfig{"sample": fc}, Destinations: map[string]config.DestinationConfig{"broker": {Type: "sqs"}}, MaxRecordBytes: 1 << 20})
	env := testEnvelope()
	env.V.Raw.Body = []byte(` { "b": 2, "a": 1 } `)
	record, ok := builder.FromFailure("sample", testHead, 200, graph.FailureEvent{NodeID: "out", NodeType: "publish", Input: env, Err: errors.New("broker unavailable")})
	if !ok || !record.Metadata.Retryable || string(record.Payload) != `{"b":2,"a":1}` {
		t.Fatalf("record retryable=%v body=%s", record.Metadata.Retryable, record.Payload)
	}
	snapshot, err := deliverysnapshot.Decode(record.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := EnvelopeFromSnapshot(*snapshot.Envelope)
	if err != nil {
		t.Fatal(err)
	}
	original := restored.Publish
	restored.V.Raw.Body = []byte(`{"different":true}`)
	opts, err := flowconfig.PublishNodeOptions(fc.Nodes[0])
	if err != nil {
		t.Fatal(err)
	}
	_, prepared, err := flowconfig.PreparePublishForNode("out", opts, restored)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(prepared.Publish, original) || prepared.MessageAttributes["changed"].Value != `["state"]` {
		t.Fatal("redrive changed frozen broker publication")
	}
}

func TestCredentialFlowsNeverPersistCallerCredentialsOrRepeatCreate(t *testing.T) {
	for _, tc := range []struct {
		mode, options string
		metadataOnly  bool
	}{{"caller", "url: https://api.example.org/create\nrequire_headers: [X-Sample-Key]\nforward_headers: [X-Sample-Key]\n", true}, {"managed", "url: https://api.example.org/proxy\ncredential: {profile: sample}\n", false}} {
		t.Run(tc.mode, func(t *testing.T) {
			var options yaml.Node
			if err := yaml.Unmarshal([]byte(tc.options), &options); err != nil {
				t.Fatal(err)
			}
			fc := graph.FlowConfig{Nodes: []graph.NodeConfig{{ID: "up", Type: "http_request", Options: options}}}
			b := NewBuilder(BuilderOptions{Flows: map[string]graph.FlowConfig{"sample": fc}, Credentials: map[string]flowconfig.CredentialProfile{"sample": {Headers: map[string]flowconfig.CredentialHeader{"X-Sample-Key": {}}}}, MaxRecordBytes: 1 << 20})
			env := testEnvelope()
			env.Upstream = &graph.UpstreamResponse{Status: 200, Header: http.Header{"Set-Cookie": {"sample=synthetic-cookie"}, "X-Sample-Key": {"synthetic-caller-key"}}, Body: []byte(`{"ok":true}`)}
			env.V.Raw.Head.Header = http.Header{"X-Sample-Key": {"synthetic-caller-key"}, "Authorization": {"synthetic-caller-token"}, "Cookie": {"synthetic-cookie"}, "X-Keep": {"yes"}}
			record, ok := b.FromFailure("sample", testHead, 200, graph.FailureEvent{NodeID: "up", NodeType: "http_request", Input: env, Err: errors.New("upstream timeout")})
			if !ok {
				t.Fatal("missing metadata")
			}
			if strings.Contains(string(record.Snapshot), "synthetic-caller") || strings.Contains(string(record.Snapshot), "synthetic-cookie") {
				t.Fatal("persisted credential")
			}
			if tc.metadataOnly && (len(record.Snapshot) > 0 || len(record.Payload) > 0 || record.Metadata.Retryable) {
				t.Fatal("creation captured an automatic retry", record.Metadata)
			}
			if !tc.metadataOnly && len(record.Snapshot) == 0 {
				t.Fatal("managed proxy lost its redacted snapshot")
			}
			if env.V.Raw.Head.Header.Get("X-Sample-Key") != "synthetic-caller-key" {
				t.Fatal("capture mutated input")
			}
		})
	}
}
