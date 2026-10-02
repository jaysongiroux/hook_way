package httpapi

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/jaysongiroux/hook_way/internal/flowconfig"
	"github.com/jaysongiroux/hook_way/internal/routing"
)

func TestManagedHTTPPrivateSessionAndExchange(t *testing.T) {
	t.Setenv("SAMPLE_CLIENT_ID", "synthetic-client")
	t.Setenv("SAMPLE_CLIENT_SECRET", "synthetic-password")
	for _, kind := range []string{"oauth_client_credentials", "token_exchange", "static"} {
		t.Run(kind, func(t *testing.T) {
			var tokens, calls atomic.Int32
			token := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n := tokens.Add(1)
				_, _ = fmt.Fprintf(w, `{"access_token":"synthetic-%d","expires_in":3600}`, n)
			}))
			defer token.Close()
			resource := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n := calls.Add(1)
				if r.Header.Get("X-Auth") != "" {
					t.Error("ingress auth forwarded")
				}
				want := "Bearer synthetic-1"
				if kind == "token_exchange" {
					want = fmt.Sprintf("Bearer synthetic-%d", n)
				}
				if kind == "static" {
					want = "Bearer synthetic-password"
				}
				if kind == "oauth_client_credentials" && n > 1 {
					want = "Bearer synthetic-2"
				}
				if r.Header.Get("Authorization") != want {
					t.Error("bad injected header", r.Header.Get("Authorization"))
				}
				if kind == "oauth_client_credentials" && n == 1 {
					w.WriteHeader(401)
					_, _ = fmt.Fprint(w, `{"code":"expired"}`)
					return
				}
				_, _ = fmt.Fprint(w, `{"ok":true}`)
			}))
			defer resource.Close()
			var options string
			switch kind {
			case "static":
				options = `headers: {Authorization: {prefix: "Bearer ", secret_ref: {provider: env, name: SAMPLE_CLIENT_SECRET}}}`
			case "oauth_client_credentials":
				options = fmt.Sprintf(`token_url: %s
    client_id: {provider: env, name: SAMPLE_CLIENT_ID}
    client_secret: {provider: env, name: SAMPLE_CLIENT_SECRET}
    invalid_session: {path: code, value: expired}`, token.URL)
			case "token_exchange":
				options = fmt.Sprintf(`token_url: %s
    token_path: access_token
    secret_parameters: {password: {provider: env, name: SAMPLE_CLIENT_SECRET}}`, token.URL)
			}
			src := fmt.Sprintf(`server: {addr: ":8080", max_body_bytes: 1024}
credentials:
  sample:
    kind: %s
    allowed_origins: [%s]
    allow_private_targets: true
    %s
flows:
  sample:
    nodes:
      - {id: in, type: http_trigger, path: /sample, methods: [POST]}
      - {id: body, type: read_body, max_bytes: 1024}
      - {id: auth, type: auth, scheme: exact, secret: ingress-only, signature_header: X-Auth}
      - {id: env, type: envelope, body_format: xml}
      - {id: script, type: code, script: 'if (JSON.stringify(input).includes("caller-credential")) throw new Error("secret leak"); return {};'}
      - {id: up, type: http_request, url: %s, allow_private_targets: true, credential: {profile: sample}, header_mode: replace, headers: {Accept: application/xml, Content-Type: application/xml}, remove_headers: [X-Auth]}
      - {id: reply, type: respond, passthrough: true}
    edges: [in>body, body>auth, auth>env, env>script, script>up, up>reply]
`, kind, resource.URL, options, resource.URL)
			var logs bytes.Buffer
			srv, e := NewFlowServer(loadFlowCfg(t, src), &fakePublisher{}, slog.New(slog.NewJSONHandler(&logs, nil)), nil)
			if e != nil {
				t.Fatal(e)
			}
			defer func() { _ = srv.Close() }()
			for range 2 {
				r := httptest.NewRequestWithContext(t.Context(), "POST", "/sample", strings.NewReader(`<sample/>`))
				r.Header.Set("X-Auth", "ingress-only")
				r.Header.Set("Authorization", "caller-credential")
				w := httptest.NewRecorder()
				srv.ServeHTTP(w, r)
				if w.Code != 200 || w.Body.String() != `{"ok":true}` {
					t.Fatal(w.Code, w.Body.String())
				}
			}
			wantCalls := int32(2)
			wantTokens := int32(0)
			if kind == "oauth_client_credentials" {
				wantCalls = 3
				wantTokens = 2
			}
			if kind == "token_exchange" {
				wantTokens = 2
			}
			if calls.Load() != wantCalls || tokens.Load() != wantTokens {
				t.Fatal(calls.Load(), tokens.Load())
			}
			if strings.Contains(logs.String(), "synthetic-password") || strings.Contains(logs.String(), "synthetic-1") || strings.Contains(logs.String(), "caller-credential") {
				t.Fatal("credential in logs")
			}
		})
	}
}

type registrationTestStore struct {
	writes  int
	outage  bool
	records []routing.Record
}

func (s *registrationTestStore) Get(context.Context, routing.Key) (routing.Record, bool, error) {
	return routing.Record{}, false, nil
}
func (s *registrationTestStore) Put(_ context.Context, r routing.Record) error {
	s.writes++
	if s.outage {
		return fmt.Errorf("sensitive fake backend error")
	}
	s.records = append(s.records, r)
	return nil
}
func TestCreateSuccessSurvivesFailedRegistrationWithoutSecondCreate(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
		status           int
		outage           bool
		writes           int
	}{{"registered", `{"data":{"id":"42"}}`, "registered", 201, false, 1}, {"pending", `{"data":{"id":"42"}}`, "pending", 200, true, 3}, {"malformed", `not json`, "unregistered", 201, false, 0}, {"missing", `{}`, "unregistered", 200, false, 0}, {"failed", `{"error":"rejected"}`, "", 409, false, 0}} {
		t.Run(tc.name, func(t *testing.T) {
			var creates atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				creates.Add(1)
				if r.Header.Get("X-Sample-Key") != "caller-credential" || r.Header.Get("Cookie") != "session=caller-cookie" {
					t.Error("required caller credentials lost")
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = fmt.Fprint(w, tc.body)
			}))
			defer upstream.Close()
			src := fmt.Sprintf(`server: {addr: "127.0.0.1:8080", max_body_bytes: 1024}
routing:
  stores: {primary: {table: synthetic-routing}}
  profiles: {sandbox: {account_id: "000000000000", credential_mode: sandbox}}
  environments: {preview: {profiles: [sandbox], accounts: ["000000000000"], destination_prefix: preview}}
flows:
  sample:
    nodes:
      - {id: in, type: http_trigger, path: /private/create, methods: [POST]}
      - {id: body, type: read_body, max_bytes: 1024}
      - {id: auth, type: auth, scheme: exact, secret: ingress-only, signature_header: X-Private-Token}
      - {id: env, type: envelope, body_format: json}
      - {id: intent, type: filter, conditions: [{source: header, path: X-Intent, operator: eq, value: record_a}], on_no_match: reject}
      - {id: route, type: route, profile: sandbox, candidates: [{source: body, path: env_name}]}
      - {id: script, type: code, script: 'if (JSON.stringify(input).includes("caller-credential") || JSON.stringify(input).includes("caller-cookie")) throw new Error("secret leak"); return {};'}
      - {id: create, type: http_request, url: %s, method: POST, require_headers: [X-Intent, X-Sample-Key, X-Sample-Version, Cookie], require_body_fields: [payload], forward_headers: [X-Sample-Key, X-Sample-Version, Cookie], header_mode: forward, remove_headers: [X-Private-Token], response_bindings: {fields: {created_id: data.id}}}
      - {id: register, type: register_route, mode: after_create, store: primary, profile: sandbox, environment_variable: route, id_source: upstream, id_path: created_id, id_type: record_a, max_attempts: 3, backoff: 1ms}
      - {id: reply, type: respond, passthrough: true}
    edges: [in>body, body>auth, auth>env, env>intent, intent>route, route>script, script>create, create>register, register>reply]
`, upstream.URL)
			store := &registrationTestStore{outage: tc.outage}
			var logs bytes.Buffer
			srv, e := newFlowServer(loadFlowCfg(t, src), &fakePublisher{}, slog.New(slog.NewJSONHandler(&logs, nil)), nil, flowconfig.RegistryOptions{RoutingStores: map[string]routing.Reader{"primary": store}})
			if e != nil {
				t.Fatal(e)
			}
			defer func() { _ = srv.Close() }()
			post := func(body, intent, key string) *httptest.ResponseRecorder {
				r := httptest.NewRequestWithContext(t.Context(), "POST", "/private/create", strings.NewReader(body))
				r.Header.Set("X-Private-Token", "ingress-only")
				r.Header.Set("X-Intent", intent)
				if intent == "duplicate" {
					r.Header.Set("X-Intent", "record_a")
					r.Header.Add("X-Intent", "record_b")
				}
				r.Header.Set("X-Sample-Key", key)
				r.Header.Set("X-Sample-Version", "v1")
				r.Header.Set("Cookie", "session=caller-cookie")
				w := httptest.NewRecorder()
				srv.ServeHTTP(w, r)
				return w
			}
			for _, invalid := range []struct{ body, intent, key string }{{`{"env_name":"production","payload":{}}`, "record_a", "caller-credential"}, {`{"env_name":"preview","payload":{}}`, "unsupported", "caller-credential"}, {`{"env_name":"preview","payload":{}}`, "record_a", ""}, {`{"env_name":"preview"}`, "record_a", "caller-credential"}, {`{"env_name":"preview","payload":{}}`, "duplicate", "caller-credential"}} {
				w := post(invalid.body, invalid.intent, invalid.key)
				if w.Code < 400 || creates.Load() != 0 || store.writes != 0 {
					t.Fatal("preflight reached create", w.Code, creates.Load(), store.writes)
				}
			}
			w := post(`{"env_name":"preview","payload":{}}`, "record_a", "caller-credential")
			if w.Code != tc.status || w.Body.String() != tc.body || w.Header().Get("X-Routing-Registration") != tc.want || creates.Load() != 1 || store.writes != tc.writes {
				t.Fatal(w.Code, w.Body.String(), w.Header(), creates.Load(), store.writes)
			}
			if strings.Contains(logs.String(), "sensitive fake backend") || strings.Contains(logs.String(), "caller-credential") || strings.Contains(logs.String(), "caller-cookie") {
				t.Fatal("unsafe recovery log", logs.String())
			}
			if tc.want == "pending" {
				if !strings.Contains(logs.String(), `"id":"42"`) || !strings.Contains(logs.String(), `"env_name":"preview"`) || !strings.Contains(logs.String(), `"request_id"`) {
					t.Fatal("missing recovery context", logs.String())
				}
				m := httptest.NewRecorder()
				promhttp.HandlerFor(srv.metrics.Registry(), promhttp.HandlerOpts{}).ServeHTTP(m, httptest.NewRequestWithContext(t.Context(), "GET", "/metrics", nil))
				if !strings.Contains(m.Body.String(), `hookway_routing_total{node="register",result="registration_pending"} 1`) {
					t.Fatal("missing pending metric")
				}
			}
		})
	}
}

func TestRegistrationNotificationRepeatedWithoutUpstream(t *testing.T) {
	t.Setenv("SAMPLE_PRIVATE_TOKEN", "synthetic-ingress")
	source, e := os.ReadFile("../../examples/routing/registration.yaml")
	if e != nil {
		t.Fatal(e)
	}
	store := &registrationTestStore{}
	srv, e := newFlowServer(loadFlowCfg(t, string(source)), &fakePublisher{}, nil, nil, flowconfig.RegistryOptions{RoutingStores: map[string]routing.Reader{"primary": store}})
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = srv.Close() }()
	for _, tc := range []struct {
		body, intent string
		status       int
	}{{`{"env_name":"preview","id":"friendly-42"}`, "record_c", 200}, {`{"env_name":"staging","id":"friendly-42"}`, "record_c", 200}, {`{"env_name":"production","id":"friendly-42"}`, "record_c", 403}, {`{"env_name":"preview","id":"bad:id"}`, "record_c", 400}, {`{"env_name":"preview","id":"friendly-42"}`, "wrong", 400}} {
		r := httptest.NewRequestWithContext(t.Context(), "POST", "/private/register", strings.NewReader(tc.body))
		r.Header.Set("X-Private-Token", "synthetic-ingress")
		r.Header.Set("X-Intent", tc.intent)
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatal(w.Code, w.Body.String(), tc)
		}
		if tc.status == 200 && w.Body.String() != "registered friendly-42" {
			t.Fatal(w.Body.String())
		}
	}
	if store.writes != 2 || store.records[0].IDType != "record_c" || store.records[1].Environment != "preview" {
		t.Fatal(store)
	}
	duplicate := httptest.NewRequestWithContext(t.Context(), "POST", "/private/register", strings.NewReader(`{"env_name":"preview","id":"friendly-42"}`))
	duplicate.Header.Set("X-Private-Token", "synthetic-ingress")
	duplicate.Header.Set("X-Intent", "record_c")
	duplicate.Header.Add("X-Intent", "other")
	duplicateReply := httptest.NewRecorder()
	srv.ServeHTTP(duplicateReply, duplicate)
	if duplicateReply.Code != 400 || store.writes != 2 {
		t.Fatal("ambiguous intent reached store", duplicateReply.Code, store.writes)
	}
	store.outage = true
	r := httptest.NewRequestWithContext(t.Context(), "POST", "/private/register", strings.NewReader(`{"env_name":"preview","id":"friendly-42"}`))
	r.Header.Set("X-Private-Token", "synthetic-ingress")
	r.Header.Set("X-Intent", "record_c")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	if w.Code != 503 {
		t.Fatal("notification falsely succeeded", w.Code)
	}
}
func TestEnvironmentCredentialSelectionCannotUseUnadmittedName(t *testing.T) {
	source, e := os.ReadFile("../../examples/credentials/static.yaml")
	if e != nil {
		t.Fatal(e)
	}
	cfg := loadFlowCfg(t, string(source))
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("X-Sample-Key") != "preview-key" {
			t.Error("wrong environment key")
		}
		w.WriteHeader(200)
	}))
	defer upstream.Close()
	u, _ := url.Parse(upstream.URL)
	port, _ := strconv.Atoi(u.Port())
	binding := cfg.Routing.Environments["preview"]
	binding.BackendHost = u.Hostname()
	cfg.Routing.Environments["preview"] = binding
	flow := cfg.Flows["managed_proxy"]
	for i, nc := range flow.Nodes {
		if nc.Type != "http_request" {
			continue
		}
		var opts map[string]any
		if err := nc.Options.Decode(&opts); err != nil {
			t.Fatal(err)
		}
		opts["upstream"].(map[string]any)["scheme"] = "http"
		opts["upstream"].(map[string]any)["port"] = port
		opts["allow_private_targets"] = true
		if err := nc.Options.Encode(opts); err != nil {
			t.Fatal(err)
		}
		flow.Nodes[i] = nc
	}
	cfg.Flows["managed_proxy"] = flow
	c := cfg.Credentials["preview_key"]
	c.AllowedOrigins = []string{upstream.URL}
	c.AllowPrivateTargets = true
	cfg.Credentials["preview_key"] = c
	// Retain the unused production profile as a native-bound env secret for this fixture.
	c = cfg.Credentials["production_key"]
	c.Headers = map[string]flowconfig.CredentialHeader{"X-Sample-Key": {SecretRef: flowconfig.SecretRefOptions{Provider: "env", Name: "SAMPLE_PRODUCTION_KEY"}}}
	cfg.Credentials["production_key"] = c
	t.Setenv("SAMPLE_PREVIEW_KEY", "preview-key")
	t.Setenv("SAMPLE_PRODUCTION_KEY", "production-key")
	t.Setenv("SAMPLE_INGRESS_TOKEN", "synthetic-ingress")
	srv, e := NewFlowServer(cfg, &fakePublisher{}, nil, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = srv.Close() }()
	for _, tc := range []struct {
		name   string
		status int
	}{{"staging", 200}, {"unknown", 403}, {"preview-key", 403}} {
		r := httptest.NewRequestWithContext(t.Context(), "GET", "/proxy/managed/sample", nil)
		r.Header.Set("X-Auth", "synthetic-ingress")
		r.Header.Set("X-Env", tc.name)
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatal(tc, w.Code, w.Body.String())
		}
	}
	if calls.Load() != 1 {
		t.Fatal("unadmitted environment reached credential target", calls.Load())
	}
}
