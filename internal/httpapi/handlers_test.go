package httpapi

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasongiroux/hookway/internal/config"
	"github.com/jasongiroux/hookway/internal/queue"
)

func TestWebhookSuccessReturnsAccepted(t *testing.T) {
	pub := &fakePublisher{}
	srv := NewServer(testConfig(), pub, slog.Default())
	body := []byte(`{"event_type":"subscription.created"}`)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/paddle/billing", bytes.NewReader(body))
	req.Header.Set("Paddle-Signature", signed("secret", time.Now(), body))
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if len(pub.published) != 1 {
		t.Fatalf("published = %#v, want 1 publish", pub.published)
	}
	if pub.published[0].destinationID != "billing" {
		t.Fatalf("destination = %q", pub.published[0].destinationID)
	}
	if pub.published[0].msg.DestinationQueueID != "billing" {
		t.Fatalf("message destination = %q", pub.published[0].msg.DestinationQueueID)
	}
	var res struct {
		Status       string `json:"status"`
		Endpoint     string `json:"endpoint"`
		EventID      string `json:"event_id"`
		Destinations []struct {
			DestinationQueueID string `json:"destination_queue_id"`
			Queue              string `json:"queue"`
		} `json:"destinations"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.Status != "accepted" || res.Endpoint != "paddle_billing" || res.EventID == "" {
		t.Fatalf("bad response: %#v", res)
	}
	if len(res.Destinations) != 1 || res.Destinations[0].DestinationQueueID != "billing" || res.Destinations[0].Queue != "billing-events" {
		t.Fatalf("bad response: %#v", res)
	}
}

func TestWebhookRoutesToEndpointDestination(t *testing.T) {
	cfg := testConfig()
	cfg.Endpoints["github_events"] = config.EndpointConfig{
		Enabled: true, Path: "/webhooks/github",
		Destinations: []config.EndpointDestinationConfig{{
			DestinationQueueID: "activity", Queue: "github-events",
		}},
		Auth: config.AuthConfig{Type: "hmac_sha256", Scheme: "github", Secret: "secret", SignatureHeader: "X-Hub-Signature-256"},
	}
	pub := &fakePublisher{}
	srv := NewServer(cfg, pub, slog.Default())
	body := []byte(`{"zen":"keep it logically awesome"}`)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", githubSigned("secret", body))
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if len(pub.published) != 1 {
		t.Fatalf("published = %#v, want 1 publish", pub.published)
	}
	if pub.published[0].destinationID != "activity" || pub.published[0].queue != "github-events" {
		t.Fatalf("publish target = %q/%q", pub.published[0].destinationID, pub.published[0].queue)
	}
}

func TestWebhookFansOutToEndpointDestinations(t *testing.T) {
	cfg := testConfig()
	endpoint := cfg.Endpoints["paddle_billing"]
	endpoint.Destinations = []config.EndpointDestinationConfig{
		{DestinationQueueID: "billing", Queue: "billing-events"},
		{DestinationQueueID: "activity", Queue: "activity-events"},
	}
	cfg.Endpoints["paddle_billing"] = endpoint
	pub := &fakePublisher{}
	srv := NewServer(cfg, pub, slog.Default())
	body := []byte(`{"event_type":"subscription.created"}`)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/paddle/billing", bytes.NewReader(body))
	req.Header.Set("Paddle-Signature", signed("secret", time.Now(), body))
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if len(pub.published) != 2 {
		t.Fatalf("published = %#v", pub.published)
	}
	byDestination := map[string]publishedMessage{}
	for _, message := range pub.published {
		byDestination[message.destinationID] = message
	}
	if byDestination["billing"].queue != "billing-events" || byDestination["billing"].msg.Queue != "billing-events" {
		t.Fatalf("billing publish = %#v", byDestination["billing"])
	}
	if byDestination["activity"].queue != "activity-events" || byDestination["activity"].msg.Queue != "activity-events" {
		t.Fatalf("activity publish = %#v", byDestination["activity"])
	}
	var res struct {
		Status       string `json:"status"`
		Endpoint     string `json:"endpoint"`
		Destinations []struct {
			DestinationQueueID string `json:"destination_queue_id"`
			Queue              string `json:"queue"`
		} `json:"destinations"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Destinations) != 2 || res.Destinations[1].DestinationQueueID != "activity" || res.Destinations[1].Queue != "activity-events" {
		t.Fatalf("bad response: %#v", res)
	}
}

func TestWebhookFanoutFailureReturnsUnavailableAndAllowsRetry(t *testing.T) {
	cfg := testConfig()
	endpoint := cfg.Endpoints["paddle_billing"]
	endpoint.Destinations = []config.EndpointDestinationConfig{
		{DestinationQueueID: "billing", Queue: "billing-events"},
		{DestinationQueueID: "activity", Queue: "activity-events"},
	}
	endpoint.ReplayProtection = config.ReplayProtectionConfig{Enabled: true, TTL: time.Minute, MaxEntries: 10}
	cfg.Endpoints["paddle_billing"] = endpoint
	pub := &fakePublisher{err: errors.New("activity down"), errDestination: "activity"}
	srv := NewServer(cfg, pub, slog.Default())
	body := []byte(`true`)
	sig := signed("secret", time.Now(), body)

	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/webhooks/paddle/billing", bytes.NewReader(body))
		req.Header.Set("Paddle-Signature", sig)
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("request %d status = %d body = %s", i, rec.Code, rec.Body.String())
		}
	}
	if len(pub.published) != 4 {
		t.Fatalf("published = %#v, want 4 attempts", pub.published)
	}
	successes, failures := 0, 0
	for _, published := range pub.published {
		switch published.destinationID {
		case "billing":
			if published.err != nil {
				t.Fatalf("billing publish failed: %#v", published)
			}
			successes++
		case "activity":
			if !errors.Is(published.err, pub.err) {
				t.Fatalf("activity publish = %#v", published)
			}
			failures++
		default:
			t.Fatalf("unexpected publish = %#v", published)
		}
	}
	if successes != 2 || failures != 2 {
		t.Fatalf("successes=%d failures=%d published=%#v", successes, failures, pub.published)
	}
}

func TestWebhookFanoutHonorsConcurrencyLimit(t *testing.T) {
	cfg := testConfig()
	cfg.Server.FanoutConcurrency = 1
	endpoint := cfg.Endpoints["paddle_billing"]
	endpoint.Destinations = []config.EndpointDestinationConfig{
		{DestinationQueueID: "billing", Queue: "billing-events"},
		{DestinationQueueID: "activity", Queue: "activity-events"},
	}
	cfg.Endpoints["paddle_billing"] = endpoint
	pub := &fakePublisher{delay: 10 * time.Millisecond}
	srv := NewServer(cfg, pub, slog.Default())
	body := []byte(`{"event_type":"subscription.created"}`)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/paddle/billing", bytes.NewReader(body))
	req.Header.Set("Paddle-Signature", signed("secret", time.Now(), body))
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if pub.maxConcurrent != 1 {
		t.Fatalf("max concurrent publishes = %d, want 1", pub.maxConcurrent)
	}
}

func TestRequestIDPropagatesToResponseEnvelopeAndLogs(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	pub := &fakePublisher{}
	srv := NewServer(testConfig(), pub, logger)
	body := []byte(`true`)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/paddle/billing", bytes.NewReader(body))
	req.Header.Set("X-Request-ID", "req_abc")
	req.Header.Set("Traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-00")
	req.Header.Set("Paddle-Signature", signed("secret", time.Now(), body))
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Header().Get("X-Request-ID") != "req_abc" {
		t.Fatalf("response request id = %q", rec.Header().Get("X-Request-ID"))
	}
	if len(pub.published) != 1 {
		t.Fatalf("published = %#v, want 1 publish", pub.published)
	}
	if pub.published[0].msg.RequestID != "req_abc" {
		t.Fatalf("published request id = %q", pub.published[0].msg.RequestID)
	}
	if pub.published[0].msg.TraceID != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Fatalf("published trace id = %q", pub.published[0].msg.TraceID)
	}
	if !strings.Contains(logs.String(), `"request_id":"req_abc"`) || !strings.Contains(logs.String(), `"trace_id":"4bf92f3577b34da6a3ce929d0e0e4736"`) {
		t.Fatalf("logs missing ids: %s", logs.String())
	}
}

func TestHealthEndpointUsesReadinessButPublicErrorIsGeneric(t *testing.T) {
	var logs bytes.Buffer
	pub := &fakePublisher{readyErr: errors.New("rabbitmq dial tcp secret-url failed")}
	srv := NewServer(testConfig(), pub, slog.New(slog.NewJSONHandler(&logs, nil)))
	for _, path := range []string{"/health", "/ready", "/readyz"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s status = %d", path, rec.Code)
		}
		if strings.Contains(rec.Body.String(), "rabbitmq") || !strings.Contains(rec.Body.String(), "not ready") {
			t.Fatalf("%s leaked readiness detail: %s", path, rec.Body.String())
		}
	}
	if !strings.Contains(logs.String(), "rabbitmq dial tcp") {
		t.Fatalf("internal readiness reason not logged: %s", logs.String())
	}
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("/healthz status = %d, want liveness OK", rec.Code)
	}
}

func TestEndpointBodyLimitRateLimitIPAllowlistAndReplay(t *testing.T) {
	cfg := testConfig()
	endpoint := cfg.Endpoints["paddle_billing"]
	endpoint.MaxBodyBytes = 4
	endpoint.IPAllowlist = []string{"203.0.113.0/24"}
	rateEnabled := true
	endpoint.RateLimit = config.EndpointRateLimitConfig{Enabled: &rateEnabled, RequestsPerMinute: 1, Burst: 1, KeyBy: "ip"}
	endpoint.ReplayProtection = config.ReplayProtectionConfig{Enabled: true, TTL: time.Minute, MaxEntries: 10}
	cfg.Endpoints["paddle_billing"] = endpoint
	srv := NewServer(cfg, &fakePublisher{}, slog.Default())

	req := httptest.NewRequest(http.MethodPost, "/webhooks/paddle/billing", bytes.NewReader([]byte("12345")))
	req.RemoteAddr = "203.0.113.9:1234"
	req.Header.Set("Paddle-Signature", signed("secret", time.Now(), []byte("12345")))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("body limit status = %d", rec.Code)
	}

	body := []byte(`true`)
	req = httptest.NewRequest(http.MethodPost, "/webhooks/paddle/billing", bytes.NewReader(body))
	req.RemoteAddr = "198.51.100.1:1234"
	req.Header.Set("Paddle-Signature", signed("secret", time.Now(), body))
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("allowlist status = %d", rec.Code)
	}

	sig := signed("secret", time.Now(), body)
	for i, want := range []int{http.StatusAccepted, http.StatusTooManyRequests} {
		req = httptest.NewRequest(http.MethodPost, "/webhooks/paddle/billing", bytes.NewReader(body))
		req.RemoteAddr = "203.0.113.10:1234"
		req.Header.Set("Paddle-Signature", sig)
		rec = httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Fatalf("rate request %d status = %d want %d", i, rec.Code, want)
		}
	}

	cfg = testConfig()
	endpoint = cfg.Endpoints["paddle_billing"]
	endpoint.ReplayProtection = config.ReplayProtectionConfig{Enabled: true, TTL: time.Minute, MaxEntries: 10}
	cfg.Endpoints["paddle_billing"] = endpoint
	srv = NewServer(cfg, &fakePublisher{}, slog.Default())
	sig = signed("secret", time.Now(), body)
	for i, want := range []int{http.StatusAccepted, http.StatusConflict} {
		req = httptest.NewRequest(http.MethodPost, "/webhooks/paddle/billing", bytes.NewReader(body))
		req.RemoteAddr = "203.0.113.11:1234"
		req.Header.Set("Paddle-Signature", sig)
		rec = httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Fatalf("replay request %d status = %d want %d", i, rec.Code, want)
		}
	}
}

func TestWebhookStatusCodes(t *testing.T) {
	body := []byte(`{"ok":true}`)
	tests := map[string]struct {
		method string
		path   string
		header string
		body   []byte
		pubErr error
		want   int
	}{
		"invalid method":    {http.MethodGet, "/webhooks/paddle/billing", "", body, nil, http.StatusMethodNotAllowed},
		"unknown endpoint":  {http.MethodPost, "/unknown/billing-events", signed("secret", time.Now(), body), body, nil, http.StatusNotFound},
		"invalid signature": {http.MethodPost, "/webhooks/paddle/billing", "bad", body, nil, http.StatusUnauthorized},
		"publish failure":   {http.MethodPost, "/webhooks/paddle/billing", signed("secret", time.Now(), body), body, errors.New("down"), http.StatusServiceUnavailable},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			pub := &fakePublisher{err: tt.pubErr}
			srv := NewServer(testConfig(), pub, slog.Default())
			req := httptest.NewRequest(tt.method, tt.path, bytes.NewReader(tt.body))
			req.Header.Set("Paddle-Signature", tt.header)
			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, req)
			if rec.Code != tt.want {
				t.Fatalf("status = %d want %d body = %s", rec.Code, tt.want, rec.Body.String())
			}
		})
	}
}

func TestWebhookRejectsOversizedAndRateLimited(t *testing.T) {
	cfg := testConfig()
	cfg.Server.MaxBodyBytes = 4
	srv := NewServer(cfg, &fakePublisher{}, slog.Default())
	req := httptest.NewRequest(http.MethodPost, "/webhooks/paddle/billing", bytes.NewReader([]byte("12345")))
	req.Header.Set("Paddle-Signature", signed("secret", time.Now(), []byte("12345")))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d", rec.Code)
	}

	cfg = testConfig()
	cfg.RateLimit.Enabled = true
	cfg.RateLimit.RequestsPerMinute = 1
	cfg.RateLimit.Burst = 1
	srv = NewServer(cfg, &fakePublisher{}, slog.Default())
	body := []byte(`{"ok":true}`)
	for i, want := range []int{http.StatusAccepted, http.StatusTooManyRequests} {
		req := httptest.NewRequest(http.MethodPost, "/webhooks/paddle/billing", bytes.NewReader(body))
		req.RemoteAddr = "192.0.2.1:1234"
		req.Header.Set("Paddle-Signature", signed("secret", time.Now(), body))
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Fatalf("request %d status = %d want %d", i, rec.Code, want)
		}
	}
}

func TestHealthReadyAndDisabledMetrics(t *testing.T) {
	cfg := testConfig()
	cfg.Metrics.Enabled = false
	srv := NewServer(cfg, &fakePublisher{}, slog.Default())
	for path, want := range map[string]int{"/health": 200, "/healthz": 200, "/ready": 200, "/readyz": 200, "/metrics": 404} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Fatalf("%s status = %d want %d", path, rec.Code, want)
		}
	}
}

type publishedMessage struct {
	destinationID string
	queue         string
	msg           queue.Message
	err           error
}

type fakePublisher struct {
	err            error
	errDestination string
	readyErr       error
	delay          time.Duration
	mu             sync.Mutex
	active         int
	maxConcurrent  int
	published      []publishedMessage
}

func (f *fakePublisher) Publish(ctx context.Context, destinationID, q string, msg queue.Message) error {
	f.mu.Lock()
	f.active++
	if f.active > f.maxConcurrent {
		f.maxConcurrent = f.active
	}
	f.mu.Unlock()
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	defer func() {
		f.mu.Lock()
		f.active--
		f.mu.Unlock()
	}()
	if f.err != nil && (f.errDestination == "" || f.errDestination == destinationID) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.published = append(f.published, publishedMessage{destinationID: destinationID, queue: q, msg: msg, err: f.err})
		return f.err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.published = append(f.published, publishedMessage{destinationID: destinationID, queue: q, msg: msg})
	return nil
}
func (f *fakePublisher) Ready(ctx context.Context) error { return f.readyErr }
func (f *fakePublisher) Close() error                    { return nil }

func testConfig() config.Config {
	return config.Config{
		Server:    config.ServerConfig{Addr: ":0", MaxBodyBytes: 1024},
		Security:  config.SecurityConfig{RequestIDHeader: "X-Request-ID"},
		RateLimit: config.RateLimitConfig{Enabled: false, RequestsPerMinute: 120, Burst: 20, KeyBy: "ip"},
		Metrics:   config.MetricsConfig{Enabled: true, Path: "/metrics"},
		MessageQueues: map[string]config.MessageQueueConfig{
			"billing":  {Type: "rabbitmq", RabbitMQ: config.RabbitMQConfig{URL: "amqp://guest:guest@localhost:5672/", Exchange: "hookway.billing", ExchangeType: "direct", PublishTimeout: time.Second}},
			"activity": {Type: "rabbitmq", RabbitMQ: config.RabbitMQConfig{URL: "amqp://guest:guest@localhost:5673/", Exchange: "hookway.activity", ExchangeType: "direct", PublishTimeout: time.Second}},
		},
		Endpoints: map[string]config.EndpointConfig{
			"paddle_billing": {
				Enabled: true, Path: "/webhooks/paddle/billing",
				Destinations: []config.EndpointDestinationConfig{{
					DestinationQueueID: "billing", Queue: "billing-events",
				}},
				Auth: config.AuthConfig{Type: "hmac_sha256", Secret: "secret", SignatureHeader: "Paddle-Signature", TimestampTolerance: 5 * time.Minute},
			},
		},
	}
}

func signed(secret string, ts time.Time, body []byte) string {
	payload := fmt.Sprintf("%d:%s", ts.Unix(), body)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	return fmt.Sprintf("ts=%d;h1=%s", ts.Unix(), hex.EncodeToString(mac.Sum(nil)))
}
