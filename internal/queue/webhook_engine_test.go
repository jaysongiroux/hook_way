package queue

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestWebhookIgnoredResponseDoesNotDrainUnboundedTail(t *testing.T) {
	closed := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_, _ = w.Write(bytes.Repeat([]byte("x"), maxWebhookResponseDrainBytes+1))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(closed)
	}))
	defer srv.Close()
	d := envelopeDelivery(srv.URL)
	d.client.Timeout = 3 * time.Second
	done := make(chan error, 1)
	go func() {
		outcome, attempts, err := d.run(context.Background(), Message{ID: "bounded-response"}, nil)
		if outcome != "2xx" || attempts != 1 {
			err = fmt.Errorf("outcome=%s attempts=%d error=%w", outcome, attempts, err)
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		srv.CloseClientConnections()
		t.Fatal("ignored response tail consumed attempt budget")
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		srv.CloseClientConnections()
		t.Fatal("response body was not closed")
	}
}

func TestWebhookByteAdmissionCountsActiveAndReleases(t *testing.T) {
	started, release := make(chan struct{}, 2), make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		started <- struct{}{}
		<-release
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	msg := Message{ID: "byte-budget", Body: map[string]any{"value": strings.Repeat("x", 1024)}, RawBody: bytes.Repeat([]byte("r"), 1024), OrigHeaders: http.Header{"X-Long": {strings.Repeat("h", 1024)}}}
	charge, ok := webhookMessageCharge(msg, 1<<20)
	if !ok {
		t.Fatal("fixture charge")
	}
	m := &webhookByteMetrics{}
	e := newWebhookDeliveryEngine("budget", 1, 1000, 3*time.Second, m, testLogger(), charge)
	if m.capacity.Load() != charge || m.used.Load() != 0 {
		t.Fatal("initial accounted gauges")
	}
	if err := e.Enqueue(webhookJob{delivery: envelopeDelivery(srv.URL), msg: msg}); err != nil {
		t.Fatal(err)
	}
	<-started
	if m.used.Load() != charge {
		t.Fatal("active bytes missing from metric")
	}
	if _, _, saturated := e.saturated(); !saturated {
		t.Fatal("active bytes did not saturate readiness")
	}
	if err := e.Enqueue(webhookJob{delivery: envelopeDelivery(srv.URL), msg: msg}); !errors.Is(err, ErrWebhookQueueFull) {
		t.Fatalf("second active job: %v", err)
	}
	close(release)
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	e.mu.RLock()
	held := e.pendingBytes
	e.mu.RUnlock()
	if held != 0 {
		t.Fatalf("bytes leaked after drain: %d", held)
	}
	if m.used.Load() != 0 {
		t.Fatal("drain did not clear byte gauge")
	}
}

type webhookByteMetrics struct {
	fakeWebhookMetrics
	used     atomic.Int64
	capacity atomic.Int64
}

func (m *webhookByteMetrics) WebhookQueueBytes(_ string, used, capacity int64) {
	m.used.Store(used)
	m.capacity.Store(capacity)
}

func TestWebhookBytePreflightCountsAllFormsAndRejectsCycles(t *testing.T) {
	base, ok := webhookMessageCharge(Message{}, 1<<20)
	if !ok {
		t.Fatal("empty message")
	}
	for name, msg := range map[string]Message{
		"body": {Body: strings.Repeat("x", 1024)}, "headers": {Headers: http.Header{"X": {strings.Repeat("x", 1024)}}},
		"raw": {RawBody: make([]byte, 1024)}, "object": {ObjectBytes: make([]byte, 1024)},
		"base64": {RawBodyBase64: func() *string { s := strings.Repeat("x", 1024); return &s }()},
	} {
		t.Run(name, func(t *testing.T) {
			if _, ok := webhookMessageCharge(msg, base+100); ok {
				t.Fatal("retained form did not consume budget")
			}
		})
	}
	cycle := map[string]any{}
	cycle["self"] = cycle
	if _, ok := webhookMessageCharge(Message{Body: cycle}, 1<<20); ok {
		t.Fatal("cyclic body accepted")
	}
	var deep any = "leaf"
	for i := 0; i < 1024; i++ {
		deep = map[string]any{"child": deep}
	}
	if _, ok := webhookMessageCharge(Message{Body: deep}, 1<<20); !ok {
		t.Fatal("supported explicit 1024-level JSON depth rejected by queue preflight")
	}
}

func testEngine(t *testing.T, workers, queue int) *webhookDeliveryEngine {
	t.Helper()
	return newWebhookDeliveryEngine("notify", workers, queue, 5*time.Second, nil, testLogger())
}

func envelopeDelivery(url string) *webhookDelivery {
	return &webhookDelivery{destID: "notify", target: "orders", url: url, mode: "envelope",
		client: &http.Client{Timeout: 2 * time.Second}, retry: RetryPolicy{MaxAttempts: 3, InitialBackoff: time.Millisecond, MaxBackoff: 5 * time.Millisecond},
		auth: webhookAuth{Type: "none"}, logger: testLogger()}
}

func TestWebhookEngineEnvelopeDelivers(t *testing.T) {
	var gotBody []byte
	var gotCT string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		gotCT = r.Header.Get("Content-Type")
		w.WriteHeader(200)
	}))
	defer srv.Close()
	e := testEngine(t, 2, 10)
	if err := e.Enqueue(webhookJob{delivery: envelopeDelivery(srv.URL), msg: Message{ID: "evt_1", Endpoint: "e", Queue: "orders", ReceivedAt: time.Now()}}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	_ = e.Close() // drains
	if gotCT != "application/json" || len(gotBody) == 0 {
		t.Fatalf("did not receive JSON envelope: ct=%q body=%q", gotCT, gotBody)
	}
}

func proxyDelivery(url string) *webhookDelivery {
	return &webhookDelivery{destID: "notify", target: "orders", url: url, mode: "proxy",
		client: &http.Client{Timeout: 2 * time.Second}, retry: RetryPolicy{MaxAttempts: 1, InitialBackoff: time.Millisecond, MaxBackoff: 5 * time.Millisecond},
		auth: webhookAuth{Type: "proxy"}, logger: testLogger()}
}

func TestWebhookEngineStampsHopHeaderEnvelopeMode(t *testing.T) {
	var gotHops string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHops = r.Header.Get("X-Hookway-Hops")
		w.WriteHeader(200)
	}))
	defer srv.Close()
	e := testEngine(t, 1, 10)
	// Inbound hop count 2 → outbound must be stamped 3.
	_ = e.Enqueue(webhookJob{delivery: envelopeDelivery(srv.URL), msg: Message{ID: "evt", ReceivedAt: time.Now(), Hops: 2}})
	_ = e.Close()
	if gotHops != "3" {
		t.Fatalf("outbound X-Hookway-Hops = %q, want 3", gotHops)
	}
}

func TestWebhookEngineStampsHopHeaderProxyModeOverwritesInbound(t *testing.T) {
	var gotHops []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHops = r.Header.Values("X-Hookway-Hops")
		w.WriteHeader(200)
	}))
	defer srv.Close()
	e := testEngine(t, 1, 10)
	// Proxy mode copies OrigHeaders (which carry an inbound hop of 1); stamping
	// must overwrite it with the incremented value, not append a second header.
	orig := http.Header{}
	orig.Set("X-Hookway-Hops", "1")
	_ = e.Enqueue(webhookJob{delivery: proxyDelivery(srv.URL), msg: Message{ID: "evt", ReceivedAt: time.Now(), Hops: 1, OrigHeaders: orig, RawBody: []byte("{}")}})
	_ = e.Close()
	if len(gotHops) != 1 || gotHops[0] != "2" {
		t.Fatalf("outbound X-Hookway-Hops = %v, want single value 2", gotHops)
	}
}

// TestWebhookEngineSetsTraceparentHeaderEnvelopeMode covers C4: the outbound
// request must carry msg.Traceparent verbatim (buildWebhookRequest is the
// only place that stamps it — the envelope JSON body separately carries the
// field too, but the header is what actually propagates the trace to a
// receiver that isn't parsing the envelope).
func TestWebhookEngineSetsTraceparentHeaderEnvelopeMode(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Traceparent")
		w.WriteHeader(200)
	}))
	defer srv.Close()
	e := testEngine(t, 1, 10)
	want := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	_ = e.Enqueue(webhookJob{delivery: envelopeDelivery(srv.URL), msg: Message{ID: "evt", ReceivedAt: time.Now(), Traceparent: want}})
	_ = e.Close()
	if got != want {
		t.Fatalf("outbound Traceparent = %q, want %q", got, want)
	}
}

// TestWebhookEngineSetsTraceparentHeaderProxyModeOverwritesInbound covers C4
// for proxy mode: traceparent is not hop-by-hop, so any inbound Traceparent
// copied via OrigHeaders must be overwritten with msg.Traceparent (the value
// computed from the request Hookway actually received), not left as whatever
// the original sender happened to send.
func TestWebhookEngineSetsTraceparentHeaderProxyModeOverwritesInbound(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Values("Traceparent")
		w.WriteHeader(200)
	}))
	defer srv.Close()
	e := testEngine(t, 1, 10)
	orig := http.Header{}
	orig.Set("Traceparent", "00-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-aaaaaaaaaaaaaaaa-00")
	want := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	_ = e.Enqueue(webhookJob{delivery: proxyDelivery(srv.URL), msg: Message{
		ID: "evt", ReceivedAt: time.Now(), OrigHeaders: orig, RawBody: []byte("{}"), Traceparent: want,
	}})
	_ = e.Close()
	if len(got) != 1 || got[0] != want {
		t.Fatalf("outbound Traceparent = %v, want single value %q", got, want)
	}
}

// TestWebhookEngineOmitsTraceparentHeaderWhenMessageHasNone guards the
// "only set when non-empty" rule: an empty msg.Traceparent must not stamp an
// empty header (nor leave a copied-but-blank one in proxy mode).
func TestWebhookEngineOmitsTraceparentHeaderWhenMessageHasNone(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Values("Traceparent")
		w.WriteHeader(200)
	}))
	defer srv.Close()
	e := testEngine(t, 1, 10)
	_ = e.Enqueue(webhookJob{delivery: envelopeDelivery(srv.URL), msg: Message{ID: "evt", ReceivedAt: time.Now()}})
	_ = e.Close()
	if len(got) != 0 {
		t.Fatalf("expected no Traceparent header, got %v", got)
	}
}

func TestWebhookEngineRetriesThenSucceeds(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) < 2 {
			w.WriteHeader(503)
			return
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()
	e := testEngine(t, 1, 10)
	_ = e.Enqueue(webhookJob{delivery: envelopeDelivery(srv.URL), msg: Message{ID: "evt", ReceivedAt: time.Now()}})
	_ = e.Close()
	if atomic.LoadInt32(&calls) != 2 {
		t.Fatalf("expected 2 attempts (503 then 200), got %d", calls)
	}
}

func TestWebhookEngineTerminalOn4xxNoRetry(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(400)
	}))
	defer srv.Close()
	e := testEngine(t, 1, 10)
	_ = e.Enqueue(webhookJob{delivery: envelopeDelivery(srv.URL), msg: Message{ID: "evt", ReceivedAt: time.Now()}})
	_ = e.Close()
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("4xx must not retry; got %d attempts", calls)
	}
}

func TestWebhookEngineQueueFull(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-block; w.WriteHeader(200) }))
	defer srv.Close()
	defer close(block)
	e := newWebhookDeliveryEngine("notify", 1, 1, time.Second, nil, testLogger())
	d := envelopeDelivery(srv.URL)
	// first fills the worker, second fills the queue(1), third must be rejected
	_ = e.Enqueue(webhookJob{delivery: d, msg: Message{ID: "1", ReceivedAt: time.Now()}})
	_ = e.Enqueue(webhookJob{delivery: d, msg: Message{ID: "2", ReceivedAt: time.Now()}})
	var full bool
	for i := 0; i < 50; i++ { // account for scheduling; eventually queue is full
		if err := e.Enqueue(webhookJob{delivery: d, msg: Message{ID: "x", ReceivedAt: time.Now()}}); errors.Is(err, ErrWebhookQueueFull) {
			full = true
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !full {
		t.Fatalf("expected ErrWebhookQueueFull under load")
	}
	// block is unblocked by deferred close(block) above
}

func TestWebhookEngineDrainDeliversQueued(t *testing.T) {
	var delivered int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(20 * time.Millisecond)
		atomic.AddInt32(&delivered, 1)
		w.WriteHeader(200)
	}))
	defer srv.Close()
	e := testEngine(t, 2, 10)
	for i := 0; i < 5; i++ {
		_ = e.Enqueue(webhookJob{delivery: envelopeDelivery(srv.URL), msg: Message{ID: "e", ReceivedAt: time.Now()}})
	}
	_ = e.Close() // must block until all 5 delivered (within drain timeout)
	if atomic.LoadInt32(&delivered) != 5 {
		t.Fatalf("drain should deliver all queued jobs; delivered %d/5", delivered)
	}
}

// TestWebhookEngineUsesRetryAfterSecondsOverComputedBackoff guards the D8 fix:
// a 429 with Retry-After must win over the (much smaller) configured
// exponential backoff for the wait before the next attempt.
func TestWebhookEngineUsesRetryAfterSecondsOverComputedBackoff(t *testing.T) {
	var calls int32
	var secondCallAt time.Time
	firstCallAt := time.Now()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(429)
			return
		}
		secondCallAt = time.Now()
		w.WriteHeader(200)
	}))
	defer srv.Close()
	e := testEngine(t, 1, 10)
	d := envelopeDelivery(srv.URL)
	d.retry = RetryPolicy{MaxAttempts: 2, InitialBackoff: time.Millisecond, MaxBackoff: 10 * time.Second}
	_ = e.Enqueue(webhookJob{delivery: d, msg: Message{ID: "evt", ReceivedAt: time.Now()}})
	_ = e.Close()
	if atomic.LoadInt32(&calls) != 2 {
		t.Fatalf("expected 2 attempts (429 then 200), got %d", calls)
	}
	if secondCallAt.Sub(firstCallAt) < 900*time.Millisecond {
		t.Fatalf("retry fired after %v, want >= ~1s per Retry-After header (not the 1ms computed backoff)", secondCallAt.Sub(firstCallAt))
	}
}

// dlqDelivery builds a delivery whose target always fails terminally and routes
// to the given dead-letter destination/target.
func dlqDelivery(url, dlqDest, dlqTarget string) *webhookDelivery {
	d := envelopeDelivery(url)
	d.retry = RetryPolicy{MaxAttempts: 3, InitialBackoff: time.Millisecond, MaxBackoff: 2 * time.Millisecond}
	d.dlqDestination = dlqDest
	d.dlqTarget = dlqTarget
	return d
}

func TestWebhookEngineDeadLetterDispatchedOnTerminal4xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
	}))
	defer srv.Close()

	var gotDest, gotTarget string
	var gotMsg Message
	done := make(chan struct{})
	m := &fakeWebhookMetrics{}
	e := newWebhookDeliveryEngine("notify", 1, 10, time.Second, m, testLogger())
	e.setDeadLetter(func(ctx context.Context, destID, target string, msg Message) error {
		gotDest, gotTarget, gotMsg = destID, target, msg
		close(done)
		return nil
	})
	_ = e.Enqueue(webhookJob{delivery: dlqDelivery(srv.URL, "dlq", "dead"), msg: Message{ID: "evt", ReceivedAt: time.Now()}})
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("dead-letter resolver was not invoked")
	}
	_ = e.Close()

	if gotDest != "dlq" || gotTarget != "dead" {
		t.Fatalf("resolver got dest=%q target=%q, want dlq/dead", gotDest, gotTarget)
	}
	if gotMsg.DeadLetter == nil {
		t.Fatal("dead-letter meta not populated on message")
	}
	dl := gotMsg.DeadLetter
	if dl.Destination != "notify" || dl.Target != "orders" || dl.Outcome != "4xx" {
		t.Fatalf("dead-letter meta = %#v", dl)
	}
	if dl.Attempts != 1 {
		t.Fatalf("dead-letter attempts = %d, want 1 (4xx is terminal on first try)", dl.Attempts)
	}
	if dl.FailedAt.IsZero() {
		t.Fatal("dead-letter FailedAt not set")
	}
	if atomic.LoadInt32(&m.deadLetter) != 1 {
		t.Fatalf("WebhookDeadLetter metric = %d, want 1", m.deadLetter)
	}
}

func TestWebhookEngineDeadLetterAttemptsCountOnExhaustedRetries(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500) // always 5xx => retryable, exhausts attempts
	}))
	defer srv.Close()

	var attempts int
	done := make(chan struct{})
	e := newWebhookDeliveryEngine("notify", 1, 10, time.Second, nil, testLogger())
	e.setDeadLetter(func(ctx context.Context, destID, target string, msg Message) error {
		attempts = msg.DeadLetter.Attempts
		close(done)
		return nil
	})
	d := dlqDelivery(srv.URL, "dlq", "dead")
	d.retry = RetryPolicy{MaxAttempts: 3, InitialBackoff: time.Millisecond, MaxBackoff: 2 * time.Millisecond}
	_ = e.Enqueue(webhookJob{delivery: d, msg: Message{ID: "evt", ReceivedAt: time.Now()}})
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("resolver not invoked")
	}
	_ = e.Close()
	if attempts != 3 {
		t.Fatalf("dead-letter attempts = %d, want 3 (exhausted)", attempts)
	}
}

func TestWebhookEngineDeadLetterResolverErrorRecordsFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
	}))
	defer srv.Close()

	done := make(chan struct{})
	m := &fakeWebhookMetrics{}
	e := newWebhookDeliveryEngine("notify", 1, 10, time.Second, m, testLogger())
	e.setDeadLetter(func(ctx context.Context, destID, target string, msg Message) error {
		close(done)
		return context.DeadlineExceeded // simulate DLQ publish failure
	})
	_ = e.Enqueue(webhookJob{delivery: dlqDelivery(srv.URL, "dlq", "dead"), msg: Message{ID: "evt", ReceivedAt: time.Now()}})
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("resolver not invoked")
	}
	_ = e.Close()
	if atomic.LoadInt32(&m.deadLetterFail) != 1 {
		t.Fatalf("WebhookDeadLetterFailure = %d, want 1", m.deadLetterFail)
	}
	if atomic.LoadInt32(&m.deadLetter) != 0 {
		t.Fatalf("WebhookDeadLetter should not fire on failure, got %d", m.deadLetter)
	}
}

func TestWebhookEngineDeadLetterFailureLogsSafeDiagnostics(t *testing.T) {
	for name, dispatchErr := range map[string]error{
		"URL": fmt.Errorf("DLQ dispatch failed: %w", &url.Error{Op: "Post", URL: `http://user:USERSECRET@dlq.example.test/PATHSECRET?api_key=quoted"QUERYSECRET`, Err: context.DeadlineExceeded}), // #nosec G101 -- synthetic redaction markers, not credentials.
		"DSN": fmt.Errorf("connect host=dlq.example.test password='DSNSECRET with spaces' token=TOKENSECRET: %w", context.DeadlineExceeded),
	} {
		t.Run(name, func(t *testing.T) {
			source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) }))
			defer source.Close()
			var logs bytes.Buffer
			m := &fakeWebhookMetrics{}
			e := newWebhookDeliveryEngine("notify", 1, 10, time.Second, m, slog.New(slog.NewJSONHandler(&logs, nil)))
			originalText := dispatchErr.Error()
			var dispatched Message
			e.setDeadLetter(func(_ context.Context, _, _ string, msg Message) error {
				dispatched = msg
				return dispatchErr
			})
			var terminal terminalRecorder
			e.setTerminalFailure(terminal.record)
			body := []byte("PAYLOADSECRET")
			if err := e.Enqueue(webhookJob{delivery: dlqDelivery(source.URL, "dlq", "dead"), msg: Message{ID: "evt", RawBody: body}, origin: Origin{Flow: "orders"}}); err != nil {
				t.Fatal(err)
			}
			if err := e.Close(); err != nil {
				t.Fatal(err)
			}
			for _, marker := range []string{"USERSECRET", "PATHSECRET", "QUERYSECRET", "DSNSECRET", "TOKENSECRET", "PAYLOADSECRET"} {
				if strings.Contains(logs.String(), marker) {
					t.Fatalf("DLQ diagnostic retained %s", marker)
				}
			}
			if !strings.Contains(logs.String(), "dlq.example.test") || !strings.Contains(logs.String(), "context deadline exceeded") {
				t.Fatal("DLQ diagnostic lost backend host or underlying error class")
			}
			got := terminal.all()
			if len(got) != 1 || got[0].Outcome != "4xx" || got[0].Attempts != 1 || got[0].Err == nil ||
				!bytes.Equal(got[0].Message.RawBody, body) || !bytes.Equal(dispatched.RawBody, body) || dispatchErr.Error() != originalText || !errors.Is(dispatchErr, context.DeadlineExceeded) {
				t.Fatal("diagnostic redaction changed original delivery outcome, error or retained bytes")
			}
			if atomic.LoadInt32(&m.deadLetterFail) != 1 || atomic.LoadInt32(&m.deadLetter) != 0 {
				t.Fatal("DLQ failure metric classification changed")
			}
		})
	}
}

func TestWebhookEngineNoDeadLetterLogsAndDrops(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
	}))
	defer srv.Close()

	var resolverCalled int32
	e := newWebhookDeliveryEngine("notify", 1, 10, time.Second, nil, testLogger())
	e.setDeadLetter(func(ctx context.Context, destID, target string, msg Message) error {
		atomic.AddInt32(&resolverCalled, 1)
		return nil
	})
	// delivery with empty dlqDestination => log-and-drop, resolver never called.
	d := envelopeDelivery(srv.URL)
	d.retry = RetryPolicy{MaxAttempts: 1, InitialBackoff: time.Millisecond, MaxBackoff: time.Millisecond}
	_ = e.Enqueue(webhookJob{delivery: d, msg: Message{ID: "evt", ReceivedAt: time.Now()}})
	_ = e.Close()
	if atomic.LoadInt32(&resolverCalled) != 0 {
		t.Fatalf("resolver must not be called when no dead_letter configured, called %d", resolverCalled)
	}
}

func TestWebhookEngineSaturatedBoundaryAt90Percent(t *testing.T) {
	// Bare engine, no workers, so the buffer depth is fully controlled.
	e := &webhookDeliveryEngine{destID: "notify", jobs: make(chan webhookJob, 10), logger: testLogger()}
	fill := func(n int) {
		for len(e.jobs) < n {
			e.jobs <- webhookJob{}
		}
		for len(e.jobs) > n {
			<-e.jobs
		}
	}
	fill(8)
	if _, _, sat := e.saturated(); sat {
		t.Fatalf("8/10 must not be saturated")
	}
	fill(9) // exactly 90%
	if depth, capacity, sat := e.saturated(); !sat {
		t.Fatalf("9/10 must be saturated (depth=%d cap=%d)", depth, capacity)
	}
}

func TestWebhookEngineQueueDepthMetricOnEnqueue(t *testing.T) {
	// Bare engine, no workers: Enqueue reports the post-send depth deterministically.
	m := &fakeWebhookMetrics{}
	e := &webhookDeliveryEngine{destID: "notify", jobs: make(chan webhookJob, 10), metrics: m, logger: testLogger()}
	if err := e.Enqueue(webhookJob{}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if got := atomic.LoadInt64(&m.lastDepth); got != 1 {
		t.Fatalf("WebhookQueueDepth reported %d, want 1", got)
	}
}

// TestWebhookEngineGarbageRetryAfterFallsBackToComputedBackoff guards the
// fallback path: an unparseable Retry-After must not stall delivery — the
// engine should fall back to the (fast, in this test) computed backoff.
func TestWebhookEngineGarbageRetryAfterFallsBackToComputedBackoff(t *testing.T) {
	var calls int32
	start := time.Now()
	var secondCallAt time.Time
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.Header().Set("Retry-After", "not-a-valid-value")
			w.WriteHeader(429)
			return
		}
		secondCallAt = time.Now()
		w.WriteHeader(200)
	}))
	defer srv.Close()
	e := testEngine(t, 1, 10)
	d := envelopeDelivery(srv.URL)
	d.retry = RetryPolicy{MaxAttempts: 2, InitialBackoff: time.Millisecond, MaxBackoff: 5 * time.Millisecond}
	_ = e.Enqueue(webhookJob{delivery: d, msg: Message{ID: "evt", ReceivedAt: time.Now()}})
	_ = e.Close()
	if atomic.LoadInt32(&calls) != 2 {
		t.Fatalf("expected 2 attempts (429 then 200), got %d", calls)
	}
	if secondCallAt.Sub(start) > time.Second {
		t.Fatalf("garbage Retry-After should fall back to fast computed backoff, took %v", secondCallAt.Sub(start))
	}
}

// TestWebhookEngineWorkerRecoversPanicAndKeepsDeliveringNextJob covers plan
// item C2: a delivery that panics inside attempt() (here, a nil *http.Client,
// which panics on Do) must not take down the worker goroutine. The key
// assertion is that the test process survives (does not crash) and the next
// enqueued job on the same worker is still delivered afterward.
func TestWebhookEngineWorkerRecoversPanicAndKeepsDeliveringNextJob(t *testing.T) {
	var delivered int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&delivered, 1)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	m := &fakeWebhookMetrics{}
	e := newWebhookDeliveryEngine("notify", 1, 10, 5*time.Second, m, testLogger())

	panicking := envelopeDelivery(srv.URL)
	panicking.client = nil // (*http.Client)(nil).Do panics with a nil pointer dereference
	if err := e.Enqueue(webhookJob{delivery: panicking, msg: Message{ID: "boom", ReceivedAt: time.Now()}}); err != nil {
		t.Fatalf("enqueue panicking job: %v", err)
	}
	if err := e.Enqueue(webhookJob{delivery: envelopeDelivery(srv.URL), msg: Message{ID: "ok", ReceivedAt: time.Now()}}); err != nil {
		t.Fatalf("enqueue healthy job: %v", err)
	}
	_ = e.Close()

	if atomic.LoadInt32(&delivered) != 1 {
		t.Fatalf("worker must survive the panic and still deliver the next job; delivered=%d", delivered)
	}
	if atomic.LoadInt32(&m.panics) != 1 {
		t.Fatalf("Panic metric calls = %d, want 1", m.panics)
	}
	if m.lastPanicLocation != "webhook_worker" {
		t.Fatalf("panic location = %q, want webhook_worker", m.lastPanicLocation)
	}
	if e.pendingBytes != 0 {
		t.Fatalf("panic leaked accounted bytes: %d", e.pendingBytes)
	}
}
