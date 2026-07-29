package queue

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasongiroux/hookway/internal/config"
	"github.com/jasongiroux/hookway/internal/metrics"
)

// These tests exercise the full outbound-webhook stack end to end: a real
// config.Config drives Factory.Create, which builds a per-destination delivery
// engine and a WebhookPublisher, and deliveries land on a live httptest.Server.
// Unlike
// the container-backed suites (Postgres/Mongo/Kafka/...), the webhook
// destination talks plain HTTP, so the "integration" here is a loopback server
// rather than Docker — no external dependency, always runs.

// webhookIntegrationConfig builds a single-webhook config wired to url, routed
// from one enabled endpoint to the given target.
func webhookIntegrationConfig(url, target string, auth config.WebhookAuthConfig, retry config.RetryPolicyConfig) config.Config {
	return config.Config{
		Server: config.ServerConfig{FanoutConcurrency: 4, ShutdownTimeout: 5 * time.Second},
		Destinations: map[string]config.DestinationConfig{
			"notify": {Type: "webhook", Webhook: config.WebhookConfig{
				URL: url, Timeout: 2 * time.Second, Auth: auth, Retry: retry,
			}},
		},
		Endpoints: map[string]config.EndpointConfig{
			"hook": {Enabled: true, Destinations: []config.EndpointDestinationConfig{
				{Destination: "notify", Target: target},
			}},
		},
	}
}

// TestWebhookIntegrationEnvelopeDeliveryThroughFactory drives the real factory
// path (including the concrete *metrics.Metrics type flowing through) and
// verifies the envelope JSON arrives at base+/<target> with the message body
// intact. Close() is the barrier: after it returns, the async delivery is done.
func TestWebhookIntegrationEnvelopeDeliveryThroughFactory(t *testing.T) {
	var hits int32
	var gotPath, gotCT string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		gotPath = r.URL.Path
		gotCT = r.Header.Get("Content-Type")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	factory := NewFactory(FactoryOptions{Metrics: metrics.New(), Logger: testLogger()})
	pub, err := factory.Create(context.Background(),
		webhookIntegrationConfig(srv.URL, "orders", config.WebhookAuthConfig{Type: "none"}, config.RetryPolicyConfig{}))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	msg := Message{ID: "evt_1", Endpoint: "hook", DestinationQueueID: "notify", Queue: "orders",
		ReceivedAt: time.Now(), Body: map[string]any{"hello": "world"}}
	if err := pub.Publish(context.Background(), "notify", "orders", msg); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if err := pub.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if atomic.LoadInt32(&hits) != 1 {
		t.Fatalf("expected exactly 1 delivery, got %d", hits)
	}
	if gotPath != "/orders" {
		t.Fatalf("delivered to %q, want /orders", gotPath)
	}
	if gotCT != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", gotCT)
	}
	var decoded Message
	if err := json.Unmarshal(gotBody, &decoded); err != nil {
		t.Fatalf("envelope body is not valid JSON: %v (%s)", err, gotBody)
	}
	if decoded.ID != "evt_1" {
		t.Fatalf("envelope id = %q, want evt_1", decoded.ID)
	}
	body, _ := decoded.Body.(map[string]any)
	if body["hello"] != "world" {
		t.Fatalf("envelope body did not round-trip: %#v", decoded.Body)
	}
}

// TestWebhookIntegrationRetriesOnServerErrorThenSucceeds proves the retry loop
// runs end to end: the server fails twice with 503 then returns 200, and the
// delivery ultimately succeeds after exactly 3 attempts.
func TestWebhookIntegrationRetriesOnServerErrorThenSucceeds(t *testing.T) {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&attempts, 1)
		if n < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	retry := config.RetryPolicyConfig{MaxAttempts: 3, InitialBackoff: 5 * time.Millisecond, MaxBackoff: 20 * time.Millisecond}
	factory := NewFactory(FactoryOptions{Logger: testLogger()})
	pub, err := factory.Create(context.Background(),
		webhookIntegrationConfig(srv.URL, "orders", config.WebhookAuthConfig{Type: "none"}, retry))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := pub.Publish(context.Background(), "notify", "orders", Message{ID: "evt", ReceivedAt: time.Now()}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	_ = pub.Close() // drains, waiting out the backoff sleeps

	if got := atomic.LoadInt32(&attempts); got != 3 {
		t.Fatalf("expected 3 attempts (2 failures + 1 success), got %d", got)
	}
}

// TestWebhookIntegrationTerminal4xxNotRetried proves a 4xx response is terminal:
// even with max_attempts=3 the server is hit exactly once.
func TestWebhookIntegrationTerminal4xxNotRetried(t *testing.T) {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	retry := config.RetryPolicyConfig{MaxAttempts: 3, InitialBackoff: 5 * time.Millisecond, MaxBackoff: 20 * time.Millisecond}
	factory := NewFactory(FactoryOptions{Logger: testLogger()})
	pub, err := factory.Create(context.Background(),
		webhookIntegrationConfig(srv.URL, "orders", config.WebhookAuthConfig{Type: "none"}, retry))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := pub.Publish(context.Background(), "notify", "orders", Message{ID: "evt", ReceivedAt: time.Now()}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	_ = pub.Close()

	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Fatalf("expected exactly 1 attempt for terminal 4xx, got %d", got)
	}
}

// TestWebhookIntegrationHMACSignatureVerifiedByConsumer stands in for a
// consumer implementing the documented verification recipe: it parses the
// t=<ts>,v1=<hex> signature header and recomputes HMAC-SHA256 over "<ts>.<body>"
// with the shared secret, confirming the value hook_way signs is verifiable.
func TestWebhookIntegrationHMACSignatureVerifiedByConsumer(t *testing.T) {
	const secret = "shhh-super-secret"
	var verified int32
	var sawEventID string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		sawEventID = r.Header.Get("X-Hookway-Event-Id")
		sig := r.Header.Get("X-Hookway-Signature") // "t=<ts>,v1=<hex>"
		ts, v1, ok := parseTsDotSignature(sig)
		if !ok {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write([]byte(fmt.Sprintf("%d.%s", ts, body)))
		want := hex.EncodeToString(mac.Sum(nil))
		if hmac.Equal([]byte(want), []byte(v1)) {
			atomic.AddInt32(&verified, 1)
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	auth := config.WebhookAuthConfig{Type: "hmac", Secret: secret, Scheme: "hmac_sha256_ts_dot"}
	factory := NewFactory(FactoryOptions{Logger: testLogger()})
	pub, err := factory.Create(context.Background(),
		webhookIntegrationConfig(srv.URL, "orders", auth, config.RetryPolicyConfig{}))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := pub.Publish(context.Background(), "notify", "orders", Message{ID: "evt_hmac", ReceivedAt: time.Now()}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	_ = pub.Close()

	if atomic.LoadInt32(&verified) != 1 {
		t.Fatal("consumer could not verify the HMAC signature hook_way sent")
	}
	if sawEventID != "evt_hmac" {
		t.Fatalf("X-Hookway-Event-Id = %q, want evt_hmac", sawEventID)
	}
}

// webhookDLQConfig routes an endpoint to webhook "notify" (async) whose terminal
// failures dead-letter to a non-webhook destination "dlq"/"dead". The dlq is a
// rabbitmq destination injected via the RabbitMQ connector seam in tests.
func webhookDLQConfig(url string, retry config.RetryPolicyConfig) config.Config {
	return config.Config{
		Server: config.ServerConfig{FanoutConcurrency: 2, ShutdownTimeout: 5 * time.Second},
		Destinations: map[string]config.DestinationConfig{
			"notify": {Type: "webhook", Webhook: config.WebhookConfig{
				URL: url, Timeout: 2 * time.Second, DeliveryMode: "async", Retry: retry,
				Auth:       config.WebhookAuthConfig{Type: "none"},
				DeadLetter: config.WebhookDeadLetterConfig{Destination: "dlq", Target: "dead"},
			}},
			"dlq": {Type: "rabbitmq", RabbitMQ: config.RabbitMQConfig{URL: "amqp://x", Exchange: "hookway.dlq", ExchangeType: "direct"}},
		},
		Endpoints: map[string]config.EndpointConfig{
			"hook": {Enabled: true, Path: "/hook", Destinations: []config.EndpointDestinationConfig{
				{Destination: "notify", Target: "orders"},
			}},
		},
	}
}

// TestWebhookIntegrationDeadLetterOnTerminal4xx drives a full delivery that fails
// terminally (404) and asserts the message is redirected to the dead-letter
// destination with populated DeadLetter meta.
func TestWebhookIntegrationDeadLetterOnTerminal4xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	rec := &recordingPublisher{}
	f := NewFactory(FactoryOptions{
		RabbitMQConnector: func(ctx context.Context, id string, cfg RabbitMQConfig) (Publisher, error) { return rec, nil },
		Logger:            testLogger(),
	})
	pub, err := f.Create(context.Background(), webhookDLQConfig(srv.URL, config.RetryPolicyConfig{MaxAttempts: 1}))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := pub.Publish(context.Background(), "notify", "orders", Message{ID: "evt_dlq", ReceivedAt: time.Now()}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	_ = pub.Close() // drains: delivery fails, dead-letter dispatched

	if rec.published != 1 {
		t.Fatalf("dead-letter publisher received %d messages, want 1", rec.published)
	}
	if rec.queue != "dead" {
		t.Fatalf("dead-letter target = %q, want \"dead\"", rec.queue)
	}
	if rec.msg.DeadLetter == nil || rec.msg.DeadLetter.Outcome != "4xx" || rec.msg.DeadLetter.Destination != "notify" {
		t.Fatalf("dead-letter meta = %#v", rec.msg.DeadLetter)
	}
}

// TestWebhookIntegrationDeadLetterOnExhaustedRetries proves the attempts count is
// recorded in the dead-letter meta after retries are exhausted on 500s.
func TestWebhookIntegrationDeadLetterOnExhaustedRetries(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	rec := &recordingPublisher{}
	f := NewFactory(FactoryOptions{
		RabbitMQConnector: func(ctx context.Context, id string, cfg RabbitMQConfig) (Publisher, error) { return rec, nil },
		Logger:            testLogger(),
	})
	retry := config.RetryPolicyConfig{MaxAttempts: 3, InitialBackoff: 2 * time.Millisecond, MaxBackoff: 5 * time.Millisecond}
	pub, err := f.Create(context.Background(), webhookDLQConfig(srv.URL, retry))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := pub.Publish(context.Background(), "notify", "orders", Message{ID: "evt", ReceivedAt: time.Now()}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	_ = pub.Close()

	if rec.msg.DeadLetter == nil {
		t.Fatal("dead-letter meta missing")
	}
	if rec.msg.DeadLetter.Attempts != 3 || rec.msg.DeadLetter.Outcome != "5xx" {
		t.Fatalf("dead-letter meta = %#v, want 3 attempts / 5xx", rec.msg.DeadLetter)
	}
}

// TestWebhookIntegrationSyncMode proves sync delivery returns the downstream
// outcome to the caller: an error on 503, nil on 200 (delivered before return).
func TestWebhookIntegrationSyncMode(t *testing.T) {
	t.Run("503 returns error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		}))
		defer srv.Close()
		cfg := webhookIntegrationConfig(srv.URL, "orders", config.WebhookAuthConfig{Type: "none"}, config.RetryPolicyConfig{MaxAttempts: 1})
		notify := cfg.Destinations["notify"]
		notify.Webhook.DeliveryMode = "sync"
		cfg.Destinations["notify"] = notify
		pub, err := NewFactory(FactoryOptions{Logger: testLogger()}).Create(context.Background(), cfg)
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		defer pub.Close()
		if err := pub.Publish(context.Background(), "notify", "orders", Message{ID: "e", ReceivedAt: time.Now()}); err == nil {
			t.Fatal("sync 503 must return an error to the caller")
		}
	})
	t.Run("200 returns nil and delivers before return", func(t *testing.T) {
		var hits int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&hits, 1)
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()
		cfg := webhookIntegrationConfig(srv.URL, "orders", config.WebhookAuthConfig{Type: "none"}, config.RetryPolicyConfig{})
		notify := cfg.Destinations["notify"]
		notify.Webhook.DeliveryMode = "sync"
		cfg.Destinations["notify"] = notify
		pub, err := NewFactory(FactoryOptions{Logger: testLogger()}).Create(context.Background(), cfg)
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		defer pub.Close()
		if err := pub.Publish(context.Background(), "notify", "orders", Message{ID: "e", ReceivedAt: time.Now()}); err != nil {
			t.Fatalf("sync 200 must return nil, got %v", err)
		}
		if atomic.LoadInt32(&hits) != 1 {
			t.Fatalf("sync mode must deliver before Publish returns; hits=%d", hits)
		}
	})
}

// parseTsDotSignature parses a "t=<ts>,v1=<hex>" header, mirroring what a
// downstream consumer would do.
func parseTsDotSignature(sig string) (ts int64, v1 string, ok bool) {
	for _, part := range strings.Split(sig, ",") {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			continue
		}
		switch kv[0] {
		case "t":
			n, err := strconv.ParseInt(kv[1], 10, 64)
			if err != nil {
				return 0, "", false
			}
			ts = n
		case "v1":
			v1 = kv[1]
		}
	}
	return ts, v1, ts != 0 && v1 != ""
}

// TestWebhookIntegrationGracefulDrainDeliversInFlight publishes a burst and then
// calls Close(); every enqueued delivery must complete during the drain.
func TestWebhookIntegrationGracefulDrainDeliversInFlight(t *testing.T) {
	const n = 50
	var delivered int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A small delay makes the drain do real work (deliveries still in flight
		// when Close is called).
		time.Sleep(2 * time.Millisecond)
		atomic.AddInt32(&delivered, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	factory := NewFactory(FactoryOptions{Logger: testLogger()})
	pub, err := factory.Create(context.Background(),
		webhookIntegrationConfig(srv.URL, "orders", config.WebhookAuthConfig{Type: "none"}, config.RetryPolicyConfig{}))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	for i := 0; i < n; i++ {
		msg := Message{ID: "evt_" + strconv.Itoa(i), ReceivedAt: time.Now()}
		if err := pub.Publish(context.Background(), "notify", "orders", msg); err != nil {
			t.Fatalf("Publish %d: %v", i, err)
		}
	}
	if err := pub.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if got := atomic.LoadInt32(&delivered); got != n {
		t.Fatalf("graceful drain delivered %d/%d messages", got, n)
	}
}

// TestWebhookIntegrationProxyPassthroughThroughFactory verifies proxy mode wired
// via the factory forwards the original raw body and vendor headers verbatim and
// adds no hook_way signature.
func TestWebhookIntegrationProxyPassthroughThroughFactory(t *testing.T) {
	var gotBody []byte
	var gotVendor, gotSig, gotCT string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		gotVendor = r.Header.Get("X-Vendor-Signature")
		gotSig = r.Header.Get("X-Hookway-Signature")
		gotCT = r.Header.Get("Content-Type")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	factory := NewFactory(FactoryOptions{Logger: testLogger()})
	// Empty target => deliver to the base URL (no path suffix).
	pub, err := factory.Create(context.Background(),
		webhookIntegrationConfig(srv.URL, "", config.WebhookAuthConfig{Type: "proxy"}, config.RetryPolicyConfig{}))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	orig := http.Header{}
	orig.Set("X-Vendor-Signature", "vendor-abc")
	orig.Set("Content-Type", "application/xml")
	msg := Message{ID: "evt_proxy", ReceivedAt: time.Now(), RawBody: []byte("<payload/>"), OrigHeaders: orig}
	if err := pub.Publish(context.Background(), "notify", "", msg); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	_ = pub.Close()

	if string(gotBody) != "<payload/>" {
		t.Fatalf("proxy body = %q, want <payload/>", gotBody)
	}
	if gotVendor != "vendor-abc" {
		t.Fatalf("vendor header not forwarded: %q", gotVendor)
	}
	if gotCT != "application/xml" {
		t.Fatalf("proxy Content-Type = %q, want application/xml", gotCT)
	}
	if gotSig != "" {
		t.Fatalf("proxy mode must not add a hook_way signature, got %q", gotSig)
	}
}
