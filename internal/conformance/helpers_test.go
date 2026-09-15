package conformance

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasongiroux/hookway/internal/config"
	"github.com/jasongiroux/hookway/internal/flowconfig"
	"github.com/jasongiroux/hookway/internal/httpapi"
	"github.com/jasongiroux/hookway/internal/queue"
	"github.com/jasongiroux/hookway/internal/replaystore"
)

const testSecret = "testsecret"

type failIfRead struct {
	t    *testing.T
	read atomic.Bool
}

func (f *failIfRead) Read([]byte) (int, error) {
	f.read.Store(true)
	f.t.Error("body was read; a pre-body gate must reject without buffering")
	return 0, io.EOF
}
func (f *failIfRead) Close() error { return nil }

type testPublisher struct {
	mu        sync.Mutex
	err       error
	delay     time.Duration
	entered   chan struct{}
	release   chan struct{}
	published int
	sawCancel atomic.Bool
}

func (p *testPublisher) Publish(ctx context.Context, _, _ string, _ queue.Message) error {
	if p.entered != nil {
		select {
		case p.entered <- struct{}{}:
		default:
		}
	}
	if p.release != nil {
		<-p.release
	}
	if p.delay > 0 {
		timer := time.NewTimer(p.delay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			p.sawCancel.Store(true)
			return ctx.Err()
		}
	}
	if err := ctx.Err(); err != nil {
		p.sawCancel.Store(true)
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err != nil {
		return p.err
	}
	p.published++
	return nil
}
func (p *testPublisher) Ready(context.Context) error { return nil }
func (p *testPublisher) Close() error                { return nil }

type blockingCapturer struct {
	started, release chan struct{}
	err              error
	panic            bool
}

func (c *blockingCapturer) Capture(context.Context, replaystore.Event) error {
	if c.started != nil {
		close(c.started)
	}
	if c.release != nil {
		<-c.release
	}
	if c.panic {
		panic("capture panic")
	}
	return c.err
}

func (c *blockingCapturer) RecordOutcome(context.Context, string, []replaystore.Delivery) error {
	return nil
}

func githubHMAC(body []byte) string {
	mac := hmac.New(sha256.New, []byte(testSecret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func loadCfg(t *testing.T, src string) flowconfig.Config {
	t.Helper()
	cfg, issues := flowconfig.LoadBytes([]byte(src), "conformance", false)
	for _, issue := range issues {
		if issue.Severity != config.SeverityWarning {
			t.Fatalf("load issues: %+v", issues)
		}
	}
	return cfg
}

func loadCfgAllowingErrors(t *testing.T, src string) flowconfig.Config {
	t.Helper()
	cfg, _ := flowconfig.LoadBytes([]byte(src), "conformance", false)
	if cfg.Flows == nil {
		t.Fatal("expected flows to decode")
	}
	return cfg
}

func newServer(t *testing.T, src string, pub queue.Publisher) *httpapi.FlowServer {
	t.Helper()
	if pub == nil {
		pub = &testPublisher{}
	}
	srv, err := httpapi.NewFlowServer(loadCfg(t, src), pub, slog.Default(), nil)
	if err != nil {
		t.Fatal(err)
	}
	return srv
}

func signedRequest(method, path string, body []byte) *http.Request {
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Hub-Signature-256", githubHMAC(body))
	return req
}

func post(srv http.Handler, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

func flowYAML(extraNodes, afterEnv, serverLines string) string {
	if serverLines == "" {
		serverLines = "  addr: \":8080\"\n  max_body_bytes: 1048576"
	}
	if afterEnv == "" {
		afterEnv = ", env>out"
	}
	return `
schema_version: 2
server:
` + serverLines + `
destinations:
  primary:
    type: webhook
    webhook:
      url: "https://example.com/hooks"
flows:
  github:
    nodes:
      - {id: in, type: http_trigger, path: /webhooks/github, methods: [POST]}
      - {id: loop, type: loop_guard, max_hops: 3}
      - {id: allow, type: ip_allowlist, cidrs: ["0.0.0.0/0", "::/0"]}
      - {id: rl, type: rate_limit, rpm: 6000, burst: 6000}
      - {id: body, type: read_body, max_bytes: 1048576}
      - {id: auth, type: auth, scheme: hmac_sha256_hex, secret: testsecret, signature_header: X-Hub-Signature-256}
      - {id: replay, type: replay_guard, ttl: 5m, max_entries: 100}
      - {id: env, type: envelope, body_format: json}
` + extraNodes + `
      - {id: out, type: publish, destination: primary, target: github-webhooks}
    edges: [in>loop, loop>allow, allow>rl, rl>body, body>auth, auth>replay, replay>env` + afterEnv + `]
`
}

func gatedYAML() string {
	return flowYAML("", "", "")
}
