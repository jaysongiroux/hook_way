package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jaysongiroux/hook_way/internal/queue"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const mcpFixture = `
server: {addr: ':8080', max_body_bytes: 1048576}
flows:
  gateway:
    capture_failures: false
    nodes:
      - id: mcp
        type: mcp_trigger
        path: /mcp
        public_url: http://localhost/mcp
        auth:
          api_keys:
            reader: {secret_ref: {provider: env, name: TEST_MCP_READER}}
            other: {secret_ref: {provider: env, name: TEST_MCP_OTHER}}
        tools:
          - name: first
            description: First native operation.
            read_only: true
            access: {api_keys: [reader]}
            input_schema: {type: object, additionalProperties: false}
            output_schema: {type: object, properties: {selected: {type: string}}, required: [selected]}
          - name: second
            description: Second native operation.
            read_only: true
            access: {api_keys: [other]}
            input_schema: {type: object, additionalProperties: false}
            output_schema: {type: object, properties: {selected: {type: string}}, required: [selected]}
      - {id: first_result, type: mcp_result, source: constant, value: {selected: first}}
      - {id: second_result, type: mcp_result, source: constant, value: {selected: second}}
    edges: ['mcp:first>first_result', 'mcp:second>second_result']
`
const readerToken = "reader-012345678901234567890123456789"
const otherToken = "other-0123456789012345678901234567890"

type mcpDeadlineWriter struct {
	*httptest.ResponseRecorder
	deadline time.Time
}

func (w *mcpDeadlineWriter) SetWriteDeadline(deadline time.Time) error {
	w.deadline = deadline
	return nil
}

func TestMCPWriteDeadlineReachesUnderlyingConnection(t *testing.T) {
	t.Setenv("TEST_MCP_READER", readerToken)
	t.Setenv("TEST_MCP_OTHER", otherToken)
	server, err := NewFlowServer(loadFlowCfg(t, mcpFixture), &fakePublisher{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = server.Close() }()
	r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	r.Header.Set("Authorization", "Bearer "+readerToken)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json, text/event-stream")
	w := &mcpDeadlineWriter{ResponseRecorder: httptest.NewRecorder()}
	server.ServeHTTP(w, r)
	if w.deadline.Before(time.Now().Add(120*time.Second)) || w.deadline.After(time.Now().Add(126*time.Second)) {
		t.Fatalf("MCP deadline hidden by middleware: %v", w.deadline)
	}
}

type bearerTransport struct{ token string }

func (b bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}

func TestMCPBlocksPrivateTargetsAndDoesNotFollowRedirects(t *testing.T) {
	for _, allowPrivate := range []bool{false, true} {
		t.Run(map[bool]string{false: "blocked_private", true: "redirect_not_followed"}[allowPrivate], func(t *testing.T) {
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				http.Redirect(w, r, "/other", http.StatusFound)
			}))
			defer upstream.Close()
			node := `- {id: request, type: http_request, method: GET, url: "` + upstream.URL + `/operation", allow_private_targets: ` + map[bool]string{false: "false", true: "true"}[allowPrivate] + `}
      - {id: first_result, type: mcp_result, source: upstream}`
			src := strings.Replace(mcpFixture, "- {id: first_result, type: mcp_result, source: constant, value: {selected: first}}", node, 1)
			src = strings.Replace(src, "mcp:first>first_result", "mcp:first>request', 'request>first_result", 1)
			ts := mcpTestServer(t, src)
			client := mcp.NewClient(&mcp.Implementation{Name: "egress-test", Version: "1"}, nil)
			session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: ts.URL + "/mcp", HTTPClient: &http.Client{Transport: bearerTransport{readerToken}}}, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = session.Close() }()
			result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "first", Arguments: map[string]any{}})
			want := int32(0)
			if allowPrivate {
				want = 1
			}
			if err != nil || !result.IsError || calls.Load() != want {
				t.Fatalf("calls=%d result=%+v err=%v", calls.Load(), result, err)
			}
		})
	}
}

func TestMCPClientCancellationReachesHTTPBranch(t *testing.T) {
	entered, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(entered)
		select {
		case <-r.Context().Done():
			close(canceled)
		case <-release:
		}
		_, _ = io.WriteString(w, `{"selected":"first"}`)
	}))
	defer upstream.Close()
	defer close(release)
	src := strings.Replace(mcpFixture, "read_only: true", "read_only: true\n            timeout: 2s", 1)
	src = strings.Replace(src, "- {id: first_result, type: mcp_result, source: constant, value: {selected: first}}", `- {id: request, type: http_request, method: GET, url: "`+upstream.URL+`/operation", allow_private_targets: true}
      - {id: first_result, type: mcp_result, source: upstream}`, 1)
	src = strings.Replace(src, "mcp:first>first_result", "mcp:first>request', 'request>first_result", 1)
	ts := mcpTestServer(t, src)
	client := mcp.NewClient(&mcp.Implementation{Name: "cancellation-test", Version: "1"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: ts.URL + "/mcp", HTTPClient: &http.Client{Transport: bearerTransport{readerToken}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = session.CallTool(ctx, &mcp.CallToolParams{Name: "first", Arguments: map[string]any{}})
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("upstream not entered")
	}
	cancel()
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("caller cancellation did not reach the upstream request")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("client call leaked after cancellation")
	}
}

type mcpBlockingPublisher struct{ entered, accepted chan struct{} }

func (p *mcpBlockingPublisher) Publish(ctx context.Context, _, _ string, _ queue.Message) error {
	close(p.entered)
	select {
	case <-p.accepted:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (*mcpBlockingPublisher) Ready(context.Context) error { return nil }
func (*mcpBlockingPublisher) Close() error                { return nil }

func TestMCPQueueResultWaitsForPublisherAcceptance(t *testing.T) {
	src := strings.Replace(mcpFixture, "flows:", "destinations: {jobs: {type: sqs, sqs: {region: us-east-1}}}\nflows:", 1)
	src = strings.Replace(src, "read_only: true", "read_only: false", 1)
	src = strings.Replace(src, "access: {api_keys: [reader]}", "access: {api_keys: [reader], allow_mutation: true}", 1)
	src = strings.Replace(src, "- {id: first_result, type: mcp_result, source: constant, value: {selected: first}}", `- {id: publish, type: publish, destination: jobs, target: jobs, create: true}
      - {id: first_result, type: mcp_result, source: constant, value: {selected: accepted}}`, 1)
	src = strings.Replace(src, "mcp:first>first_result", "mcp:first>publish', 'publish>first_result", 1)
	t.Setenv("TEST_MCP_READER", readerToken)
	t.Setenv("TEST_MCP_OTHER", otherToken)
	pub := &mcpBlockingPublisher{entered: make(chan struct{}), accepted: make(chan struct{})}
	server, err := NewFlowServer(loadFlowCfg(t, src), pub, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = server.Close() }()
	ts := httptest.NewServer(server)
	defer ts.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "queue-test", Version: "1"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: ts.URL + "/mcp", HTTPClient: &http.Client{Transport: bearerTransport{readerToken}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()
	done := make(chan *mcp.CallToolResult, 1)
	fail := make(chan error, 1)
	go func() {
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "first", Arguments: map[string]any{}})
		if err != nil {
			fail <- err
			return
		}
		done <- result
	}()
	select {
	case <-pub.entered:
	case err := <-fail:
		t.Fatal(err)
	case <-ctx.Done():
		t.Fatal("publisher never entered")
	}
	select {
	case result := <-done:
		t.Fatalf("reported completion before publisher acceptance: %+v", result)
	default:
	}
	close(pub.accepted)
	select {
	case result := <-done:
		if result.IsError || result.Meta["io.hookway/request_id"] == "" {
			t.Fatalf("missing acceptance/correlation: %+v", result)
		}
		body, _ := json.Marshal(result.StructuredContent)
		if string(body) != `{"selected":"accepted"}` {
			t.Fatalf("wrong acceptance: %s", body)
		}
	case err := <-fail:
		t.Fatal(err)
	case <-ctx.Done():
		t.Fatal("acceptance not returned")
	}
}
func mcpTestServer(t *testing.T, src string) *httptest.Server {
	t.Helper()
	t.Setenv("TEST_MCP_READER", readerToken)
	t.Setenv("TEST_MCP_OTHER", otherToken)
	s, e := NewFlowServer(loadFlowCfg(t, src), &fakePublisher{}, nil, nil)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = s.Close() })
	ts := httptest.NewServer(s)
	t.Cleanup(ts.Close)
	return ts
}
func TestMCPSDKRoutesAndFiltersNativeTools(t *testing.T) {
	ts := mcpTestServer(t, mcpFixture)
	for _, tc := range []struct{ token, name string }{{readerToken, "first"}, {otherToken, "second"}} {
		t.Run(tc.name, func(t *testing.T) {
			c := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
			session, e := c.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: ts.URL + "/mcp", HTTPClient: &http.Client{Transport: bearerTransport{tc.token}}}, nil)
			if e != nil {
				t.Fatal(e)
			}
			defer func() { _ = session.Close() }()
			listed, e := session.ListTools(context.Background(), &mcp.ListToolsParams{})
			if e != nil {
				t.Fatal(e)
			}
			if len(listed.Tools) != 1 || listed.Tools[0].Name != tc.name {
				t.Fatalf("wrong catalogue: %+v", listed.Tools)
			}
			result, e := session.CallTool(context.Background(), &mcp.CallToolParams{Name: tc.name, Arguments: map[string]any{}})
			if e != nil {
				t.Fatal(e)
			}
			if result.IsError {
				t.Fatalf("tool error: %+v", result)
			}
			b, _ := json.Marshal(result.StructuredContent)
			if !bytes.Contains(b, []byte(tc.name)) {
				t.Fatalf("wrong result: %s", b)
			}
		})
	}
}

func TestMCPCustomEndpointsIsolateCredentialsCataloguesAndResults(t *testing.T) {
	var src strings.Builder
	src.WriteString("server: {addr: ':8080', max_body_bytes: 1048576}\nflows:\n")
	for _, name := range []string{"services", "utilities"} {
		fmt.Fprintf(&src, `  %[1]s:
    capture_failures: false
    nodes:
      - id: mcp
        type: mcp_trigger
        path: /%[1]s/mcp
        public_url: https://gateway.example/%[1]s/mcp
        auth:
          api_keys:
            reader: {secret_ref: {provider: env, name: TEST_MCP_%[2]s}}
          oauth: {issuer: "https://%[1]s.identity.example", audience: "https://gateway.example/%[1]s/mcp"}
        tools:
          - name: get_status
            description: Status for %[1]s.
            read_only: true
            access: {api_keys: [reader]}
            input_schema: {type: object, additionalProperties: false}
            output_schema: {type: object, required: [selected], properties: {selected: {type: string}}}
      - {id: result, type: mcp_result, source: constant, value: {selected: %[1]s}}
    edges: ['mcp:get_status>result']
`, name, map[string]string{"services": "READER", "utilities": "OTHER"}[name])
	}
	src.WriteString(`  ordinary_http:
    nodes:
      - {id: in, type: http_trigger, path: /api/status, methods: [GET]}
      - {id: body, type: read_body, max_bytes: 1024, allow_empty: true}
      - {id: auth, type: auth, scheme: exact, secret: ordinary-http-key, signature_header: X-Auth}
      - {id: env, type: envelope, body_format: raw}
      - {id: reply, type: respond, status: 201, body: ordinary-http-response}
    edges: ['in>body', 'body>auth', 'auth>env', 'env>reply']
`)
	ts := mcpTestServer(t, src.String())
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/status", nil)
	request.Header.Set("X-Auth", "ordinary-http-key")
	w := httptest.NewRecorder()
	ts.Config.Handler.ServeHTTP(w, request)
	if w.Code != http.StatusCreated || w.Body.String() != "ordinary-http-response" {
		t.Fatalf("ordinary synchronous HTTP route changed: %d %s", w.Code, w.Body.String())
	}
	for _, tc := range []struct{ name, token, wrongToken string }{{"services", readerToken, otherToken}, {"utilities", otherToken, readerToken}} {
		t.Run(tc.name, func(t *testing.T) {
			client := mcp.NewClient(&mcp.Implementation{Name: "multiple-endpoints", Version: "1"}, nil)
			session, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: ts.URL + "/" + tc.name + "/mcp", HTTPClient: &http.Client{Transport: bearerTransport{tc.token}}}, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = session.Close() }()
			listed, err := session.ListTools(t.Context(), &mcp.ListToolsParams{})
			if err != nil || len(listed.Tools) != 1 || listed.Tools[0].Description != "Status for "+tc.name+"." {
				t.Fatalf("wrong endpoint catalogue: %+v %v", listed, err)
			}
			result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "get_status", Arguments: map[string]any{}})
			if err != nil || result.IsError {
				t.Fatalf("tool call: %+v %v", result, err)
			}
			raw, _ := json.Marshal(result.StructuredContent)
			if string(raw) != `{"selected":"`+tc.name+`"}` {
				t.Fatalf("call crossed endpoint boundaries: %s", raw)
			}
			// Exercise the mounted server directly for an unauthorized credential
			// without giving the SDK an opportunity to initiate OAuth discovery.
			w := httptest.NewRecorder()
			request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/"+tc.name+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
			request.Header.Set("Authorization", "Bearer "+tc.wrongToken)
			// The real server is shared by both endpoints.
			ts.Config.Handler.ServeHTTP(w, request)
			if w.Code != http.StatusUnauthorized || !strings.Contains(w.Header().Get("WWW-Authenticate"), "/.well-known/oauth-protected-resource/"+tc.name+"/mcp") {
				t.Fatalf("credential crossed endpoint boundary: %d %s", w.Code, w.Body.String())
			}
			w = httptest.NewRecorder()
			ts.Config.Handler.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/.well-known/oauth-protected-resource/"+tc.name+"/mcp", nil))
			var metadata struct {
				Resource             string   `json:"resource"`
				AuthorizationServers []string `json:"authorization_servers"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &metadata); err != nil || w.Code != http.StatusOK || metadata.Resource != "https://gateway.example/"+tc.name+"/mcp" || len(metadata.AuthorizationServers) != 1 || metadata.AuthorizationServers[0] != "https://"+tc.name+".identity.example" {
				t.Fatalf("metadata crossed endpoint boundary: %d %s", w.Code, w.Body.String())
			}
		})
	}
}
func TestMCPAuthenticationAuthorizationOriginAndLimits(t *testing.T) {
	ts := mcpTestServer(t, mcpFixture)
	for _, tc := range []struct {
		name, token, origin, body string
		status                    int
	}{
		{"missing", "", "", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, 401},
		{"bad", "invalid", "", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, 401},
		{"forbidden", readerToken, "", `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"second","arguments":{}}}`, 403},
		{"origin", readerToken, "https://evil.example", `{}`, 403},
		{"oversize", readerToken, "", strings.Repeat("x", (1<<20)+1), 413},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, ts.URL+"/mcp", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Accept", "application/json, text/event-stream")
			if tc.token != "" {
				req.Header.Set("Authorization", "Bearer "+tc.token)
			}
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			r, e := http.DefaultClient.Do(req)
			if e != nil {
				t.Fatal(e)
			}
			defer func() { _ = r.Body.Close() }()
			raw, _ := io.ReadAll(r.Body)
			if r.StatusCode != tc.status {
				t.Fatalf("status %d: %s", r.StatusCode, raw)
			}
			if bytes.Contains(raw, []byte(readerToken)) {
				t.Fatal("credential leaked")
			}
		})
	}
}
func TestMCPLegacyProtocolAndInvalidArguments(t *testing.T) {
	ts := mcpTestServer(t, mcpFixture)
	for _, version := range []string{"2025-06-18", "2025-11-25"} {
		body := `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"first","arguments":{"unexpected":true}}}`
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, ts.URL+"/mcp", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+readerToken)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("MCP-Protocol-Version", version)
		res, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		raw, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if !bytes.Contains(raw, []byte("invalid_arguments")) {
			t.Fatalf("version %s: %d %s", version, res.StatusCode, raw)
		}
	}
}

func TestMCPHTTPMappingAndCredentialSeparation(t *testing.T) {
	var gotPath, gotQuery, gotAuth, gotHeader, gotBody string
	echoCredential := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		gotQuery = r.URL.Query().Get("q")
		gotAuth = r.Header.Get("Authorization")
		gotHeader = r.Header.Get("X-Name")
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		w.Header().Set("Content-Type", "application/json")
		if echoCredential {
			_ = json.NewEncoder(w).Encode(map[string]string{"selected": gotAuth})
			return
		}
		_, _ = io.WriteString(w, `{"selected":"http"}`)
	}))
	defer upstream.Close()
	t.Setenv("TEST_UPSTREAM_TOKEN", "upstream-private-token")
	src := strings.Replace(mcpFixture, "flows:", `credentials:
  private_api:
    kind: static
    allow_private_targets: true
    allowed_origins: ["`+upstream.URL+`"]
    headers:
      Authorization: {prefix: 'Bearer ', secret_ref: {provider: env, name: TEST_UPSTREAM_TOKEN}}
flows:`, 1)
	src = strings.Replace(src, "read_only: true", "read_only: false\n            request: {variables: {id: id}, query: {q: q}, headers: {X-Name: id}, body: {value: value}}", 1)
	src = strings.Replace(src, "access: {api_keys: [reader]}", "access: {api_keys: [reader], allow_mutation: true}", 1)
	src = strings.Replace(src, "input_schema: {type: object, additionalProperties: false}", "input_schema: {type: object, properties: {id: {type: string}, q: {type: string}, value: {type: object}}, required: [id, q, value], additionalProperties: false}", 1)
	src = strings.Replace(src, "- {id: first_result, type: mcp_result, source: constant, value: {selected: first}}", `- {id: request, type: http_request, method: POST, url: "`+upstream.URL+`/op/{id}", credential: {profile: private_api}, allow_private_targets: true, forward_query: true, body: transformed}
      - {id: first_result, type: mcp_result, source: upstream}`, 1)
	src = strings.Replace(src, "mcp:first>first_result", "mcp:first>request', 'request>first_result", 1)
	ts := mcpTestServer(t, src)
	c := mcp.NewClient(&mcp.Implementation{Name: "http-test", Version: "1"}, nil)
	session, e := c.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: ts.URL + "/mcp", HTTPClient: &http.Client{Transport: bearerTransport{readerToken}}}, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = session.Close() }()
	result, e := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "first", Arguments: map[string]any{"id": "a/b?c", "q": "x&y", "value": map[string]any{"n": 12}}})
	if e != nil || result.IsError {
		t.Fatalf("HTTP tool: %+v %v", result, e)
	}
	if gotPath != "/op/a%2Fb%3Fc" || gotQuery != "x&y" || gotAuth != "Bearer upstream-private-token" || gotHeader != "a/b?c" || gotBody != `{"value":{"n":12}}` {
		t.Fatalf("mapping or credential boundary: %q %q %q %q %q", gotPath, gotQuery, gotAuth, gotHeader, gotBody)
	}
	echoCredential = true
	result, e = session.CallTool(context.Background(), &mcp.CallToolParams{Name: "first", Arguments: map[string]any{"id": "safe", "q": "query", "value": map[string]any{}}})
	raw, _ := json.Marshal(result)
	if e != nil || !result.IsError || bytes.Contains(raw, []byte("upstream-private-token")) {
		t.Fatalf("credential echo escaped isolation: %s %v", raw, e)
	}
}

func TestMCPMutatingHTTPDoesNotRefreshAndResend(t *testing.T) {
	attempts, tokens := 0, 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			tokens++
			_, _ = io.WriteString(w, `{"access_token":"private-upstream-token","expires_in":60}`)
			return
		}
		attempts++
		w.WriteHeader(401)
		_, _ = io.WriteString(w, `{"error":"expired","secret":"never disclose"}`)
	}))
	defer upstream.Close()
	t.Setenv("TEST_CLIENT_ID", "client")
	t.Setenv("TEST_CLIENT_SECRET", "secret")
	src := strings.Replace(mcpFixture, "flows:", `credentials:
  private_api:
    kind: oauth_client_credentials
    allow_private_targets: true
    allowed_origins: ["`+upstream.URL+`"]
    token_url: "`+upstream.URL+`/token"
    client_id: {provider: env, name: TEST_CLIENT_ID}
    client_secret: {provider: env, name: TEST_CLIENT_SECRET}
    invalid_session: {path: error, value: expired}
flows:`, 1)
	src = strings.Replace(src, "read_only: true", "read_only: false", 1)
	src = strings.Replace(src, "access: {api_keys: [reader]}", "access: {api_keys: [reader], allow_mutation: true}", 1)
	src = strings.Replace(src, "- {id: first_result, type: mcp_result, source: constant, value: {selected: first}}", `- {id: request, type: http_request, method: POST, url: "`+upstream.URL+`/create", credential: {profile: private_api}, allow_private_targets: true}
      - {id: first_result, type: mcp_result, source: upstream}`, 1)
	src = strings.Replace(src, "mcp:first>first_result", "mcp:first>request', 'request>first_result", 1)
	ts := mcpTestServer(t, src)
	c := mcp.NewClient(&mcp.Implementation{Name: "mutation-test", Version: "1"}, nil)
	session, e := c.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: ts.URL + "/mcp", HTTPClient: &http.Client{Transport: bearerTransport{readerToken}}}, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = session.Close() }()
	result, e := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "first", Arguments: map[string]any{}})
	if e != nil || !result.IsError {
		t.Fatalf("expected safe tool error: %+v %v", result, e)
	}
	raw, _ := json.Marshal(result)
	if attempts != 1 || tokens != 1 || bytes.Contains(raw, []byte("never disclose")) {
		t.Fatalf("unsafe resend/error: attempts=%d tokens=%d result=%s", attempts, tokens, raw)
	}
	// Invalidate the expired token for a later call without resending this call.
	_, e = session.CallTool(context.Background(), &mcp.CallToolParams{Name: "first", Arguments: map[string]any{}})
	if e != nil || attempts != 2 || tokens != 2 {
		t.Fatalf("later call did not reacquire once: attempts=%d tokens=%d err=%v", attempts, tokens, e)
	}
}

func TestMCPMutationIsNotReplayedByHTTPTransportAfterLostReply(t *testing.T) {
	var attempts atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if attempts.Add(1) == 1 {
			_, _ = io.WriteString(w, `{"selected":"first"}`)
			return
		}
		connection, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		_ = connection.Close()
	}))
	defer upstream.Close()
	src := strings.Replace(mcpFixture, "read_only: true", "read_only: false", 1)
	src = strings.Replace(src, "access: {api_keys: [reader]}", "access: {api_keys: [reader], allow_mutation: true}", 1)
	src = strings.Replace(src, "- {id: first_result, type: mcp_result, source: constant, value: {selected: first}}", `- {id: request, type: http_request, method: POST, url: "`+upstream.URL+`/create", allow_private_targets: true, headers: {Idempotency-Key: fixture-key}}
      - {id: first_result, type: mcp_result, source: upstream}`, 1)
	src = strings.Replace(src, "mcp:first>first_result", "mcp:first>request', 'request>first_result", 1)
	ts := mcpTestServer(t, src)
	client := mcp.NewClient(&mcp.Implementation{Name: "transport-retry-test", Version: "1"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: ts.URL + "/mcp", HTTPClient: &http.Client{Transport: bearerTransport{readerToken}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()
	for i := 0; i < 2; i++ {
		result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "first", Arguments: map[string]any{}})
		if err != nil || result.IsError != (i == 1) {
			t.Fatalf("call %d: %+v %v", i, result, err)
		}
	}
	if attempts.Load() != 2 {
		t.Fatalf("lost mutation reply triggered a hidden resend: %d operations for two calls", attempts.Load())
	}
}
