package openapiimport

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/jaysongiroux/hook_way/internal/flowconfig"
	"gopkg.in/yaml.v3"
)

func fixture(t *testing.T) []byte {
	t.Helper()
	b, e := os.ReadFile("testdata/api.yaml")
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func settings() Settings {
	return Settings{Endpoint: Endpoint{Path: "/mcp", PublicURL: "http://localhost:8080/mcp", Auth: flowconfig.MCPAuthOptions{APIKeys: map[string]flowconfig.MCPKeyOptions{"reader": {SecretRef: flowconfig.SecretRefOptions{Provider: "env", Name: "IMPORT_READER"}}, "writer": {SecretRef: flowconfig.SecretRefOptions{Provider: "env", Name: "IMPORT_WRITER"}}}}}, Upstream: Upstream{BaseURL: "https://api.example.com/v1"}, Operations: []Selection{{Method: "GET", Path: "/customers/{id}", Access: flowconfig.MCPAccessOptions{APIKeys: []string{"reader", "writer"}}}, {Method: "PATCH", Path: "/customers/{id}", Access: flowconfig.MCPAccessOptions{APIKeys: []string{"writer"}, AllowMutation: true}}}}
}
func requireGenerated(t *testing.T, b []byte, s Settings) Result {
	t.Helper()
	r := Generate(b, s)
	if r.HasErrors() || len(r.YAML) == 0 {
		t.Fatalf("generation failed: %+v", r.Diagnostics)
	}
	return r
}
func TestGenerateEquivalentDocumentsAndDeterministic(t *testing.T) {
	source := fixture(t)
	first := requireGenerated(t, source, settings())
	for range 5 {
		if !bytes.Equal(first.YAML, requireGenerated(t, source, settings()).YAML) {
			t.Fatal("non-deterministic output")
		}
	}
	var root map[string]any
	if e := yaml.Unmarshal(source, &root); e != nil {
		t.Fatal(e)
	}
	jsonDoc, _ := json.Marshal(root)
	if !bytes.Equal(first.YAML, requireGenerated(t, jsonDoc, settings()).YAML) {
		t.Fatal("JSON/YAML differ")
	}
	root["openapi"] = "3.0.3"
	legacy, _ := yaml.Marshal(root)
	requireGenerated(t, legacy, settings())
	cfg := first.Config
	opts, ok := flowconfig.MCPOptions(cfg.Flows["imported_api"])
	if !ok || len(opts.Tools) != 2 {
		t.Fatal("missing tools")
	}
	tool := opts.Tools[0]
	for _, args := range []string{`{"path":{"id":"a"}}`, `{"path":{"id":"a"},"query":{"details":false}}`} {
		s, e := flowconfig.CompileMCPSchema(tool.InputSchema)
		if e != nil {
			t.Fatal(e)
		}
		var value any
		_ = flowconfig.DecodeMCPJSON([]byte(args), &value)
		if e = s.Validate(value); e != nil {
			t.Fatal(e)
		}
	}
	if cfg.Flows["imported_api"].CapturesFailures() {
		t.Fatal("failure capture enabled")
	}
	if !strings.Contains(string(first.YAML), "https://api.example.com/v1/customers/{path_1}") {
		t.Fatal("lost base path")
	}
}
func TestInspectReportsUnsupportedWithoutSkipping(t *testing.T) {
	source := strings.Replace(string(fixture(t)), "type: boolean", "type: array, items: {type: string}", 1)
	r := Inspect([]byte(source))
	if len(r.Operations) != 2 || !hasErrors(r.Operations[0].Diagnostics) {
		t.Fatalf("%+v", r)
	}
	out := Generate([]byte(source), settings())
	if !out.HasErrors() || len(out.YAML) > 0 {
		t.Fatal("partial output")
	}
}
func TestImportFailsClosed(t *testing.T) {
	for name, change := range map[string]func(*Settings){
		"unselected":        func(s *Settings) { s.Operations = nil },
		"mutation_grant":    func(s *Settings) { s.Operations[1].Access.AllowMutation = false },
		"anonymous":         func(s *Settings) { s.Endpoint.Auth = flowconfig.MCPAuthOptions{NoAuth: true} },
		"missing_grant":     func(s *Settings) { s.Operations[0].Access = flowconfig.MCPAccessOptions{} },
		"unknown_key":       func(s *Settings) { s.Operations[0].Access.APIKeys = []string{"other"} },
		"collision":         func(s *Settings) { s.Operations[1].Name = "get_customer" },
		"reserved_name":     func(s *Settings) { s.Operations[0].Name = "out" },
		"unknown_operation": func(s *Settings) { s.Operations[0].Path = "/missing" },
		"unknown_profile":   func(s *Settings) { s.Operations[0].Credential = "missing" },
		"target_injection":  func(s *Settings) { s.Upstream.BaseURL = "https://{host}" },
		"userinfo":          func(s *Settings) { s.Upstream.BaseURL = "https://secret@api.example.com" },
	} {
		t.Run(name, func(t *testing.T) {
			s := settings()
			change(&s)
			r := Generate(fixture(t), s)
			if !r.HasErrors() || len(r.YAML) > 0 {
				t.Fatal(r.Diagnostics)
			}
		})
	}
}
func TestDocumentsCannotLoadExternalState(t *testing.T) {
	for _, ref := range []string{"file:///etc/passwd", "http://127.0.0.1:1/private", "other.yaml#/Customer", "https://169.254.169.254/latest/meta-data"} {
		doc := strings.Replace(string(fixture(t)), "#/components/schemas/Customer", ref, 1)
		r := Inspect([]byte(doc))
		if !hasErrors(r.Diagnostics) {
			t.Fatalf("accepted %s", ref)
		}
	}
	for _, doc := range [][]byte{[]byte("openapi: ["), []byte("openapi: 3.1.0\nopenapi: 3.0.3"), []byte("a: &alias {}\nb: *alias"), []byte("openapi: 3.1.0\n---\n{}"), bytes.Repeat([]byte("x"), MaxDocumentBytes+1)} {
		if !hasErrors(Inspect(doc).Diagnostics) {
			t.Fatal("accepted malformed or oversized input")
		}
	}
}
func TestSchemaConstraintsAndRecursiveReferences(t *testing.T) {
	root := map[string]any{"openapi": "3.0.3", "components": map[string]any{"schemas": map[string]any{"Node": map[string]any{"type": "object", "properties": map[string]any{"child": map[string]any{"$ref": "#/components/schemas/Node"}}}}}}
	conv := converter(root, "request")
	v, e := conv.convert(map[string]any{"type": "object", "additionalProperties": false, "required": []any{"value", "id"}, "properties": map[string]any{"value": map[string]any{"type": "number", "minimum": 1, "exclusiveMinimum": true, "nullable": true, "enum": []any{2, nil}}, "id": map[string]any{"type": "string", "readOnly": true}, "node": map[string]any{"$ref": "#/components/schemas/Node"}}}, 0)
	if e != nil {
		t.Fatal(e)
	}
	compiled, e := flowconfig.CompileMCPSchema(conv.finish(object(v)))
	if e != nil {
		t.Fatal(e)
	}
	for _, doc := range []string{`{"value":2,"node":{"child":{}}}`, `{"value":null}`} {
		var args any
		_ = flowconfig.DecodeMCPJSON([]byte(doc), &args)
		if e = compiled.Validate(args); e != nil {
			t.Fatalf("%s: %v", doc, e)
		}
	}
	for _, doc := range []string{`{"value":1}`, `{"value":3}`, `{}`} {
		var args any
		_ = flowconfig.DecodeMCPJSON([]byte(doc), &args)
		if compiled.Validate(args) == nil {
			t.Fatalf("accepted %s", doc)
		}
	}
	for _, keyword := range []string{"discriminator", "unevaluatedProperties", "$id"} {
		if _, e := converter(root, "request").convert(map[string]any{keyword: true}, 0); e == nil {
			t.Fatalf("silently ignored %s", keyword)
		}
	}
}
func TestSettingsAreStrictAndDoNotExpand(t *testing.T) {
	for _, doc := range []string{"unknown: true", "server: {typo: 1}", "endpoint: {tools: []}", "endpoint: {}\n---\n{}"} {
		if _, e := DecodeSettings([]byte(doc)); e == nil {
			t.Fatalf("accepted %s", doc)
		}
	}
	t.Setenv("IMPORT_MARKER", "must-not-be-expanded")
	s, e := DecodeSettings([]byte("endpoint: {public_url: '${IMPORT_MARKER}'}\nserver: {max_body_bytes: 1234, write_timeout: 10s}"))
	if e != nil || s.Endpoint.PublicURL != "${IMPORT_MARKER}" || s.Server.MaxBodyBytes != 1234 {
		t.Fatalf("%+v %v", s, e)
	}
}

func TestCommittedExampleMatchesGenerator(t *testing.T) {
	source, e := os.ReadFile("../../examples/openapi/api.yaml")
	if e != nil {
		t.Fatal(e)
	}
	settingsData, e := os.ReadFile("../../examples/openapi/settings.yaml")
	if e != nil {
		t.Fatal(e)
	}
	s, e := DecodeSettings(settingsData)
	if e != nil {
		t.Fatal(e)
	}
	result := requireGenerated(t, source, s)
	committed, e := os.ReadFile("../../examples/openapi/core.yaml")
	if e != nil {
		t.Fatal(e)
	}
	if !bytes.Equal(committed, result.YAML) {
		t.Fatal("examples/openapi/core.yaml is stale; regenerate with import-openapi")
	}
}

func TestParameterOverridesAliasesAndRequiredness(t *testing.T) {
	source := string(fixture(t))
	source = strings.Replace(source, "{name: details, in: query, schema: {type: boolean}}", "{name: details, in: query, schema: {type: boolean}}\n        - {name: id, in: path, required: true, schema: {type: string, pattern: '^customer-'}}\n        - {name: x.request, in: header, schema: {type: string}}", 1)
	r := requireGenerated(t, []byte(source), settings())
	opts, _ := flowconfig.MCPOptions(r.Config.Flows["imported_api"])
	tool := opts.Tools[0]
	compiled, e := flowconfig.CompileMCPSchema(tool.InputSchema)
	if e != nil {
		t.Fatal(e)
	}
	for _, tc := range []struct {
		doc   string
		valid bool
	}{{`{"path":{"id":"customer-1"},"headers":{"x_request":"ok"}}`, true}, {`{"path":{"id":"other"}}`, false}, {`{"headers":{"x_request":"ok"}}`, false}} {
		var args any
		_ = flowconfig.DecodeMCPJSON([]byte(tc.doc), &args)
		if (compiled.Validate(args) == nil) != tc.valid {
			t.Fatal(tc.doc)
		}
	}
	if tool.Request.OptionalHeaders["x.request"] != "headers.x_request" {
		t.Fatal(tool.Request)
	}
}
func TestSecurityRequirementsAreExplicit(t *testing.T) {
	source := string(fixture(t)) + "\nsecurity: [{api_key: []}]\n"
	source = strings.Replace(source, "components:\n", "components:\n  securitySchemes:\n    api_key: {type: apiKey, in: header, name: X-API-Key}\n", 1)
	s := settings()
	if !Generate([]byte(source), s).HasErrors() {
		t.Fatal("secured operation imported without credential")
	}
	s.Credentials = map[string]flowconfig.CredentialProfile{"api": {Kind: "static", AllowedOrigins: []string{"https://api.example.com"}, Headers: map[string]flowconfig.CredentialHeader{"X-API-Key": {SecretRef: flowconfig.SecretRefOptions{Provider: "env", Name: "NOT_LOADED"}}}}}
	for i := range s.Operations {
		s.Operations[i].Credential = "api"
	}
	requireGenerated(t, []byte(source), s)
	bad := strings.Replace(source, "in: header, name: X-API-Key", "in: query, name: key", 1)
	if !Generate([]byte(bad), s).HasErrors() {
		t.Fatal("unsupported query credential imported")
	}
	source = strings.Replace(source, "parameters:\n        - {name: details", "parameters:\n        - {name: X-API-Key, in: header, schema: {type: string}}\n        - {name: details", 1)
	if !Generate([]byte(source), s).HasErrors() {
		t.Fatal("credential accepted as agent argument")
	}
}
func TestUnsupportedSchemaAndProtocolFeatures(t *testing.T) {
	source := string(fixture(t))
	for name, changed := range map[string]string{
		"cookie":          strings.Replace(source, "name: details, in: query", "name: details, in: cookie", 1),
		"serialization":   strings.Replace(source, "name: details, in: query", "name: details, in: query, style: deepObject", 1),
		"nullable_scalar": strings.Replace(source, "type: boolean", "type: [boolean, 'null']", 1),
		"unknown_keyword": strings.Replace(source, "type: boolean", "type: boolean, unevaluatedProperties: false", 1),
		"multipart":       strings.Replace(source, "application/json:", "multipart/form-data:", 1),
		"callback":        strings.Replace(source, "operationId: get_customer", "callbacks: {unsupported: {}}\n      operationId: get_customer", 1),
		"unknown_dialect": source + "\njsonSchemaDialect: https://example.com/custom-schema\n",
		"missing_path":    strings.Replace(source, "name: id, in: path", "name: other, in: path", 1),
		"status_range":    strings.Replace(source, "'200':", "'2XX':", 1),
	} {
		t.Run(name, func(t *testing.T) {
			r := Generate([]byte(changed), settings())
			if !r.HasErrors() || len(r.YAML) > 0 {
				t.Fatal("unsupported input accepted")
			}
		})
	}
}
