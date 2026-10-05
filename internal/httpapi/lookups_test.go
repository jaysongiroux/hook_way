package httpapi

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jaysongiroux/hook_way/internal/flowconfig"
	"github.com/jaysongiroux/hook_way/internal/metrics"
)

func standaloneLookupYAML(url, extra string) string {
	return fmt.Sprintf(`server: {addr: ":8080", max_body_bytes: 1048576}
lookups:
  tenant:
    timeout: 100ms
    cache: {ttl: 5m, max_entries: 2}
    http:
      url: '%s/{key}'
      allow_private_targets: true
      value_path: tenant
      %s
flows:
  demo:
    nodes:
    - {id: in, type: http_trigger, path: /demo, methods: [POST]}
    - {id: body, type: read_body, max_bytes: 1048576}
    - {id: auth, type: no_auth}
    - {id: env, type: envelope}
    - {id: find, type: lookup, provider: tenant, key: '{body.id}', name: tenant}
    - {id: out, type: respond, status: 200}
    edges: [in>body,body>auth,auth>env,env>find,find>out]
`, url, extra)
}
func lookupRequest(t *testing.T, s *FlowServer, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequestWithContext(t.Context(), "POST", "/demo", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}

func TestStandaloneLookupHTTPSecretsCacheAndShutdown(t *testing.T) {
	t.Setenv("LOOKUP_TEST_TOKEN", "synthetic-token")
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer synthetic-token" {
			t.Errorf("missing secret header")
		}
		if r.URL.Path != "/a/b?c&d" {
			t.Errorf("key not encoded safely: %s", r.URL.String())
		}
		if r.URL.RawQuery != "" {
			t.Errorf("key injected query: %s", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"tenant":"sandbox"}`))
	}))
	defer upstream.Close()
	cfg := loadFlowCfg(t, standaloneLookupYAML(upstream.URL, `secret_headers: {Authorization: {prefix: 'Bearer ', secret_ref: {provider: env, name: LOOKUP_TEST_TOKEN}}}`))
	srv, e := NewFlowServer(cfg, &fakePublisher{}, nil, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = srv.Close() }()
	for range 2 {
		w := lookupRequest(t, srv, `{"id":"a/b?c&d"}`)
		if w.Code != 200 {
			t.Fatalf("%d %s", w.Code, w.Body.String())
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("cache made %d calls", calls.Load())
	}
	if e := srv.Close(); e != nil {
		t.Fatal(e)
	}
}

func TestStandaloneLookupDeniesRedirectAndPrivateDNS(t *testing.T) {
	var targetCalls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetCalls.Add(1)
		_, _ = w.Write([]byte(`{"tenant":"forbidden"}`))
	}))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) }))
	defer redirect.Close()
	for _, tc := range []struct {
		name, url string
		guard     bool
	}{{"redirect", redirect.URL, false}, {"private DNS", strings.Replace(target.URL, "127.0.0.1", "localhost", 1), true}} {
		t.Run(tc.name, func(t *testing.T) {
			src := standaloneLookupYAML(tc.url, "")
			cfg := loadFlowCfg(t, src)
			if tc.guard {
				c := cfg.Lookups["tenant"]
				c.HTTP.AllowPrivateTargets = false
				bind, closers, e := bindLookups(map[string]flowconfig.LookupProviderConfig{"tenant": c}, nil, metrics.New())
				if e != nil {
					t.Fatal(e)
				}
				for _, closer := range closers {
					defer func() { _ = closer.Close() }()
				}
				_, _, e = bind(flowconfig.LookupOptions{Provider: "tenant"})(t.Context(), "secret-key")
				if e == nil {
					t.Fatal("DNS-resolved private address allowed")
				}
				if targetCalls.Load() != 0 {
					t.Fatal("private target reached")
				}
				return
			}
			srv, e := NewFlowServer(cfg, &fakePublisher{}, nil, nil)
			if e != nil {
				t.Fatal(e)
			}
			defer func() { _ = srv.Close() }()
			if w := lookupRequest(t, srv, `{"id":"secret-key"}`); w.Code == 200 {
				t.Fatal("redirect allowed")
			}
			if targetCalls.Load() != 0 {
				t.Fatal("redirect target reached")
			}
		})
	}
}

func TestStandaloneLookupTimeoutAndMissingSecret(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer upstream.Close()
	src := standaloneLookupYAML(upstream.URL, "")
	srv, e := NewFlowServer(loadFlowCfg(t, src), &fakePublisher{}, nil, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = srv.Close() }()
	started := time.Now()
	if w := lookupRequest(t, srv, `{"id":"timeout"}`); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("lookup timeout status %d, want 503", w.Code)
	}
	if time.Since(started) > time.Second {
		t.Fatal("lookup timeout not bounded")
	}
	t.Setenv("LOOKUP_TEST_MISSING", "")
	src = standaloneLookupYAML(upstream.URL, `secret_headers: {Authorization: {secret_ref: {provider: env, name: LOOKUP_TEST_MISSING}}}`)
	if _, e := NewFlowServer(loadFlowCfg(t, src), &fakePublisher{}, nil, nil); e == nil {
		t.Fatal("missing lookup secret accepted at startup")
	}
}

func TestStandaloneLookupRefreshesHeaderSecret(t *testing.T) {
	t.Setenv("LOOKUP_ROTATING_TOKEN", "before")
	var seen atomic.Value
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.Store(r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(`{"tenant":"sandbox"}`))
	}))
	defer upstream.Close()
	src := standaloneLookupYAML(upstream.URL, `secret_headers: {Authorization: {prefix: 'Bearer ', secret_ref: {provider: env, name: LOOKUP_ROTATING_TOKEN, refresh: 5ms}}}`)
	srv, e := NewFlowServer(loadFlowCfg(t, src), &fakePublisher{}, nil, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = srv.Close() }()
	if w := lookupRequest(t, srv, `{"id":"initial"}`); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if seen.Load() != "Bearer before" {
		t.Fatal("initial secret missing")
	}
	t.Setenv("LOOKUP_ROTATING_TOKEN", "after")
	deadline := time.Now().Add(2 * time.Second)
	for i := 0; time.Now().Before(deadline); i++ {
		if w := lookupRequest(t, srv, fmt.Sprintf(`{"id":"fresh-%d"}`, i)); w.Code != 200 {
			t.Fatal(w.Code)
		}
		if seen.Load() == "Bearer after" {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("refreshed secret was not used on cache miss")
}
