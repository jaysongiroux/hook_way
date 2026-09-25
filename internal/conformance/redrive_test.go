package conformance

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jaysongiroux/hook_way/internal/capture"
	"github.com/jaysongiroux/hook_way/internal/control"
	"github.com/jaysongiroux/hook_way/internal/httpapi"
	"github.com/jaysongiroux/hook_way/internal/metrics"
	"github.com/jaysongiroux/hook_way/internal/queue"
	"github.com/jaysongiroux/hook_way/internal/replaywire"
)

const testControlToken = "control-token-0123456789abcdefghij"

type sinkRecord struct {
	meta     replaywire.FailureMetadata
	snapshot []byte
}

// recordSink is a fake replay service that keeps every uploaded record.
type recordSink struct {
	mu   sync.Mutex
	got  []sinkRecord
	seen chan struct{}
}

func (s *recordSink) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	mr, err := r.MultipartReader()
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	var rec sinkRecord
	for {
		p, err := mr.NextPart()
		if err != nil {
			break
		}
		data, _ := io.ReadAll(p)
		switch p.FormName() {
		case replaywire.PartMetadata:
			_ = json.Unmarshal(data, &rec.meta)
		case replaywire.PartSnapshot:
			rec.snapshot = data
		}
	}
	s.mu.Lock()
	s.got = append(s.got, rec)
	s.mu.Unlock()
	w.WriteHeader(http.StatusCreated)
	select {
	case s.seen <- struct{}{}:
	default:
	}
}

func (s *recordSink) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.got)
}

func (s *recordSink) wait(t *testing.T, n int) []sinkRecord {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for s.count() < n {
		if time.Now().After(deadline) {
			t.Fatalf("got %d records, want %d", s.count(), n)
		}
		time.Sleep(10 * time.Millisecond)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]sinkRecord(nil), s.got...)
}

// countingPublisher stands in for the rabbitmq dead-letter destination.
type countingPublisher struct{ n atomic.Int32 }

func (p *countingPublisher) Publish(context.Context, string, string, queue.Message) error {
	p.n.Add(1)
	return nil
}
func (p *countingPublisher) Ready(context.Context) error { return nil }
func (p *countingPublisher) Close() error                { return nil }

// hitCounter is a webhook target that answers status and counts requests.
func hitCounter(t *testing.T, status int) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n.Add(1)
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return srv, &n
}

// redriveYAML is a flow fanning out to a healthy webhook and to "notify".
// mode sets notify's delivery_mode; dlq adds a dead_letter to rabbitmq "dlq".
func redriveYAML(replayURL, okURL, notifyURL, mode string, dlq bool) string {
	deadLetter, dlqDest := "", ""
	if dlq {
		deadLetter = `, dead_letter: {destination: dlq, target: dead}`
		dlqDest = `  dlq: {type: rabbitmq, rabbitmq: {url: "amqp://x", exchange: hookway.dlq, exchange_type: direct}}
`
	}
	return `
server: {addr: ":8080", max_body_bytes: 1048576}
replay:
  url: "` + replayURL + `"
  capture_token: "capture-token-0123456789abcdefghijkl"
  control: {addr: ":9092", token: "` + testControlToken + `"}
destinations:
  ok: {type: webhook, webhook: {url: "` + okURL + `"}}
  notify: {type: webhook, webhook: {url: "` + notifyURL + `", delivery_mode: ` + mode + `, retry: {max_attempts: 1}` + deadLetter + `}}
` + dlqDest + `flows:
  orders:
    delivery_failure_policy: skip_failed
    nodes:
      - {id: in, type: http_trigger, path: /hooks/orders, methods: [POST]}
      - {id: body, type: read_body, max_bytes: 65536}
      - {id: auth, type: no_auth}
      - {id: env, type: envelope}
      - {id: good, type: publish, destination: ok, target: a}
      - {id: out, type: publish, destination: notify, target: orders}
    edges: [in>body, body>auth, auth>env, env>good, env>out]
`
}

type core struct {
	app     *httpapi.FlowServer
	control *httptest.Server
	dlq     queue.Publisher // the dead-letter destination's publisher
}

// startCore builds core with its real queue factory, capture, and control
// listener.
func startCore(t *testing.T, src string) core {
	t.Helper()
	return startCoreWith(t, src, &countingPublisher{})
}

// startCoreWith is startCore with the dead-letter destination's publisher.
func startCoreWith(t *testing.T, src string, dlq queue.Publisher) core {
	t.Helper()
	cfg := loadCfg(t, src)
	c, err := capture.New(capture.Options{Config: cfg, Metrics: metrics.New(), Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	pub, err := queue.NewFactory(queue.FactoryOptions{
		Logger: slog.New(slog.DiscardHandler), OnTerminalFailure: c.OnTerminalFailure,
		RabbitMQConnector: func(context.Context, string, queue.RabbitMQConfig) (queue.Publisher, error) { return dlq, nil },
	}).Create(context.Background(), cfg.ServingConfig())
	if err != nil {
		t.Fatal(err)
	}
	app, err := httpapi.NewFlowServer(cfg, pub, slog.New(slog.DiscardHandler), metrics.New())
	if err != nil {
		t.Fatal(err)
	}
	app.SetCapturer(c)
	ctl := control.New(control.Options{Token: testControlToken, MaxRecordBytes: cfg.Replay.MaxRecordBytes,
		Flows: []string{"orders"}, Redriver: app, Logger: slog.New(slog.DiscardHandler)})
	srv := httptest.NewServer(ctl.HTTPServer().Handler)
	t.Cleanup(func() {
		srv.Close()
		_ = app.Close()
		_ = pub.Close()
		_ = c.Close()
	})
	return core{app: app, control: srv, dlq: dlq}
}

func (c core) post(t *testing.T) {
	t.Helper()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/hooks/orders", bytes.NewReader([]byte(`{"id":"42"}`)))
	req.Header.Set("Content-Type", "application/json")
	if rec := post(c.app, req); rec.Code != http.StatusAccepted {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
}

func deliverFor(r sinkRecord) replaywire.DeliverRequest {
	m := r.meta
	return replaywire.DeliverRequest{RunID: "run-1", FailureID: m.ID, Kind: m.Kind, Flow: m.Flow, NodeID: m.NodeID,
		NodeType: m.NodeType, DestinationID: m.DestinationID, DestinationType: m.DestinationType,
		RenderedTarget: m.RenderedTarget, NodeConfigHash: m.NodeConfigHash, DestinationHash: m.DestinationHash,
		SnapshotSHA256: m.Snapshot.SHA256, SnapshotVersion: m.Snapshot.Version, DeadlineMS: 5000,
		InboundStatus: m.InboundStatus, MaxRecordBytes: 1 << 20}
}

func (c core) deliver(t *testing.T, req replaywire.DeliverRequest, snapshot []byte) (int, []byte) {
	t.Helper()
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for _, p := range []struct {
		name string
		data []byte
	}{{replaywire.PartRequest, raw}, {replaywire.PartSnapshot, snapshot}} {
		pw, _ := w.CreateFormField(p.name)
		_, _ = pw.Write(p.data)
	}
	_ = w.Close()
	hreq, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, c.control.URL+replaywire.DeliverPath, &buf)
	hreq.Header.Set("Content-Type", w.FormDataContentType())
	hreq.Header.Set("Authorization", "Bearer "+testControlToken)
	resp, err := http.DefaultClient.Do(hreq)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, body
}

// I21: a redrive delivers inline and reports its outcome; it never captures,
// dead-letters, or retries in the background — for resume and publish_only
// records alike.
func TestI21_RedriveNeverCapturesOrDeadLetters(t *testing.T) {
	okSrv, _ := hitCounter(t, http.StatusOK)
	for name, mode := range map[string]string{"resume": "sync", "publish_only": "async"} {
		t.Run(name, func(t *testing.T) {
			sink := &recordSink{seen: make(chan struct{}, 8)}
			replay := httptest.NewServer(sink)
			defer replay.Close()
			failing, _ := hitCounter(t, http.StatusInternalServerError)
			// Capture with no dead letter, so the failure is kept.
			first := startCore(t, redriveYAML(replay.URL, okSrv.URL, failing.URL, mode, false))
			first.post(t)
			rec := sink.wait(t, 1)[0]
			if rec.meta.Kind != name || rec.meta.NodeID != "out" {
				t.Fatalf("captured %+v", rec.meta)
			}
			// Redrive against a configuration that would dead-letter an async
			// failure, while the destination still fails.
			target, hits := hitCounter(t, http.StatusInternalServerError)
			second := startCore(t, redriveYAML(replay.URL, okSrv.URL, target.URL, "async", true))
			status, body := second.deliver(t, deliverFor(rec), rec.snapshot)
			if status != http.StatusOK || !strings.Contains(string(body), `"outcome":"failed"`) {
				t.Fatalf("deliver = %d %s", status, body)
			}
			time.Sleep(200 * time.Millisecond) // anything async would have happened by now
			dead := second.dlq.(*countingPublisher).n.Load()
			if hits.Load() != 1 || dead != 0 || sink.count() != 1 {
				t.Fatalf("target hits %d, dead letters %d, records %d; want 1, 0, 1", hits.Load(), dead, sink.count())
			}
		})
	}
}

func TestBlockedRedrivesSendNothing(t *testing.T) {
	okSrv, _ := hitCounter(t, http.StatusOK)
	sink := &recordSink{seen: make(chan struct{}, 8)}
	replay := httptest.NewServer(sink)
	defer replay.Close()
	failing, _ := hitCounter(t, http.StatusInternalServerError)
	startCore(t, redriveYAML(replay.URL, okSrv.URL, failing.URL, "sync", false)).post(t)
	rec := sink.wait(t, 1)[0]
	cases := map[string]struct {
		edit func(string) string
		req  func(replaywire.DeliverRequest) replaywire.DeliverRequest
		code string
	}{
		"flow missing": {func(s string) string { return strings.Replace(s, "  orders:\n", "  renamed:\n", 1) }, nil, replaywire.BlockFlowMissing},
		"node missing": {func(s string) string {
			return strings.Replace(strings.Replace(s, "{id: out,", "{id: moved,", 1), "env>out]", "env>moved]", 1)
		}, nil, replaywire.BlockNodeMissing},
		"destination changed": {func(s string) string {
			return strings.Replace(s, "destination: notify, target: orders", "destination: ok, target: orders", 1)
		}, nil, replaywire.BlockDestinationChanged},
		"hash mismatch": {nil, func(r replaywire.DeliverRequest) replaywire.DeliverRequest {
			r.SnapshotSHA256 = strings.Repeat("0", 64)
			return r
		}, replaywire.BlockHashMismatch},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			target, hits := hitCounter(t, http.StatusOK)
			src := redriveYAML(replay.URL, okSrv.URL, target.URL, "sync", false)
			if c.edit != nil {
				src = c.edit(src)
			}
			req := deliverFor(rec)
			if c.req != nil {
				req = c.req(req)
			}
			status, body := startCore(t, src).deliver(t, req, rec.snapshot)
			if status != http.StatusUnprocessableEntity || !strings.Contains(string(body), `"code":"`+c.code+`"`) || hits.Load() != 0 {
				t.Fatalf("deliver = %d %s after %d sends; want 422 %s and none", status, body, hits.Load(), c.code)
			}
		})
	}
}
