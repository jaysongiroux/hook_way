package queue

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jaysongiroux/hook_way/internal/config"
)

func TestJoinWebhookURL(t *testing.T) {
	cases := [][3]string{
		{"https://x/hooks", "orders", "https://x/hooks/orders"},
		{"https://x/hooks/", "orders", "https://x/hooks/orders"},
		{"https://x/hooks", "", "https://x/hooks"},
	}
	for _, c := range cases {
		if got := joinWebhookURL(c[0], c[1]); got != c[2] {
			t.Fatalf("join(%q,%q)=%q want %q", c[0], c[1], got, c[2])
		}
	}
}

func TestWebhookPublisherEnqueuesAndDelivers(t *testing.T) {
	var hits int32
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		gotPath = r.URL.Path
		_, _ = io.ReadAll(r.Body)
		w.WriteHeader(200)
	}))
	defer srv.Close()
	pub, err := NewWebhookPublisher(config.WebhookConfig{URL: srv.URL, Timeout: time.Second, Auth: config.WebhookAuthConfig{Type: "none"}},
		"notify", []WebhookTarget{{Name: "orders"}}, WebhookPublisherOptions{Workers: 2, DefaultDrain: 2 * time.Second, Logger: testLogger()})
	if err != nil {
		t.Fatalf("ctor: %v", err)
	}
	if err := pub.Publish(context.Background(), "notify", "orders", Message{ID: "evt", ReceivedAt: time.Now()}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	_ = pub.Close() // drains engine
	if atomic.LoadInt32(&hits) != 1 || gotPath != "/orders" {
		t.Fatalf("expected 1 delivery to /orders, got hits=%d path=%q", hits, gotPath)
	}
}

func TestWebhookPublisherRejectsUnknownTarget(t *testing.T) {
	pub, _ := NewWebhookPublisher(config.WebhookConfig{URL: "http://x", Auth: config.WebhookAuthConfig{Type: "none"}}, "notify", []WebhookTarget{{Name: "orders"}}, WebhookPublisherOptions{Workers: 1, DefaultDrain: time.Second, Logger: testLogger()})
	if err := pub.Publish(context.Background(), "notify", "nope", Message{ID: "x", ReceivedAt: time.Now()}); err == nil {
		t.Fatal("expected error for unknown target")
	}
	_ = pub.Close()
}

func TestWebhookPublisherProxyForwardsRawBodyAndHeaders(t *testing.T) {
	var gotBody []byte
	var gotSig string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		gotSig = r.Header.Get("X-Vendor-Signature")
		w.WriteHeader(200)
	}))
	defer srv.Close()
	pub, _ := NewWebhookPublisher(config.WebhookConfig{URL: srv.URL, Timeout: time.Second, Auth: config.WebhookAuthConfig{Type: "proxy"}}, "notify", []WebhookTarget{{Name: ""}}, WebhookPublisherOptions{Workers: 1, DefaultDrain: 2 * time.Second, Logger: testLogger()})
	orig := http.Header{}
	orig.Set("X-Vendor-Signature", "abc123")
	orig.Set("Content-Type", "application/xml")
	if err := pub.Publish(context.Background(), "notify", "", Message{ID: "x", ReceivedAt: time.Now(), RawBody: []byte("<xml/>"), OrigHeaders: orig}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	_ = pub.Close()
	if string(gotBody) != "<xml/>" || gotSig != "abc123" {
		t.Fatalf("proxy did not forward original body+headers: body=%q sig=%q", gotBody, gotSig)
	}
}

func TestWebhookPublisherDefaultRetryPolicy(t *testing.T) {
	// Build a publisher with an empty retry config (all-zero) and confirm the
	// delivery gets the documented defaults: max_attempts=3, initial_backoff=500ms,
	// max_backoff=30s. This is the fast, deterministic approach (no real HTTP calls).
	pub, err := NewWebhookPublisher(config.WebhookConfig{
		URL:  "https://example.com/hooks",
		Auth: config.WebhookAuthConfig{Type: "none"},
		// Retry intentionally left zero (unset)
	}, "notify", []WebhookTarget{{Name: "orders"}}, WebhookPublisherOptions{Workers: 1, DefaultDrain: time.Second, Logger: testLogger()})
	if err != nil {
		t.Fatalf("ctor: %v", err)
	}
	defer func() { _ = pub.Close() }()

	d, ok := pub.delivery.lookup("orders")
	if !ok || d == nil {
		t.Fatal("delivery for 'orders' not found")
	}
	if d.retry.MaxAttempts != 3 {
		t.Errorf("MaxAttempts = %d, want 3", d.retry.MaxAttempts)
	}
	if d.retry.InitialBackoff != 500*time.Millisecond {
		t.Errorf("InitialBackoff = %v, want 500ms", d.retry.InitialBackoff)
	}
	if d.retry.MaxBackoff != 30*time.Second {
		t.Errorf("MaxBackoff = %v, want 30s", d.retry.MaxBackoff)
	}
}

func TestWebhookSyncPublishReturnsNilOn2xx(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(200)
	}))
	defer srv.Close()
	pub, err := NewWebhookPublisher(config.WebhookConfig{URL: srv.URL, Timeout: time.Second, DeliveryMode: "sync", Auth: config.WebhookAuthConfig{Type: "none"}},
		"notify", []WebhookTarget{{Name: "orders"}}, WebhookPublisherOptions{Logger: testLogger()})
	if err != nil {
		t.Fatalf("ctor: %v", err)
	}
	if err := pub.Publish(context.Background(), "notify", "orders", Message{ID: "e", ReceivedAt: time.Now()}); err != nil {
		t.Fatalf("sync publish: %v", err)
	}
	// Sync delivery happens before Publish returns — no Close/drain needed.
	if atomic.LoadInt32(&hits) != 1 {
		t.Fatalf("sync mode should deliver inline; hits=%d", hits)
	}
	if err := pub.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}

func TestWebhookRedirectDoesNotForwardCredentialsOrBody(t *testing.T) {
	for _, status := range []int{301, 302, 303, 307, 308} {
		for _, authType := range []string{"api_key", "proxy"} {
			for _, sameOrigin := range []bool{false, true} {
				t.Run(fmt.Sprintf("status=%d/auth=%s/same-origin=%t", status, authType, sameOrigin), func(t *testing.T) {
					var leaked, calls atomic.Int32
					other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						leaked.Add(1)
						w.WriteHeader(http.StatusOK)
					}))
					defer other.Close()
					source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if r.URL.Path == "/redirected" {
							leaked.Add(1)
							w.WriteHeader(http.StatusOK)
							return
						}
						calls.Add(1)
						body, _ := io.ReadAll(r.Body)
						if r.Method != http.MethodPost || r.Header.Get("X-Delivery-Key") != "CREDENTIALSECRET" || !strings.Contains(string(body), "PAYLOADSECRET") {
							t.Error("configured local receiver did not receive expected POST/auth/body")
						}
						target := other.URL
						if sameOrigin {
							target = "/redirected"
						}
						w.Header().Set("Location", target)
						w.WriteHeader(status)
					}))
					defer source.Close()
					pub, err := NewWebhookPublisher(config.WebhookConfig{URL: source.URL, Timeout: time.Second, DeliveryMode: "sync",
						Auth: config.WebhookAuthConfig{Type: authType, Header: "X-Delivery-Key", Value: "CREDENTIALSECRET"}},
						"notify", []WebhookTarget{{Name: "orders"}}, WebhookPublisherOptions{Logger: testLogger()})
					if err != nil {
						t.Fatal(err)
					}
					defer func() { _ = pub.Close() }()
					msg := Message{ID: "PAYLOADSECRET", ReceivedAt: time.Now(), RawBody: []byte("PAYLOADSECRET"), OrigHeaders: http.Header{"X-Delivery-Key": {"CREDENTIALSECRET"}}}
					if err := pub.Publish(t.Context(), "notify", "orders", msg); err == nil {
						t.Fatal("redirect was reported as successful delivery")
					}
					if calls.Load() != 1 || leaked.Load() != 0 {
						t.Fatal("redirect forwarded credentials/body or retried a terminal response")
					}
				})
			}
		}
	}
}

func TestWebhookSyncPublishReturnsErrorOn4xx(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(400)
	}))
	defer srv.Close()
	pub, _ := NewWebhookPublisher(config.WebhookConfig{URL: srv.URL, Timeout: time.Second, DeliveryMode: "sync", Auth: config.WebhookAuthConfig{Type: "none"},
		Retry: config.RetryPolicyConfig{MaxAttempts: 3, InitialBackoff: time.Millisecond, MaxBackoff: 2 * time.Millisecond}},
		"notify", []WebhookTarget{{Name: "orders"}}, WebhookPublisherOptions{Logger: testLogger()})
	if err := pub.Publish(context.Background(), "notify", "orders", Message{ID: "e", ReceivedAt: time.Now()}); err == nil {
		t.Fatal("sync publish should return error on terminal 4xx")
	}
	if atomic.LoadInt32(&hits) != 1 {
		t.Fatalf("4xx is terminal; want exactly 1 attempt, got %d", hits)
	}
}

func TestWebhookSyncPublishReturnsErrorOnExhausted5xx(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(503)
	}))
	defer srv.Close()
	pub, _ := NewWebhookPublisher(config.WebhookConfig{URL: srv.URL, Timeout: time.Second, DeliveryMode: "sync", Auth: config.WebhookAuthConfig{Type: "none"},
		Retry: config.RetryPolicyConfig{MaxAttempts: 3, InitialBackoff: time.Millisecond, MaxBackoff: 2 * time.Millisecond}},
		"notify", []WebhookTarget{{Name: "orders"}}, WebhookPublisherOptions{Logger: testLogger()})
	if err := pub.Publish(context.Background(), "notify", "orders", Message{ID: "e", ReceivedAt: time.Now()}); err == nil {
		t.Fatal("sync publish should return error after exhausting retries on 5xx")
	}
	if atomic.LoadInt32(&hits) != 3 {
		t.Fatalf("want 3 attempts on exhausted 5xx, got %d", hits)
	}
}

func TestWebhookSyncPublishHonorsContextDeadlineMidBackoff(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503) // retryable, forces a backoff sleep the ctx will cut short
	}))
	defer srv.Close()
	pub, _ := NewWebhookPublisher(config.WebhookConfig{URL: srv.URL, Timeout: time.Second, DeliveryMode: "sync", Auth: config.WebhookAuthConfig{Type: "none"},
		Retry: config.RetryPolicyConfig{MaxAttempts: 5, InitialBackoff: time.Second, MaxBackoff: 5 * time.Second}},
		"notify", []WebhookTarget{{Name: "orders"}}, WebhookPublisherOptions{Logger: testLogger()})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := pub.Publish(ctx, "notify", "orders", Message{ID: "e", ReceivedAt: time.Now()}); err == nil {
		t.Fatal("expected error when context deadline expires mid-backoff")
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("ctx deadline should cut the 1s backoff short; took %v", elapsed)
	}
}

func TestWebhookReadyReflectsSaturation(t *testing.T) {
	// Use a worker-less engine so buffer depth is fully controlled (a live worker
	// pool would consume the junk jobs and is racy). Same-package struct literals.
	e := &webhookDeliveryEngine{destID: "notify", jobs: make(chan webhookJob, 10), logger: testLogger()}
	pub := &WebhookPublisher{engine: e, destID: "notify", logger: testLogger()}
	if err := pub.Ready(context.Background()); err != nil {
		t.Fatalf("empty queue must be Ready, got %v", err)
	}
	for len(e.jobs) < 9 { // 90% => saturated
		e.jobs <- webhookJob{}
	}
	if err := pub.Ready(context.Background()); err == nil {
		t.Fatal("saturated queue must report not-Ready")
	}
}

func TestWebhookSyncModeHasNoEngine(t *testing.T) {
	pub, _ := NewWebhookPublisher(config.WebhookConfig{URL: "http://x", DeliveryMode: "sync", Auth: config.WebhookAuthConfig{Type: "none"}},
		"notify", []WebhookTarget{{Name: "orders"}}, WebhookPublisherOptions{Logger: testLogger()})
	if pub.engine != nil {
		t.Fatal("sync mode must not build a delivery engine")
	}
	if err := pub.Ready(context.Background()); err != nil {
		t.Fatalf("sync mode Ready must be nil, got %v", err)
	}
	if err := pub.Close(); err != nil {
		t.Fatalf("sync mode Close must be nil, got %v", err)
	}
}

// fakeWebhookMetrics counts calls to each metric method.
type fakeWebhookMetrics struct {
	dropped             int32
	retries             int32
	deadLetter          int32
	deadLetterFail      int32
	lastDroppedReceived string
	lastDepth           int64
	lastCapacity        int64
	panics              int32
	lastPanicLocation   string
}

func (f *fakeWebhookMetrics) WebhookDropped(_, _, reason string) {
	atomic.AddInt32(&f.dropped, 1)
	f.lastDroppedReceived = reason
}
func (f *fakeWebhookMetrics) WebhookRetry(_, _ string) { atomic.AddInt32(&f.retries, 1) }
func (f *fakeWebhookMetrics) WebhookDelivery(_, _, outcome string) {
	// not needed for this test, but must satisfy the interface
}
func (f *fakeWebhookMetrics) WebhookDeadLetter(_, _, _ string) { atomic.AddInt32(&f.deadLetter, 1) }
func (f *fakeWebhookMetrics) WebhookDeadLetterFailure(_, _ string) {
	atomic.AddInt32(&f.deadLetterFail, 1)
}
func (f *fakeWebhookMetrics) WebhookQueueDepth(_ string, depth int) {
	atomic.StoreInt64(&f.lastDepth, int64(depth))
}
func (f *fakeWebhookMetrics) WebhookQueueCapacity(_ string, capacity int) {
	atomic.StoreInt64(&f.lastCapacity, int64(capacity))
}
func (f *fakeWebhookMetrics) Panic(location string) {
	atomic.AddInt32(&f.panics, 1)
	f.lastPanicLocation = location
}

func TestWebhookPublisherDroppedMetricOnQueueFull(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-block
		w.WriteHeader(200)
	}))
	defer srv.Close()
	defer close(block)

	// engine: 1 worker, queue size 1 — so once the worker is busy and the single
	// buffer slot is taken, the next Enqueue must fail and WebhookDropped fires.
	m := &fakeWebhookMetrics{}
	pub, err := NewWebhookPublisher(config.WebhookConfig{URL: srv.URL, Timeout: time.Second, MaxQueue: 1, Auth: config.WebhookAuthConfig{Type: "none"}},
		"notify", []WebhookTarget{{Name: "orders"}}, WebhookPublisherOptions{Workers: 1, DefaultDrain: time.Second, Metrics: m, Logger: testLogger()})
	if err != nil {
		t.Fatalf("ctor: %v", err)
	}

	msg := Message{ID: "x", ReceivedAt: time.Now()}

	// Spin until the engine is demonstrably full (first failure from Publish).
	// Reset the dropped counter after that to isolate the one-shot assertion.
	var filledErr error
	for i := 0; i < 500; i++ {
		if filledErr = pub.Publish(context.Background(), "notify", "orders", msg); filledErr != nil {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if filledErr == nil {
		t.Fatal("expected queue to fill within timeout")
	}
	// We don't care how many drops occurred during warm-up; reset and measure
	// that exactly one more call to a full queue increments the counter once.
	atomic.StoreInt32(&m.dropped, 0)

	dropErr := pub.Publish(context.Background(), "notify", "orders", msg)
	if dropErr == nil {
		t.Fatal("expected Publish to return an error when queue is full")
	}
	if got := atomic.LoadInt32(&m.dropped); got != 1 {
		t.Fatalf("expected WebhookDropped called exactly once, got %d", got)
	}
	if m.lastDroppedReceived != "queue_full" {
		t.Fatalf("expected drop reason %q, got %q", "queue_full", m.lastDroppedReceived)
	}
}
