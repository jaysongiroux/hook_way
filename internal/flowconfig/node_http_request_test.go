package flowconfig

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasongiroux/hookway/internal/config"
	"github.com/jasongiroux/hookway/internal/graph"
	"github.com/jasongiroux/hookway/internal/transform"
)

func mustHTTPRequestNode(t *testing.T, body string, allowlist, denylist []string) graph.Node {
	t.Helper()
	reg, err := NewRegistryWithOptions(RegistryOptions{HeaderAllowlist: allowlist, HeaderDenylist: denylist})
	if err != nil {
		t.Fatal(err)
	}
	spec, _ := reg.Lookup("http_request")
	node, err := spec.New(nodeConfigFromYAML(t, "http_request", body))
	if err != nil {
		t.Fatal(err)
	}
	return node
}

func TestHTTPRequestCapturesUpstreamResponse(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("method = %s, want PUT", r.Method)
		}
		if r.URL.Path != "/api/v1/thing" {
			t.Errorf("path = %s, want /api/v1/thing", r.URL.Path)
		}
		if r.URL.Query().Get("a") != "1" {
			t.Errorf("query = %s, want a=1", r.URL.RawQuery)
		}
		if r.Header.Get(config.LoopHopsHeader) != "2" {
			t.Errorf("hops = %q, want 2", r.Header.Get(config.LoopHopsHeader))
		}
		if r.Header.Get("Authorization") != "" {
			t.Error("inbound Authorization was forwarded")
		}
		w.Header().Set("X-Upstream", "yes")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("upstream-body"))
	}))
	defer upstream.Close()

	node := mustHTTPRequestNode(t,
		"url: "+upstream.URL+"/{rest}\nforward_method: true\nforward_query: true\nforward_headers: [X-Request-ID]\n",
		nil, nil)
	env := graph.Envelope{
		V: graph.Verified{Raw: graph.RawRequest{
			Head: graph.RequestHead{
				Method: http.MethodPut,
				Header: http.Header{
					"Authorization": []string{"secret"},
					"X-Request-Id":  []string{"req-1"},
				},
				Query: url.Values{"a": {"1"}},
				Hops:  1,
			},
			Body: []byte(`{"k":"v"}`),
		}},
		Vars: map[string]string{"rest": "api/v1/thing"},
	}
	out, err := node.Run(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	got := out.(graph.Envelope)
	if got.Upstream == nil || got.Upstream.Status != http.StatusCreated {
		t.Fatalf("upstream = %+v", got.Upstream)
	}
	if string(got.Upstream.Body) != "upstream-body" {
		t.Fatalf("body = %q", got.Upstream.Body)
	}
	if got.Upstream.Header.Get("X-Upstream") != "yes" {
		t.Fatalf("headers = %v", got.Upstream.Header)
	}
}

func TestHTTPRequestTimeoutIs504(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	node := mustHTTPRequestNode(t, "url: "+upstream.URL+"\ntimeout: 50ms\n", nil, nil)
	_, err := node.Run(context.Background(), graph.Envelope{V: graph.Verified{Raw: graph.RawRequest{Head: graph.RequestHead{Method: http.MethodPost}}}})
	var ne graph.NodeError
	if err == nil || !asNodeError(err, &ne) || ne.Status != http.StatusGatewayTimeout {
		t.Fatalf("err = %v, want 504", err)
	}
}

func TestHTTPRequestNon2xxIsPassthroughByDefault(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, "down")
	}))
	defer upstream.Close()
	node := mustHTTPRequestNode(t, "url: "+upstream.URL+"\n", nil, nil)
	out, err := node.Run(context.Background(), graph.Envelope{V: graph.Verified{Raw: graph.RawRequest{Head: graph.RequestHead{Method: http.MethodPost}}}})
	if err != nil {
		t.Fatalf("non-2xx must pass through by default: %v", err)
	}
	if out.(graph.Envelope).Upstream.Status != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", out.(graph.Envelope).Upstream.Status)
	}
}

func TestHTTPRequestErrorOnStatus5xx(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer upstream.Close()
	node := mustHTTPRequestNode(t, "url: "+upstream.URL+"\nerror_on_status: [5xx]\n", nil, nil)
	_, err := node.Run(context.Background(), graph.Envelope{V: graph.Verified{Raw: graph.RawRequest{Head: graph.RequestHead{Method: http.MethodPost}}}})
	var ne graph.NodeError
	if err == nil || !asNodeError(err, &ne) || ne.Status != http.StatusBadGateway {
		t.Fatalf("err = %v, want 502", err)
	}
}

func asNodeError(err error, ne *graph.NodeError) bool {
	return errors.As(err, ne)
}

func TestCollapseDoubleSlashInPath(t *testing.T) {
	got := collapseDoubleSlashInPath("http://example.test//api/v1/thing")
	if got != "http://example.test/api/v1/thing" {
		t.Fatalf("got %q", got)
	}
}

func TestHTTPRequestDoesNotFollowRedirects(t *testing.T) {
	var hops int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hops++
		if r.URL.Path == "/from" {
			http.Redirect(w, r, "/to", http.StatusFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	node := mustHTTPRequestNode(t, "url: "+upstream.URL+"/from\n", nil, nil)
	out, err := node.Run(context.Background(), graph.Envelope{V: graph.Verified{Raw: graph.RawRequest{Head: graph.RequestHead{Method: http.MethodGet}}}})
	if err != nil {
		t.Fatal(err)
	}
	if hops != 1 {
		t.Fatalf("hops = %d, proxy must not follow redirects", hops)
	}
	if out.(graph.Envelope).Upstream.Status != http.StatusFound {
		t.Fatalf("status = %d, want 302 passthrough", out.(graph.Envelope).Upstream.Status)
	}
}

func TestHTTPRequestJoinsRestWithoutDoubleSlash(t *testing.T) {
	var gotPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	node := mustHTTPRequestNode(t, "url: "+upstream.URL+"/{rest}\n", nil, nil)
	_, err := node.Run(context.Background(), graph.Envelope{
		V:    graph.Verified{Raw: graph.RawRequest{Head: graph.RequestHead{Method: http.MethodGet}}},
		Vars: map[string]string{"rest": "api/v1/thing"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/api/v1/thing" {
		t.Fatalf("path = %q", gotPath)
	}
}

func TestHTTPRequestAllowlistGovernsForwardedHeaders(t *testing.T) {
	var got http.Header
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	node := mustHTTPRequestNode(t, "url: "+upstream.URL+"\n", []string{"X-Request-ID"}, nil)
	_, err := node.Run(context.Background(), graph.Envelope{V: graph.Verified{Raw: graph.RawRequest{Head: graph.RequestHead{
		Method: http.MethodGet,
		Header: http.Header{"X-Request-Id": []string{"a"}, "X-Other": []string{"b"}},
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	if got.Get("X-Request-Id") != "a" {
		t.Fatalf("allowlisted header missing: %v", got)
	}
	if got.Get("X-Other") != "" {
		t.Fatalf("non-allowlisted header forwarded: %v", got)
	}
}

func TestRespondPassthroughCopiesUpstreamAndStripsHopByHop(t *testing.T) {
	node, err := newRespondNode("resp", RespondOptions{Passthrough: true})
	if err != nil {
		t.Fatal(err)
	}
	out, err := node.Run(context.Background(), graph.Envelope{Upstream: &graph.UpstreamResponse{
		Status: http.StatusCreated,
		Header: http.Header{
			"Content-Type":      []string{"text/plain"},
			"Connection":        []string{"keep-alive"},
			"Transfer-Encoding": []string{"chunked"},
			"Set-Cookie":        []string{"sid=1"},
			"X-Upstream":        []string{"yes"},
		},
		Body: []byte("hello"),
	}})
	if err != nil {
		t.Fatal(err)
	}
	resp := out.(graph.Terminal).Response
	if resp.Status != http.StatusCreated || string(resp.Body) != "hello" {
		t.Fatalf("response = %+v", resp)
	}
	if resp.Header.Get("X-Upstream") != "yes" || resp.Header.Get("Content-Type") != "text/plain" {
		t.Fatalf("headers = %v", resp.Header)
	}
	if resp.Header.Get("Connection") != "" || resp.Header.Get("Transfer-Encoding") != "" || resp.Header.Get("Set-Cookie") != "" {
		t.Fatalf("hop-by-hop or Set-Cookie leaked: %v", resp.Header)
	}
}

func TestRespondExplicitStatusAndBody(t *testing.T) {
	node, err := newRespondNode("resp", RespondOptions{Status: 201, Body: "created-{id}", Headers: map[string]string{"X-Ok": "1"}})
	if err != nil {
		t.Fatal(err)
	}
	out, err := node.Run(context.Background(), graph.Envelope{Vars: map[string]string{"id": "9"}})
	if err != nil {
		t.Fatal(err)
	}
	resp := out.(graph.Terminal).Response
	if resp.Status != 201 || string(resp.Body) != "created-9" || resp.Header.Get("X-Ok") != "1" {
		t.Fatalf("response = %+v", resp)
	}
}

func TestRespondPassthroughWithoutUpstreamIs502(t *testing.T) {
	node, err := newRespondNode("resp", RespondOptions{Passthrough: true})
	if err != nil {
		t.Fatal(err)
	}
	_, err = node.Run(context.Background(), graph.Envelope{})
	var ne graph.NodeError
	if err == nil || !asNodeError(err, &ne) || ne.Status != http.StatusBadGateway {
		t.Fatalf("err = %v, want 502", err)
	}
}

func TestHTTPRequestBodyRawDefaultSendsOriginalBytes(t *testing.T) {
	var got []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	doc, err := transform.ParseXML([]byte(`<ROOT><KEEP>ok</KEEP></ROOT>`))
	if err != nil {
		t.Fatal(err)
	}
	node := mustHTTPRequestNode(t, "url: "+upstream.URL+"\n", nil, nil)
	_, err = node.Run(context.Background(), graph.Envelope{
		V:    graph.Verified{Raw: graph.RawRequest{Head: graph.RequestHead{Method: http.MethodPost}, Body: []byte("original-raw")}},
		Body: doc,
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "original-raw" {
		t.Fatalf("body = %q, want original raw bytes", got)
	}
}

func TestHTTPRequestBodyTransformedSendsSerializedXML(t *testing.T) {
	var got []byte
	var gotType string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = io.ReadAll(r.Body)
		gotType = r.Header.Get("Content-Type")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	doc, err := transform.ParseXML([]byte(`<ROOT><KEEP>ok</KEEP></ROOT>`))
	if err != nil {
		t.Fatal(err)
	}
	node := mustHTTPRequestNode(t, "url: "+upstream.URL+"\nbody: transformed\n", nil, nil)
	_, err = node.Run(context.Background(), graph.Envelope{
		V:    graph.Verified{Raw: graph.RawRequest{Head: graph.RequestHead{Method: http.MethodPost}, Body: []byte("original-raw")}},
		Body: doc,
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(got) == "original-raw" || !strings.Contains(string(got), "<KEEP>ok</KEEP>") {
		t.Fatalf("body = %q, want serialized XML", got)
	}
	if gotType != "application/xml" {
		t.Fatalf("content-type = %q, want application/xml", gotType)
	}
}

func TestHeaderExactSchemeAlias(t *testing.T) {
	if defaultAuthScheme("header_exact") != "exact" {
		t.Fatalf("header_exact should alias to exact, got %q", defaultAuthScheme("header_exact"))
	}
}

func TestReviewConnectionNominatedHeadersAreRemoved(t *testing.T) {
	src := http.Header{"Connection": {"X-Internal, X-Other"}, "X-Internal": {"private"}, "X-Other": {"private"}, "X-Public": {"visible"}}
	n, err := newHTTPRequestNode("up", HTTPRequestOptions{URL: "https://example.test"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://example.test", nil)
	n.(httpRequestNode).copyForwardedHeaders(req, src)
	for _, h := range []http.Header{req.Header, filterPassthroughHeaders(src)} {
		if h.Get("X-Internal") != "" || h.Get("X-Other") != "" || h.Get("X-Public") != "visible" {
			t.Fatalf("hop-by-hop leak: %v", h)
		}
	}
}

func retryEnvelope(body string) graph.Envelope {
	return graph.Envelope{
		V:    graph.Verified{Raw: graph.RawRequest{Head: graph.RequestHead{Method: http.MethodPost, Header: http.Header{}}, Body: []byte(body)}},
		Vars: map[string]string{},
	}
}

func TestHTTPRequestRetriesUntilSuccess(t *testing.T) {
	var calls int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ := io.ReadAll(r.Body)
		if string(got) != `{"k":"v"}` {
			t.Errorf("attempt %d body = %q, want the full payload resent", atomic.LoadInt32(&calls)+1, got)
		}
		if atomic.AddInt32(&calls, 1) < 3 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	node := mustHTTPRequestNode(t,
		"url: "+upstream.URL+"\nretry:\n  max_attempts: 3\n  initial_backoff: 1ms\n  max_backoff: 5ms\n",
		nil, nil)
	out, err := node.Run(context.Background(), retryEnvelope(`{"k":"v"}`))
	if err != nil {
		t.Fatalf("Run() = %v, want success on the third attempt", err)
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Fatalf("upstream calls = %d, want 3", got)
	}
	if up := out.(graph.Envelope).Upstream; up == nil || up.Status != http.StatusOK {
		t.Fatalf("upstream = %+v, want status 200", up)
	}
}

func TestHTTPRequestDoesNotRetryTerminalStatus(t *testing.T) {
	var calls int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer upstream.Close()

	node := mustHTTPRequestNode(t,
		"url: "+upstream.URL+"\nretry:\n  max_attempts: 4\n  initial_backoff: 1ms\n",
		nil, nil)
	if _, err := node.Run(context.Background(), retryEnvelope("{}")); err != nil {
		t.Fatalf("Run() = %v, want the 400 captured as the upstream response", err)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("upstream calls = %d, want 1 (a 4xx is terminal)", got)
	}
}

func TestHTTPRequestHonorsRetryAfterOn429(t *testing.T) {
	var calls int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()

	// max_backoff caps the 1s Retry-After down to 20ms so the test stays fast
	// while still proving the header was read and clamped.
	node := mustHTTPRequestNode(t,
		"url: "+upstream.URL+"\nretry:\n  max_attempts: 2\n  initial_backoff: 1ms\n  max_backoff: 20ms\n",
		nil, nil)
	start := time.Now()
	if _, err := node.Run(context.Background(), retryEnvelope("{}")); err != nil {
		t.Fatalf("Run() = %v, want success after the 429", err)
	}
	elapsed := time.Since(start)
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("upstream calls = %d, want 2", got)
	}
	if elapsed >= time.Second {
		t.Fatalf("elapsed = %v, want the 1s Retry-After clamped by max_backoff", elapsed)
	}
	if elapsed < 20*time.Millisecond {
		t.Fatalf("elapsed = %v, want at least the 20ms clamped wait", elapsed)
	}
}

func TestHTTPRequestRetryExhaustionAppliesErrorOnStatus(t *testing.T) {
	var calls int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer upstream.Close()

	node := mustHTTPRequestNode(t,
		"url: "+upstream.URL+"\nerror_on_status: [5xx]\nretry:\n  max_attempts: 3\n  initial_backoff: 1ms\n  max_backoff: 2ms\n",
		nil, nil)
	_, err := node.Run(context.Background(), retryEnvelope("{}"))
	var nodeErr graph.NodeError
	if !errors.As(err, &nodeErr) {
		t.Fatalf("Run() = %v, want a graph.NodeError after exhausting attempts", err)
	}
	if nodeErr.Status != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", nodeErr.Status)
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Fatalf("upstream calls = %d, want 3", got)
	}
}

func TestHTTPRequestRetryStopsOnCancelledContext(t *testing.T) {
	var calls int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer upstream.Close()

	ctx, cancel := context.WithCancel(context.Background())
	node := mustHTTPRequestNode(t,
		"url: "+upstream.URL+"\nretry:\n  max_attempts: 5\n  initial_backoff: 200ms\n  max_backoff: 200ms\n",
		nil, nil)
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	if _, err := node.Run(ctx, retryEnvelope("{}")); err == nil {
		t.Fatal("Run() = nil, want an error when the context is cancelled mid-backoff")
	}
	if got := atomic.LoadInt32(&calls); got > 2 {
		t.Fatalf("upstream calls = %d, want the loop to stop at cancellation", got)
	}
}

func TestHTTPRequestWithoutRetryMakesOneAttempt(t *testing.T) {
	var calls int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer upstream.Close()

	node := mustHTTPRequestNode(t, "url: "+upstream.URL+"\n", nil, nil)
	if _, err := node.Run(context.Background(), retryEnvelope("{}")); err != nil {
		t.Fatalf("Run() = %v, want the 503 captured without error_on_status", err)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("upstream calls = %d, want exactly 1 with no retry block", got)
	}
}
