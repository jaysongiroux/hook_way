package mcpgateway

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jaysongiroux/hook_way/internal/flowconfig"
	"github.com/jaysongiroux/hook_way/internal/graph"
)

type eventRuntimeStub struct {
	principal        flowconfig.EventPrincipal
	endpoint, method string
	authorize        func(context.Context, flowconfig.EventPrincipal) bool
	fail             bool
}

func (s *eventRuntimeStub) Ready(context.Context) error { return nil }
func (s *eventRuntimeStub) BindEventAuthorizer(_ string, f func(context.Context, flowconfig.EventPrincipal) bool) {
	s.authorize = f
}
func (s *eventRuntimeStub) EventCall(_ context.Context, endpoint, method string, p flowconfig.EventPrincipal, _ json.RawMessage) (any, error) {
	s.endpoint = endpoint
	s.method = method
	s.principal = p
	if s.fail {
		return nil, &flowconfig.EventRPCError{Code: -32015, Message: "CallbackEndpointError", Reason: "challenge_failed"}
	}
	return map[string]any{"events": []any{}}, nil
}
func TestEventMethodsDiscoveryAndExpiringPrincipal(t *testing.T) {
	token := strings.Repeat("k", 32)
	t.Setenv("MCP_EVENTS_TEST", token)
	expiry := time.Now().Add(time.Minute).UTC().Truncate(time.Second)
	runtime := &eventRuntimeStub{}
	tool := testTool()
	tool.Access = flowconfig.MCPAccessOptions{APIKeys: []string{"reader"}}
	g, err := New(Options{Endpoint: "agents", Events: runtime, Config: flowconfig.MCPTriggerOptions{Path: "/mcp", PublicURL: "http://localhost/mcp", Auth: flowconfig.MCPAuthOptions{APIKeys: map[string]flowconfig.MCPKeyOptions{"reader": {SecretRef: flowconfig.SecretRefOptions{Provider: "env", Name: "MCP_EVENTS_TEST"}, ExpiresAt: expiry.Format(time.RFC3339)}}}, Tools: []flowconfig.MCPToolOptions{tool}, Events: []flowconfig.MCPEventOptions{{Event: "issue.opened", InputSchema: map[string]any{"type": "object", "additionalProperties": false}, Access: tool.Access}}}, Run: func(context.Context, graph.Envelope) (graph.Terminal, error) { return graph.Terminal{}, nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = g.Close() }()
	call := func(method, version string, params map[string]any) map[string]any {
		if version == "2026-07-28" {
			params["_meta"] = map[string]any{"io.modelcontextprotocol/protocolVersion": version, "io.modelcontextprotocol/clientCapabilities": map[string]any{}}
		}
		b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
		req := httptest.NewRequestWithContext(t.Context(), "POST", "/mcp", strings.NewReader(string(b)))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("MCP-Protocol-Version", version)
		req.Header.Set("Mcp-Method", method)
		w := httptest.NewRecorder()
		g.ServeHTTP(w, req)
		body := w.Body.String()
		for _, line := range strings.Split(body, "\n") {
			if strings.HasPrefix(line, "data: ") {
				body = strings.TrimPrefix(line, "data: ")
				break
			}
		}
		var out map[string]any
		if err := json.Unmarshal([]byte(body), &out); err != nil {
			t.Fatal(body)
		}
		return out
	}
	discovered := call("server/discover", "2026-07-28", map[string]any{})
	result := discovered["result"].(map[string]any)
	caps := result["capabilities"].(map[string]any)
	if result["resultType"] != "complete" || caps["events"] == nil || caps["tools"] == nil || result["ttlMs"] != float64(0) || result["cacheScope"] != "private" {
		t.Fatal("discovery fields lost", result)
	}
	out := call("events/list", "2026-07-28", map[string]any{})
	if out["error"] != nil || runtime.endpoint != "agents" || runtime.principal.Key != "reader" || !runtime.principal.ExpiresAt.Equal(expiry) {
		t.Fatal("principal or dispatch lost", out, runtime.principal)
	}
	if !runtime.authorize(t.Context(), runtime.principal) {
		t.Fatal("current key invalid")
	}
	expired := runtime.principal
	expired.ExpiresAt = time.Now().Add(-time.Minute)
	if runtime.authorize(t.Context(), expired) {
		t.Fatal("expired principal allowed")
	}
	runtime.fail = true
	out = call("events/subscribe", "2026-07-28", map[string]any{})
	failure := out["error"].(map[string]any)
	if failure["code"] != float64(-32015) {
		t.Fatal(out)
	}
	old := call("initialize", "2025-11-25", map[string]any{"protocolVersion": "2025-11-25", "capabilities": map[string]any{}, "clientInfo": map[string]string{"name": "legacy", "version": "1"}})
	if old["error"] != nil {
		t.Fatal(old)
	}
	oldCaps := old["result"].(map[string]any)["capabilities"].(map[string]any)
	if oldCaps["events"] != nil || oldCaps["tools"] == nil {
		t.Fatal("legacy tool capabilities changed", oldCaps)
	}
}
