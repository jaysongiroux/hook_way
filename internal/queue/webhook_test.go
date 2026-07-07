package queue

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasongiroux/hookway/internal/config"
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
	eng := newWebhookDeliveryEngine(2, 10, 2*time.Second, nil, testLogger())
	pub, err := NewWebhookPublisher(eng, config.WebhookConfig{URL: srv.URL, Timeout: time.Second, Auth: config.WebhookAuthConfig{Type: "none"}},
		"notify", []WebhookTarget{{Name: "orders"}}, nil, testLogger())
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
	eng := newWebhookDeliveryEngine(1, 1, time.Second, nil, testLogger())
	pub, _ := NewWebhookPublisher(eng, config.WebhookConfig{URL: "http://x", Auth: config.WebhookAuthConfig{Type: "none"}}, "notify", []WebhookTarget{{Name: "orders"}}, nil, testLogger())
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
	eng := newWebhookDeliveryEngine(1, 10, 2*time.Second, nil, testLogger())
	pub, _ := NewWebhookPublisher(eng, config.WebhookConfig{URL: srv.URL, Timeout: time.Second, Auth: config.WebhookAuthConfig{Type: "proxy"}}, "notify", []WebhookTarget{{Name: ""}}, nil, testLogger())
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
	eng := newWebhookDeliveryEngine(1, 10, time.Second, nil, testLogger())
	pub, err := NewWebhookPublisher(eng, config.WebhookConfig{
		URL:  "https://example.com/hooks",
		Auth: config.WebhookAuthConfig{Type: "none"},
		// Retry intentionally left zero (unset)
	}, "notify", []WebhookTarget{{Name: "orders"}}, nil, testLogger())
	if err != nil {
		t.Fatalf("ctor: %v", err)
	}
	defer pub.Close()

	d := pub.delivery["orders"]
	if d == nil {
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

// fakeWebhookMetrics counts calls to each metric method.
type fakeWebhookMetrics struct {
	dropped int32
	retries int32
	// deliveries[outcome]++
	deliveries map[string]int32
}

func (f *fakeWebhookMetrics) WebhookDropped(_, _ string) { atomic.AddInt32(&f.dropped, 1) }
func (f *fakeWebhookMetrics) WebhookRetry(_, _ string)   { atomic.AddInt32(&f.retries, 1) }
func (f *fakeWebhookMetrics) WebhookDelivery(_, _, outcome string) {
	// not needed for this test, but must satisfy the interface
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
	eng := newWebhookDeliveryEngine(1, 1, time.Second, nil, testLogger())
	m := &fakeWebhookMetrics{}
	pub, err := NewWebhookPublisher(eng, config.WebhookConfig{URL: srv.URL, Timeout: time.Second, Auth: config.WebhookAuthConfig{Type: "none"}},
		"notify", []WebhookTarget{{Name: "orders"}}, m, testLogger())
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
}
