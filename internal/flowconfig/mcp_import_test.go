package flowconfig

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"github.com/jaysongiroux/hook_way/internal/graph"
)

func TestMCPOptionalMappingsAndWholeBody(t *testing.T) {
	tool := MCPToolOptions{Request: MCPRequestMapping{OptionalQuery: map[string]string{"limit": "query.limit", "empty": "query.empty", "enabled": "query.enabled"}, OptionalHeaders: map[string]string{"X-Request-ID": "headers.request_id"}, BodySource: "body", BodyOptional: true}}
	for _, tc := range []struct {
		input, body string
		query       map[string]string
	}{
		{`{}`, "", nil},
		{`{"query":{"limit":0,"empty":"","enabled":false},"body":null}`, `null`, map[string]string{"limit": "0", "empty": "", "enabled": "false"}},
		{`{"body":{"nested":[1,true]}}`, `{"nested":[1,true]}`, nil},
	} {
		var args any
		if e := DecodeMCPJSON([]byte(tc.input), &args); e != nil {
			t.Fatal(e)
		}
		env, e := MCPEnvelope(tool, args, graph.RequestHead{})
		if e != nil {
			t.Fatal(e)
		}
		if string(env.V.Raw.Body) != tc.body {
			t.Fatalf("body %q != %q", env.V.Raw.Body, tc.body)
		}
		if len(env.V.Raw.Head.Query) != len(tc.query) {
			t.Fatal(env.V.Raw.Head.Query)
		}
		for k, v := range tc.query {
			if env.V.Raw.Head.Query.Get(k) != v {
				t.Fatal(env.V.Raw.Head.Query)
			}
		}
	}
	for _, doc := range []string{`{"query":{"limit":null}}`, `{"query":{"limit":[]}}`, `{"headers":{"request_id":"bad\r\nheader"}}`} {
		var args any
		_ = DecodeMCPJSON([]byte(doc), &args)
		if _, e := MCPEnvelope(tool, args, graph.RequestHead{}); e == nil {
			t.Fatalf("accepted %s", doc)
		}
	}
	tool.Request.BodyOptional = false
	if _, e := MCPEnvelope(tool, map[string]any{}, graph.RequestHead{}); e == nil {
		t.Fatal("missing required body accepted")
	}
}
func TestMCPImportMappingValidation(t *testing.T) {
	for name, change := range map[string]func(*MCPRequestMapping){
		"duplicate_query": func(r *MCPRequestMapping) {
			r.Query = map[string]string{"a": "a"}
			r.OptionalQuery = map[string]string{"a": "a"}
		},
		"duplicate_header": func(r *MCPRequestMapping) {
			r.Headers = map[string]string{"X-Custom": "a"}
			r.OptionalHeaders = map[string]string{"x-custom": "a"}
		},
		"optional_auth":         func(r *MCPRequestMapping) { r.OptionalHeaders = map[string]string{"Authorization": "a"} },
		"conflicting_body":      func(r *MCPRequestMapping) { r.Body = map[string]string{"a": "a"}; r.BodySource = "body" },
		"optional_without_body": func(r *MCPRequestMapping) { r.BodyOptional = true },
	} {
		t.Run(name, func(t *testing.T) {
			o := validMCP(t)
			change(&o.Tools[0].Request)
			if o.Validate() == nil {
				t.Fatal("invalid mapping accepted")
			}
		})
	}
}
func TestMCPWrappedResultsAndDeclaredEmptyStatuses(t *testing.T) {
	ctx := WithMCPInvocation(context.Background(), "test", false, true)
	for _, tc := range []struct {
		status     int
		body, want string
		valid      bool
	}{
		{200, `[1,"two"]`, `{"data":[1,"two"]}`, true},
		{200, `"value"`, `{"data":"value"}`, true},
		{200, `false`, `{"data":false}`, true},
		{204, "", `{"data":null}`, true},
		{200, "", "", false},
		{500, `{}`, "", false},
		{200, "not-json", "", false},
	} {
		node, e := newMCPResult("result", MCPResultOptions{Source: "upstream", Wrap: "data", EmptyStatuses: []int{204}})
		if e != nil {
			t.Fatal(e)
		}
		out, e := node.Run(ctx, graph.Envelope{Upstream: &graph.UpstreamResponse{Status: tc.status, Body: []byte(tc.body)}})
		if (e == nil) != tc.valid {
			t.Fatalf("status %d body %q: %v", tc.status, tc.body, e)
		}
		if tc.valid {
			got := out.(graph.Terminal).Result.JSON
			if string(got) != tc.want {
				t.Fatalf("%s != %s", got, tc.want)
			}
		}
	}
	for _, opts := range []MCPResultOptions{{Source: "body", EmptyStatuses: []int{204}}, {Source: "upstream", EmptyStatuses: []int{500}}, {Source: "upstream", EmptyStatuses: []int{204, 204}}} {
		if _, e := newMCPResult("result", opts); e == nil {
			t.Fatal("invalid empty statuses accepted")
		}
	}
	env := graph.Envelope{Body: map[string]any{"secret": "ignored"}, V: graph.Verified{Raw: graph.RawRequest{Body: json.RawMessage(`{"secret":"ignored"}`)}}}
	raw, typ, e := outboundBody(env, "none")
	if e != nil || len(raw) > 0 || typ != "" {
		t.Fatalf("body:none: %s %s %v", raw, typ, e)
	}
	n, e := newHTTPRequestNode("request", HTTPRequestOptions{Method: http.MethodPost, URL: "https://example.com", Body: "none"}, nil, nil)
	if e != nil {
		t.Fatal(e)
	}
	raw, _, e = n.(httpRequestNode).requestBody(env, http.MethodPost)
	if e != nil || !reflect.DeepEqual(raw, []byte(nil)) {
		t.Fatal("request body not suppressed")
	}
}
