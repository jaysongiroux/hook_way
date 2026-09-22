package httpapi

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jaysongiroux/hook_way/internal/flowconfig"
)

const testFlowYAML = `
server:
  addr: ":8080"
  max_body_bytes: 1048576
destinations:
  primary:
    type: webhook
    webhook:
      url: "https://example.com/hooks"
flows:
  github:
    nodes:
      - {id: in, type: http_trigger, path: /webhooks/github, methods: [POST]}
      - {id: rl, type: rate_limit, rpm: 60}
      - {id: body, type: read_body, max_bytes: 1048576}
      - {id: auth, type: auth, scheme: hmac_sha256_hex, secret: testsecret, signature_header: X-Hub-Signature-256}
      - {id: env, type: envelope, body_format: json}
      - {id: out, type: publish, destination: primary, target: github-webhooks}
    edges: [in>rl, rl>body, body>auth, auth>env, env>out]
`

func loadFlowCfg(t *testing.T, src string) flowconfig.Config {
	t.Helper()
	cfg, issues := flowconfig.LoadBytes([]byte(src), "test", false)
	for _, iss := range issues {
		if iss.Severity != "warning" {
			t.Fatalf("load issues: %+v", issues)
		}
	}
	return cfg
}

func githubHMAC(body []byte) string {
	mac := hmac.New(sha256.New, []byte("testsecret"))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func newTestFlowServer(t *testing.T, pub *fakePublisher) *FlowServer {
	t.Helper()
	srv, err := NewFlowServer(loadFlowCfg(t, testFlowYAML), pub, slog.Default(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	return srv
}

func TestFlowServerValidSignatureReturns202(t *testing.T) {
	pub := &fakePublisher{}
	srv := newTestFlowServer(t, pub)
	body := []byte(`{"action":"opened","number":1}`)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/webhooks/github", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Hub-Signature-256", githubHMAC(body))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["status"] != "accepted" {
		t.Fatalf("payload = %#v", payload)
	}
	if len(pub.published) != 1 {
		t.Fatalf("published = %d, want 1", len(pub.published))
	}
	got, _ := json.Marshal(pub.published[0].msg.Body)
	if !bytes.Contains(got, []byte(`opened`)) {
		t.Errorf("published body = %s", got)
	}
}

func TestFlowServerBadSignatureReturns401(t *testing.T) {
	srv := newTestFlowServer(t, &fakePublisher{})
	body := []byte(`{"action":"opened"}`)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/webhooks/github", bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", "sha256=deadbeef")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestPublishFailureReturns503(t *testing.T) {
	srv := newTestFlowServer(t, &fakePublisher{err: errors.New("broker down")})
	body := []byte(`{"action":"opened"}`)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/webhooks/github", bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", githubHMAC(body))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503: a publish failure must make the sender retry, never be swallowed as a success", rec.Code)
	}
}

func TestFlowServerOversizeBodyReturns413(t *testing.T) {
	src := strings.Replace(testFlowYAML, "max_bytes: 1048576", "max_bytes: 8", 1)
	srv, err := NewFlowServer(loadFlowCfg(t, src), &fakePublisher{}, slog.Default(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	body := []byte(`{"action":"opened"}`)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/webhooks/github", bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", githubHMAC(body))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", rec.Code)
	}
}

func TestFlowServerUnknownPathReturns404(t *testing.T) {
	srv := newTestFlowServer(t, &fakePublisher{})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/nope", strings.NewReader("{}"))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestFlowServerWrongMethodReturns405(t *testing.T) {
	srv := newTestFlowServer(t, &fakePublisher{})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/webhooks/github", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
	if rec.Header().Get("Allow") == "" {
		t.Fatal("missing Allow header")
	}
}

type panicReadCloser struct{}

func (panicReadCloser) Read([]byte) (int, error) { panic("boom") }
func (panicReadCloser) Close() error             { return nil }

func TestPanickingNodeReturns500(t *testing.T) {
	srv := newTestFlowServer(t, &fakePublisher{})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/webhooks/github", strings.NewReader(`{"ok":true}`))
	req.Header.Set("X-Hub-Signature-256", githubHMAC([]byte(`{"ok":true}`)))
	req.Body = io.NopCloser(panicReadCloser{})
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (process must survive a panicking node)", rec.Code)
	}
}

const replayFlowYAML = `
server:
  addr: ":8080"
  max_body_bytes: 1048576
destinations:
  primary:
    type: webhook
    webhook:
      url: "https://example.com/hooks"
flows:
  github:
    nodes:
      - {id: in, type: http_trigger, path: /webhooks/github, methods: [POST]}
      - {id: rl, type: rate_limit, rpm: 6000, burst: 6000}
      - {id: body, type: read_body, max_bytes: 1048576}
      - {id: auth, type: auth, scheme: hmac_sha256_hex, secret: testsecret, signature_header: X-Hub-Signature-256}
      - {id: replay, type: replay_guard, ttl: 5m, max_entries: 100}
      - {id: env, type: envelope, body_format: json}
      - {id: out, type: publish, destination: primary, target: github-webhooks}
    edges: [in>rl, rl>body, body>auth, auth>replay, replay>env, env>out]
`

const filterDropReplayYAML = `
server:
  addr: ":8080"
  max_body_bytes: 1048576
destinations:
  primary:
    type: webhook
    webhook:
      url: "https://example.com/hooks"
flows:
  github:
    nodes:
      - {id: in, type: http_trigger, path: /webhooks/github, methods: [POST]}
      - {id: rl, type: rate_limit, rpm: 6000, burst: 6000}
      - {id: body, type: read_body, max_bytes: 1048576}
      - {id: auth, type: auth, scheme: hmac_sha256_hex, secret: testsecret, signature_header: X-Hub-Signature-256}
      - {id: replay, type: replay_guard, ttl: 5m, max_entries: 100}
      - {id: env, type: envelope, body_format: json}
      - {id: filt, type: filter, conditions: [{source: body, path: action, operator: eq, value: never}], on_no_match: drop}
      - {id: out, type: publish, destination: primary, target: github-webhooks}
    edges: [in>rl, rl>body, body>auth, auth>replay, replay>env, env>filt, filt>out]
`

func postSignedFlow(srv *FlowServer, body []byte) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/webhooks/github", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Hub-Signature-256", githubHMAC(body))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

func TestPublishFailureReleasesReservation(t *testing.T) {
	pub := &fakePublisher{err: errors.New("broker down")}
	srv, err := NewFlowServer(loadFlowCfg(t, replayFlowYAML), pub, slog.Default(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	body := []byte(`{"action":"opened"}`)
	if rec := postSignedFlow(srv, body); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("first attempt = %d, want 503", rec.Code)
	}
	pub.mu.Lock()
	pub.err = nil
	pub.mu.Unlock()
	rec := postSignedFlow(srv, body)
	if rec.Code == http.StatusConflict {
		t.Fatal("retry after a publish failure was rejected 409: the reservation was not " +
			"released, so the only recovery path for a failed publish is closed (I17)")
	}
	if rec.Code != http.StatusAccepted {
		t.Fatalf("retry = %d, want 202", rec.Code)
	}
}

func TestFilterSilentDropKeepsReservation(t *testing.T) {
	srv, err := NewFlowServer(loadFlowCfg(t, filterDropReplayYAML), &fakePublisher{}, slog.Default(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	body := []byte(`{"action":"opened"}`)
	if rec := postSignedFlow(srv, body); rec.Code != http.StatusAccepted {
		t.Fatalf("first attempt = %d, want 202 (silent drop looks like success)", rec.Code)
	}
	if rec := postSignedFlow(srv, body); rec.Code != http.StatusConflict {
		t.Fatalf("resend = %d, want 409: a consumed delivery must keep its reservation", rec.Code)
	}
}

const skipFailedFlowYAML = `
server:
  addr: ":8080"
  max_body_bytes: 1048576
destinations:
  primary:
    type: webhook
    webhook:
      url: "https://example.com/a"
  secondary:
    type: webhook
    webhook:
      url: "https://example.com/b"
flows:
  github:
    delivery_failure_policy: skip_failed
    nodes:
      - {id: in, type: http_trigger, path: /webhooks/github, methods: [POST]}
      - {id: rl, type: rate_limit, rpm: 6000, burst: 6000}
      - {id: body, type: read_body, max_bytes: 1048576}
      - {id: auth, type: auth, scheme: hmac_sha256_hex, secret: testsecret, signature_header: X-Hub-Signature-256}
      - {id: replay, type: replay_guard, ttl: 5m, max_entries: 100}
      - {id: env, type: envelope, body_format: json}
      - {id: out_a, type: publish, destination: primary, target: github-a}
      - {id: out_b, type: publish, destination: secondary, target: github-b}
    edges: [in>rl, rl>body, body>auth, auth>replay, replay>env, env>out_a, env>out_b]
`

func TestSkipFailedPartialSuccessReturns202WithFailures(t *testing.T) {
	pub := &fakePublisher{err: errors.New("secondary down"), errDestination: "secondary"}
	srv, err := NewFlowServer(loadFlowCfg(t, skipFailedFlowYAML), pub, slog.Default(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	rec := postSignedFlow(srv, []byte(`{"action":"opened"}`))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	var res struct {
		Destinations []struct {
			DestinationQueueID string `json:"destination_queue_id"`
		} `json:"destinations"`
		FailedDestinations []struct {
			DestinationQueueID string `json:"destination_queue_id"`
		} `json:"failed_destinations"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Destinations) != 1 || res.Destinations[0].DestinationQueueID != "primary" {
		t.Fatalf("destinations = %#v", res.Destinations)
	}
	if len(res.FailedDestinations) != 1 || res.FailedDestinations[0].DestinationQueueID != "secondary" {
		t.Fatalf("failed_destinations = %#v", res.FailedDestinations)
	}
}

func TestSkipFailedAllFailuresReturn503(t *testing.T) {
	pub := &fakePublisher{err: errors.New("all down")}
	srv, err := NewFlowServer(loadFlowCfg(t, skipFailedFlowYAML), pub, slog.Default(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	rec := postSignedFlow(srv, []byte(`{"action":"opened"}`))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503: %s", rec.Code, rec.Body.String())
	}
	if len(pub.published) != 2 {
		t.Fatalf("published = %d, want both destinations attempted", len(pub.published))
	}
}

func TestSkipFailedPartialSuccessReleasesReservation(t *testing.T) {
	pub := &fakePublisher{err: errors.New("secondary down"), errDestination: "secondary"}
	srv, err := NewFlowServer(loadFlowCfg(t, skipFailedFlowYAML), pub, slog.Default(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	body := []byte(`{"action":"opened"}`)
	if rec := postSignedFlow(srv, body); rec.Code != http.StatusAccepted {
		t.Fatalf("first = %d %s", rec.Code, rec.Body.String())
	}
	rec := postSignedFlow(srv, body)
	if rec.Code == http.StatusConflict {
		t.Fatal("retry after skip_failed partial success was 409: the reservation was not released (I17)")
	}
	if rec.Code != http.StatusAccepted {
		t.Fatalf("retry = %d, want 202", rec.Code)
	}
}

func TestSkipFailedFullSuccessKeepsReservation(t *testing.T) {
	srv, err := NewFlowServer(loadFlowCfg(t, skipFailedFlowYAML), &fakePublisher{}, slog.Default(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	body := []byte(`{"action":"opened"}`)
	if rec := postSignedFlow(srv, body); rec.Code != http.StatusAccepted {
		t.Fatalf("first = %d %s", rec.Code, rec.Body.String())
	}
	if rec := postSignedFlow(srv, body); rec.Code != http.StatusConflict {
		t.Fatalf("resend = %d, want 409 after full skip_failed success", rec.Code)
	}
}

func TestPublishMessageAttributesReachQueueMessage(t *testing.T) {
	src := strings.Replace(testFlowYAML,
		"- {id: out, type: publish, destination: primary, target: github-webhooks}",
		"- {id: out, type: publish, destination: primary, target: github-webhooks, message_attributes: {event: opened, fields_changed: {data_type: String.Array, value: '[\"status\"]'}}}",
		1)
	pub := &fakePublisher{}
	srv, err := NewFlowServer(loadFlowCfg(t, src), pub, slog.Default(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	rec := postSignedFlow(srv, []byte(`{"action":"opened"}`))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d %s", rec.Code, rec.Body.String())
	}
	got := pub.published[0].msg.MessageAttributes
	if got["event"].DataType != "String" || got["event"].StringValue != "opened" {
		t.Fatalf("event = %+v", got["event"])
	}
	if got["fields_changed"].DataType != "String.Array" || got["fields_changed"].StringValue != `["status"]` {
		t.Fatalf("fields_changed = %+v", got["fields_changed"])
	}
}

func TestBrokenFlowRegistersPathWith500Not404(t *testing.T) {
	src := `
server: {addr: ":8080", max_body_bytes: 1024}
destinations:
  primary: {type: webhook, webhook: {url: "https://example.test"}}
flows:
  broken:
    nodes:
      - {id: in, type: http_trigger, path: /webhooks/broken, methods: [POST]}
      - {id: body, type: read_body, max_bytes: 1024}
      - {id: env, type: envelope}
      - {id: out, type: publish, destination: primary, target: t}
    edges: [in>body, body>env, env>out]
`
	cfg, issues := flowconfig.LoadBytes([]byte(src), "broken", false)
	if len(issues) == 0 {
		t.Fatal("expected compile issues for envelope-after-body")
	}
	srv, err := NewFlowServer(cfg, &fakePublisher{}, slog.Default(), nil)
	if err != nil {
		t.Fatalf("broken flow must still construct a server: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/webhooks/broken", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code == http.StatusNotFound {
		t.Fatal("broken flow answered 404; I13 requires 500 so a config error is not an unconfigured path")
	}
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}
