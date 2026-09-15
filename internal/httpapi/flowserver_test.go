package httpapi

import (
	"bytes"
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

	"github.com/jasongiroux/hookway/internal/flowconfig"
)

const testFlowYAML = `
schema_version: 2
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
	return srv
}

func TestFlowServerValidSignatureReturns202(t *testing.T) {
	pub := &fakePublisher{}
	srv := newTestFlowServer(t, pub)
	body := []byte(`{"action":"opened","number":1}`)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(body))
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
	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(body))
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
	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", githubHMAC(body))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503: a publish failure must make the sender retry, never be swallowed as a success (gaps doc C1)", rec.Code)
	}
}

func TestFlowServerOversizeBodyReturns413(t *testing.T) {
	src := strings.Replace(testFlowYAML, "max_bytes: 1048576", "max_bytes: 8", 1)
	srv, err := NewFlowServer(loadFlowCfg(t, src), &fakePublisher{}, slog.Default(), nil)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"action":"opened"}`)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", githubHMAC(body))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", rec.Code)
	}
}

func TestFlowServerUnknownPathReturns404(t *testing.T) {
	srv := newTestFlowServer(t, &fakePublisher{})
	req := httptest.NewRequest(http.MethodPost, "/nope", strings.NewReader("{}"))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestFlowServerWrongMethodReturns405(t *testing.T) {
	srv := newTestFlowServer(t, &fakePublisher{})
	req := httptest.NewRequest(http.MethodGet, "/webhooks/github", nil)
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
	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", strings.NewReader(`{"ok":true}`))
	req.Header.Set("X-Hub-Signature-256", githubHMAC([]byte(`{"ok":true}`)))
	req.Body = io.NopCloser(panicReadCloser{})
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (process must survive a panicking node)", rec.Code)
	}
}
