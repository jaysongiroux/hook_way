package flowconfig

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jaysongiroux/hook_way/internal/graph"
)

func TestMCPBranchCompilation(t *testing.T) {
	for _, tc := range []struct {
		name, step string
		edges      []string
		valid      bool
	}{
		{"valid", "source: body", []string{"mcp:test_tool>result"}, true},
		{"disconnected", "source: body", nil, false},
		{"undeclared", "source: body", []string{"mcp:unknown>result"}, false},
		{"default", "source: body", []string{"mcp>result"}, false},
		{"duplicate_entry", "source: body", []string{"mcp:test_tool>result", "mcp:test_tool>other"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			trigger := nodeConfigFromYAML(t, "mcp_trigger", mcpNodeFixture)
			trigger.ID = "mcp"
			result := nodeConfigFromYAML(t, "mcp_result", tc.step)
			result.ID = "result"
			nodes := []graph.NodeConfig{trigger, result}
			if tc.name == "duplicate_entry" {
				other := result
				other.ID = "other"
				nodes = append(nodes, other)
			}
			reg, err := NewRegistryWithOptions(RegistryOptions{ValidateOnly: true})
			if err != nil {
				t.Fatal(err)
			}
			_, err = graph.Compile("gateway", graph.FlowConfig{Nodes: nodes, Edges: tc.edges}, reg)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v: %v", tc.valid, err)
			}
		})
	}
}

func TestMCPHTTPBoundaryEscapesNativeVariables(t *testing.T) {
	node, err := newHTTPRequestNode("http", HTTPRequestOptions{URL: "https://api.example.com/items/{id}"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	n := node.(httpRequestNode)
	ctx := WithMCPInvocation(context.Background(), "test_tool", false, true)
	env := graph.Envelope{Vars: map[string]string{"id": "a/b?query=1&c#fragment"}}
	got, err := n.requestTarget(ctx, env)
	if err != nil || got != "https://api.example.com/items/a%2Fb%3Fquery=1&c%23fragment" {
		t.Fatalf("target %q: %v", got, err)
	}
	if env.Vars["id"] != "a/b?query=1&c#fragment" {
		t.Fatal("input variables mutated")
	}
	for _, value := range []string{"..", "../admin", "..\\admin", "x/./admin"} {
		env.Vars["id"] = value
		if _, err := n.requestTarget(ctx, env); err == nil {
			t.Fatalf("accepted traversal %q", value)
		}
	}
}

func validMCP(t *testing.T) MCPTriggerOptions {
	t.Helper()
	o, e := decodeOptions[MCPTriggerOptions](nodeConfigFromYAML(t, "mcp_trigger", mcpNodeFixture))
	if e != nil {
		t.Fatal(e)
	}
	return o
}
func TestMCPConfigurationFailsClosed(t *testing.T) {
	for name, change := range map[string]func(*MCPTriggerOptions){
		"missing_auth":        func(o *MCPTriggerOptions) { o.Auth = MCPAuthOptions{} },
		"auth_fallback":       func(o *MCPTriggerOptions) { o.Auth.APIKeys = map[string]MCPKeyOptions{"key": {}} },
		"duplicate_tool":      func(o *MCPTriggerOptions) { o.Tools = append(o.Tools, o.Tools[0]) },
		"unscoped":            func(o *MCPTriggerOptions) { o.Tools[0].Access = MCPAccessOptions{} },
		"write_without_grant": func(o *MCPTriggerOptions) { o.Tools[0].ReadOnly = false },
		"write_retry": func(o *MCPTriggerOptions) {
			o.Tools[0].ReadOnly = false
			o.Tools[0].Access.AllowMutation = true
			o.Tools[0].AllowRetries = true
		},
		"scope_without_oauth": func(o *MCPTriggerOptions) { o.Tools[0].Access.Scopes = []string{"read"} },
		"unknown_key":         func(o *MCPTriggerOptions) { o.Tools[0].Access.APIKeys = []string{"unknown"} },
		"remote_ref":          func(o *MCPTriggerOptions) { o.Tools[0].InputSchema["$ref"] = "https://evil.example/schema" },
		"missing_output":      func(o *MCPTriggerOptions) { o.Tools[0].OutputSchema = nil },
		"deadline":            func(o *MCPTriggerOptions) { o.Tools[0].Timeout = "121s" },
		"caller_auth_header":  func(o *MCPTriggerOptions) { o.Tools[0].Request.Headers = map[string]string{"Authorization": "token"} },
		"dynamic_path":        func(o *MCPTriggerOptions) { o.Path = "/mcp/{caller}" },
		"reserved_name":       func(o *MCPTriggerOptions) { o.Tools[0].Name = "on_error" },
	} {
		t.Run(name, func(t *testing.T) {
			o := validMCP(t)
			change(&o)
			if validateMCPTrigger(o) == nil {
				t.Fatal("unsafe config accepted")
			}
		})
	}
}
func TestMCPMappingIsTypedAndExcludesTransportCredentials(t *testing.T) {
	args := map[string]any{"id": "a/b?c", "search": "x&y", "count": json.Number("12"), "payload": map[string]any{"enabled": true}}
	tool := validMCP(t).Tools[0]
	tool.Request = MCPRequestMapping{Variables: map[string]string{"id": "id"}, Query: map[string]string{"q": "search"}, Headers: map[string]string{"X-Count": "count"}, Body: map[string]string{"data": "payload"}}
	env, e := MCPEnvelope(tool, args, graph.RequestHead{})
	if e != nil {
		t.Fatal(e)
	}
	if env.Vars["id"] != "a/b?c" || env.V.Raw.Head.Query.Encode() != "q=x%26y" || env.V.Raw.Head.Header.Get("X-Count") != "12" {
		t.Fatalf("mapping: %+v", env)
	}
	if env.V.Raw.Head.Header.Get("Authorization") != "" || string(env.V.Raw.Body) != `{"data":{"enabled":true}}` {
		t.Fatalf("unsafe body/headers: %s", env.V.Raw.Body)
	}
	tool.Request.Headers["X-Count"] = "payload"
	if _, e = MCPEnvelope(tool, args, graph.RequestHead{}); e == nil {
		t.Fatal("object coerced to header")
	}
}
func TestMCPResultContractsAndInvocationBoundary(t *testing.T) {
	ctx := WithMCPInvocation(context.Background(), "test_tool", false, true)
	for _, tc := range []struct {
		source string
		opts   MCPResultOptions
		env    graph.Envelope
		want   string
	}{
		{"constant", MCPResultOptions{Source: "constant", Value: map[string]any{"ok": true}}, graph.Envelope{}, `{"ok":true}`},
		{"body", MCPResultOptions{Source: "body", Field: "nested"}, graph.Envelope{Body: map[string]any{"nested": []any{true}}}, `[true]`},
		{"upstream", MCPResultOptions{Source: "upstream", Fields: map[string]string{"value": "nested.value"}}, graph.Envelope{Upstream: &graph.UpstreamResponse{Status: 200, Body: []byte(`{"nested":{"value":1}}`)}}, `{"value":1}`},
	} {
		t.Run(tc.source, func(t *testing.T) {
			n, e := newMCPResult("result", tc.opts)
			if e != nil {
				t.Fatal(e)
			}
			out, e := n.Run(ctx, tc.env)
			if e != nil {
				t.Fatal(e)
			}
			if string(out.(graph.Terminal).Result.JSON) != tc.want {
				t.Fatal("wrong result")
			}
			if _, e = n.Run(context.Background(), tc.env); e == nil {
				t.Fatal("result used outside MCP")
			}
		})
	}
	n, _ := newMCPResult("result", MCPResultOptions{Source: "upstream"})
	if _, e := n.Run(ctx, graph.Envelope{Upstream: &graph.UpstreamResponse{Status: 503, Body: []byte("private error")}}); e == nil {
		t.Fatal("upstream failure returned as success")
	}
	var v any
	if DecodeMCPJSON([]byte(`{} {}`), &v) == nil {
		t.Fatal("trailing JSON accepted")
	}
	if DecodeMCPJSON([]byte(strings.Repeat(" ", MCPMaxBytes+1)), &v) == nil {
		t.Fatal("oversized JSON")
	}
}

func TestMCPContractsAssertDeclaredFormats(t *testing.T) {
	schema, err := CompileMCPSchema(map[string]any{"type": "string", "format": "date-time", "pattern": "Z$"})
	if err != nil {
		t.Fatal(err)
	}
	if err = schema.Validate("2026-10-15T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"yesterday", "2026-99-99T00:00:00Z", "2026-10-15T00:00:00+01:00"} {
		if schema.Validate(value) == nil {
			t.Fatalf("invalid UTC timestamp accepted: %s", value)
		}
	}
}

func TestMCPEndpointPathsMustBeDistinctFromAllHTTPRoutes(t *testing.T) {
	flow := func(path string) graph.FlowConfig {
		return graph.FlowConfig{Nodes: []graph.NodeConfig{nodeConfigFromYAML(t, "mcp_trigger", strings.ReplaceAll(mcpNodeFixture, "/mcp", path))}}
	}
	cfg := Config{Flows: map[string]graph.FlowConfig{"services": flow("/services/mcp"), "utilities": flow("/utilities/mcp")}}
	if issues := MCPConfigurationIssues(cfg); len(issues) != 0 {
		t.Fatalf("multiple endpoints rejected: %+v", issues)
	}
	if issues := RoutingIssues(cfg); len(issues) != 0 {
		t.Fatalf("distinct paths rejected: %+v", issues)
	}
	cfg.Flows["utilities"] = flow("/services/mcp")
	if len(RoutingIssues(cfg)) == 0 {
		t.Fatal("overlapping MCP endpoints accepted")
	}
	cfg.Flows["utilities"] = graph.FlowConfig{Nodes: []graph.NodeConfig{nodeConfigFromYAML(t, "http_trigger", "path: /services/mcp")}}
	if len(RoutingIssues(cfg)) == 0 {
		t.Fatal("MCP and HTTP trigger overlap accepted")
	}
	cfg.Flows["utilities"] = graph.FlowConfig{Nodes: []graph.NodeConfig{nodeConfigFromYAML(t, "http_trigger", "path: /.well-known/oauth-protected-resource/services/mcp")}}
	if len(RoutingIssues(cfg)) == 0 {
		t.Fatal("endpoint metadata route shadowed")
	}
}
