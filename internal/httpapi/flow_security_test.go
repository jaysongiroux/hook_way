package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// proxyFlowYAML builds a sync-proxy flow that confines callers to an upstream
// /safe/ prefix -- the access-control boundary the traversal tests probe.
func proxyFlowYAML(upstreamURL string) string {
	return `
server: {addr: ":8080", max_body_bytes: 1048576}
destinations: {}
flows:
  proxy:
    nodes:
      - {id: in, type: http_trigger, path: "/proxy/{rest...}", methods: [ANY]}
      - {id: body, type: read_body, max_bytes: 1048576}
      - {id: auth, type: auth, scheme: exact, secret: testtoken, signature_header: X-Auth}
      - {id: env, type: envelope, body_format: raw}
      - {id: up, type: http_request, url: "` + upstreamURL + `/safe/{rest}", forward_method: true}
      - {id: resp, type: respond, passthrough: true}
    edges: [in>body, body>auth, auth>env, env>up, up>resp]
`
}

// suffixProxyFlowYAML builds a sync-proxy flow whose upstream template puts a
// mandatory literal suffix AFTER the captured variable. That suffix is the
// access-control boundary: "/tenants/{tenant}/readonly" is what confines a
// caller to the read-only view of one tenant. Anything that truncates the
// rendered URL before "/readonly" is a privilege escalation.
func suffixProxyFlowYAML(upstreamURL string) string {
	return `
server: {addr: ":8080", max_body_bytes: 1048576}
destinations: {}
flows:
  proxy:
    nodes:
      - {id: in, type: http_trigger, path: "/proxy/{tenant}", methods: [ANY]}
      - {id: body, type: read_body, max_bytes: 1048576}
      - {id: auth, type: auth, scheme: exact, secret: testtoken, signature_header: X-Auth}
      - {id: env, type: envelope, body_format: raw}
      - {id: up, type: http_request, url: "` + upstreamURL + `/tenants/{tenant}/readonly", forward_method: true}
      - {id: resp, type: respond, passthrough: true}
    edges: [in>body, body>auth, auth>env, env>up, up>resp]
`
}

// TestProxyRejectsEncodedURLDelimitersInPathVars is a security regression test.
//
// URL.Path is already percent-decoded when a handler sees it, so "%23" becomes
// "#" and "%3F" becomes "?" inside the captured {tenant}. Before the fix,
// url.Parse then re-read those as fragment/query delimiters while building the
// outbound request:
//
//	tenant="acme#"      -> upstream path "/tenants/acme"  (/readonly DROPPED)
//	tenant="acme?x=1&"  -> upstream path "/tenants/acme", query "x=1&/readonly"
//
// Either way the mandatory "/readonly" suffix is gone and the caller reaches a
// broader upstream resource than the config allows.
func TestProxyRejectsEncodedURLDelimitersInPathVars(t *testing.T) {
	type hit struct{ escapedPath, decodedPath, query string }
	var upstreamHits []hit
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHits = append(upstreamHits, hit{
			escapedPath: r.URL.EscapedPath(),
			decodedPath: r.URL.Path,
			query:       r.URL.RawQuery,
		})
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(upstream.Close)

	srv, err := NewFlowServer(loadFlowCfg(t, suffixProxyFlowYAML(upstream.URL)), &fakePublisher{}, slog.New(slog.DiscardHandler), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	cases := []struct {
		target string
		// wantEscapedPath is the exact upstream path a forwarded request must
		// carry. Empty means the request must be rejected without ever
		// reaching the upstream.
		wantEscapedPath string
	}{
		{target: "/proxy/acme%23"},
		{target: "/proxy/acme%23/readonly"},
		{target: "/proxy/acme%3Fx=1%26"},
		{target: "/proxy/acme%3f%23"},
		{target: "/proxy/acme%00"},
		{target: "/proxy/acme%5c..%5cadmin"},
		// Double-encoded: the capture is the literal text "%2e%2e%2fadmin".
		// It must reach the upstream still encoded -- decoding it a second
		// time anywhere on the way out would manufacture a real traversal.
		{target: "/proxy/%252e%252e%252fadmin", wantEscapedPath: "/tenants/%2e%2e%2fadmin/readonly"},
	}
	for _, tc := range cases {
		t.Run(tc.target, func(t *testing.T) {
			upstreamHits = nil
			req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, tc.target, strings.NewReader("x"))
			req.Header.Set("X-Auth", "testtoken")
			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, req)

			for _, h := range upstreamHits {
				if !strings.HasSuffix(h.escapedPath, "/readonly") {
					t.Fatalf("upstream path = %q (query %q): the mandatory /readonly suffix was dropped -- access control bypassed", h.escapedPath, h.query)
				}
				if !strings.HasPrefix(h.escapedPath, "/tenants/") {
					t.Fatalf("upstream path = %q: escaped the /tenants/ prefix", h.escapedPath)
				}
				if h.query != "" {
					t.Fatalf("upstream query = %q, want empty: forward_query is off, so no query may be injected", h.query)
				}
			}

			if tc.wantEscapedPath == "" {
				if len(upstreamHits) != 0 {
					t.Fatalf("upstream was reached at %v; the request must not be proxied", upstreamHits)
				}
				// 400 is the path-variable rejection. 404 is the earlier
				// rejection for inputs whose decoded path no longer matches
				// the single-segment trigger (for example "acme#/readonly").
				if rec.Code != http.StatusBadRequest && rec.Code != http.StatusNotFound {
					t.Fatalf("status = %d, want 400 or 404 for a rejected request", rec.Code)
				}
				return
			}
			if len(upstreamHits) != 1 {
				t.Fatalf("upstream hits = %v, want exactly one forwarded request", upstreamHits)
			}
			if got := upstreamHits[0].escapedPath; got != tc.wantEscapedPath {
				t.Fatalf("upstream escaped path = %q, want %q (decoded %q)",
					got, tc.wantEscapedPath, upstreamHits[0].decodedPath)
			}
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
		})
	}
}

// TestProxyRejectsEncodedPathTraversal is a security regression test.
//
// net/http hands handlers an already percent-decoded URL.Path. Go's ServeMux
// redirects a literal "/proxy/../admin", but "%2e%2e%2f" only becomes "../"
// after routing -- so before the fix, a greedy {rest...} capture carried the
// traversal straight into the upstream URL ("/safe/../../admin"). Upstreams
// normalize that to "/admin", escaping the /safe/ prefix the config exists to
// enforce.
//
// The upstream handler fails the test if it is reached at all: asserting on
// the response status alone would still pass if the request were forwarded and
// merely happened to 404.
func TestProxyRejectsEncodedPathTraversal(t *testing.T) {
	var upstreamHits []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHits = append(upstreamHits, r.URL.Path)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(upstream.Close)

	srv, err := NewFlowServer(loadFlowCfg(t, proxyFlowYAML(upstream.URL)), &fakePublisher{}, slog.New(slog.DiscardHandler), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	attacks := []string{
		"/proxy/..%2f..%2fadmin",
		"/proxy/%2e%2e%2fadmin",
		"/proxy/a%2f..%2f..%2fadmin",
		"/proxy/%2E%2E/admin",
		"/proxy/..%2fadmin",
	}
	for _, target := range attacks {
		t.Run(target, func(t *testing.T) {
			upstreamHits = nil
			req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, target, strings.NewReader("x"))
			req.Header.Set("X-Auth", "testtoken")
			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, req)

			for _, hit := range upstreamHits {
				if strings.Contains(hit, "..") {
					t.Fatalf("upstream received %q: traversal escaped the /safe/ prefix", hit)
				}
			}
			if len(upstreamHits) > 0 {
				t.Fatalf("upstream was reached at %v; a traversal attempt must not be proxied", upstreamHits)
			}
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 for a traversal attempt", rec.Code)
			}
		})
	}
}

// TestProxyForwardsLegitimateNestedPaths guards the other direction: the
// traversal check must not break ordinary nested proxy paths, including names
// that merely contain dots.
func TestProxyForwardsLegitimateNestedPaths(t *testing.T) {
	var gotPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(upstream.Close)

	srv, err := NewFlowServer(loadFlowCfg(t, proxyFlowYAML(upstream.URL)), &fakePublisher{}, slog.New(slog.DiscardHandler), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	for _, tc := range []struct{ target, wantPath string }{
		{"/proxy/api/v1/thing", "/safe/api/v1/thing"},
		{"/proxy/a..b", "/safe/a..b"},
		{"/proxy/..hidden/file", "/safe/..hidden/file"},
	} {
		t.Run(tc.target, func(t *testing.T) {
			gotPath = ""
			req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, tc.target, strings.NewReader("x"))
			req.Header.Set("X-Auth", "testtoken")
			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 for a legitimate path", rec.Code)
			}
			if gotPath != tc.wantPath {
				t.Fatalf("upstream path = %q, want %q", gotPath, tc.wantPath)
			}
		})
	}
}

// TestProxyDoesNotForwardInboundAuthorization keeps a caller credential from
// leaking to a third-party upstream. The inbound Authorization header
// authenticates the caller TO Hookway; forwarding it hands that credential to
// whatever the proxy points at.
func TestProxyDoesNotForwardInboundAuthorization(t *testing.T) {
	var gotAuth, gotCookie string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotCookie = r.Header.Get("Cookie")
		w.Header().Set("Set-Cookie", "session=attacker; Path=/")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok")
	}))
	t.Cleanup(upstream.Close)

	srv, err := NewFlowServer(loadFlowCfg(t, proxyFlowYAML(upstream.URL)), &fakePublisher{}, slog.New(slog.DiscardHandler), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/proxy/thing", strings.NewReader("x"))
	req.Header.Set("X-Auth", "testtoken")
	req.Header.Set("Authorization", "Bearer caller-credential")
	req.Header.Set("Cookie", "session=caller")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if gotAuth != "" {
		t.Errorf("upstream saw Authorization = %q; the inbound caller credential must not be forwarded", gotAuth)
	}
	if gotCookie != "" {
		t.Errorf("upstream saw Cookie = %q; inbound cookies must not be forwarded", gotCookie)
	}
	// An upstream must not be able to set a cookie on Hookway's origin.
	if got := rec.Header().Get("Set-Cookie"); got != "" {
		t.Errorf("response carried Set-Cookie = %q from upstream; it must be stripped", got)
	}
}
