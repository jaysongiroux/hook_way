package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jaysongiroux/hook_way/internal/capture"
	"github.com/jaysongiroux/hook_way/internal/control"
	"github.com/jaysongiroux/hook_way/internal/deliverysnapshot"
	"github.com/jaysongiroux/hook_way/internal/flowconfig"
	"github.com/jaysongiroux/hook_way/internal/metrics"
	"github.com/jaysongiroux/hook_way/internal/queue"
	"github.com/jaysongiroux/hook_way/internal/replaywire"
)

// redriveFlowYAML fans out to a healthy and a broken destination; the broken
// publish has a success continuation, so a redrive of it can fail downstream.
const redriveFlowYAML = `
server: {addr: ":8080", max_body_bytes: 1048576}
replay:
  url: "REPLAY_URL"
  capture_token: "capture-token-0123456789abcdefghijkl"
  control: {addr: ":9092", token: "control-token-0123456789abcdefghij"}
destinations:
  primary: {type: webhook, webhook: {url: "https://example.com/a"}}
  broken: {type: webhook, webhook: {url: "https://example.com/b"}}
  third: {type: webhook, webhook: {url: "https://example.com/c"}}
flows:
  orders:
    delivery_failure_policy: skip_failed
    nodes:
      - {id: in, type: http_trigger, path: /hooks/orders, methods: [POST]}
      - {id: body, type: read_body, max_bytes: 65536}
      - {id: auth, type: no_auth}
      - {id: env, type: envelope}
      - {id: ok, type: publish, destination: primary, target: a}
      - {id: bad, type: publish, destination: broken, target: b}
      - {id: after, type: publish, destination: third, target: c}
    edges: [in>body, body>auth, auth>env, env>ok, env>bad, bad>after]
`

type sentMessage struct {
	dest, target string
	msg          queue.Message
	inline       bool
	origin       bool
}

// redrivePublisher fails the destinations in fail and records every publish.
type redrivePublisher struct {
	mu   sync.Mutex
	fail map[string]error
	sent []sentMessage
}

func (p *redrivePublisher) Publish(ctx context.Context, dest, target string, msg queue.Message) error {
	_, origin := queue.OriginFrom(ctx)
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.fail[dest]; err != nil {
		if errors.Is(err, queue.ErrTargetNotConfigured) {
			return err // refused before sending
		}
		p.sent = append(p.sent, sentMessage{dest, target, msg, queue.InlineDelivery(ctx), origin})
		return err
	}
	p.sent = append(p.sent, sentMessage{dest, target, msg, queue.InlineDelivery(ctx), origin})
	return nil
}
func (p *redrivePublisher) Ready(context.Context) error { return nil }
func (p *redrivePublisher) Close() error                { return nil }

func (p *redrivePublisher) setFail(fail map[string]error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.fail, p.sent = fail, nil
}

func (p *redrivePublisher) messages() []sentMessage {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]sentMessage(nil), p.sent...)
}

type upload struct {
	meta     replaywire.FailureMetadata
	snapshot []byte
}

// uploadSink is a fake replay service that keeps each captured record.
type uploadSink struct {
	mu   sync.Mutex
	got  []upload
	done chan struct{}
}

func (s *uploadSink) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	mr, err := r.MultipartReader()
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	var u upload
	for {
		p, err := mr.NextPart()
		if err != nil {
			break
		}
		data, _ := io.ReadAll(p)
		switch p.FormName() {
		case replaywire.PartMetadata:
			_ = json.Unmarshal(data, &u.meta)
		case replaywire.PartSnapshot:
			u.snapshot = data
		}
	}
	s.mu.Lock()
	s.got = append(s.got, u)
	s.mu.Unlock()
	w.WriteHeader(http.StatusCreated)
	s.done <- struct{}{}
}

func redriveConfig(t *testing.T, replayURL string, edit func(string) string) flowconfig.Config {
	t.Helper()
	src := strings.ReplaceAll(redriveFlowYAML, "REPLAY_URL", replayURL)
	if edit != nil {
		src = edit(src)
	}
	cfg, _ := flowconfig.LoadBytes([]byte(src), "redrive", false)
	if cfg.Flows == nil {
		t.Fatal("config did not decode")
	}
	return cfg
}

func redriveServer(t *testing.T, cfg flowconfig.Config, pub queue.Publisher) *FlowServer {
	t.Helper()
	srv, err := NewFlowServer(cfg, pub, slog.New(slog.DiscardHandler), metrics.New())
	if err != nil {
		t.Fatal(err)
	}
	c, err := capture.New(capture.Options{Config: cfg, Metrics: metrics.New(), Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	srv.SetCapturer(c)
	t.Cleanup(func() {
		_ = srv.Close()
		_ = c.Close()
	})
	return srv
}

// capturedBad sends one request whose publish to "broken" fails, and returns
// the record core uploaded for it.
func capturedBad(t *testing.T) (upload, *redrivePublisher, string) {
	t.Helper()
	sink := &uploadSink{done: make(chan struct{}, 4)}
	replay := httptest.NewServer(sink)
	t.Cleanup(replay.Close)
	pub := &redrivePublisher{fail: map[string]error{"broken": errors.New("broken is down")}}
	srv := redriveServer(t, redriveConfig(t, replay.URL, nil), pub)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/hooks/orders", bytes.NewReader([]byte(`{"id":"42"}`)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	select {
	case <-sink.done:
	case <-time.After(5 * time.Second):
		t.Fatal("no upload")
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if len(sink.got) != 1 || sink.got[0].meta.NodeID != "bad" || sink.got[0].snapshot == nil {
		t.Fatalf("uploads = %+v", sink.got)
	}
	return sink.got[0], pub, replay.URL
}

func requestFor(m replaywire.FailureMetadata) replaywire.DeliverRequest {
	return replaywire.DeliverRequest{RunID: "run-1", FailureID: m.ID, Kind: m.Kind, Flow: m.Flow, NodeID: m.NodeID,
		NodeType: m.NodeType, DestinationID: m.DestinationID, DestinationType: m.DestinationType,
		RenderedTarget: m.RenderedTarget, NodeConfigHash: m.NodeConfigHash, DestinationHash: m.DestinationHash,
		SnapshotSHA256: m.Snapshot.SHA256, SnapshotVersion: m.Snapshot.Version, DeadlineMS: 5000,
		InboundStatus: m.InboundStatus, MaxRecordBytes: 1 << 20}
}

func decode(t *testing.T, raw []byte) deliverysnapshot.Snapshot {
	t.Helper()
	snap, err := deliverysnapshot.Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

func redrive(t *testing.T, srv *FlowServer, req replaywire.DeliverRequest, snap deliverysnapshot.Snapshot) (control.Result, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return srv.Redrive(ctx, req, snap)
}

func outcomes(res control.Result) map[string]string {
	out := map[string]string{}
	for _, n := range res.Nodes {
		out[n.NodeID] = n.Outcome
	}
	return out
}

func TestRedriveResumesAtTheFailedNodeInline(t *testing.T) {
	up, pub, replayURL := capturedBad(t)
	pub.setFail(nil) // the destination recovered
	srv := redriveServer(t, redriveConfig(t, replayURL, nil), pub)
	res, err := redrive(t, srv, requestFor(up.meta), decode(t, up.snapshot))
	if err != nil {
		t.Fatal(err)
	}
	if got := outcomes(res); got["bad"] != replaywire.NodeDelivered || got["after"] != replaywire.NodeDelivered || len(got) != 2 {
		t.Fatalf("outcomes = %v (upstream nodes and siblings must not rerun)", got)
	}
	sent := pub.messages()
	if len(sent) != 2 || sent[0].dest != "broken" || sent[0].msg.ID != up.meta.EventID || !sent[0].inline || sent[0].origin {
		t.Fatalf("sent = %+v; want the original envelope id, inline, with no capture origin", sent)
	}
	if res.NodeConfigHash != up.meta.NodeConfigHash || res.DestinationHash != up.meta.DestinationHash || len(res.Children) != 0 {
		t.Fatalf("result = %+v", res)
	}
}

func TestRedriveReportsDownstreamFailuresAsChildren(t *testing.T) {
	up, pub, replayURL := capturedBad(t)
	pub.setFail(map[string]error{"third": errors.New("third is down")})
	srv := redriveServer(t, redriveConfig(t, replayURL, nil), pub)
	res, err := redrive(t, srv, requestFor(up.meta), decode(t, up.snapshot))
	if err != nil {
		t.Fatal(err)
	}
	if got := outcomes(res); got["bad"] != replaywire.NodeDelivered || got["after"] != replaywire.NodeFailed {
		t.Fatalf("outcomes = %v", got)
	}
	if len(res.Children) != 1 {
		t.Fatalf("children = %+v", res.Children)
	}
	child := res.Children[0].Metadata
	if child.NodeID != "after" || child.EventID != up.meta.EventID || child.InboundStatus != up.meta.InboundStatus ||
		child.ID == up.meta.ID || !child.Retryable {
		t.Fatalf("child = %+v", child)
	}
	if err := child.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestRedriveReportsAFailedResumeNodeWithoutChildren(t *testing.T) {
	up, pub, replayURL := capturedBad(t)
	pub.setFail(map[string]error{"broken": &queue.DeliveryError{Outcome: "5xx", Attempts: 3, Err: errors.New("still down")}})
	srv := redriveServer(t, redriveConfig(t, replayURL, nil), pub)
	res, err := redrive(t, srv, requestFor(up.meta), decode(t, up.snapshot))
	if err != nil {
		t.Fatal(err)
	}
	var bad replaywire.NodeOutcome
	for _, n := range res.Nodes {
		if n.NodeID == "bad" {
			bad = n
		}
	}
	if bad.Outcome != replaywire.NodeFailed || bad.ErrorClass != replaywire.ErrorClass5xx || bad.Attempts != 3 ||
		!strings.Contains(bad.ErrorMessage, "still down") || len(res.Children) != 0 {
		t.Fatalf("bad = %+v, children %d", bad, len(res.Children))
	}
}

func TestRedriveHonorsCaptureFailuresFalse(t *testing.T) {
	up, pub, replayURL := capturedBad(t)
	pub.setFail(map[string]error{"third": errors.New("third is down")})
	cfg := redriveConfig(t, replayURL, func(s string) string {
		return strings.Replace(s, "    delivery_failure_policy: skip_failed\n", "    delivery_failure_policy: skip_failed\n    capture_failures: false\n", 1)
	})
	res, err := redrive(t, redriveServer(t, cfg, pub), requestFor(up.meta), decode(t, up.snapshot))
	if err != nil || len(res.Children) != 0 {
		t.Fatalf("children %d, err %v", len(res.Children), err)
	}
}

func TestRedriveBlocksOnConfigurationChanges(t *testing.T) {
	up, pub, replayURL := capturedBad(t)
	pub.setFail(nil)
	cases := map[string]struct {
		edit func(string) string
		req  func(replaywire.DeliverRequest) replaywire.DeliverRequest
		code string
	}{
		"flow missing": {nil, func(r replaywire.DeliverRequest) replaywire.DeliverRequest { r.Flow = "gone"; return r }, replaywire.BlockFlowMissing},
		"flow broken": {func(s string) string {
			return strings.Replace(s, "edges: [in>body, body>auth, auth>env,", "edges: [in>body, body>env, auth>env,", 1)
		}, nil, replaywire.BlockFlowBroken},
		"node missing": {nil, func(r replaywire.DeliverRequest) replaywire.DeliverRequest { r.NodeID = "gone"; return r }, replaywire.BlockNodeMissing},
		"node type changed": {func(s string) string {
			return strings.Replace(s, "{id: bad, type: publish, destination: broken, target: b}", "{id: bad, type: code, script: \"return {body: input.body, vars: input.vars || {}};\"}", 1)
		}, nil, replaywire.BlockNodeTypeChanged},
		"destination id changed": {func(s string) string {
			return strings.Replace(s, "{id: bad, type: publish, destination: broken, target: b}", "{id: bad, type: publish, destination: primary, target: b}", 1)
		}, nil, replaywire.BlockDestinationChanged},
		"destination type changed": {func(s string) string {
			return strings.Replace(s, `broken: {type: webhook, webhook: {url: "https://example.com/b"}}`,
				`broken: {type: redis_list, redis: {url: "redis://localhost:6379"}}`, 1)
		}, nil, replaywire.BlockDestinationChanged},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			pub.setFail(nil)
			srv := redriveServer(t, redriveConfig(t, replayURL, c.edit), pub)
			req := requestFor(up.meta)
			if c.req != nil {
				req = c.req(req)
			}
			_, err := redrive(t, srv, req, decode(t, up.snapshot))
			var block *control.Block
			if !errors.As(err, &block) || block.Code != c.code {
				t.Fatalf("Redrive = %v, want block %s", err, c.code)
			}
			if sent := pub.messages(); len(sent) != 0 {
				t.Fatalf("a blocked redrive sent %+v", sent)
			}
		})
	}
}

func publishOnlySnapshot(t *testing.T, id string) deliverysnapshot.Snapshot {
	t.Helper()
	dto, err := capture.SnapshotFromMessage(queue.Message{ID: id, ReceivedAt: time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC),
		Body: map[string]any{"a": 1.0}, PayloadMode: "envelope"})
	if err != nil {
		t.Fatal(err)
	}
	return deliverysnapshot.Snapshot{Version: deliverysnapshot.Version, Kind: deliverysnapshot.KindPublishOnly, Message: &dto}
}

func publishOnlyRequest() replaywire.DeliverRequest {
	return replaywire.DeliverRequest{RunID: "run-2", FailureID: "fail-2", Kind: replaywire.KindPublishOnly, Flow: "orders",
		NodeID: "bad", NodeType: "publish", DestinationID: "broken", DestinationType: "webhook", RenderedTarget: "b",
		DeadlineMS: 5000, InboundStatus: 202}
}

func TestRedrivePublishOnlyDeliversInline(t *testing.T) {
	pub := &redrivePublisher{}
	srv := redriveServer(t, redriveConfig(t, "http://127.0.0.1:1", nil), pub)
	res, err := redrive(t, srv, publishOnlyRequest(), publishOnlySnapshot(t, "evt-9"))
	if err != nil {
		t.Fatal(err)
	}
	sent := pub.messages()
	if len(res.Nodes) != 1 || res.Nodes[0].Outcome != replaywire.NodeDelivered || len(sent) != 1 ||
		sent[0].msg.ID != "evt-9" || sent[0].target != "b" || !sent[0].inline {
		t.Fatalf("nodes %+v, sent %+v", res.Nodes, sent)
	}
	pub.setFail(map[string]error{"broken": &queue.DeliveryError{Outcome: "4xx", Attempts: 1, Err: errors.New("404")}})
	res, err = redrive(t, srv, publishOnlyRequest(), publishOnlySnapshot(t, "evt-9"))
	if err != nil || res.Nodes[0].Outcome != replaywire.NodeFailed || res.Nodes[0].ErrorClass != replaywire.ErrorClass4xx {
		t.Fatalf("nodes %+v, err %v", res.Nodes, err)
	}
}

func TestRedrivePublishOnlyBlocksAnUnknownTarget(t *testing.T) {
	pub := &redrivePublisher{fail: map[string]error{"broken": fmt.Errorf("webhook: %w", queue.ErrTargetNotConfigured)}}
	srv := redriveServer(t, redriveConfig(t, "http://127.0.0.1:1", nil), pub)
	_, err := redrive(t, srv, publishOnlyRequest(), publishOnlySnapshot(t, "evt-9"))
	var block *control.Block
	if !errors.As(err, &block) || block.Code != replaywire.BlockTargetNotConfigured || len(pub.messages()) != 0 {
		t.Fatalf("Redrive = %v, sent %d", err, len(pub.messages()))
	}
}

// The configured destination id for the node that published must match too:
// a publish_only record retargeted to another destination is refused.
func TestRedrivePublishOnlyChecksTheOriginNode(t *testing.T) {
	pub := &redrivePublisher{}
	srv := redriveServer(t, redriveConfig(t, "http://127.0.0.1:1", nil), pub)
	req := publishOnlyRequest()
	req.DestinationID = "primary"
	_, err := redrive(t, srv, req, publishOnlySnapshot(t, "evt-9"))
	var block *control.Block
	if !errors.As(err, &block) || block.Code != replaywire.BlockDestinationChanged {
		t.Fatalf("Redrive = %v", err)
	}
}
