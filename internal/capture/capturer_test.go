package capture

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jaysongiroux/hook_way/internal/config"
	"github.com/jaysongiroux/hook_way/internal/flowconfig"
	"github.com/jaysongiroux/hook_way/internal/graph"
	"github.com/jaysongiroux/hook_way/internal/queue"
	"github.com/jaysongiroux/hook_way/internal/replaywire"
)

func captureConfig(t *testing.T, replayURL, captureFlag string) flowconfig.Config {
	t.Helper()
	src := `
server: {addr: ":8080", max_body_bytes: 1048576}
replay:
  url: "` + replayURL + `"
  capture_token: "` + testToken + `"
  control: {addr: ":9092", token: "control-token-0123456789abcdefghij"}
destinations:
  queue: {type: webhook, webhook: {url: "https://example.com/q"}}
flows:
  orders:
` + captureFlag + `
    delivery_failure_policy: skip_failed
    nodes:
      - {id: in, type: http_trigger, path: /hooks/orders, methods: [POST]}
      - {id: body, type: read_body, max_bytes: 65536}
      - {id: auth, type: no_auth}
      - {id: env, type: envelope}
      - {id: out, type: publish, destination: queue, target: "orders-{tenant}"}
    edges: [in>body, body>auth, auth>env, env>out]
`
	cfg, issues := flowconfig.LoadBytes([]byte(src), "test", false)
	for _, i := range issues {
		if i.EffectiveSeverity() == config.SeverityError {
			t.Fatalf("config issues: %+v", issues)
		}
	}
	return cfg
}

func newTestCapturer(t *testing.T, replayURL, captureFlag string, m *fakeMetrics) *Capturer {
	t.Helper()
	c, err := New(Options{Config: captureConfig(t, replayURL, captureFlag), Metrics: m, Logger: slog.New(slog.DiscardHandler),
		CoreVersion: "test", CoreInstance: "core-0"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func publishFailure() graph.FailureEvent {
	return graph.FailureEvent{NodeID: "out", NodeType: "publish", Input: testEnvelope(), Err: errors.New("down")}
}

func TestSettleKeepsFailuresOnlyForAcknowledgedRequests(t *testing.T) {
	srv, got := fakeReplay(t, 201)
	m := newFakeMetrics()
	c := newTestCapturer(t, srv.URL, "", m)
	refused := c.Begin("orders", testHead)
	refused.OnFailure(publishFailure())
	refused.Settle(503) // the sender retries: not ours
	accepted := c.Begin("orders", testHead)
	accepted.OnFailure(publishFailure())
	accepted.Settle(202)
	waitUntil(t, "an upload", func() bool { return m.count("uploaded", "orders") == 1 })
	time.Sleep(50 * time.Millisecond)
	if ups := got(); len(ups) != 1 || ups[0].meta.InboundStatus != 202 || ups[0].meta.NodeID != "out" {
		t.Fatalf("uploads = %+v", ups)
	}
	// Not built and dropped either: replay's own validation would refuse a
	// non-2xx record, so only the drop count shows it was never kept.
	if n := m.count("dropped", DropRejected); n != 0 {
		t.Fatalf("%d records were built for the refused request", n)
	}
}

func TestBeginSkipsFlowsWithCaptureOff(t *testing.T) {
	c := newTestCapturer(t, "http://127.0.0.1:1", "    capture_failures: false", newFakeMetrics())
	req := c.Begin("orders", testHead)
	if req != nil {
		t.Fatal("capture_failures: false still collected")
	}
	req.OnFailure(publishFailure()) // nil-safe
	req.Settle(202)
	if ctx := req.WithOrigin(context.Background(), "out"); ctx != context.Background() {
		t.Fatal("a nil request changed the context")
	}
	var none *Capturer
	if none.Begin("orders", testHead) != nil {
		t.Fatal("a nil capturer began a request")
	}
	none.OnTerminalFailure(queue.TerminalFailure{})
}

func terminalFrom(ctx context.Context) queue.TerminalFailure {
	origin, _ := queue.OriginFrom(ctx)
	return queue.TerminalFailure{DestinationID: "queue", Target: "orders-{tenant}", RenderedTarget: "orders-acme",
		Message: queue.Message{ID: "evt-1", ReceivedAt: fixedNow, Body: map[string]any{"a": 1.0}, PayloadMode: "envelope"},
		Outcome: "5xx", Attempts: 3, Err: errors.New("500"), FailedAt: fixedNow, Origin: origin}
}

func TestAsyncFailureWaitsForTheFinalStatus(t *testing.T) {
	srv, got := fakeReplay(t, 201)
	m := newFakeMetrics()
	c := newTestCapturer(t, srv.URL, "", m)
	for _, status := range []int{503, 202} {
		req := c.Begin("orders", testHead)
		ctx := req.WithOrigin(context.Background(), "out")
		c.OnTerminalFailure(terminalFrom(ctx)) // fails before the request settles
		time.Sleep(30 * time.Millisecond)
		if len(got()) != 0 {
			t.Fatal("an async failure was uploaded before its request settled")
		}
		req.Settle(status)
	}
	waitUntil(t, "an upload", func() bool { return m.count("uploaded", "orders") == 1 })
	time.Sleep(50 * time.Millisecond)
	ups := got()
	if len(ups) != 1 || ups[0].meta.Kind != replaywire.KindPublishOnly || ups[0].meta.InboundStatus != 202 {
		t.Fatalf("uploads = %+v", ups)
	}
	if n := m.count("dropped", DropRejected); n != 0 {
		t.Fatalf("%d records were built for the refused request", n)
	}
}

func TestOnTerminalFailureIgnoresMessagesWithoutAnOrigin(t *testing.T) {
	srv, got := fakeReplay(t, 201)
	c := newTestCapturer(t, srv.URL, "", newFakeMetrics())
	c.OnTerminalFailure(queue.TerminalFailure{DestinationID: "queue", Message: queue.Message{ID: "evt-1"}})
	time.Sleep(50 * time.Millisecond)
	if len(got()) != 0 {
		t.Fatal("a failure from a flow without capture was uploaded")
	}
}

func TestNewReportsBadTLS(t *testing.T) {
	cfg := captureConfig(t, "https://replay", "")
	cfg.Replay.TLS.CACert = "/nonexistent/ca.pem"
	if _, err := New(Options{Config: cfg, Metrics: newFakeMetrics()}); err == nil || !strings.Contains(err.Error(), "replay tls") {
		t.Fatalf("New = %v", err)
	}
}

// hangingReplay never answers until the test ends.
func hangingReplay(t *testing.T) string {
	t.Helper()
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-block:
		case <-r.Context().Done():
		}
		w.WriteHeader(http.StatusCreated)
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(block) })
	return srv.URL
}

// A queued failure keeps its whole envelope alive until its record is built
// — an http_request node's upstream body included — so the byte budget must
// charge for all of it.
func TestQueuedFailuresAreChargedForEverythingTheyKeep(t *testing.T) {
	m := newFakeMetrics()
	c := newTestCapturer(t, hangingReplay(t), "", m)
	c.uploader.drainWait = 50 * time.Millisecond
	upstream := make([]byte, 2<<20)
	const n = 40
	for range n {
		req := c.Begin("orders", testHead)
		env := testEnvelope()
		env.Upstream = &graph.UpstreamResponse{Status: 200, Body: upstream}
		req.OnFailure(graph.FailureEvent{NodeID: "out", NodeType: "publish", Input: env, Err: errors.New("down")})
		req.Settle(202)
	}
	if kept := n - m.count("dropped", DropBufferFull); int64(kept)*int64(len(upstream)) > maxPendingBytes {
		t.Fatalf("%d failures each keeping 2 MiB alive were accepted into a %d-byte buffer", kept, maxPendingBytes)
	}
}

// The request's raw bytes and the captured event's copy are one slice; the
// estimate counts memory, so it counts them once.
func TestEnvelopeEstimateCountsASharedBodyOnce(t *testing.T) {
	env := testEnvelope()
	body := make([]byte, 1<<20)
	env.V.Raw.Body = body
	ce := env.Extra.(flowconfig.CapturedEvent)
	ce.Event.RawBody = body
	env.Extra = ce
	if got, want := envelopeEstimate(env), 3*int64(len(body))+estimateOverhead; got != want {
		t.Fatalf("estimate = %d, want %d", got, want)
	}
}

// A failure whose bodies alone overflow the upload buffer still reaches
// replay: as the metadata-only too_large record the spec calls for, built at
// once so nothing keeps the bodies alive.
func TestAFailureTooLargeToBufferIsUploadedMetadataOnly(t *testing.T) {
	srv, got := fakeReplay(t, http.StatusCreated)
	m := newFakeMetrics()
	c := newTestCapturer(t, srv.URL, "", m)
	env := testEnvelope()
	env.Upstream = &graph.UpstreamResponse{Status: 200, Body: make([]byte, 30<<20)}
	req := c.Begin("orders", testHead)
	req.OnFailure(graph.FailureEvent{NodeID: "out", NodeType: "publish", Input: env, Err: errors.New("down")})
	req.Settle(http.StatusAccepted)
	waitUntil(t, "an upload", func() bool { return m.count("uploaded", "orders") == 1 })
	up := got()[0]
	if up.meta.Retryable || up.meta.NotRetryableReason != replaywire.NotRetryableTooLarge || len(up.parts) != 1 ||
		up.meta.DestinationID != "queue" || up.meta.RenderedTarget != "orders-acme" {
		t.Fatalf("upload = %+v (%d parts)", up.meta, len(up.parts))
	}
	if err := up.meta.Validate(); err != nil {
		t.Fatal(err)
	}
	if n := m.count("dropped", DropBufferFull); n != 0 {
		t.Fatalf("%d buffer_full drops", n)
	}
}

func TestAnAsyncFailureTooLargeToBufferIsUploadedMetadataOnly(t *testing.T) {
	srv, got := fakeReplay(t, http.StatusCreated)
	m := newFakeMetrics()
	c := newTestCapturer(t, srv.URL, "", m)
	req := c.Begin("orders", testHead)
	tf := terminalFrom(req.WithOrigin(context.Background(), "out"))
	tf.Message.RawBody = make([]byte, 30<<20)
	c.OnTerminalFailure(tf)
	req.Settle(http.StatusAccepted)
	waitUntil(t, "an upload", func() bool { return m.count("uploaded", "orders") == 1 })
	up := got()[0]
	if up.meta.Kind != replaywire.KindPublishOnly || up.meta.Retryable || up.meta.NotRetryableReason != replaywire.NotRetryableTooLarge {
		t.Fatalf("upload = %+v", up.meta)
	}
}
