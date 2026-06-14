package httpapi

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jasongiroux/hookway/internal/config"
	"github.com/jasongiroux/hookway/internal/queue"
)

var errInjected = errors.New("injected publish failure")

type blockingPublisher struct {
	entered chan struct{}
	release chan struct{}
}

func (b *blockingPublisher) Publish(ctx context.Context, destinationID, q string, msg queue.Message) error {
	b.entered <- struct{}{}
	<-b.release
	return nil
}
func (b *blockingPublisher) Ready(ctx context.Context) error { return nil }
func (b *blockingPublisher) Close() error                    { return nil }

func TestMaxInflightReturnsServiceUnavailableWhenSaturated(t *testing.T) {
	cfg := testConfig()
	cfg.Server.MaxInflightRequests = 1
	pub := &blockingPublisher{entered: make(chan struct{}), release: make(chan struct{})}
	srv := NewServer(cfg, pub, slog.Default())
	body := []byte(`{"ok":true}`)

	// First request occupies the only slot and blocks inside Publish.
	go func() {
		req := httptest.NewRequest(http.MethodPost, "/webhooks/paddle/billing", bytes.NewReader(body))
		req.Header.Set("Paddle-Signature", signed("secret", time.Now(), body))
		srv.ServeHTTP(httptest.NewRecorder(), req)
	}()
	<-pub.entered

	// Second request finds the limit reached and is shed with 503 + Retry-After.
	req := httptest.NewRequest(http.MethodPost, "/webhooks/paddle/billing", bytes.NewReader(body))
	req.Header.Set("Paddle-Signature", signed("secret", time.Now(), body))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("saturated status = %d, want 503", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("expected Retry-After header when shedding load")
	}
	close(pub.release)
}

func TestMetricsServedOnSeparatePortWhenAddrSet(t *testing.T) {
	cfg := testConfig()
	cfg.Metrics.Addr = ":0"
	srv := NewServer(cfg, &fakePublisher{}, slog.Default())

	// Not on the public mux.
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("public /metrics status = %d, want 404 when on a separate port", rec.Code)
	}

	// Available on the dedicated metrics server.
	ms := srv.MetricsServer()
	if ms == nil {
		t.Fatal("expected a dedicated metrics server")
	}
	req = httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec = httptest.NewRecorder()
	ms.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("dedicated /metrics status = %d, want 200", rec.Code)
	}
}

func githubSigned(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func githubConfig() config.Config {
	cfg := testConfig()
	cfg.Endpoints["github_events"] = config.EndpointConfig{
		Enabled: true, Path: "/webhooks/github",
		Destinations: []config.EndpointDestinationConfig{{
			DestinationQueueID: "activity", Queue: "github-events",
		}},
		Auth: config.AuthConfig{
			Type: "hmac_sha256", Scheme: "github", Secret: "ghsecret",
			SignatureHeader: "X-Hub-Signature-256", IdempotencyHeader: "X-GitHub-Delivery",
		},
	}
	return cfg
}

func TestGitHubSchemeEndpointVerifiesAndUsesDeliveryIdempotencyKey(t *testing.T) {
	pub := &fakePublisher{}
	srv := NewServer(githubConfig(), pub, slog.Default())
	body := []byte(`{"action":"opened"}`)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", githubSigned("ghsecret", body))
	req.Header.Set("X-GitHub-Delivery", "delivery-123")
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if len(pub.published) != 1 {
		t.Fatalf("published = %#v, want 1 publish", pub.published)
	}
	if pub.published[0].msg.IdempotencyKey != "delivery-123" {
		t.Fatalf("idempotency key = %q, want delivery header value", pub.published[0].msg.IdempotencyKey)
	}
}

func TestGitHubSchemeRejectsBadSignature(t *testing.T) {
	srv := NewServer(githubConfig(), &fakePublisher{}, slog.Default())
	body := []byte(`{"action":"opened"}`)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", githubSigned("wrong", body))
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestPaddleIdempotencyKeyFallsBackToSignatureHashAndIsStable(t *testing.T) {
	pub := &fakePublisher{}
	srv := NewServer(testConfig(), pub, slog.Default())
	body := []byte(`{"ok":true}`)
	sig := signed("secret", time.Now(), body)

	send := func() string {
		req := httptest.NewRequest(http.MethodPost, "/webhooks/paddle/billing", bytes.NewReader(body))
		req.Header.Set("Paddle-Signature", sig)
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != http.StatusAccepted {
			t.Fatalf("status = %d", rec.Code)
		}
		if len(pub.published) == 0 {
			t.Fatal("expected publish")
		}
		return pub.published[len(pub.published)-1].msg.IdempotencyKey
	}

	first := send()
	second := send()
	if first == "" {
		t.Fatal("expected a fallback idempotency key")
	}
	if first != second {
		t.Fatalf("idempotency key not stable across retries: %q vs %q", first, second)
	}
}

func TestSpoofedForwardedHeaderIgnoredUnlessPeerIsTrustedProxy(t *testing.T) {
	body := []byte(`{"ok":true}`)
	cfg := testConfig()
	endpoint := cfg.Endpoints["paddle_billing"]
	endpoint.IPAllowlist = []string{"203.0.113.10"}
	cfg.Endpoints["paddle_billing"] = endpoint
	cfg.Security.TrustedProxyHeaders = true

	// Peer is not a configured trusted proxy: the spoofed XFF must be ignored,
	// so the real (disallowed) peer IP is used and the request is forbidden.
	cfg.Security.TrustedProxies = []string{"10.0.0.0/8"}
	srv := NewServer(cfg, &fakePublisher{}, slog.Default())
	req := httptest.NewRequest(http.MethodPost, "/webhooks/paddle/billing", bytes.NewReader(body))
	req.RemoteAddr = "198.51.100.5:1234"
	req.Header.Set("X-Forwarded-For", "203.0.113.10")
	req.Header.Set("Paddle-Signature", signed("secret", time.Now(), body))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("spoofed XFF should be ignored: status = %d, want 403", rec.Code)
	}

	// Peer is a trusted proxy: the forwarded client IP is honoured and allowed.
	req = httptest.NewRequest(http.MethodPost, "/webhooks/paddle/billing", bytes.NewReader(body))
	req.RemoteAddr = "10.1.2.3:1234"
	req.Header.Set("X-Forwarded-For", "203.0.113.10")
	req.Header.Set("Paddle-Signature", signed("secret", time.Now(), body))
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("trusted proxy forwarding should be honoured: status = %d, want 202", rec.Code)
	}
}

func TestPublishFailureDoesNotPoisonReplayCacheForRetry(t *testing.T) {
	cfg := testConfig()
	endpoint := cfg.Endpoints["paddle_billing"]
	endpoint.ReplayProtection = config.ReplayProtectionConfig{Enabled: true, TTL: time.Minute, MaxEntries: 10}
	cfg.Endpoints["paddle_billing"] = endpoint

	pub := &fakePublisher{err: errInjected}
	srv := NewServer(cfg, pub, slog.Default())
	body := []byte(`{"ok":true}`)
	sig := signed("secret", time.Now(), body)

	do := func() int {
		req := httptest.NewRequest(http.MethodPost, "/webhooks/paddle/billing", bytes.NewReader(body))
		req.RemoteAddr = "203.0.113.50:1234"
		req.Header.Set("Paddle-Signature", sig)
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		return rec.Code
	}

	// First attempt: publish fails -> 503, reservation released.
	if code := do(); code != http.StatusServiceUnavailable {
		t.Fatalf("first attempt status = %d, want 503", code)
	}
	// Sender retries the same signature; now the broker is healthy. It must be
	// accepted, not rejected as a replay.
	pub.err = nil
	if code := do(); code != http.StatusAccepted {
		t.Fatalf("retry after transient failure status = %d, want 202", code)
	}
}

func TestForwardedHeaderAllowlistDropsUnlistedHeaders(t *testing.T) {
	pub := &fakePublisher{}
	cfg := testConfig()
	cfg.Security.ForwardedHeaderAllowlist = []string{"Content-Type"}
	srv := NewServer(cfg, pub, slog.Default())
	body := []byte(`{"ok":true}`)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/paddle/billing", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Secret-Internal", "leak-me")
	req.Header.Set("Paddle-Signature", signed("secret", time.Now(), body))
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d", rec.Code)
	}
	if len(pub.published) != 1 {
		t.Fatalf("published = %#v, want 1 publish", pub.published)
	}
	if _, ok := pub.published[0].msg.Headers["x-secret-internal"]; ok {
		t.Fatalf("non-allowlisted header forwarded to queue: %#v", pub.published[0].msg.Headers)
	}
	if _, ok := pub.published[0].msg.Headers["content-type"]; !ok {
		t.Fatalf("allowlisted header missing from envelope: %#v", pub.published[0].msg.Headers)
	}
}
