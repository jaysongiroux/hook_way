package httpapi

import (
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
			req := httptest.NewRequest(http.MethodGet, target, strings.NewReader("x"))
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
			req := httptest.NewRequest(http.MethodGet, tc.target, strings.NewReader("x"))
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

	req := httptest.NewRequest(http.MethodGet, "/proxy/thing", strings.NewReader("x"))
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
