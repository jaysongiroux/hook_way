package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jaysongiroux/hook_way/internal/capture"
	"github.com/jaysongiroux/hook_way/internal/deliverysnapshot"
	"github.com/jaysongiroux/hook_way/internal/flowconfig"
	"github.com/jaysongiroux/hook_way/internal/graph"
	"github.com/jaysongiroux/hook_way/internal/queue"
	"github.com/jaysongiroux/hook_way/internal/replaywire"
)

func TestNamedEventsExampleDeliveryAndRetries(t *testing.T) {
	var mu sync.Mutex
	var deliveries [][]byte
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		deliveries = append(deliveries, raw)
		attempt := len(deliveries)
		mu.Unlock()
		if r.URL.Path != "/issue-events" || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("incorrect destination request: %s %v", r.URL.Path, r.Header)
		}
		if attempt == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer receiver.Close()
	t.Setenv("SAMPLE_WEBHOOK_SECRET", "test-source-token")
	t.Setenv("WEBHOOK_URL", receiver.URL)
	raw, err := os.ReadFile("../../examples/adapters/named-events.yaml")
	if err != nil {
		t.Fatal(err)
	}
	cfg, issues := flowconfig.LoadBytes(raw, "example", true)
	if len(issues) != 0 {
		t.Fatal(issues)
	}
	logger := slog.New(slog.DiscardHandler)
	pub, err := queue.NewFactory(queue.FactoryOptions{Logger: logger}).Create(t.Context(), cfg.ServingConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pub.Close() }()
	srv, err := NewFlowServer(cfg, pub, logger, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Close() }()
	for _, test := range []struct {
		name, body, token string
		status, sends     int
	}{
		{"valid", `{"action":"opened","issue":{"id":"issue-1","title":"New issue"}}`, "test-source-token", 202, 2},
		{"invalid schema", `{"action":"opened","issue":{"id":"issue-2","title":12}}`, "test-source-token", 400, 2},
		{"missing field", `{"action":"opened","issue":{"id":"issue-2"}}`, "test-source-token", 400, 2},
		{"filtered", `{"action":"closed","issue":{"id":"issue-3","title":"Closed"}}`, "test-source-token", 202, 2},
		{"unverified", `{"action":"opened","issue":{"id":"issue-4","title":"Unverified"}}`, "incorrect-token", 401, 2},
		{"malformed", `{`, "test-source-token", 400, 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/webhooks/issues", strings.NewReader(test.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Api-Key", test.token)
			w := httptest.NewRecorder()
			srv.ServeHTTP(w, req)
			mu.Lock()
			count := len(deliveries)
			mu.Unlock()
			if w.Code != test.status || count != test.sends {
				t.Fatalf("status=%d body=%s deliveries=%d, want status=%d deliveries=%d", w.Code, w.Body.String(), count, test.status, test.sends)
			}
		})
	}
	mu.Lock()
	defer mu.Unlock()
	if !bytes.Equal(deliveries[0], deliveries[1]) {
		t.Fatal("retry rebuilt the event identity, timestamp or payload")
	}
	var event map[string]any
	if err := json.Unmarshal(deliveries[0], &event); err != nil {
		t.Fatal(err)
	}
	if len(event) != 4 || event["eventId"] != "issue-1" || event["name"] != "issue.opened" {
		t.Fatalf("incorrect occurrence: %#v", event)
	}
	if _, err := time.Parse(time.RFC3339Nano, event["timestamp"].(string)); err != nil {
		t.Fatal(err)
	}
	data := event["data"].(map[string]any)
	if len(data) != 2 || data["title"] != "New issue" || data["issue_id"] != "issue-1" || bytes.Contains(deliveries[0], []byte("test-source-token")) {
		t.Fatalf("payload projection leaked input metadata: %s", deliveries[0])
	}
}

func TestNamedEventCaptureAndDestinationRedriveKeepOccurrence(t *testing.T) {
	raw, err := os.ReadFile("../../examples/adapters/named-events.yaml")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("SAMPLE_WEBHOOK_SECRET", "test-source-token")
	t.Setenv("WEBHOOK_URL", "http://localhost:12345")
	cfg, issues := flowconfig.LoadBytes(bytes.ReplaceAll(raw, []byte(", event_id_path: issue_id"), nil), "example", true)
	if len(issues) > 0 {
		t.Fatal(issues)
	}
	catalog, _ := flowconfig.CompileEventCatalog(cfg.Events)
	registry, err := flowconfig.NewRegistryWithOptions(flowconfig.RegistryOptions{Events: catalog, Publishers: queueResolver{pub: &fakePublisher{}}})
	if err != nil {
		t.Fatal(err)
	}
	var event, publish graph.NodeConfig
	for _, node := range cfg.Flows["issues"].Nodes {
		if node.Type == "event" {
			event = node
		}
		if node.Type == "publish" {
			publish = node
		}
	}
	spec, _ := registry.Lookup("event")
	creator, err := spec.New(event)
	if err != nil {
		t.Fatal(err)
	}
	created, err := creator.Run(t.Context(), graph.Envelope{V: graph.Verified{Raw: graph.RawRequest{Body: []byte(`{}`)}}, BodyFormat: "json", Body: map[string]any{"issue_id": "id", "title": "Keep"}})
	if err != nil {
		t.Fatal(err)
	}
	env := created.(graph.Envelope)
	opts, err := flowconfig.PublishNodeOptions(publish)
	if err != nil {
		t.Fatal(err)
	}
	_, prepared, err := flowconfig.PreparePublishForNode(publish.ID, opts, env)
	if err != nil {
		t.Fatal(err)
	}
	builder := capture.NewBuilder(capture.BuilderOptions{Flows: cfg.Flows, Destinations: cfg.Destinations, MaxRecordBytes: 1 << 20})
	record, ok := builder.FromFailure("issues", graph.RequestHead{}, 202, graph.FailureEvent{NodeID: publish.ID, NodeType: "publish", Input: prepared, Err: context.DeadlineExceeded})
	if !ok || !record.Metadata.Retryable {
		t.Fatalf("event publication could not be captured: %+v", record.Metadata)
	}
	snapshot, err := deliverysnapshot.Decode(record.Snapshot)
	if err != nil || snapshot.Envelope == nil {
		t.Fatalf("snapshot: %v", err)
	}
	restored, err := capture.EnvelopeFromSnapshot(*snapshot.Envelope)
	if err != nil {
		t.Fatal(err)
	}
	_, resumed, err := flowconfig.PreparePublishForNode(publish.ID, opts, restored)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(prepared.Publish.Body, resumed.Publish.Body) || !bytes.Equal(record.Payload, prepared.Publish.Body) {
		t.Fatalf("capture/redrive changed event JSON: before=%s after=%s capture=%s", prepared.Publish.Body, resumed.Publish.Body, record.Payload)
	}
	pub := &fakePublisher{}
	srv, err := NewFlowServer(cfg, pub, slog.New(slog.DiscardHandler), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Close() }()
	result, err := srv.Redrive(t.Context(), replaywire.DeliverRequest{
		Flow: "issues", NodeID: publish.ID, NodeType: "publish", ReplayMode: replaywire.ReplayResume,
		DestinationID: opts.Destination, DestinationType: "webhook", RenderedTarget: opts.Target,
		NodeConfigHash: record.Metadata.NodeConfigHash, DestinationHash: record.Metadata.DestinationHash, DeadlineMS: 1000,
	}, snapshot)
	if err != nil || len(result.Nodes) != 1 || result.Nodes[0].Outcome != replaywire.NodeDelivered || len(pub.published) != 1 {
		t.Fatalf("destination redrive failed: %+v %v", result, err)
	}
	if !bytes.Equal(pub.published[0].msg.Publish.Body, prepared.Publish.Body) {
		t.Fatal("real destination redrive reconstructed the occurrence")
	}
}
