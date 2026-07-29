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

func TestMetricsRequiresAPIKeyWhenConfigured(t *testing.T) {
	cfg := testConfig()
	cfg.Security.APIKey = "s3cret"
	srv := NewServer(cfg, &fakePublisher{}, slog.Default())

	// No credentials → 401.
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("/metrics without key = %d, want 401", rec.Code)
	}

	// Correct bearer → 200.
	req = httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.Header.Set("Authorization", "Bearer s3cret")
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("/metrics with key = %d, want 200", rec.Code)
	}

	// Health must stay open (handleLive always returns 200).
	req = httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("/healthz without key = %d, want 200 (health must stay open)", rec.Code)
	}
}

func TestMetricsAPIKeyOnDedicatedPort(t *testing.T) {
	cfg := testConfig()
	cfg.Security.APIKey = "s3cret"
	cfg.Metrics.Addr = ":0"
	srv := NewServer(cfg, &fakePublisher{}, slog.Default())
	ms := srv.MetricsServer()
	if ms == nil {
		t.Fatal("expected a dedicated metrics server")
	}

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	ms.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("dedicated /metrics without key = %d, want 401", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.Header.Set("Authorization", "Bearer s3cret")
	rec = httptest.NewRecorder()
	ms.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("dedicated /metrics with key = %d, want 200", rec.Code)
	}
}

func hmacSHA256HexSigned(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func hmacSHA256HexConfig() config.Config {
	cfg := testConfig()
	cfg.Endpoints["github_events"] = config.EndpointConfig{
		Enabled: true, Path: "/webhooks/github",
		Destinations: []config.EndpointDestinationConfig{{
			Destination: "activity", Target: "github-events",
		}},
		Auth: config.AuthConfig{
			Type: "hmac_sha256", Scheme: "hmac_sha256_hex", Secret: "ghsecret",
			SignatureHeader: "X-Hub-Signature-256", IdempotencyHeader: "X-GitHub-Delivery",
		},
	}
	return cfg
}

func TestHMACSHA256HexSchemeEndpointVerifiesAndUsesDeliveryIdempotencyKey(t *testing.T) {
	pub := &fakePublisher{}
	srv := NewServer(hmacSHA256HexConfig(), pub, slog.Default())
	body := []byte(`{"action":"opened"}`)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", hmacSHA256HexSigned("ghsecret", body))
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

func TestHMACSHA256HexSchemeRejectsBadSignature(t *testing.T) {
	srv := NewServer(hmacSHA256HexConfig(), &fakePublisher{}, slog.Default())
	body := []byte(`{"action":"opened"}`)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", hmacSHA256HexSigned("wrong", body))
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestIdempotencyKeyFallsBackToSignatureHashAndIsStable(t *testing.T) {
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

func TestSourceIPWalksForwardedForRightToLeftSkippingTrustedProxies(t *testing.T) {
	cfg := testConfig()
	cfg.Security.TrustedProxyHeaders = true
	cfg.Security.TrustedProxies = []string{"10.0.0.0/8"}
	srv := NewServer(cfg, &fakePublisher{}, slog.Default())

	newReq := func(remoteAddr, xff string) *http.Request {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = remoteAddr
		if xff != "" {
			req.Header.Set("X-Forwarded-For", xff)
		}
		return req
	}

	t.Run("spoofed leftmost entry ignored; real client from the right is used", func(t *testing.T) {
		// A single untrusted hop between the spoofed client-controlled entry
		// and the trusted immediate peer: the spoofed leftmost value must be
		// ignored since it is not the rightmost non-trusted-proxy entry...
		// but with only one entry it IS the rightmost, so use two entries
		// where the attacker prepends a fake allowlisted IP to the left of
		// their own real (rightmost) address.
		req := newReq("10.0.0.9:1234", "203.0.113.10, 198.51.100.77")
		if got := srv.sourceIP(req); got != "198.51.100.77" {
			t.Fatalf("sourceIP = %q, want rightmost entry 198.51.100.77 (leftmost spoofed entry must be ignored)", got)
		}
	})

	t.Run("chain of two trusted proxies resolves to real client", func(t *testing.T) {
		req := newReq("10.0.0.9:1234", "203.0.113.10, 10.0.0.5")
		if got := srv.sourceIP(req); got != "203.0.113.10" {
			t.Fatalf("sourceIP = %q, want real client 203.0.113.10 after skipping trusted proxy hop 10.0.0.5", got)
		}
	})

	t.Run("garbage entry stops the walk and uses the last good rightmost hop", func(t *testing.T) {
		req := newReq("10.0.0.9:1234", "not-an-ip, 10.0.0.5")
		if got := srv.sourceIP(req); got != "10.0.0.5" {
			t.Fatalf("sourceIP = %q, want last good hop 10.0.0.5 (never continue toward the client-controlled left end past a malformed entry)", got)
		}
	})

	t.Run("all entries are trusted proxies falls back to leftmost", func(t *testing.T) {
		req := newReq("10.0.0.9:1234", "10.0.0.1, 10.0.0.2")
		if got := srv.sourceIP(req); got != "10.0.0.1" {
			t.Fatalf("sourceIP = %q, want leftmost entry 10.0.0.1 when every hop is a trusted proxy", got)
		}
	})

	t.Run("untrusted peer ignores XFF entirely", func(t *testing.T) {
		req := newReq("203.0.113.200:1234", "203.0.113.10, 10.0.0.5")
		if got := srv.sourceIP(req); got != "203.0.113.200" {
			t.Fatalf("sourceIP = %q, want peer address 203.0.113.200 since the peer is not a trusted proxy", got)
		}
	})
}

func TestSourceIPUsesRightmostEntryWhenTrustedProxiesNotConfigured(t *testing.T) {
	// trusted_proxy_headers on with no trusted_proxies configured trusts any
	// peer (legacy permissive mode). There are no "trusted proxy" addresses
	// to recognize and skip in the XFF chain, so the walker returns the
	// rightmost entry immediately.
	cfg := testConfig()
	cfg.Security.TrustedProxyHeaders = true
	srv := NewServer(cfg, &fakePublisher{}, slog.Default())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "203.0.113.200:1234"
	req.Header.Set("X-Forwarded-For", "203.0.113.10, 198.51.100.77")

	if got := srv.sourceIP(req); got != "198.51.100.77" {
		t.Fatalf("sourceIP = %q, want rightmost entry 198.51.100.77", got)
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

func revenueCatConfig() config.Config {
	cfg := testConfig()
	cfg.Endpoints = map[string]config.EndpointConfig{
		"revenuecat_billing": {
			Enabled: true,
			Path:    "/webhooks/revenuecat",
			Destinations: []config.EndpointDestinationConfig{{
				Destination: "billing", Target: "revenuecat-events",
			}},
			ReplayProtection: config.ReplayProtectionConfig{
				Enabled: true, TTL: 10 * time.Minute, MaxEntries: 1000,
			},
			Auth: config.AuthConfig{
				Type: "header_exact", Scheme: "exact", Secret: "rc-token",
				SignatureHeader: "Authorization", RemoveBearer: true,
			},
		},
	}
	return cfg
}

func TestHeaderExactAuthAcceptsMatchingAuthorization(t *testing.T) {
	pub := &fakePublisher{}
	srv := NewServer(revenueCatConfig(), pub, slog.Default())
	body := []byte(`{"id":"evt_1","type":"INITIAL_PURCHASE"}`)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/revenuecat", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer rc-token")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
}

func TestHeaderExactAuthRejectsWrongToken(t *testing.T) {
	srv := NewServer(revenueCatConfig(), &fakePublisher{}, slog.Default())
	body := []byte(`{"id":"evt_1"}`)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/revenuecat", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer wrong")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestHeaderExactReplayProtectionUsesBodyHashNotStaticAuthHeader(t *testing.T) {
	pub := &fakePublisher{}
	srv := NewServer(revenueCatConfig(), pub, slog.Default())
	authHeader := "Bearer rc-token"

	send := func(body []byte) int {
		req := httptest.NewRequest(http.MethodPost, "/webhooks/revenuecat", bytes.NewReader(body))
		req.Header.Set("Authorization", authHeader)
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		return rec.Code
	}

	if code := send([]byte(`{"id":"evt_1"}`)); code != http.StatusAccepted {
		t.Fatalf("first event status = %d", code)
	}
	if code := send([]byte(`{"id":"evt_2"}`)); code != http.StatusAccepted {
		t.Fatalf("second distinct event status = %d, replay protection likely keyed on static auth header", code)
	}
	if code := send([]byte(`{"id":"evt_1"}`)); code != http.StatusConflict {
		t.Fatalf("replay status = %d, want 409", code)
	}
}
