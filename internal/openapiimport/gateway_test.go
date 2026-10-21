package openapiimport

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jaysongiroux/hook_way/internal/flowconfig"
	"github.com/jaysongiroux/hook_way/internal/httpapi"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type tokenTransport string

func (b tokenTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+string(b))
	return http.DefaultTransport.RoundTrip(r)
}
func connect(t *testing.T, endpoint, token string) *mcp.ClientSession {
	t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "import-test", Version: "1"}, nil)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)
	session, e := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: endpoint, HTTPClient: &http.Client{Transport: tokenTransport(token)}}, nil)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}
func importedServer(t *testing.T, r Result) *httptest.Server {
	t.Helper()
	server, e := httpapi.NewFlowServer(r.Config, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = server.Close() })
	ts := httptest.NewServer(server)
	t.Cleanup(ts.Close)
	return ts
}
func TestGeneratedToolsUseRealGatewayAndRespectGrants(t *testing.T) {
	const reader = "reader-test-012345678901234567890123456789"
	const writer = "writer-test-012345678901234567890123456789"
	const upstreamSecret = "upstream-test-01234567890123456789012345"
	t.Setenv("IMPORT_READER", reader)
	t.Setenv("IMPORT_WRITER", writer)
	t.Setenv("IMPORT_UPSTREAM", upstreamSecret)
	var reads, writes atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+upstreamSecret {
			t.Error("caller credential forwarded or upstream credential missing")
		}
		raw, _ := io.ReadAll(r.Body)
		if r.Method == "GET" {
			reads.Add(1)
			if len(raw) > 0 {
				t.Error("GET sent argument body")
			}
			if r.URL.EscapedPath() != "/v1/customers/a%2Fb%3F" {
				t.Errorf("unescaped path %s", r.URL.EscapedPath())
			}
			if r.URL.RawQuery != "details=false" {
				t.Errorf("query %q", r.URL.RawQuery)
			}
		} else {
			writes.Add(1)
			if string(raw) != `{"name":"Pat"}` {
				t.Errorf("body %s", raw)
			}
			if r.URL.RawQuery != "" {
				t.Error("query leaked")
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"customer","name":"Pat"}`)
	}))
	defer upstream.Close()
	s := settings()
	s.Upstream = Upstream{BaseURL: upstream.URL + "/v1", AllowPrivateTargets: true}
	s.Credentials = map[string]flowconfig.CredentialProfile{"api": {Kind: "static", AllowedOrigins: []string{upstream.URL}, AllowPrivateTargets: true, Headers: map[string]flowconfig.CredentialHeader{"Authorization": {Prefix: "Bearer ", SecretRef: flowconfig.SecretRefOptions{Provider: "env", Name: "IMPORT_UPSTREAM"}}}}}
	for i := range s.Operations {
		s.Operations[i].Credential = "api"
	}
	result := requireGenerated(t, fixture(t), s)
	for _, secret := range []string{reader, writer, upstreamSecret} {
		if strings.Contains(string(result.YAML), secret) {
			t.Fatal("secret resolved during import")
		}
	}
	ts := importedServer(t, result)
	readerSession := connect(t, ts.URL+"/mcp", reader)
	tools, e := readerSession.ListTools(t.Context(), &mcp.ListToolsParams{})
	if e != nil || len(tools.Tools) != 1 || tools.Tools[0].Name != "get_customer" {
		t.Fatalf("reader tools %+v %v", tools, e)
	}
	call, e := readerSession.CallTool(t.Context(), &mcp.CallToolParams{Name: "get_customer", Arguments: map[string]any{"path": map[string]any{"id": "a/b?"}, "query": map[string]any{"details": false}}})
	if e != nil || call.IsError {
		t.Fatalf("read %+v %v", call, e)
	}
	encoded, _ := json.Marshal(call)
	if !strings.Contains(string(encoded), "customer") {
		t.Fatal("result missing data")
	}
	for _, secret := range []string{reader, writer, upstreamSecret} {
		if strings.Contains(string(encoded), secret) {
			t.Fatal("secret in tool result")
		}
	}
	for _, args := range []map[string]any{{"path": map[string]any{"id": "a"}, "host": "attacker"}, {"path": map[string]any{"id": "a"}, "headers": map[string]any{"Authorization": writer}}, {"path": map[string]any{"id": "../admin"}}} {
		c, e := readerSession.CallTool(t.Context(), &mcp.CallToolParams{Name: "get_customer", Arguments: args})
		if e == nil && !c.IsError {
			t.Fatal("tampered arguments accepted")
		}
	}
	denied, e := readerSession.CallTool(t.Context(), &mcp.CallToolParams{Name: "update_customer", Arguments: map[string]any{"path": map[string]any{"id": "a"}, "body": map[string]any{"name": "Pat"}}})
	if e == nil && !denied.IsError {
		t.Fatal("reader performed write")
	}
	if writes.Load() != 0 {
		t.Fatal("unauthorized mutation reached upstream")
	}
	writerSession := connect(t, ts.URL+"/mcp", writer)
	call, e = writerSession.CallTool(t.Context(), &mcp.CallToolParams{Name: "update_customer", Arguments: map[string]any{"path": map[string]any{"id": "a"}, "body": map[string]any{"name": "Pat"}}})
	if e != nil || call.IsError {
		t.Fatalf("write %+v %v", call, e)
	}
	if reads.Load() != 1 || writes.Load() != 1 {
		t.Fatalf("read/write attempts %d/%d", reads.Load(), writes.Load())
	}
}

func TestGeneratedOptionalBodiesResponsesAndCancellation(t *testing.T) {
	t.Setenv("IMPORT_READER", "reader-test-012345678901234567890123456789")
	t.Setenv("IMPORT_WRITER", "writer-test-012345678901234567890123456789")
	doc := `openapi: 3.1.0
info: {title: Test API, version: '1'}
paths:
  /operation:
    post:
      operationId: operate
      requestBody:
        content:
          application/json:
            schema: {type: [object, 'null'], properties: {value: {type: string}}}
      responses:
        '200':
          description: JSON
          content:
            application/json:
              schema: {anyOf: [{type: array, items: {type: integer}}, {type: string}, {type: boolean}]}
        '204': {description: Empty}
`
	var mode atomic.Int32
	var calls atomic.Int32
	entered, cancelled := make(chan struct{}, 1), make(chan struct{}, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		raw, _ := io.ReadAll(r.Body)
		switch mode.Load() {
		case 0:
			if len(raw) > 0 {
				t.Errorf("omitted body %q", raw)
			}
			_, _ = io.WriteString(w, `[1,2]`)
		case 1:
			if string(raw) != "null" {
				t.Errorf("null body %q", raw)
			}
			_, _ = io.WriteString(w, `"text"`)
		case 2:
			if string(raw) != `{"value":"yes"}` {
				t.Errorf("object body %q", raw)
			}
			w.WriteHeader(204)
		case 3:
			w.WriteHeader(200)
		case 4:
			_, _ = io.WriteString(w, `{"unexpected":true}`)
		case 5:
			w.WriteHeader(503)
		case 6:
			_, _ = io.WriteString(w, "not JSON")
		case 7:
			entered <- struct{}{}
			<-r.Context().Done()
			cancelled <- struct{}{}
		}
	}))
	defer upstream.Close()
	s := settings()
	s.Upstream = Upstream{BaseURL: upstream.URL, AllowPrivateTargets: true}
	s.Operations = []Selection{{Method: "POST", Path: "/operation", Access: flowconfig.MCPAccessOptions{APIKeys: []string{"writer"}, AllowMutation: true}}}
	ts := importedServer(t, requireGenerated(t, []byte(doc), s))
	session := connect(t, ts.URL+"/mcp", "writer-test-012345678901234567890123456789")
	for _, tc := range []struct {
		mode    int32
		args    map[string]any
		want    string
		failure bool
	}{
		{0, map[string]any{}, `{"data":[1,2]}`, false},
		{1, map[string]any{"body": nil}, `{"data":"text"}`, false},
		{2, map[string]any{"body": map[string]any{"value": "yes"}}, `{"data":null}`, false},
		{3, map[string]any{}, "", true}, {4, map[string]any{}, "", true}, {5, map[string]any{}, "", true}, {6, map[string]any{}, "", true},
	} {
		mode.Store(tc.mode)
		before := calls.Load()
		r, e := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "operate", Arguments: tc.args})
		if e != nil {
			t.Fatal(e)
		}
		if r.IsError != tc.failure {
			t.Fatalf("mode %d: %+v", tc.mode, r)
		}
		if !tc.failure {
			raw, _ := json.Marshal(r.StructuredContent)
			if string(raw) != tc.want {
				t.Fatalf("%s != %s", raw, tc.want)
			}
		}
		if calls.Load() != before+1 {
			t.Fatal("mutation retried")
		}
	}
	mode.Store(7)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = session.CallTool(ctx, &mcp.CallToolParams{Name: "operate", Arguments: map[string]any{}})
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		cancel()
		t.Fatal("request never started")
	}
	cancel()
	select {
	case <-cancelled:
	case <-time.After(3 * time.Second):
		t.Fatal("cancellation did not reach upstream")
	}
	<-done
}
