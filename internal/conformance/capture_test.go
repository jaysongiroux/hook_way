package conformance

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jaysongiroux/hook_way/internal/capture"
	"github.com/jaysongiroux/hook_way/internal/httpapi"
	"github.com/jaysongiroux/hook_way/internal/metrics"
	"github.com/jaysongiroux/hook_way/internal/queue"
	"github.com/jaysongiroux/hook_way/internal/replaywire"
)

// destPublisher fails publishes to the listed destinations.
type destPublisher struct{ fail map[string]bool }

func (p *destPublisher) Publish(_ context.Context, dest, _ string, _ queue.Message) error {
	if p.fail[dest] {
		return errors.New(dest + " is down")
	}
	return nil
}
func (p *destPublisher) Ready(context.Context) error { return nil }
func (p *destPublisher) Close() error                { return nil }

// replayRecorder is a fake replay service that records uploaded metadata.
type replayRecorder struct {
	mu      sync.Mutex
	records []replaywire.FailureMetadata
	block   chan struct{}
}

func (r *replayRecorder) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if r.block != nil {
		<-r.block
	}
	mr, err := req.MultipartReader()
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	for {
		p, err := mr.NextPart()
		if err != nil {
			break
		}
		data, _ := io.ReadAll(p)
		if p.FormName() == replaywire.PartMetadata {
			var m replaywire.FailureMetadata
			_ = json.Unmarshal(data, &m)
			r.mu.Lock()
			r.records = append(r.records, m)
			r.mu.Unlock()
		}
	}
	w.WriteHeader(http.StatusCreated)
}

func (r *replayRecorder) nodes() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.records))
	for i, m := range r.records {
		out[i] = m.NodeID
	}
	return out
}

// captureYAML is a flow publishing to a healthy and a broken destination.
// An empty replayURL leaves the replay block out.
func captureYAML(replayURL, policy, captureFlag, extraNodes, extraEdges string) string {
	replay := ""
	if replayURL != "" {
		replay = `replay:
  url: "` + replayURL + `"
  capture_token: "capture-token-0123456789abcdefghijkl"
  control: {addr: ":9092", token: "control-token-0123456789abcdefghij"}
`
	}
	return `
server: {addr: ":8080", max_body_bytes: 1048576}
` + replay + `destinations:
  primary: {type: webhook, webhook: {url: "https://example.com/a"}}
  broken: {type: webhook, webhook: {url: "https://example.com/b"}}
flows:
  orders:
    delivery_failure_policy: ` + policy + `
` + captureFlag + `
    nodes:
      - {id: in, type: http_trigger, path: /hooks/orders, methods: [POST]}
      - {id: body, type: read_body, max_bytes: 65536}
      - {id: auth, type: no_auth}
      - {id: env, type: envelope}
      - {id: ok, type: publish, destination: primary, target: a}
      - {id: bad, type: publish, destination: broken, target: b}
` + extraNodes + `
    edges: [in>body, body>auth, auth>env, env>ok, env>bad` + extraEdges + `]
`
}

func captureServer(t *testing.T, src string, pub queue.Publisher) *httpapi.FlowServer {
	srv, _ := captureServerWithMetrics(t, src, pub)
	return srv
}

func captureServerWithMetrics(t *testing.T, src string, pub queue.Publisher) (*httpapi.FlowServer, *metrics.Metrics) {
	t.Helper()
	cfg := loadCfg(t, src)
	srv, err := httpapi.NewFlowServer(cfg, pub, slog.New(slog.DiscardHandler), metrics.New())
	if err != nil {
		t.Fatal(err)
	}
	m := metrics.New()
	c, err := capture.New(capture.Options{Config: cfg, Metrics: m, Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	srv.SetCapturer(c)
	t.Cleanup(func() {
		_ = srv.Close()
		_ = c.Close()
	})
	return srv, m
}

// captureDrops sums hookway_capture_dropped_total across reasons.
func captureDrops(t *testing.T, m *metrics.Metrics) float64 {
	t.Helper()
	families, err := m.Registry().Gather()
	if err != nil {
		t.Fatal(err)
	}
	var n float64
	for _, f := range families {
		if f.GetName() == "hookway_capture_dropped_total" {
			for _, s := range f.GetMetric() {
				n += s.GetCounter().GetValue()
			}
		}
	}
	return n
}

func postOrder(srv http.Handler) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/hooks/orders", bytes.NewReader([]byte(`{"id":"42"}`)))
	req.Header.Set("Content-Type", "application/json")
	return post(srv, req)
}

// I20: a failure is captured only when the final inbound status is 2xx and no
// on_error or dead_letter path handled it.
func TestI20_CaptureOnlyAcknowledgedUnhandledFailures(t *testing.T) {
	onError := "      - {id: fallback, type: publish, destination: primary, target: fallback}\n"
	cases := map[string]struct {
		policy, flag, nodes, edges string
		status                     int
		captured                   []string
	}{
		"skip_failed partial failure":           {"skip_failed", "", "", "", http.StatusAccepted, []string{"bad"}},
		"stop_on_failure is the sender's retry": {"stop_on_failure", "", "", "", http.StatusServiceUnavailable, nil},
		"on_error handled it":                   {"skip_failed", "", onError, ", bad:on_error>fallback", http.StatusAccepted, nil},
		"capture_failures false":                {"skip_failed", "    capture_failures: false", "", "", http.StatusAccepted, nil},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			rec := &replayRecorder{}
			replay := httptest.NewServer(rec)
			defer replay.Close()
			srv, m := captureServerWithMetrics(t, captureYAML(replay.URL, c.policy, c.flag, c.nodes, c.edges), &destPublisher{fail: map[string]bool{"broken": true}})
			if resp := postOrder(srv); resp.Code != c.status {
				t.Fatalf("status %d, want %d (%s)", resp.Code, c.status, resp.Body.String())
			}
			deadline := time.Now().Add(3 * time.Second)
			for len(rec.nodes()) < len(c.captured) && time.Now().Before(deadline) {
				time.Sleep(10 * time.Millisecond)
			}
			time.Sleep(100 * time.Millisecond) // nothing more may arrive
			if got := rec.nodes(); strings.Join(got, ",") != strings.Join(c.captured, ",") {
				t.Fatalf("captured nodes %v, want %v", got, c.captured)
			}
			// A record built and then refused by validation never reaches
			// replay either; only a drop count would show it.
			if n := captureDrops(t, m); n != 0 {
				t.Fatalf("%v records were built and dropped", n)
			}
		})
	}
}

func closedURL(t *testing.T) string {
	t.Helper()
	l, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	url := "http://" + l.Addr().String()
	_ = l.Close()
	return url
}

func responseShape(t *testing.T, resp *httptest.ResponseRecorder) (int, map[string]any) {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("body %q: %v", resp.Body.String(), err)
	}
	delete(body, "event_id")
	delete(body, "request_id")
	return resp.Code, body
}

// I15: capture never changes a response or readiness — with replay down,
// hanging, or absent.
func TestI15_CaptureNeverChangesTheResponseOrReadiness(t *testing.T) {
	pub := func() queue.Publisher { return &destPublisher{fail: map[string]bool{"broken": true}} }
	plain := newServer(t, captureYAML("", "skip_failed", "", "", ""), pub())
	wantStatus, wantBody := responseShape(t, postOrder(plain))

	hanging := &replayRecorder{block: make(chan struct{})}
	hangSrv := httptest.NewServer(hanging)
	defer hangSrv.Close()
	var release sync.Once
	unblock := func() { release.Do(func() { close(hanging.block) }) }
	defer unblock()
	for name, url := range map[string]string{"replay down": closedURL(t), "replay hanging": hangSrv.URL} {
		t.Run(name, func(t *testing.T) {
			srv := captureServer(t, captureYAML(url, "skip_failed", "", "", ""), pub())
			t.Cleanup(unblock) // runs before the capturer's Close, so it need not wait out the upload budget
			start := time.Now()
			status, body := responseShape(t, postOrder(srv))
			if took := time.Since(start); took > time.Second {
				t.Fatalf("the response took %v with replay %s", took, name)
			}
			if status != wantStatus || !jsonEqual(body, wantBody) {
				t.Fatalf("response %d %v, want %d %v", status, body, wantStatus, wantBody)
			}
			ready := post(srv, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/readyz", nil))
			plainReady := post(plain, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/readyz", nil))
			if ready.Code != plainReady.Code {
				t.Fatalf("readiness %d with capture, %d without", ready.Code, plainReady.Code)
			}
		})
	}
}

func jsonEqual(a, b map[string]any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

// failingPublisher stands in for a dead-letter destination that is down.
type failingPublisher struct{}

func (failingPublisher) Publish(context.Context, string, string, queue.Message) error {
	return errors.New("dead-letter destination is down")
}
func (failingPublisher) Ready(context.Context) error { return nil }
func (failingPublisher) Close() error                { return nil }

// I20 for async webhooks, through the real factory, engine, and capturer: a
// delivery that fails for good after the 202 is captured unless a dead letter
// accepted it.
func TestI20_AsyncWebhookFailures(t *testing.T) {
	okSrv, _ := hitCounter(t, http.StatusOK)
	cases := map[string]struct {
		dlq      bool
		dlqPub   queue.Publisher
		captured int
	}{
		"no dead letter":      {false, &countingPublisher{}, 1},
		"dead letter accepts": {true, &countingPublisher{}, 0},
		"dead letter fails":   {true, failingPublisher{}, 1},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			sink := &recordSink{seen: make(chan struct{}, 8)}
			replay := httptest.NewServer(sink)
			defer replay.Close()
			failing, hits := hitCounter(t, http.StatusInternalServerError)
			core := startCoreWith(t, redriveYAML(replay.URL, okSrv.URL, failing.URL, "async", c.dlq), c.dlqPub)
			core.post(t)
			waitHit := time.Now().Add(5 * time.Second)
			for hits.Load() == 0 && time.Now().Before(waitHit) {
				time.Sleep(10 * time.Millisecond)
			}
			if hits.Load() == 0 {
				t.Fatal("the async delivery never reached the failing webhook")
			}
			if dlq, ok := c.dlqPub.(*countingPublisher); ok && c.dlq {
				// The accepting dead letter must actually have received it,
				// or "not captured" proves nothing.
				waitDLQ := time.Now().Add(5 * time.Second)
				for dlq.n.Load() == 0 && time.Now().Before(waitDLQ) {
					time.Sleep(10 * time.Millisecond)
				}
				if dlq.n.Load() != 1 {
					t.Fatalf("the dead letter received %d messages, want 1", dlq.n.Load())
				}
			}
			if c.captured > 0 {
				rec := sink.wait(t, c.captured)[0]
				if rec.meta.Kind != replaywire.KindPublishOnly || rec.meta.InboundStatus != http.StatusAccepted || rec.meta.NodeID != "out" {
					t.Fatalf("captured %+v", rec.meta)
				}
			}
			time.Sleep(200 * time.Millisecond) // nothing more may arrive
			if got := sink.count(); got != c.captured {
				t.Fatalf("captured %d records, want %d", got, c.captured)
			}
		})
	}
}
