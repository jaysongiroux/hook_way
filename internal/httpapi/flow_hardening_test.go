package httpapi

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jasongiroux/hookway/internal/config"
	"github.com/jasongiroux/hookway/internal/metrics"
)

func githubPOST(body []byte) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Hub-Signature-256", githubHMAC(body))
	return req
}

func TestRequestIDPropagatesToResponseEnvelopeAndLogs(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	pub := &fakePublisher{}
	srv, err := NewFlowServer(loadFlowCfg(t, testFlowYAML), pub, logger, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	body := []byte(`{"ok":true}`)
	req := githubPOST(body)
	req.Header.Set("X-Request-ID", "req_abc")
	req.Header.Set("Traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-00")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Header().Get("X-Request-ID") != "req_abc" {
		t.Fatalf("response request id = %q", rec.Header().Get("X-Request-ID"))
	}
	if len(pub.published) != 1 {
		t.Fatalf("published = %#v, want 1", pub.published)
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

func TestInboundTraceparentPropagatesByteIdenticalToPublishedMessage(t *testing.T) {
	pub := &fakePublisher{}
	srv := newTestFlowServer(t, pub)
	body := []byte(`{"ok":true}`)
	req := githubPOST(body)
	want := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	req.Header.Set("Traceparent", want)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if len(pub.published) != 1 {
		t.Fatalf("published = %#v, want 1", pub.published)
	}
	if pub.published[0].msg.Traceparent != want {
		t.Fatalf("published traceparent = %q, want %q", pub.published[0].msg.Traceparent, want)
	}
}

func TestAbsentInboundTraceparentSynthesizesValidOneMatchingTraceID(t *testing.T) {
	pub := &fakePublisher{}
	srv := newTestFlowServer(t, pub)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, githubPOST([]byte(`{"ok":true}`)))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if len(pub.published) != 1 {
		t.Fatalf("published = %#v, want 1", pub.published)
	}
	msg := pub.published[0].msg
	parts := strings.Split(msg.Traceparent, "-")
	if len(parts) != 4 || parts[0] != "00" || len(parts[1]) != 32 || len(parts[2]) != 16 || parts[3] != "01" {
		t.Fatalf("synthesized traceparent %q is not syntactically valid", msg.Traceparent)
	}
	if parts[1] != msg.TraceID {
		t.Fatalf("synthesized traceparent trace-id %q != envelope trace id %q", parts[1], msg.TraceID)
	}
}

func TestMalformedInboundTraceparentIsSynthesizedNotPropagated(t *testing.T) {
	pub := &fakePublisher{}
	srv := newTestFlowServer(t, pub)
	req := githubPOST([]byte(`{"ok":true}`))
	garbage := "not-a-traceparent"
	req.Header.Set("Traceparent", garbage)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if len(pub.published) != 1 {
		t.Fatalf("published = %#v, want 1", pub.published)
	}
	msg := pub.published[0].msg
	if msg.Traceparent == garbage {
		t.Fatal("malformed inbound traceparent must not be propagated verbatim")
	}
	parts := strings.Split(msg.Traceparent, "-")
	if len(parts) != 4 || len(parts[1]) != 32 || len(parts[2]) != 16 {
		t.Fatalf("synthesized traceparent %q is not syntactically valid", msg.Traceparent)
	}
}

func TestNonHexInboundTraceIDIsNotAdoptedForSynthesizedTraceparent(t *testing.T) {
	pub := &fakePublisher{}
	srv := newTestFlowServer(t, pub)
	req := githubPOST([]byte(`{"ok":true}`))
	nonHexTraceID := strings.Repeat("g", 32)
	req.Header.Set("Traceparent", "00-"+nonHexTraceID+"-00f067aa0ba902b7-01")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if len(pub.published) != 1 {
		t.Fatalf("published = %#v, want 1", pub.published)
	}
	msg := pub.published[0].msg
	parts := strings.Split(msg.Traceparent, "-")
	if len(parts) != 4 || len(parts[1]) != 32 {
		t.Fatalf("synthesized traceparent %q is not syntactically valid", msg.Traceparent)
	}
	if !isHex(parts[1]) {
		t.Fatalf("synthesized traceparent trace-id %q is not valid hex", parts[1])
	}
	if parts[1] == nonHexTraceID {
		t.Fatal("synthesized traceparent must not adopt the non-hex inbound trace id verbatim")
	}
	if parts[1] != msg.TraceID {
		t.Fatalf("synthesized traceparent trace-id %q != envelope trace id %q", parts[1], msg.TraceID)
	}
}

func TestMaxInflightReturnsServiceUnavailableWhenSaturated(t *testing.T) {
	cfg := loadFlowCfg(t, testFlowYAML)
	cfg.Server.MaxInflightRequests = 1
	pub := &blockingPublisher{entered: make(chan struct{}), release: make(chan struct{})}
	srv, err := NewFlowServer(cfg, pub, slog.Default(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	body := []byte(`{"ok":true}`)
	go func() {
		srv.ServeHTTP(httptest.NewRecorder(), githubPOST(body))
	}()
	<-pub.entered

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, githubPOST(body))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("saturated status = %d, want 503", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("expected Retry-After header when shedding load")
	}
	close(pub.release)
}

func TestMetricsServedOnSeparatePortWhenAddrSet(t *testing.T) {
	cfg := loadFlowCfg(t, testFlowYAML)
	cfg.Metrics.Enabled = true
	cfg.Metrics.Path = "/metrics"
	cfg.Metrics.Addr = ":0"
	srv, err := NewFlowServer(cfg, &fakePublisher{}, slog.Default(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("public /metrics status = %d, want 404 when on a separate port", rec.Code)
	}
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
	cfg := loadFlowCfg(t, testFlowYAML)
	cfg.Metrics.Enabled = true
	cfg.Metrics.Path = "/metrics"
	cfg.Security.APIKey = "s3cret"
	srv, err := NewFlowServer(cfg, &fakePublisher{}, slog.Default(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("/metrics without key = %d, want 401", rec.Code)
	}
	req = httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.Header.Set("Authorization", "Bearer s3cret")
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("/metrics with key = %d, want 200", rec.Code)
	}
	req = httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("/healthz without key = %d, want 200", rec.Code)
	}
}

func TestMetricsAPIKeyOnDedicatedPort(t *testing.T) {
	cfg := loadFlowCfg(t, testFlowYAML)
	cfg.Metrics.Enabled = true
	cfg.Metrics.Path = "/metrics"
	cfg.Metrics.Addr = ":0"
	cfg.Security.APIKey = "s3cret"
	srv, err := NewFlowServer(cfg, &fakePublisher{}, slog.Default(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
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

func TestSpoofedForwardedHeaderIgnoredUnlessPeerIsTrustedProxy(t *testing.T) {
	src := `
server:
  addr: ":8080"
  max_body_bytes: 1024
security:
  trusted_proxy_headers: true
  trusted_proxies: ["10.0.0.0/8"]
destinations:
  primary:
    type: webhook
    webhook: {url: "https://example.test"}
flows:
  github:
    nodes:
      - {id: in, type: http_trigger, path: /webhooks/github, methods: [POST]}
      - {id: ip, type: ip_allowlist, cidrs: ["203.0.113.10"]}
      - {id: body, type: read_body, max_bytes: 1024}
      - {id: auth, type: auth, scheme: hmac_sha256_hex, secret: testsecret, signature_header: X-Hub-Signature-256}
      - {id: env, type: envelope}
      - {id: out, type: publish, destination: primary, target: github-webhooks}
    edges: [in>ip, ip>body, body>auth, auth>env, env>out]
`
	srv, err := NewFlowServer(loadFlowCfg(t, src), &fakePublisher{}, slog.Default(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	body := []byte(`{"ok":true}`)
	req := githubPOST(body)
	req.RemoteAddr = "198.51.100.5:1234"
	req.Header.Set("X-Forwarded-For", "203.0.113.10")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("spoofed XFF should be ignored: status = %d, want 403", rec.Code)
	}

	req = githubPOST(body)
	req.RemoteAddr = "10.1.2.3:1234"
	req.Header.Set("X-Forwarded-For", "203.0.113.10")
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("trusted proxy forwarding should be honoured: status = %d body = %s, want 202", rec.Code, rec.Body.String())
	}
}

func sourceIPHelper(trustedProxyHeaders bool, trusted []string) *Server {
	return &Server{
		cfg: config.Config{Security: config.SecurityConfig{
			TrustedProxyHeaders: trustedProxyHeaders,
			TrustedProxies:      trusted,
		}},
		trustedProxies: parseCIDRs(trusted),
	}
}

func TestSourceIPWalksForwardedForRightToLeftSkippingTrustedProxies(t *testing.T) {
	srv := sourceIPHelper(true, []string{"10.0.0.0/8"})
	newReq := func(remoteAddr, xff string) *http.Request {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = remoteAddr
		if xff != "" {
			req.Header.Set("X-Forwarded-For", xff)
		}
		return req
	}
	t.Run("spoofed leftmost entry ignored; real client from the right is used", func(t *testing.T) {
		req := newReq("10.0.0.9:1234", "203.0.113.10, 198.51.100.77")
		if got := srv.sourceIP(req); got != "198.51.100.77" {
			t.Fatalf("sourceIP = %q, want 198.51.100.77", got)
		}
	})
	t.Run("chain of two trusted proxies resolves to real client", func(t *testing.T) {
		req := newReq("10.0.0.9:1234", "203.0.113.10, 10.0.0.5")
		if got := srv.sourceIP(req); got != "203.0.113.10" {
			t.Fatalf("sourceIP = %q, want 203.0.113.10", got)
		}
	})
	t.Run("garbage entry stops the walk and uses the last good rightmost hop", func(t *testing.T) {
		req := newReq("10.0.0.9:1234", "not-an-ip, 10.0.0.5")
		if got := srv.sourceIP(req); got != "10.0.0.5" {
			t.Fatalf("sourceIP = %q, want 10.0.0.5", got)
		}
	})
	t.Run("all entries are trusted proxies falls back to leftmost", func(t *testing.T) {
		req := newReq("10.0.0.9:1234", "10.0.0.1, 10.0.0.2")
		if got := srv.sourceIP(req); got != "10.0.0.1" {
			t.Fatalf("sourceIP = %q, want 10.0.0.1", got)
		}
	})
	t.Run("untrusted peer ignores XFF entirely", func(t *testing.T) {
		req := newReq("203.0.113.200:1234", "203.0.113.10, 10.0.0.5")
		if got := srv.sourceIP(req); got != "203.0.113.200" {
			t.Fatalf("sourceIP = %q, want 203.0.113.200", got)
		}
	})
}

func TestSourceIPUsesRightmostEntryWhenTrustedProxiesNotConfigured(t *testing.T) {
	srv := sourceIPHelper(true, nil)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "203.0.113.200:1234"
	req.Header.Set("X-Forwarded-For", "203.0.113.10, 198.51.100.77")
	if got := srv.sourceIP(req); got != "198.51.100.77" {
		t.Fatalf("sourceIP = %q, want 198.51.100.77", got)
	}
}

func TestForwardedHeaderAllowlistDropsUnlistedHeaders(t *testing.T) {
	cfg := loadFlowCfg(t, testFlowYAML)
	cfg.Security.ForwardedHeaderAllowlist = []string{"Content-Type"}
	pub := &fakePublisher{}
	srv, err := NewFlowServer(cfg, pub, slog.Default(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	body := []byte(`{"ok":true}`)
	req := githubPOST(body)
	req.Header.Set("X-Secret-Internal", "leak-me")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if len(pub.published) != 1 {
		t.Fatalf("published = %#v, want 1", pub.published)
	}
	if _, ok := pub.published[0].msg.Headers["x-secret-internal"]; ok {
		t.Fatalf("non-allowlisted header forwarded: %#v", pub.published[0].msg.Headers)
	}
	if _, ok := pub.published[0].msg.Headers["content-type"]; !ok {
		t.Fatalf("allowlisted header missing: %#v", pub.published[0].msg.Headers)
	}
}

func TestHeaderExactAuthAcceptsMatchingAuthorization(t *testing.T) {
	pub := &fakePublisher{}
	srv, err := NewFlowServer(loadFlowCfg(t, headerExactYAML), pub, slog.Default(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
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
	srv, err := NewFlowServer(loadFlowCfg(t, headerExactYAML), &fakePublisher{}, slog.Default(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	req := httptest.NewRequest(http.MethodPost, "/webhooks/revenuecat", bytes.NewReader([]byte(`{"id":"evt_1"}`)))
	req.Header.Set("Authorization", "Bearer wrong")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestNewFlowServerWarnsWhenFlowUsesNoAuth(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	src := `
server: {addr: ":8080", max_body_bytes: 1024}
destinations:
  primary: {type: webhook, webhook: {url: "https://example.test"}}
flows:
  open_webhook:
    nodes:
      - {id: in, type: http_trigger, path: /open, methods: [POST]}
      - {id: body, type: read_body, max_bytes: 1024}
      - {id: auth, type: no_auth}
      - {id: env, type: envelope}
      - {id: out, type: publish, destination: primary, target: t}
    edges: [in>body, body>auth, auth>env, env>out]
`
	if _, err := NewFlowServer(loadFlowCfg(t, src), &fakePublisher{}, logger, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logs.String(), `"level":"WARN"`) {
		t.Fatalf("expected a warning log, got: %s", logs.String())
	}
	if !strings.Contains(logs.String(), `"flow":"open_webhook"`) {
		t.Fatalf("warning should name the flow, got: %s", logs.String())
	}
}

func TestNewFlowServerDoesNotWarnWithoutNoAuth(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	if _, err := NewFlowServer(loadFlowCfg(t, testFlowYAML), &fakePublisher{}, logger, nil); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(logs.String(), `"level":"WARN"`) {
		t.Fatalf("did not expect a warning log, got: %s", logs.String())
	}
}

const headerExactYAML = `
server:
  addr: ":8080"
  max_body_bytes: 1024
destinations:
  primary:
    type: webhook
    webhook: {url: "https://example.test"}
flows:
  revenuecat:
    nodes:
      - {id: in, type: http_trigger, path: /webhooks/revenuecat, methods: [POST]}
      - {id: body, type: read_body, max_bytes: 1024}
      - {id: auth, type: auth, scheme: exact, secret: rc-token, signature_header: Authorization, remove_bearer: true}
      - {id: env, type: envelope}
      - {id: out, type: publish, destination: primary, target: revenuecat-events}
    edges: [in>body, body>auth, auth>env, env>out]
`

// TestFlowServerCloseStopsSecretRefreshGoroutines guards against the resolver
// leak: bindSecretRef starts a refresh ticker per secret_ref, and if the
// server drops the closer those goroutines outlive it. Constructing and
// closing many servers must not grow the goroutine count.
func TestFlowServerCloseStopsSecretRefreshGoroutines(t *testing.T) {
	t.Setenv("HOOKWAY_TEST_ROTATING_SECRET", "testsecret")
	// testFlowYAML with the auth node's inline secret swapped for a
	// secret_ref, which is what starts the refresh goroutine.
	src := strings.Replace(testFlowYAML,
		"secret: testsecret, signature_header: X-Hub-Signature-256",
		`signature_header: X-Hub-Signature-256, secret_ref: {provider: env, name: HOOKWAY_TEST_ROTATING_SECRET, refresh: "1s"}`,
		1)
	if src == testFlowYAML {
		t.Fatal("fixture substitution failed; testFlowYAML changed shape")
	}
	cfg := loadFlowCfg(t, src)

	// Warm up once so one-off runtime goroutines are not counted as growth.
	warm, err := NewFlowServer(cfg, &fakePublisher{}, slog.New(slog.DiscardHandler), metrics.New())
	if err != nil {
		t.Fatalf("NewFlowServer() = %v", err)
	}
	if err := warm.Close(); err != nil {
		t.Fatalf("Close() = %v", err)
	}
	settle()
	before := runtime.NumGoroutine()

	for range 20 {
		s, err := NewFlowServer(cfg, &fakePublisher{}, slog.New(slog.DiscardHandler), metrics.New())
		if err != nil {
			t.Fatalf("NewFlowServer() = %v", err)
		}
		if err := s.Close(); err != nil {
			t.Fatalf("Close() = %v", err)
		}
	}
	settle()

	if grew := runtime.NumGoroutine() - before; grew > 2 {
		t.Fatalf("goroutines grew by %d across 20 construct/Close cycles; "+
			"secret_ref refresh tickers are leaking", grew)
	}
}

// settle gives closed goroutines a moment to actually exit before counting.
func settle() {
	for range 50 {
		runtime.Gosched()
		time.Sleep(2 * time.Millisecond)
	}
}
