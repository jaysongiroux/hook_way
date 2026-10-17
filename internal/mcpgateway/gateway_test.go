package mcpgateway

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jaysongiroux/hook_way/internal/flowconfig"
	"github.com/jaysongiroux/hook_way/internal/graph"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestOAuthIsolationDiscoveryAndKeyRotation(t *testing.T) {
	signing, e := rsa.GenerateKey(rand.Reader, 2048)
	if e != nil {
		t.Fatal(e)
	}
	var calls atomic.Int32
	var issuer string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			_ = json.NewEncoder(w).Encode(map[string]any{"issuer": issuer, "jwks_uri": issuer + "/keys"})
		case "/keys":
			calls.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]any{"kty": "RSA", "kid": "first", "alg": "RS256", "n": base64.RawURLEncoding.EncodeToString(signing.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(signing.E)).Bytes())}}})
		default:
			w.WriteHeader(404)
		}
	}))
	defer ts.Close()
	issuer = ts.URL
	a, e := newAuthenticator(flowconfig.MCPAuthOptions{OAuth: &flowconfig.MCPOAuthOptions{Issuer: issuer, Audience: "hookway-only", RolesClaim: "roles", AllowPrivateTargets: true}})
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = a.Close() }()
	token := func(patch jwt.MapClaims, method jwt.SigningMethod) string {
		claims := jwt.MapClaims{"iss": issuer, "aud": "hookway-only", "sub": "alice", "exp": time.Now().Add(time.Minute).Unix(), "scope": "usage:read", "roles": []string{"reader"}}
		for k, v := range patch {
			claims[k] = v
		}
		tok := jwt.NewWithClaims(method, claims)
		tok.Header["kid"] = "first"
		var key any = signing
		if method == jwt.SigningMethodHS256 {
			key = []byte("unsafe")
		}
		s, err := tok.SignedString(key)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	verify := func(raw string) (principal, error) {
		r := httptest.NewRequestWithContext(context.Background(), "POST", "/mcp", nil)
		r.Header.Set("Authorization", "Bearer "+raw)
		return a.verify(context.Background(), r)
	}
	good := token(nil, jwt.SigningMethodRS256)
	p, e := verify(good)
	if e != nil {
		t.Fatal(e)
	}
	if p.ID == "" || !allowed(p, flowconfig.MCPToolOptions{ReadOnly: true, Access: flowconfig.MCPAccessOptions{Scopes: []string{"usage:read"}, Roles: []string{"reader"}}}) {
		t.Fatal("identity/grants lost")
	}
	for name, claims := range map[string]jwt.MapClaims{"audience": {"aud": "another-api"}, "issuer": {"iss": "https://other.example"}, "expired": {"exp": time.Now().Add(-time.Minute).Unix()}, "missing_exp": {"exp": nil}, "subject": {"sub": ""}, "not_yet_valid": {"nbf": time.Now().Add(time.Hour).Unix()}} {
		t.Run(name, func(t *testing.T) {
			if _, e := verify(token(claims, jwt.SigningMethodRS256)); e == nil {
				t.Fatal("invalid token accepted")
			}
		})
	}
	if _, e := verify(token(nil, jwt.SigningMethodHS256)); e == nil {
		t.Fatal("algorithm confusion")
	}
	if _, e := verify(good + "broken"); e == nil {
		t.Fatal("tampered token accepted")
	}
	if calls.Load() != 1 {
		t.Fatal("JWKS not cached")
	}
	a.expires = time.Time{}
	a.refreshAfter = time.Time{}
	if _, e := verify(good); e != nil {
		t.Fatal(e)
	}
	if calls.Load() != 2 {
		t.Fatal("JWKS did not refresh")
	}
	ts.Close()
	a.expires = time.Time{}
	a.refreshAfter = time.Time{}
	if _, e := verify(good); e == nil {
		t.Fatal("expired validation cache accepted during outage")
	}
}
func TestOpaqueIntrospectionAndCredentialIsolation(t *testing.T) {
	t.Setenv("MCP_INTROSPECT_ID", "client")
	t.Setenv("MCP_INTROSPECT_SECRET", "secret")
	var issuer string
	active := true
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, secret, ok := r.BasicAuth()
		if !ok || id != "client" || secret != "secret" {
			t.Error("introspection credentials missing")
		}
		if e := r.ParseForm(); e != nil {
			t.Error(e)
		}
		if r.Form.Get("token") != "opaque" && r.Form.Get("token") != "opaque.with.dots" {
			t.Error("token missing")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"active": active, "iss": issuer, "aud": "hookway", "sub": "alice", "exp": time.Now().Add(time.Minute).Unix(), "scope": "read"})
	}))
	defer ts.Close()
	issuer = ts.URL
	a, e := newAuthenticator(flowconfig.MCPAuthOptions{OAuth: &flowconfig.MCPOAuthOptions{Issuer: issuer, Audience: "hookway", IntrospectionURL: ts.URL, AllowPrivateTargets: true, ClientID: &flowconfig.SecretRefOptions{Provider: "env", Name: "MCP_INTROSPECT_ID"}, ClientSecret: &flowconfig.SecretRefOptions{Provider: "env", Name: "MCP_INTROSPECT_SECRET"}}})
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = a.Close() }()
	r := httptest.NewRequestWithContext(context.Background(), "POST", "/mcp", nil)
	for _, token := range []string{"opaque", "opaque.with.dots"} {
		r.Header.Set("Authorization", "Bearer "+token)
		if _, e := a.verify(context.Background(), r); e != nil {
			t.Fatalf("token %q: %v", token, e)
		}
	}
	active = false
	if _, e := a.verify(context.Background(), r); e == nil {
		t.Fatal("inactive token accepted")
	}
}

func TestConfiguredConcurrencyAboveDefault(t *testing.T) {
	const requests = 40
	tool := testTool()
	tool.MaxInflight = requests
	tool.Access = flowconfig.MCPAccessOptions{}
	keys := map[string]flowconfig.MCPKeyOptions{}
	for i := range requests {
		name := fmt.Sprintf("caller_%d", i)
		env := fmt.Sprintf("MCP_CONCURRENT_%d", i)
		t.Setenv(env, fmt.Sprintf("caller-%d-012345678901234567890123456789", i))
		keys[name] = flowconfig.MCPKeyOptions{SecretRef: flowconfig.SecretRefOptions{Provider: "env", Name: env}}
		tool.Access.APIKeys = append(tool.Access.APIKeys, name)
	}
	entered := make(chan struct{}, requests)
	release := make(chan struct{})
	g, err := New(Options{
		Config: flowconfig.MCPTriggerOptions{Path: "/mcp", PublicURL: "http://localhost/mcp", Auth: flowconfig.MCPAuthOptions{APIKeys: keys}, Tools: []flowconfig.MCPToolOptions{tool}, MaxInflight: requests},
		Run: func(ctx context.Context, _ graph.Envelope) (graph.Terminal, error) {
			entered <- struct{}{}
			select {
			case <-release:
				return graph.Terminal{Result: &graph.Result{JSON: []byte(`{"ok":true}`)}}, nil
			case <-ctx.Done():
				return graph.Terminal{}, ctx.Err()
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = g.Close() }()
	var wg sync.WaitGroup
	defer func() { close(release); wg.Wait() }()
	finished := make(chan int, requests)
	for i := range requests {
		wg.Go(func() {
			r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}},"name":"test","arguments":{}}}`))
			r.Header.Set("Authorization", fmt.Sprintf("Bearer caller-%d-012345678901234567890123456789", i))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Accept", "application/json, text/event-stream")
			r.Header.Set("MCP-Protocol-Version", "2026-07-28")
			r.Header.Set("MCP-Method", "tools/call")
			r.Header.Set("MCP-Name", "test")
			w := httptest.NewRecorder()
			g.ServeHTTP(w, r)
			if w.Code != http.StatusOK {
				t.Errorf("caller %d: status=%d body=%s", i, w.Code, w.Body.String())
			}
			finished <- w.Code
		})
	}
	for range requests {
		select {
		case <-entered:
		case status := <-finished:
			t.Fatalf("call ended before all %d configured slots were used: %d", requests, status)
		case <-time.After(5 * time.Second):
			t.Fatal("configured concurrency could not be reached")
		}
	}
}
func testTool() flowconfig.MCPToolOptions {
	return flowconfig.MCPToolOptions{Name: "test", Description: "Test operation", ReadOnly: true, Access: flowconfig.MCPAccessOptions{Anonymous: true}, InputSchema: map[string]any{"type": "object", "additionalProperties": false}, OutputSchema: map[string]any{"type": "object", "required": []any{"ok"}, "properties": map[string]any{"ok": map[string]any{"type": "boolean"}}}}
}

func TestNewRejectsInvalidRuntimeConfiguration(t *testing.T) {
	for name, change := range map[string]func(*Options){
		"negative capacity":      func(o *Options) { o.Config.MaxInflight = -1 },
		"negative tool capacity": func(o *Options) { o.Config.Tools[0].MaxInflight = -1 },
		"missing runner":         func(o *Options) { o.Run = nil },
		"missing auth":           func(o *Options) { o.Config.Auth = flowconfig.MCPAuthOptions{} },
		"missing introspection references": func(o *Options) {
			o.Config.Auth = flowconfig.MCPAuthOptions{OAuth: &flowconfig.MCPOAuthOptions{Issuer: "https://identity.example", Audience: "hookway", IntrospectionURL: "https://identity.example/introspect"}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			o := Options{Config: flowconfig.MCPTriggerOptions{Path: "/mcp", PublicURL: "http://localhost/mcp", Auth: flowconfig.MCPAuthOptions{NoAuth: true}, Tools: []flowconfig.MCPToolOptions{testTool()}}, Run: func(context.Context, graph.Envelope) (graph.Terminal, error) { return graph.Terminal{}, nil }}
			change(&o)
			g, err := New(o)
			if err == nil {
				_ = g.Close()
				t.Fatal("invalid runtime configuration accepted")
			}
		})
	}
}
func testGateway(t *testing.T, tool flowconfig.MCPToolOptions, run Runner) *Gateway {
	t.Helper()
	g, e := New(Options{Config: flowconfig.MCPTriggerOptions{Path: "/mcp", PublicURL: "http://localhost/mcp", Auth: flowconfig.MCPAuthOptions{NoAuth: true}, Tools: []flowconfig.MCPToolOptions{tool}}, Run: run})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = g.Close() })
	return g
}
func TestCallValidationDeadlinesLimitsAndSafeErrors(t *testing.T) {
	var calls atomic.Int32
	tool := testTool()
	g := testGateway(t, tool, func(ctx context.Context, env graph.Envelope) (graph.Terminal, error) {
		calls.Add(1)
		if env.V.Raw.Head.Header.Get("Authorization") != "" {
			t.Fatal("secret entered envelope")
		}
		return graph.Terminal{Result: &graph.Result{JSON: []byte(`{"ok":true}`)}}, nil
	})
	req := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Name: "test", Arguments: json.RawMessage(`{}`)}}
	p := principal{ID: "anonymous", Anonymous: true}
	result, e := g.call(context.Background(), p, g.tools["test"], req)
	if e != nil || result.IsError || calls.Load() != 1 {
		t.Fatalf("call: %+v %v", result, e)
	}
	req.Params.Arguments = json.RawMessage(`{"extra":1}`)
	result, _ = g.call(context.Background(), p, g.tools["test"], req)
	if !result.IsError || calls.Load() != 1 {
		t.Fatal("invalid args dispatched")
	}
	req.Params.Arguments = json.RawMessage(`{}`)
	result, _ = g.call(context.Background(), principal{Key: "other"}, g.tools["test"], req)
	if !result.IsError {
		t.Fatal("unauthorized internal call")
	}
	for i := 0; i < cap(g.slots); i++ {
		g.slots <- struct{}{}
	}
	result, _ = g.call(context.Background(), p, g.tools["test"], req)
	if !result.IsError {
		t.Fatal("capacity bypassed")
	}
	for len(g.slots) > 0 {
		<-g.slots
	}
	for i := 0; i < cap(g.tools["test"].slots); i++ {
		g.tools["test"].slots <- struct{}{}
	}
	result, _ = g.call(context.Background(), p, g.tools["test"], req)
	if !result.IsError {
		t.Fatal("tool capacity bypassed")
	}
	for len(g.tools["test"].slots) > 0 {
		<-g.tools["test"].slots
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, _ = g.call(ctx, p, g.tools["test"], req)
	if !result.IsError || calls.Load() != 1 {
		t.Fatal("canceled call dispatched")
	}
	g.opts.Run = func(context.Context, graph.Envelope) (graph.Terminal, error) {
		return graph.Terminal{}, errors.New("private upstream secret")
	}
	result, _ = g.call(context.Background(), p, g.tools["test"], req)
	b, _ := json.Marshal(result)
	if !result.IsError || strings.Contains(string(b), "private upstream secret") {
		t.Fatal("unsafe error")
	}
	g.opts.Run = func(context.Context, graph.Envelope) (graph.Terminal, error) {
		return graph.Terminal{Result: &graph.Result{JSON: []byte(`{"ok":"wrong"}`)}}, nil
	}
	result, _ = g.call(context.Background(), p, g.tools["test"], req)
	if !result.IsError {
		t.Fatal("invalid output accepted")
	}
	tool.Timeout = "10ms"
	tool.ReadOnly = false
	tool.Access.AllowMutation = true
	timed := testGateway(t, tool, func(ctx context.Context, _ graph.Envelope) (graph.Terminal, error) {
		<-ctx.Done()
		return graph.Terminal{SideEffects: true}, ctx.Err()
	})
	result, _ = timed.call(context.Background(), p, timed.tools["test"], req)
	b, _ = json.Marshal(result)
	if !strings.Contains(string(b), "unknown_outcome") {
		t.Fatal("mutation uncertainty hidden")
	}
}
func TestPublicModeMethodsOriginAndAbuseLimits(t *testing.T) {
	g := testGateway(t, testTool(), func(context.Context, graph.Envelope) (graph.Terminal, error) { return graph.Terminal{}, nil })
	for _, tc := range []struct {
		method, origin, token string
		status                int
	}{{"GET", "", "", 405}, {"OPTIONS", "http://localhost", "", 204}, {"POST", "https://evil.example", "", 403}, {"POST", "", "Bearer unexpected", 401}} {
		r := httptest.NewRequestWithContext(context.Background(), tc.method, "http://localhost/mcp", strings.NewReader("{}"))
		r.Header.Set("Origin", tc.origin)
		if tc.token != "" {
			r.Header.Set("Authorization", tc.token)
		}
		w := httptest.NewRecorder()
		g.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("%+v: %d", tc, w.Code)
		}
	}
	for i := 0; i < 10; i++ {
		g.budget("peer", 10)
	}
	if g.budget("peer", 10) {
		t.Fatal("rate limit bypassed")
	}
	for i := 0; i < 4100; i++ {
		g.budget(string(rune(i))+"peer", 1)
	}
	if len(g.limits) > 4096 {
		t.Fatal("limiter memory unbounded")
	}
}

func TestOAuthMetadataChallengesAndCORS(t *testing.T) {
	t.Setenv("MCP_METADATA_OTHER", "other-principal-012345678901234567890123456789")
	tool := testTool()
	tool.Access = flowconfig.MCPAccessOptions{Scopes: []string{"service:read"}}
	g, err := New(Options{Config: flowconfig.MCPTriggerOptions{Path: "/mcp", PublicURL: "https://gateway.example/mcp", AllowedOrigins: []string{"https://builder.example"}, Tools: []flowconfig.MCPToolOptions{tool}, Auth: flowconfig.MCPAuthOptions{
		OAuth:   &flowconfig.MCPOAuthOptions{Issuer: "https://identity.example", Audience: "hookway", DiscoveryScopes: []string{"service:read"}},
		APIKeys: map[string]flowconfig.MCPKeyOptions{"other": {SecretRef: flowconfig.SecretRefOptions{Provider: "env", Name: "MCP_METADATA_OTHER"}}},
	}}, Run: func(context.Context, graph.Envelope) (graph.Terminal, error) { return graph.Terminal{}, nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = g.Close() }()
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, g.MetadataPath(), nil)
	r.Header.Set("Origin", "https://builder.example")
	w := httptest.NewRecorder()
	g.Metadata(w, r)
	if w.Code != 200 || w.Header().Get("Access-Control-Allow-Origin") != "https://builder.example" || !strings.Contains(w.Body.String(), `"resource":"https://gateway.example/mcp"`) {
		t.Fatalf("metadata: %d %s", w.Code, w.Body.String())
	}
	for _, token := range []string{"", "other-principal-012345678901234567890123456789"} {
		r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"test","arguments":{}}}`))
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		g.ServeHTTP(w, r)
		want := http.StatusUnauthorized
		if token != "" {
			want = http.StatusForbidden
		}
		if w.Code != want || !strings.Contains(w.Header().Get("WWW-Authenticate"), `resource_metadata="https://gateway.example/.well-known/oauth-protected-resource/mcp"`) || !strings.Contains(w.Header().Get("WWW-Authenticate"), `scope="service:read"`) {
			t.Fatalf("challenge %d %v", w.Code, w.Header())
		}
	}
	// Exhaust only the provider-validation budget. A named API key must remain
	// usable without consuming the provider's capacity or making a network call.
	for range 10 {
		g.budget("oauth_validation", 600)
	}
	r = httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`))
	r.Header.Set("Authorization", "Bearer other-principal-012345678901234567890123456789")
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json, text/event-stream")
	w = httptest.NewRecorder()
	g.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("OAuth saturation blocked an independent API key: %d %s", w.Code, w.Body.String())
	}
	r.Header.Set("Authorization", "Bearer requires-provider-validation")
	w = httptest.NewRecorder()
	g.ServeHTTP(w, r)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("OAuth validation budget bypassed: %d", w.Code)
	}
}

func TestSaturationRejectsBeforeExecutionAndReleasesGlobalSlot(t *testing.T) {
	var calls int
	g := testGateway(t, testTool(), func(context.Context, graph.Envelope) (graph.Terminal, error) {
		calls++
		return graph.Terminal{Result: &graph.Result{JSON: []byte(`{"ok":true}`)}}, nil
	})
	tool := g.tools["test"]
	for i := 0; i < cap(tool.slots); i++ {
		tool.slots <- struct{}{}
	}
	r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"test","arguments":{}}}`))
	w := httptest.NewRecorder()
	g.ServeHTTP(w, r)
	if w.Code != http.StatusServiceUnavailable || calls != 0 || len(g.slots) != 0 {
		t.Fatalf("unsafe admission: status=%d calls=%d global=%d", w.Code, calls, len(g.slots))
	}
	for i := 0; i < cap(tool.slots); i++ {
		<-tool.slots
	}
}

func TestTransportParsingCannotBypassToolRateLimit(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"batch", `[{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"test","arguments":{}}}]`, http.StatusBadRequest},
		{"case_shadowed_method", `{"jsonrpc":"2.0","id":2,"method":"tools/call","Method":"ping","params":{"name":"test","arguments":{}}}`, http.StatusTooManyRequests},
		{"case_shadowed_name", `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"test","Name":"other","arguments":{}}}`, http.StatusTooManyRequests},
	} {
		for _, version := range []string{"", "2025-03-26"} {
			t.Run(tc.name+"/version_"+version, func(t *testing.T) {
				var calls atomic.Int32
				tool := testTool()
				tool.RequestsPerMinute = 1
				g := testGateway(t, tool, func(context.Context, graph.Envelope) (graph.Terminal, error) {
					calls.Add(1)
					return graph.Terminal{Result: &graph.Result{JSON: []byte(`{"ok":true}`)}}, nil
				})
				invoke := func(body string) *httptest.ResponseRecorder {
					r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/mcp", strings.NewReader(body))
					r.Header.Set("Content-Type", "application/json")
					r.Header.Set("Accept", "application/json, text/event-stream")
					if version != "" {
						r.Header.Set("MCP-Protocol-Version", version)
					}
					w := httptest.NewRecorder()
					g.ServeHTTP(w, r)
					return w
				}
				first := invoke(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"test","arguments":{}}}`)
				if first.Code != http.StatusOK || calls.Load() != 1 {
					t.Fatalf("first call: status=%d calls=%d body=%s", first.Code, calls.Load(), first.Body.String())
				}
				second := invoke(tc.body)
				if second.Code != tc.status || calls.Load() != 1 {
					t.Fatalf("payload bypassed single-request admission: status=%d calls=%d body=%s", second.Code, calls.Load(), second.Body.String())
				}
			})
		}
	}
}
