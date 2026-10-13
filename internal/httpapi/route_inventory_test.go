package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
)

// routeInventoryFlowYAML is a one-flow config whose only trigger is
// /hooks/github. Everything else the public listener answers is either a
// health endpoint or an accident.
func routeInventoryFlowYAML(metricsBlock string) string {
	return `
server: {addr: ":8080", max_body_bytes: 1024}
` + metricsBlock + `
destinations: {}
flows:
  github:
    nodes:
      - {id: in, type: http_trigger, path: /hooks/github, methods: [POST]}
      - {id: body, type: read_body, max_bytes: 1024}
      - {id: auth, type: auth, scheme: exact, secret: testtoken, signature_header: X-Auth}
      - {id: env, type: envelope, body_format: json}
      - {id: resp, type: respond, status: 202}
    edges: [in>body, body>auth, auth>env, env>resp]
`
}

// TestPublicListenerExposesOnlyTheIntendedRoutes pins the attack surface of
// the internet-facing listener.
//
// net/http.ServeMux cannot be enumerated, so the inventory is taken by
// probing: a path is "registered" when its response differs from the flow
// catch-all's 404. The comparison is an exact set equality on purpose -- this
// test is supposed to fail the moment anyone mounts a new handler (pprof,
// expvar, a replay or config-editor endpoint) on the public port, so that
// exposure is a deliberate decision and not a merge accident.
func TestPublicListenerExposesOnlyTheIntendedRoutes(t *testing.T) {
	const triggerPath = "/hooks/github"

	// Paths that must never be reachable on the public listener.
	mustBeCatchAll := []string{
		"/debug/pprof/",
		"/debug/pprof/profile",
		"/debug/vars",
		"/replay/events",
		"/schema",
		"/validate",
		"/node-schema",
		"/internal/anything",
	}
	probes := append([]string{
		"/health", "/healthz", "/ready", "/readyz",
		"/metrics", triggerPath,
	}, mustBeCatchAll...)

	cases := []struct {
		name         string
		metricsBlock string
		want         []string
	}{
		{
			name:         "metrics off",
			metricsBlock: "",
			want:         []string{"/health", "/healthz", "/ready", "/readyz", triggerPath},
		},
		{
			name:         "metrics on the public port",
			metricsBlock: "metrics: {enabled: true, path: /metrics}\n",
			want:         []string{"/health", "/healthz", "/metrics", "/ready", "/readyz", triggerPath},
		},
		{
			name:         "metrics on a dedicated listener",
			metricsBlock: "metrics: {enabled: true, path: /metrics, addr: \":9090\"}\n",
			want:         []string{"/health", "/healthz", "/ready", "/readyz", triggerPath},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, err := NewFlowServer(loadFlowCfg(t, routeInventoryFlowYAML(tc.metricsBlock)), &fakePublisher{}, slog.New(slog.DiscardHandler), nil)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = srv.Close() })

			probe := func(path string) (int, string) {
				req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, nil)
				rec := httptest.NewRecorder()
				srv.ServeHTTP(rec, req)
				return rec.Code, rec.Body.String()
			}

			// The catch-all's own answer, taken from a path nothing could
			// plausibly register.
			notFoundCode, notFoundBody := probe("/f3a1c0-no-such-route")
			if notFoundCode != http.StatusNotFound {
				t.Fatalf("catch-all returned %d, want 404", notFoundCode)
			}

			var reachable []string
			for _, path := range probes {
				code, body := probe(path)
				if code != notFoundCode || body != notFoundBody {
					reachable = append(reachable, path)
				}
			}
			sort.Strings(reachable)

			want := append([]string(nil), tc.want...)
			sort.Strings(want)
			if strings.Join(reachable, ",") != strings.Join(want, ",") {
				t.Fatalf("reachable routes = %v, want exactly %v", reachable, want)
			}

			for _, path := range mustBeCatchAll {
				code, body := probe(path)
				if code != notFoundCode || body != notFoundBody {
					t.Fatalf("%s is reachable (status %d, body %q); it must hit the flow catch-all 404", path, code, body)
				}
			}
		})
	}
}
