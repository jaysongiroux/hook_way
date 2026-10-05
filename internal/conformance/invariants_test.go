package conformance

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jaysongiroux/hook_way/internal/config"
	"github.com/jaysongiroux/hook_way/internal/flowconfig"
	"github.com/jaysongiroux/hook_way/internal/graph"
	"github.com/jaysongiroux/hook_way/internal/httpapi"
	"github.com/jaysongiroux/hook_way/internal/queue"
)

// TestI1_LoopHopCheckRunsBeforeBodyRead asserts invariant I1 from
// docs/development/invariants.md I1: the loop-hop check runs before the body is
// buffered, so a terminal 4xx stops a webhook loop cheaply.
func TestI1_LoopHopCheckRunsBeforeBodyRead(t *testing.T) {
	srv := newServer(t, gatedYAML(), nil)
	body := &failIfRead{t: t}
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/webhooks/github", nil)
	req.Body = body
	req.Header.Set("X-Hub-Signature-256", githubHMAC(nil))
	req.Header.Set(config.LoopHopsHeader, "3")
	rec := post(srv, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "loop detected") {
		t.Fatalf("body = %s, want loop detected", rec.Body.String())
	}
	if body.read.Load() {
		t.Fatal("I1: body was read after a loop-hop rejection")
	}
}

// TestI2_InflightAcquiredBeforeBodyRead asserts invariant I2: a flood cannot
// exhaust memory by buffering unbounded bodies. Capacity is 1 (configured 0
// defaults to 256 and is not a reject-all).
func TestI2_InflightAcquiredBeforeBodyRead(t *testing.T) {
	src := strings.Replace(gatedYAML(), "  addr: \":8080\"\n  max_body_bytes: 1048576",
		"  addr: \":8080\"\n  max_body_bytes: 1048576\n  max_inflight_requests: 1", 1)
	pub := &testPublisher{entered: make(chan struct{}, 1), release: make(chan struct{})}
	srv := newServer(t, src, pub)
	body := []byte(`{"ok":true}`)
	done := make(chan struct{})
	go func() {
		defer close(done)
		post(srv, signedRequest(http.MethodPost, "/webhooks/github", body))
	}()
	select {
	case <-pub.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("first request never entered publish")
	}
	unread := &failIfRead{t: t}
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/webhooks/github", nil)
	req.Body = unread
	req.Header.Set("X-Hub-Signature-256", githubHMAC(nil))
	rec := post(srv, req)
	close(pub.release)
	<-done
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("saturated status = %d, want 503; body = %s", rec.Code, rec.Body.String())
	}
	if unread.read.Load() {
		t.Fatal("I2: body was read after inflight rejection")
	}
}

// TestI3_IPAllowlistRunsBeforeBodyRead asserts invariant I3.
func TestI3_IPAllowlistRunsBeforeBodyRead(t *testing.T) {
	src := strings.Replace(gatedYAML(), `cidrs: ["0.0.0.0/0", "::/0"]`, `cidrs: ["10.0.0.0/8"]`, 1)
	srv := newServer(t, src, nil)
	unread := &failIfRead{t: t}
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/webhooks/github", nil)
	req.Body = unread
	req.Header.Set("X-Hub-Signature-256", githubHMAC(nil))
	rec := post(srv, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body = %s", rec.Code, rec.Body.String())
	}
	if unread.read.Load() {
		t.Fatal("I3: body was read after an IP allowlist rejection")
	}
}

// TestI4_RateLimitRunsBeforeBodyRead asserts invariant I4.
func TestI4_RateLimitRunsBeforeBodyRead(t *testing.T) {
	src := strings.Replace(gatedYAML(), "rpm: 6000, burst: 6000", "rpm: 60, burst: 1", 1)
	srv := newServer(t, src, nil)
	body := []byte(`{"ok":true}`)
	if rec := post(srv, signedRequest(http.MethodPost, "/webhooks/github", body)); rec.Code != http.StatusAccepted {
		t.Fatalf("first request = %d, want 202: %s", rec.Code, rec.Body.String())
	}
	unread := &failIfRead{t: t}
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/webhooks/github", nil)
	req.Body = unread
	req.Header.Set("X-Hub-Signature-256", githubHMAC(nil))
	rec := post(srv, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429; body = %s", rec.Code, rec.Body.String())
	}
	if unread.read.Load() {
		t.Fatal("I4: body was read after a rate-limit rejection")
	}
}

// TestI5_NothingActsOnUnverifiedPayload is a compile-rejection test: envelope
// cannot consume RawRequest, so wiring it directly to read_body cannot load.
func TestI5_NothingActsOnUnverifiedPayload(t *testing.T) {
	src := `
state_stores: {test: {namespace: tests, memory: {}}}
server: {addr: ":8080", max_body_bytes: 1024}
destinations:
  primary: {type: webhook, webhook: {url: "https://example.test"}}
flows:
  github:
    nodes:
      - {id: in, type: http_trigger, path: /w, methods: [POST]}
      - {id: body, type: read_body, max_bytes: 1024}
      - {id: env, type: envelope}
      - {id: out, type: publish, destination: primary, target: t}
    edges: [in>body, body>env, env>out]
`
	_, issues := flowconfig.LoadBytes([]byte(src), "i5", false)
	joined := issuesText(issues)
	if !strings.Contains(joined, "RawRequest") || !strings.Contains(joined, "Verified") {
		t.Fatalf("I5: compile issues = %q, want a RawRequest vs Verified mismatch", joined)
	}
}

// TestI6_AuthVerifiesRawBytes signs a body with whitespace and unsorted keys.
// If auth re-serialized JSON before verifying, the signature would not match.
func TestI6_AuthVerifiesRawBytes(t *testing.T) {
	srv := newServer(t, gatedYAML(), nil)
	body := []byte("{\n  \"z\": 1,\n  \"a\": 2\n}")
	rec := post(srv, signedRequest(http.MethodPost, "/webhooks/github", body))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("raw-byte signature rejected: %d %s", rec.Code, rec.Body.String())
	}
}

// TestI7_ReplayReservationAfterAuth is a compile-rejection test: replay_guard
// consumes Verified, so wiring it before auth cannot load.
func TestI7_ReplayReservationAfterAuth(t *testing.T) {
	src := `
state_stores: {test: {namespace: tests, memory: {}}}
server: {addr: ":8080", max_body_bytes: 1024}
destinations:
  primary: {type: webhook, webhook: {url: "https://example.test"}}
flows:
  github:
    nodes:
      - {id: in, type: http_trigger, path: /w, methods: [POST]}
      - {id: body, type: read_body, max_bytes: 1024}
      - {id: replay, type: replay_guard, store: test}
      - {id: auth, type: auth, scheme: hmac_sha256_hex, secret: s, signature_header: X-S}
      - {id: env, type: envelope}
      - {id: out, type: publish, destination: primary, target: t}
    edges: [in>body, body>replay, replay>auth, auth>env, env>out]
`
	_, issues := flowconfig.LoadBytes([]byte(src), "i7", false)
	joined := issuesText(issues)
	if !strings.Contains(joined, "RawRequest") || !strings.Contains(joined, "Verified") {
		t.Fatalf("I7: compile issues = %q, want replay_guard before auth to be a Verified/RawRequest mismatch", joined)
	}
}

// TestI8_FiltersRunBeforeVariableResolution asserts a filtered request returns
// the filter outcome, never a lookup/variable error.
func TestI8_FiltersRunBeforeVariableResolution(t *testing.T) {
	src := flowYAML(
		"      - {id: filt, type: filter, conditions: [{source: body, path: event, operator: eq, value: created}], on_no_match: drop}\n"+
			"      - {id: look, type: lookup, provider: dynamodb, key: '{missing}', name: route}\n",
		", env>filt, filt>look, look>out",
		"")

	cfg := loadCfg(t, src)
	calls := 0
	reg, err := flowconfig.NewRegistryWithOptions(bindFixtureProtection(t, flowconfig.RegistryOptions{Lookups: func(flowconfig.LookupOptions) flowconfig.LookupFunc {
		return func(context.Context, string) (string, bool, error) {
			calls++
			return "", false, errors.New("must not resolve")
		}
	}}))
	if err != nil {
		t.Fatal(err)
	}
	for name, fc := range cfg.Flows {
		flow, err := graph.Compile(name, fc, reg)
		if err != nil {
			t.Fatal(err)
		}
		body := []byte(`{"event":"deleted"}`)
		req := signedRequest(http.MethodPost, "/webhooks/github", body)
		ctx := flowconfig.WithBodyReader(req.Context(), req.Body)
		term, err := flow.Run(ctx, flowconfig.HeadFromRequest(req, nil, "127.0.0.1"), graph.RunOptions{PublishTimeout: time.Second})
		if err != nil || len(term.Deliveries) != 0 || calls != 0 {
			t.Fatalf("filter drop: err=%v deliveries=%v lookup calls=%d", err, term.Deliveries, calls)
		}
	}

}

// TestI10_PublishDetachedFromRequestContext cancels the client context while
// publish is in flight; a detached destination must still complete.
func TestI10_PublishDetachedFromRequestContext(t *testing.T) {
	pub := &testPublisher{entered: make(chan struct{}, 1), delay: 50 * time.Millisecond}
	srv := newServer(t, gatedYAML(), pub)
	ctx, cancel := context.WithCancel(context.Background())
	body := []byte(`{"ok":true}`)
	req := signedRequest(http.MethodPost, "/webhooks/github", body).WithContext(ctx)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- post(srv, req)
	}()
	select {
	case <-pub.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("publish never started")
	}
	cancel()
	rec := <-done
	if pub.sawCancel.Load() {
		t.Fatal("I10: publish observed a canceled context; destinations must run on context.WithoutCancel")
	}
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 after client cancel; body = %s", rec.Code, rec.Body.String())
	}
}

// TestI10a_PublishSuccessContinuationSharesDetachedBudget pins the publish
// success-edge contract: the continuation runs only after the first publish
// succeeds, on the same WithoutCancel + publish_timeout budget, so a cancelled
// request does not cancel the chain.
func TestI10a_PublishSuccessContinuationSharesDetachedBudget(t *testing.T) {
	pub := &orderedPublisher{entered: make(chan struct{}, 2)}
	src := `
state_stores: {test: {namespace: tests, memory: {}}}
server:
  addr: ":8080"
  max_body_bytes: 1048576
  publish_timeout: 2s
destinations:
  primary:
    type: webhook
    webhook:
      url: "https://example.test"
flows:
  chain:
    nodes:
      - {id: in, type: http_trigger, path: /webhooks/chain, methods: [POST]}
      - {id: body, type: read_body, max_bytes: 1048576}
      - {id: auth, type: auth, scheme: hmac_sha256_hex, secret: "testsecret", signature_header: X-Hub-Signature-256}
      - {id: env, type: envelope, body_format: json}
      - {id: first, type: publish, destination: primary, target: first-q}
      - {id: second, type: publish, destination: primary, target: second-q}
    edges: [in>body, body>auth, auth>env, env>first, first>second]
`
	srv := newServer(t, src, pub)
	ctx, cancel := context.WithCancel(context.Background())
	body := []byte(`{"ok":true}`)
	req := signedRequest(http.MethodPost, "/webhooks/chain", body).WithContext(ctx)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- post(srv, req)
	}()
	select {
	case <-pub.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("first publish never started")
	}
	cancel()
	rec := <-done
	if pub.sawCancel.Load() {
		t.Fatal("I10a: publish chain observed a canceled request context")
	}
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body = %s", rec.Code, rec.Body.String())
	}
	targets := pub.targets()
	if len(targets) != 2 || targets[0] != "first-q" || targets[1] != "second-q" {
		t.Fatalf("publish order = %v, want [first-q second-q]", targets)
	}
}

type orderedPublisher struct {
	mu        sync.Mutex
	order     []string
	entered   chan struct{}
	sawCancel atomic.Bool
}

func (p *orderedPublisher) Publish(ctx context.Context, _, target string, _ queue.Message) error {
	if p.entered != nil {
		select {
		case p.entered <- struct{}{}:
		default:
		}
	}
	if err := ctx.Err(); err != nil {
		p.sawCancel.Store(true)
		return err
	}
	p.mu.Lock()
	p.order = append(p.order, target)
	p.mu.Unlock()
	return nil
}

func (p *orderedPublisher) targets() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.order...)
}

func (p *orderedPublisher) Ready(context.Context) error { return nil }
func (p *orderedPublisher) Close() error                { return nil }

// TestI11_UnknownAuthSchemeFailsClosed requires rejection before serving.
func TestI11_UnknownAuthSchemeFailsClosed(t *testing.T) {
	src := strings.Replace(gatedYAML(), "scheme: hmac_sha256_hex", "scheme: not_a_real_scheme", 1)
	cfg, issues := flowconfig.LoadBytes([]byte(src), "test", false)
	found := false
	for _, issue := range issues {
		if strings.Contains(issue.Message, "unknown signature scheme") && issue.Severity != "warning" {
			found = true
		}
	}
	if !found {
		t.Fatal("unknown scheme passed validation")
	}
	srv, err := httpapi.NewFlowServer(cfg, &testPublisher{}, slog.Default(), nil)
	if srv != nil {
		_ = srv.Close()
	}
	if !errors.Is(err, flowconfig.ErrAuthConfiguration) {
		t.Fatalf("unknown scheme passed startup: %v", err)
	}
}

// TestI12_BrokenFilterChainFailsClosed asserts a chain that fails to compile
// matches nothing (silent drop), never a 500 from a half-applied filter.
func TestI12_BrokenFilterChainFailsClosed(t *testing.T) {
	src := flowYAML(
		"      - {id: filt, type: filter, conditions: [{source: not-a-source, path: event, operator: eq, value: created}], on_no_match: drop}\n",
		", env>filt, filt>out",
		"")
	srv := newServer(t, src, nil)
	body := []byte(`{"event":"created"}`)
	rec := post(srv, signedRequest(http.MethodPost, "/webhooks/github", body))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("broken filter = %d %s, want 202 silent drop", rec.Code, rec.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	dests, _ := payload["destinations"].([]any)
	if len(dests) != 0 {
		t.Fatalf("fail-closed filter must match nothing, destinations = %#v", dests)
	}
}

// TestI13_BrokenFlowRegistersPathWith500Not404 asserts a flow that fails to
// build still answers its path with 500, not 404.
func TestI13_BrokenFlowRegistersPathWith500Not404(t *testing.T) {
	src := `
state_stores: {test: {namespace: tests, memory: {}}}
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
	cfg := loadCfgAllowingErrors(t, src)
	srv, err := httpapi.NewFlowServer(cfg, &testPublisher{}, slog.Default(), nil)
	if err != nil {
		t.Fatalf("broken flow must still construct a server: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	rec := post(srv, httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/webhooks/broken", strings.NewReader(`{}`)))
	if rec.Code == http.StatusNotFound {
		t.Fatal("I13: broken flow answered 404; a config error must not look unconfigured")
	}
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

// TestI14_FilteredRoutesAreNotDeliveryFailures asserts a silent filter drop is
// 202 success, not a 503 counted as a failed destination.
func TestI14_FilteredRoutesAreNotDeliveryFailures(t *testing.T) {
	src := flowYAML(
		"      - {id: filt, type: filter, conditions: [{source: body, path: event, operator: eq, value: created}], on_no_match: drop}\n",
		", env>filt, filt>out",
		"")
	srv := newServer(t, src, nil)
	body := []byte(`{"event":"deleted"}`)
	rec := post(srv, signedRequest(http.MethodPost, "/webhooks/github", body))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("filtered = %d %s, want 202 (not a delivery failure)", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "failed") {
		t.Fatalf("filtered branch must be absent from failed: %s", rec.Body.String())
	}
}

// TestI16_HandlerPanicsBecome500 asserts the process survives a panicking node.
func TestI16_HandlerPanicsBecome500(t *testing.T) {
	srv := newServer(t, gatedYAML(), nil)
	req := signedRequest(http.MethodPost, "/webhooks/github", []byte(`{"ok":true}`))
	req.Body = io.NopCloser(panicReadCloser{})
	rec := post(srv, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (process must survive a panicking node)", rec.Code)
	}
}

type panicReadCloser struct{}

func (panicReadCloser) Read([]byte) (int, error) { panic("boom") }
func (panicReadCloser) Close() error             { return nil }

// An attempted publish can have reached the broker despite its returned error.
func TestI17_ReservationRetainedOnUncertainPublishFailure(t *testing.T) {
	pub := &testPublisher{err: errors.New("broker down")}
	srv := newServer(t, gatedYAML(), pub)
	body := []byte(`{"action":"opened"}`)
	if rec := post(srv, signedRequest(http.MethodPost, "/webhooks/github", body)); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("first attempt = %d, want 503", rec.Code)
	}
	pub.mu.Lock()
	pub.err = nil
	pub.mu.Unlock()
	rec := post(srv, signedRequest(http.MethodPost, "/webhooks/github", body))
	if rec.Code != http.StatusConflict {
		t.Fatalf("uncertain publish retry = %d, want retained guard 409", rec.Code)
	}
}

// TestI18_AuthUsesConstantTimeComparison is a structural assertion rather
// than a timing measurement: timing tests are flaky in CI.
func TestI18_AuthUsesConstantTimeComparison(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	authDir := filepath.Join(filepath.Dir(thisFile), "..", "auth")
	entries, err := os.ReadDir(authDir)
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var compared bool
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		src, err := os.ReadFile(filepath.Clean(filepath.Join(authDir, entry.Name())))
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(src, []byte("hmac.Equal")) || bytes.Contains(src, []byte("subtle.ConstantTimeCompare")) {
			compared = true
		}
		file, err := parser.ParseFile(fset, entry.Name(), src, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			bin, ok := n.(*ast.BinaryExpr)
			if !ok || (bin.Op != token.EQL && bin.Op != token.NEQ) {
				return true
			}
			if namesSecret(bin.X) && namesSecret(bin.Y) {
				t.Errorf("%s: %s compares secret material with %s", entry.Name(), fset.Position(bin.Pos()), bin.Op)
			}
			return true
		})
	}
	if !compared {
		t.Fatal("I18: no hmac.Equal or subtle.ConstantTimeCompare in internal/auth")
	}
}

func namesSecret(e ast.Expr) bool {
	id, ok := e.(*ast.Ident)
	if !ok {
		return false
	}
	n := strings.ToLower(id.Name)
	for _, needle := range []string{"secret", "signature", "mac", "digest", "got", "expected", "creds"} {
		if strings.Contains(n, needle) {
			return true
		}
	}
	return false
}

func issuesText(issues []config.ValidationIssue) string {
	var b strings.Builder
	for _, issue := range issues {
		b.WriteString(issue.Message)
		b.WriteByte('\n')
	}
	return b.String()
}
