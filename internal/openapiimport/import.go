// Package openapiimport generates ordinary Hookway flows from selected OpenAPI
// operations. It never reads files, expands environment variables or resolves secrets.
package openapiimport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/jaysongiroux/hook_way/internal/config"
	"github.com/jaysongiroux/hook_way/internal/configapi"
	"github.com/jaysongiroux/hook_way/internal/flowconfig"
	"gopkg.in/yaml.v3"
)

const MaxDocumentBytes = 8 << 20

// Diagnostic identifies a document location without echoing source values.
type Diagnostic struct {
	Severity  string `json:"severity"`
	Location  string `json:"location"`
	Operation string `json:"operation,omitempty"`
	Message   string `json:"message"`
}

type Operation struct {
	Method      string            `json:"method"`
	Path        string            `json:"path"`
	OperationID string            `json:"operation_id,omitempty"`
	Name        string            `json:"name"`
	Security    any               `json:"security"`
	Aliases     map[string]string `json:"aliases,omitempty"`
	Diagnostics []Diagnostic      `json:"diagnostics"`
}

type Report struct {
	Operations  []Operation  `json:"operations"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

type Endpoint struct {
	Path           string                    `yaml:"path"`
	PublicURL      string                    `yaml:"public_url"`
	Auth           flowconfig.MCPAuthOptions `yaml:"auth"`
	AllowedOrigins []string                  `yaml:"allowed_origins,omitempty"`
	MaxInflight    int                       `yaml:"max_inflight,omitempty"`
}

type Upstream struct {
	BaseURL             string `yaml:"base_url"`
	AllowPrivateTargets bool   `yaml:"allow_private_targets,omitempty"`
}

type Selection struct {
	Method      string                      `yaml:"method"`
	Path        string                      `yaml:"path"`
	Name        string                      `yaml:"name,omitempty"`
	Description string                      `yaml:"description,omitempty"`
	Credential  string                      `yaml:"credential,omitempty"`
	Access      flowconfig.MCPAccessOptions `yaml:"access"`
}

type Settings struct {
	Server      config.ServerConfig                     `yaml:"server,omitempty"`
	Endpoint    Endpoint                                `yaml:"endpoint"`
	Upstream    Upstream                                `yaml:"upstream"`
	Credentials map[string]flowconfig.CredentialProfile `yaml:"credentials,omitempty"`
	Operations  []Selection                             `yaml:"operations"`
}

type Result struct {
	Config      flowconfig.Config
	YAML        []byte
	Diagnostics []Diagnostic
}

func (r Result) HasErrors() bool { return hasErrors(r.Diagnostics) }
func hasErrors(ds []Diagnostic) bool {
	for _, d := range ds {
		if d.Severity == "error" {
			return true
		}
	}
	return false
}
func diagnostic(location, message string) Diagnostic {
	return Diagnostic{Severity: "error", Location: location, Message: message}
}

// DecodeSettings rejects unknown fields, aliases, duplicates and extra documents.
func DecodeSettings(data []byte) (Settings, error) {
	var s Settings
	if _, err := decodeDocument(data); err != nil {
		return s, errors.New("invalid import settings document")
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&s); err != nil {
		return s, errors.New("invalid or unknown import settings field")
	}
	return s, nil
}

func decodeDocument(data []byte) (map[string]any, error) {
	if len(data) == 0 || len(data) > MaxDocumentBytes {
		return nil, errors.New("document must be 1..8388608 bytes")
	}
	var node yaml.Node
	dec := yaml.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&node); err != nil {
		return nil, errors.New("invalid JSON or YAML document")
	}
	count := 0
	var visit func(*yaml.Node, int) error
	visit = func(n *yaml.Node, depth int) error {
		count++
		if depth > 64 || count > 100000 || n.Kind == yaml.AliasNode || n.Anchor != "" {
			return errors.New("document exceeds structure limits or contains YAML aliases")
		}
		for _, child := range n.Content {
			if err := visit(child, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	if err := visit(&node, 0); err != nil {
		return nil, err
	}
	var root map[string]any
	if err := node.Decode(&root); err != nil || root == nil {
		return nil, errors.New("document must be an object with unique string keys")
	}
	var extra yaml.Node
	if !errors.Is(dec.Decode(&extra), io.EOF) {
		return nil, errors.New("only one document is allowed")
	}
	return root, nil
}

func load(data []byte) (map[string]any, []Diagnostic) {
	root, err := decodeDocument(data)
	if err != nil {
		return nil, []Diagnostic{diagnostic("$", err.Error())}
	}
	version, _ := root["openapi"].(string)
	if !regexp.MustCompile(`^3\.(0|1)\.[0-9]+$`).MatchString(version) {
		return nil, []Diagnostic{diagnostic("openapi", "only OpenAPI 3.0.x and 3.1.x are supported")}
	}
	if dialect, ok := root["jsonSchemaDialect"]; ok && dialect != "https://json-schema.org/draft/2020-12/schema" && dialect != "https://spec.openapis.org/oas/3.1/dialect/base" {
		return nil, []Diagnostic{diagnostic("jsonSchemaDialect", "unsupported JSON Schema dialect")}
	}
	// Reject references before the library sees the document. Even a custom loader
	// must never allow external references or read a referenced file/URL.
	var refs func(any) bool
	refs = func(v any) bool {
		switch x := v.(type) {
		case map[string]any:
			for k, v := range x {
				if k == "$ref" {
					s, ok := v.(string)
					if !ok || !strings.HasPrefix(s, "#/") {
						return false
					}
				}
				if !refs(v) {
					return false
				}
			}
		case []any:
			for _, v := range x {
				if !refs(v) {
					return false
				}
			}
		}
		return true
	}
	if !refs(root) {
		return nil, []Diagnostic{diagnostic("$ref", "only same-document JSON Pointer references are supported")}
	}
	raw, err := json.Marshal(root)
	if err != nil {
		return nil, []Diagnostic{diagnostic("$", "document is not JSON compatible")}
	}
	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = false
	doc, err := loader.LoadFromData(raw)
	if err != nil {
		return nil, []Diagnostic{diagnostic("$", "invalid OpenAPI structure or unresolved reference")}
	}
	if err := doc.Validate(context.Background(), openapi3.DisableExamplesValidation()); err != nil {
		return nil, []Diagnostic{diagnostic("$", "invalid OpenAPI document")}
	}
	return root, nil
}

func object(v any) map[string]any { m, _ := v.(map[string]any); return m }
func text(v any) string           { s, _ := v.(string); return s }
func keys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
func ptrPart(s string) string { return strings.ReplaceAll(strings.ReplaceAll(s, "~", "~0"), "/", "~1") }
func resolve(root map[string]any, value any) (map[string]any, error) {
	seen := map[string]bool{}
	for i := 0; i < 64; i++ {
		m := object(value)
		if m == nil {
			return nil, errors.New("expected an object")
		}
		ref, exists := m["$ref"]
		if !exists {
			return m, nil
		}
		r, ok := ref.(string)
		if !ok || !strings.HasPrefix(r, "#/") || seen[r] {
			return nil, errors.New("invalid or cyclic object reference")
		}
		seen[r] = true
		target, err := reference(root, r)
		if err != nil {
			return nil, err
		}
		value = target
	}
	return nil, errors.New("reference depth exceeded")
}

var unsafeName = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)

func normalize(s string) string {
	s = strings.Trim(unsafeName.ReplaceAllString(s, "_"), "_")
	if s == "" {
		s = "operation"
	}
	if len(s) > 64 {
		s = s[:64]
	}
	if s == "out" || s == "on_error" {
		s = "tool_" + s
	}
	return s
}
func toolName(method, path string, op map[string]any) string {
	name := text(op["operationId"])
	if name == "" {
		name = strings.ToLower(method) + "_" + path
	}
	return normalize(name)
}

// Inspect enumerates operations and diagnoses their importability without grants.
func Inspect(data []byte) Report {
	root, ds := load(data)
	report := Report{Operations: []Operation{}, Diagnostics: ds}
	if root == nil {
		return report
	}
	for _, path := range keys(object(root["paths"])) {
		item, err := resolve(root, object(root["paths"])[path])
		if err != nil {
			report.Diagnostics = append(report.Diagnostics, diagnostic("paths."+path, "invalid path item"))
			continue
		}
		for _, method := range []string{"get", "post", "put", "patch", "delete", "head", "options", "trace"} {
			op := object(item[method])
			if op == nil {
				continue
			}
			sel := Selection{Method: strings.ToUpper(method), Path: path}
			built := buildOperation(root, item, op, sel)
			security, ok := op["security"]
			if !ok {
				security = root["security"]
			}
			report.Operations = append(report.Operations, Operation{Method: sel.Method, Path: path, OperationID: text(op["operationId"]), Name: toolName(sel.Method, path, op), Security: security, Aliases: built.aliases, Diagnostics: built.diagnostics})
		}
	}
	if len(object(root["webhooks"])) > 0 {
		report.Diagnostics = append(report.Diagnostics, diagnostic("webhooks", "webhook definitions are not imported"))
	}
	return report
}

// Generate emits a new standalone configuration, or diagnostics and no output.
func Generate(data []byte, s Settings) Result {
	root, ds := load(data)
	out := Result{Diagnostics: ds}
	if root == nil {
		return out
	}
	fail := func(loc, msg string) { out.Diagnostics = append(out.Diagnostics, diagnostic(loc, msg)) }
	if len(s.Operations) == 0 || len(s.Operations) > 64 {
		fail("operations", "select between 1 and 64 operations")
		return out
	}
	if s.Endpoint.Auth.NoAuth || (len(s.Endpoint.Auth.APIKeys) == 0 && s.Endpoint.Auth.OAuth == nil) {
		fail("endpoint.auth", "authenticated MCP access is required")
	}
	base, err := url.Parse(s.Upstream.BaseURL)
	if err != nil || base.Host == "" || (base.Scheme != "https" && base.Scheme != "http") || base.User != nil || base.RawQuery != "" || base.Fragment != "" || strings.ContainsAny(s.Upstream.BaseURL, "{}$\\\r\n") {
		fail("upstream.base_url", "a literal HTTP(S) base URL without credentials, query or fragment is required")
		return out
	}
	if s.Server.Addr == "" {
		s.Server.Addr = "127.0.0.1:8080"
	}
	if s.Server.MaxBodyBytes == 0 {
		s.Server.MaxBodyBytes = flowconfig.MCPMaxBytes
	}
	trigger := flowconfig.MCPTriggerOptions{Path: s.Endpoint.Path, PublicURL: s.Endpoint.PublicURL, Auth: s.Endpoint.Auth, AllowedOrigins: s.Endpoint.AllowedOrigins, MaxInflight: s.Endpoint.MaxInflight}
	nodes := []map[string]any{}
	edges := []string{}
	names, selected := map[string]bool{}, map[string]bool{}
	for i, sel := range s.Operations {
		loc := fmt.Sprintf("operations[%d]", i)
		if sel.Method != strings.ToUpper(sel.Method) {
			fail(loc, "method must be uppercase")
			continue
		}
		id := sel.Method + " " + sel.Path
		if selected[id] {
			fail(loc, "operation selected more than once")
			continue
		}
		selected[id] = true
		item, e := resolve(root, object(root["paths"])[sel.Path])
		if e != nil {
			fail(loc, "selected path does not exist")
			continue
		}
		op := object(item[strings.ToLower(sel.Method)])
		if op == nil {
			fail(loc, "selected method does not exist")
			continue
		}
		b := buildOperation(root, item, op, sel)
		out.Diagnostics = append(out.Diagnostics, b.diagnostics...)
		if hasErrors(b.diagnostics) {
			continue
		}
		name := sel.Name
		if name == "" {
			name = toolName(sel.Method, sel.Path, op)
		}
		if name != normalize(name) || len(name) > 64 || names[name] {
			fail(loc, "tool name must be valid, unique and not reserved")
			continue
		}
		names[name] = true
		readonly := sel.Method == http.MethodGet
		if !readonly && !sel.Access.AllowMutation {
			fail(loc, "write operations require access.allow_mutation: true")
			continue
		}
		security, ok := op["security"]
		if !ok {
			security = root["security"]
		}
		if requiresAuth(security) && sel.Credential == "" {
			fail(loc, "secured upstream operation requires an explicit credential profile")
			continue
		}
		if sel.Credential != "" {
			p, ok := s.Credentials[sel.Credential]
			if !ok {
				fail(loc, "unknown credential profile")
				continue
			}
			for _, param := range b.headerNames {
				for h := range p.Headers {
					if strings.EqualFold(h, param) {
						fail(loc, "parameter conflicts with managed credential header")
					}
				}
			}
			if !profileSatisfies(root, security, p) {
				fail(loc, "credential profile does not satisfy a supported upstream security requirement")
				continue
			}
		}
		desc := sel.Description
		if desc == "" {
			desc = text(op["summary"])
			if desc == "" {
				desc = text(op["description"])
			}
			if desc == "" {
				desc = id
			}
		}
		trigger.Tools = append(trigger.Tools, flowconfig.MCPToolOptions{Name: name, Description: desc, ReadOnly: readonly, Access: sel.Access, InputSchema: b.input, OutputSchema: b.output, Request: b.request})
		request := b.http
		request.URL = strings.TrimRight(s.Upstream.BaseURL, "/") + b.path
		request.Method = sel.Method
		request.AllowPrivateTargets = s.Upstream.AllowPrivateTargets
		if sel.Credential != "" {
			request.Credential = &flowconfig.HTTPCredentialOptions{Profile: sel.Credential}
		}
		httpID := fmt.Sprintf("request_%d", i+1)
		resultID := fmt.Sprintf("result_%d", i+1)
		nodes = append(nodes, node(httpID, "http_request", request), node(resultID, "mcp_result", flowconfig.MCPResultOptions{Source: "upstream", Wrap: "data", EmptyStatuses: b.empty}))
		edges = append(edges, "mcp:"+name+">"+httpID, httpID+">"+resultID)
		for from, to := range b.aliases {
			out.Diagnostics = append(out.Diagnostics, Diagnostic{Severity: "info", Location: loc, Operation: id, Message: "argument alias: " + from + " -> " + to})
		}
	}
	if hasErrors(out.Diagnostics) {
		return out
	}
	nodes = append([]map[string]any{node("mcp", "mcp_trigger", trigger)}, nodes...)
	document := map[string]any{"server": s.Server, "flows": map[string]any{"imported_api": map[string]any{"capture_failures": false, "nodes": nodes, "edges": edges}}}
	if len(s.Credentials) > 0 {
		document["credentials"] = s.Credentials
	}
	raw, err := marshalLiteralYAML(document)
	if err != nil {
		fail("$", "unable to serialize configuration")
		return out
	}
	for _, issue := range configapi.Validate(raw).Issues {
		out.Diagnostics = append(out.Diagnostics, Diagnostic{Severity: issue.Severity, Location: issue.Field, Message: issue.Message})
	}
	if hasErrors(out.Diagnostics) {
		return out
	}
	cfg, issues := flowconfig.LoadBytes(raw, "", false)
	if len(issues) > 0 {
		fail("$", "generated configuration failed validation")
		return out
	}
	out.Config = cfg
	out.YAML = raw
	sort.SliceStable(out.Diagnostics, func(i, j int) bool {
		a, b := out.Diagnostics[i], out.Diagnostics[j]
		return a.Location+a.Message < b.Location+b.Message
	})
	return out
}

func node(id, kind string, options any) map[string]any {
	raw, _ := yaml.Marshal(options)
	result := map[string]any{}
	_ = yaml.Unmarshal(raw, &result)
	result["id"] = id
	result["type"] = kind
	return result
}
func requiresAuth(v any) bool {
	list, _ := v.([]any)
	if len(list) == 0 {
		return false
	}
	for _, r := range list {
		if len(object(r)) == 0 {
			return false
		}
	}
	return true
}
func profileSatisfies(root map[string]any, security any, p flowconfig.CredentialProfile) bool {
	if !requiresAuth(security) {
		return true
	}
	list, _ := security.([]any)
	for _, entry := range list {
		valid := true
		for name := range object(entry) {
			scheme, e := resolve(root, object(object(root["components"])["securitySchemes"])[name])
			if e != nil {
				valid = false
				break
			}
			switch text(scheme["type"]) {
			case "apiKey":
				header := text(scheme["name"])
				found := false
				for key := range p.Headers {
					found = found || strings.EqualFold(header, key)
				}
				valid = valid && text(scheme["in"]) == "header" && found
			case "http":
				found := false
				for key := range p.Headers {
					found = found || strings.EqualFold(key, "Authorization")
				}
				valid = valid && (text(scheme["scheme"]) == "basic" || text(scheme["scheme"]) == "bearer") && (found || p.Kind != "static")
			case "oauth2", "openIdConnect":
				valid = valid && (p.Kind == "oauth_client_credentials" || p.Kind == "token_exchange")
			default:
				valid = false
			}
		}
		if valid {
			return true
		}
	}
	return false
}

// Encode dollars as YAML Unicode escapes so normal raw-text config environment
// expansion cannot reinterpret imported descriptions, schemas or other literals.
// Both expanding and non-expanding loaders decode exactly the same values.
func marshalLiteralYAML(document any) ([]byte, error) {
	var root yaml.Node
	if err := root.Encode(document); err != nil {
		return nil, err
	}
	var quote func(*yaml.Node)
	quote = func(n *yaml.Node) {
		if n.Kind == yaml.ScalarNode && n.Tag == "!!str" && strings.Contains(n.Value, "$") {
			n.Style = yaml.DoubleQuotedStyle
		}
		for _, child := range n.Content {
			quote(child)
		}
	}
	quote(&root)
	raw, err := yaml.Marshal(&root)
	if err != nil {
		return nil, err
	}
	return bytes.ReplaceAll(raw, []byte("$"), []byte(`\u0024`)), nil
}
