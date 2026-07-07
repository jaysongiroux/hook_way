package queue

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func testEngine(t *testing.T, workers, queue int) *webhookDeliveryEngine {
	t.Helper()
	return newWebhookDeliveryEngine(workers, queue, 5*time.Second, nil, testLogger())
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
	e := newWebhookDeliveryEngine(1, 1, time.Second, nil, testLogger())
	d := envelopeDelivery(srv.URL)
	// first fills the worker, second fills the queue(1), third must be rejected
	_ = e.Enqueue(webhookJob{delivery: d, msg: Message{ID: "1", ReceivedAt: time.Now()}})
	_ = e.Enqueue(webhookJob{delivery: d, msg: Message{ID: "2", ReceivedAt: time.Now()}})
	var full bool
	for i := 0; i < 50; i++ { // account for scheduling; eventually queue is full
		if err := e.Enqueue(webhookJob{delivery: d, msg: Message{ID: "x", ReceivedAt: time.Now()}}); err == errWebhookQueueFull {
			full = true
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !full {
		t.Fatalf("expected errWebhookQueueFull under load")
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
