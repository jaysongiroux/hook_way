package flowconfig

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jaysongiroux/hook_way/internal/config"
	"github.com/jaysongiroux/hook_way/internal/graph"
	"github.com/jaysongiroux/hook_way/internal/jsonbudget"
	"github.com/jaysongiroux/hook_way/internal/vars"
	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

const MCPMaxBytes = 1 << 20

var mcpName = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

type MCPTriggerOptions struct {
	Path           string           `yaml:"path" json:"path" jsonschema:"required"`
	PublicURL      string           `yaml:"public_url" json:"public_url" jsonschema:"required"`
	AllowedOrigins []string         `yaml:"allowed_origins,omitempty" json:"allowed_origins,omitempty"`
	Auth           MCPAuthOptions   `yaml:"auth" json:"auth" jsonschema:"required"`
	Tools          []MCPToolOptions `yaml:"tools" json:"tools" jsonschema:"required,minItems=1,maxItems=64"`
	MaxInflight    int              `yaml:"max_inflight,omitempty" json:"max_inflight,omitempty" jsonschema:"minimum=1,maximum=256"`
}
type MCPAuthOptions struct {
	NoAuth  bool                     `yaml:"no_auth,omitempty" json:"no_auth,omitempty"`
	APIKeys map[string]MCPKeyOptions `yaml:"api_keys,omitempty" json:"api_keys,omitempty"`
	OAuth   *MCPOAuthOptions         `yaml:"oauth,omitempty" json:"oauth,omitempty"`
}
type MCPKeyOptions struct {
	SecretRef SecretRefOptions `yaml:"secret_ref" json:"secret_ref" jsonschema:"required"`
	ExpiresAt string           `yaml:"expires_at,omitempty" json:"expires_at,omitempty"`
}
type MCPOAuthOptions struct {
	DiscoveryScopes     []string          `yaml:"discovery_scopes,omitempty" json:"discovery_scopes,omitempty"`
	Issuer              string            `yaml:"issuer" json:"issuer" jsonschema:"required"`
	Audience            string            `yaml:"audience" json:"audience" jsonschema:"required"`
	JWKSURL             string            `yaml:"jwks_url,omitempty" json:"jwks_url,omitempty"`
	RolesClaim          string            `yaml:"roles_claim,omitempty" json:"roles_claim,omitempty"`
	IntrospectionURL    string            `yaml:"introspection_url,omitempty" json:"introspection_url,omitempty"`
	ClientID            *SecretRefOptions `yaml:"client_id,omitempty" json:"client_id,omitempty"`
	ClientSecret        *SecretRefOptions `yaml:"client_secret,omitempty" json:"client_secret,omitempty"`
	AllowPrivateTargets bool              `yaml:"allow_private_targets,omitempty" json:"allow_private_targets,omitempty"`
}
type MCPAccessOptions struct {
	APIKeys       []string `yaml:"api_keys,omitempty" json:"api_keys,omitempty"`
	Scopes        []string `yaml:"scopes,omitempty" json:"scopes,omitempty"`
	Roles         []string `yaml:"roles,omitempty" json:"roles,omitempty"`
	Anonymous     bool     `yaml:"anonymous,omitempty" json:"anonymous,omitempty"`
	AllowMutation bool     `yaml:"allow_mutation,omitempty" json:"allow_mutation,omitempty"`
}
type MCPRequestMapping struct {
	OptionalQuery   map[string]string `yaml:"optional_query,omitempty" json:"optional_query,omitempty"`
	OptionalHeaders map[string]string `yaml:"optional_headers,omitempty" json:"optional_headers,omitempty"`
	BodySource      string            `yaml:"body_source,omitempty" json:"body_source,omitempty"`
	BodyOptional    bool              `yaml:"body_optional,omitempty" json:"body_optional,omitempty"`
	Variables       map[string]string `yaml:"variables,omitempty" json:"variables,omitempty"`
	Query           map[string]string `yaml:"query,omitempty" json:"query,omitempty"`
	Headers         map[string]string `yaml:"headers,omitempty" json:"headers,omitempty"`
	Body            map[string]string `yaml:"body,omitempty" json:"body,omitempty"`
}
type MCPToolOptions struct {
	Name              string            `yaml:"name" json:"name" jsonschema:"required"`
	Description       string            `yaml:"description" json:"description" jsonschema:"required"`
	InputSchema       map[string]any    `yaml:"input_schema" json:"input_schema" jsonschema:"required"`
	OutputSchema      map[string]any    `yaml:"output_schema" json:"output_schema" jsonschema:"required"`
	Access            MCPAccessOptions  `yaml:"access" json:"access" jsonschema:"required"`
	ReadOnly          bool              `yaml:"read_only" json:"read_only" jsonschema:"required"`
	Timeout           string            `yaml:"timeout,omitempty" json:"timeout,omitempty"`
	MaxInflight       int               `yaml:"max_inflight,omitempty" json:"max_inflight,omitempty" jsonschema:"minimum=1,maximum=256"`
	RequestsPerMinute int               `yaml:"requests_per_minute,omitempty" json:"requests_per_minute,omitempty" jsonschema:"minimum=1"`
	AllowRetries      bool              `yaml:"allow_retries,omitempty" json:"allow_retries,omitempty"`
	Request           MCPRequestMapping `yaml:"request,omitempty" json:"request,omitempty"`
}
type MCPResultOptions struct {
	Wrap          string            `yaml:"wrap,omitempty" json:"wrap,omitempty"`
	EmptyStatuses []int             `yaml:"empty_statuses,omitempty" json:"empty_statuses,omitempty"`
	Source        string            `yaml:"source" json:"source" jsonschema:"required,enum=body,enum=upstream,enum=constant"`
	Field         string            `yaml:"field,omitempty" json:"field,omitempty"`
	Fields        map[string]string `yaml:"fields,omitempty" json:"fields,omitempty"`
	Value         any               `yaml:"value,omitempty" json:"value,omitempty"`
}

func MCPOptions(fc graph.FlowConfig) (MCPTriggerOptions, bool) {
	for _, n := range fc.Nodes {
		if n.Type == "mcp_trigger" {
			o, _ := decodeOptions[MCPTriggerOptions](n)
			return o, true
		}
	}
	return MCPTriggerOptions{}, false
}
func (t MCPToolOptions) Deadline() time.Duration {
	if t.Timeout == "" {
		return 30 * time.Second
	}
	d, _ := time.ParseDuration(t.Timeout)
	return d
}
func (t MCPToolOptions) Concurrency() int {
	if t.MaxInflight == 0 {
		return 4
	}
	return t.MaxInflight
}
func validMCPURL(s string, private bool) bool {
	u, e := url.Parse(s)
	if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	return u.Scheme == "https" || (u.Scheme == "http" && (private || u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1"))
}

// Validate checks the trigger contract before runtime resources are created.
func (o MCPTriggerOptions) Validate() error { return validateMCPTrigger(o) }

func validateMCPTrigger(o MCPTriggerOptions) error {
	u, _ := url.Parse(o.PublicURL)
	if !validMCPURL(o.PublicURL, false) || u.Path != o.Path || !strings.HasPrefix(o.Path, "/") || strings.ContainsAny(o.Path, "{}?#% \t\r\n") || strings.HasSuffix(o.Path, "/") || path.Clean(o.Path) != o.Path || len(o.Path) > 2048 {
		return errors.New("MCP requires a literal path and matching canonical public_url (HTTPS except localhost)")
	}
	if o.MaxInflight < 0 || o.MaxInflight > 256 || len(o.Tools) == 0 || len(o.Tools) > 64 || len(o.Auth.APIKeys) > 64 {
		return errors.New("MCP limits exceeded or tools missing")
	}
	if o.Auth.NoAuth == (len(o.Auth.APIKeys) > 0 || o.Auth.OAuth != nil) {
		return errors.New("MCP requires API keys and/or OAuth, or explicit exclusive no_auth")
	}
	for name, k := range o.Auth.APIKeys {
		if !mcpName.MatchString(name) {
			return errors.New("invalid MCP key name")
		}
		if err := validateCredentialSecret(k.SecretRef); err != nil {
			return err
		}
		if k.ExpiresAt != "" {
			if _, err := time.Parse(time.RFC3339, k.ExpiresAt); err != nil {
				return errors.New("invalid MCP key expires_at")
			}
		}
	}
	if a := o.Auth.OAuth; a != nil {
		if !validMCPURL(a.Issuer, a.AllowPrivateTargets) || a.Audience == "" || len(a.Audience) > 2048 {
			return errors.New("OAuth requires issuer and dedicated audience")
		}
		if !validMCPScopes(a.DiscoveryScopes) {
			return errors.New("invalid OAuth discovery scopes")
		}
		for _, s := range []string{a.JWKSURL, a.IntrospectionURL} {
			if s != "" && !validMCPURL(s, a.AllowPrivateTargets) {
				return errors.New("invalid OAuth validation URL")
			}
		}
		if a.IntrospectionURL != "" {
			if a.ClientID == nil || a.ClientSecret == nil {
				return errors.New("introspection requires client_id and client_secret references")
			}
			if err := validateCredentialSecret(*a.ClientID); err != nil {
				return err
			}
			if err := validateCredentialSecret(*a.ClientSecret); err != nil {
				return err
			}
		}
	}
	for _, origin := range o.AllowedOrigins {
		if _, err := CredentialOrigin(origin, true); err != nil {
			return errors.New("MCP allowed_origins must be exact HTTP(S) origins")
		}
	}
	names := map[string]bool{}
	for _, t := range o.Tools {
		if t.Name == "out" || t.Name == "on_error" || !mcpName.MatchString(t.Name) || names[t.Name] || strings.TrimSpace(t.Description) == "" || len(t.Description) > 4096 {
			return errors.New("MCP tools require unique valid names and descriptions")
		}
		names[t.Name] = true
		if t.InputSchema["type"] != "object" {
			return errors.New("MCP input_schema must declare type: object")
		}
		if _, err := CompileMCPSchema(t.InputSchema); err != nil {
			return fmt.Errorf("tool %s input_schema: %w", t.Name, err)
		}
		if _, err := CompileMCPSchema(t.OutputSchema); err != nil {
			return fmt.Errorf("tool %s output_schema: %w", t.Name, err)
		}
		if t.Deadline() <= 0 || t.Deadline() > 120*time.Second || t.MaxInflight < 0 || t.MaxInflight > 256 || t.RequestsPerMinute < 0 || t.RequestsPerMinute > 1000000 {
			return errors.New("invalid MCP tool deadline or limits")
		}
		a := t.Access
		if !validMCPScopes(a.Scopes) || len(a.Roles) > 64 || len(a.APIKeys) > 64 {
			return errors.New("invalid MCP access grants")
		}
		for _, role := range a.Roles {
			if role == "" || len(role) > 256 {
				return errors.New("invalid MCP role")
			}
		}
		if len(a.APIKeys)+len(a.Scopes)+len(a.Roles) == 0 && !a.Anonymous {
			return errors.New("MCP tool requires explicit access")
		}
		if a.Anonymous && !o.Auth.NoAuth {
			return errors.New("anonymous access requires no_auth")
		}
		if !t.ReadOnly && !a.AllowMutation {
			return errors.New("mutating MCP tools require access.allow_mutation")
		}
		if !t.ReadOnly && t.AllowRetries {
			return errors.New("mutating MCP tools cannot retry automatically")
		}
		for _, k := range a.APIKeys {
			if _, ok := o.Auth.APIKeys[k]; !ok {
				return errors.New("MCP tool names an unknown API key")
			}
		}
		if (len(a.Scopes) > 0 || len(a.Roles) > 0) && o.Auth.OAuth == nil {
			return errors.New("scope/role grants require OAuth")
		}
		if (t.Request.BodySource != "" && (!validSelectionPath(t.Request.BodySource) || len(t.Request.Body) > 0)) || (t.Request.BodyOptional && t.Request.BodySource == "") {
			return errors.New("body_source requires a valid selector, excludes body mappings, and is required by body_optional")
		}
		for key := range t.Request.OptionalQuery {
			if _, exists := t.Request.Query[key]; exists {
				return errors.New("duplicate MCP query mapping")
			}
		}
		seenHeaders := map[string]bool{}
		for _, mappings := range []map[string]string{t.Request.Headers, t.Request.OptionalHeaders} {
			for key := range mappings {
				key = http.CanonicalHeaderKey(key)
				if seenHeaders[key] {
					return errors.New("duplicate MCP header mapping")
				}
				seenHeaders[key] = true
			}
		}
		for kind, m := range map[string]map[string]string{"variables": t.Request.Variables, "query": t.Request.Query, "headers": t.Request.Headers, "body": t.Request.Body, "optional_query": t.Request.OptionalQuery, "optional_headers": t.Request.OptionalHeaders} {
			if len(m) > 64 {
				return errors.New("too many MCP request mappings")
			}
			for key, selector := range m {
				if key == "" || len(key) > 128 || !validSelectionPath(selector) {
					return errors.New("invalid MCP request mapping")
				}
				if kind == "variables" && !vars.ValidName(key) {
					return errors.New("invalid MCP variable")
				}
				if (kind == "headers" || kind == "optional_headers") && (!validAuthHeader(key) || mcpCredentialHeader(key) || isHopByHop(key) || strings.EqualFold(key, "Authorization") || strings.EqualFold(key, "Cookie") || strings.HasPrefix(strings.ToLower(key), "mcp-") || strings.HasPrefix(strings.ToLower(key), "x-hookway-") || strings.EqualFold(key, "Host") || strings.EqualFold(key, "Content-Length")) {
					return errors.New("reserved MCP request header")
				}
			}
		}
	}
	return nil
}

func mcpCredentialHeader(header string) bool {
	return sensitiveResponseField(header) && !strings.EqualFold(header, "Idempotency-Key") && !strings.EqualFold(header, "X-Idempotency-Key")
}

func validMCPScopes(scopes []string) bool {
	if len(scopes) > 64 {
		return false
	}
	for _, scope := range scopes {
		if len(scope) == 0 || len(scope) > 256 {
			return false
		}
		for _, c := range scope {
			if c < 0x21 || c > 0x7e || c == '"' || c == '\\' {
				return false
			}
		}
	}
	return true
}

type mcpSchemaLoader struct{}

func (mcpSchemaLoader) Load(string) (any, error) {
	return nil, errors.New("external schema loading disabled")
}
func CompileMCPSchema(s map[string]any) (*jsonschema.Schema, error) {
	if len(s) == 0 {
		return nil, errors.New("schema is required")
	}
	raw, e := json.Marshal(s)
	if e != nil || len(raw) > 64<<10 {
		return nil, errors.New("schema exceeds limit")
	}
	if e = jsonbudget.Check(raw, jsonbudget.Limits{}); e != nil {
		return nil, errors.New("schema exceeds structure limits")
	}
	var check func(any) error
	check = func(v any) error {
		switch x := v.(type) {
		case map[string]any:
			for k, v := range x {
				if k == "$ref" || k == "$dynamicRef" {
					r, ok := v.(string)
					if !ok || !strings.HasPrefix(r, "#") {
						return errors.New("only local schema references are allowed")
					}
				}
				if e := check(v); e != nil {
					return e
				}
			}
		case []any:
			for _, v := range x {
				if e := check(v); e != nil {
					return e
				}
			}
		}
		return nil
	}
	if e = check(s); e != nil {
		return nil, e
	}
	c := jsonschema.NewCompiler()
	c.AssertFormat()
	c.UseLoader(mcpSchemaLoader{})
	if e = c.AddResource("https://hookway.invalid/mcp-schema", s); e != nil {
		return nil, errors.New("invalid JSON Schema")
	}
	out, e := c.Compile("https://hookway.invalid/mcp-schema")
	if e != nil {
		return nil, errors.New("invalid JSON Schema")
	}
	return out, nil
}

type mcpInvocationKey struct{}
type MCPInvocation struct {
	Tool         string
	AllowRetries bool
	ReadOnly     bool
	credentials  *mcpCredentials
}

// Native credential material stays in trusted context, outside graph data.
// Detect accidental credential echoes before returning projected upstream data.
type mcpCredentials struct {
	mu       sync.Mutex
	values   []string
	bytes    int
	overflow bool
}

func WithMCPInvocation(ctx context.Context, tool string, retries, readOnly bool) context.Context {
	return context.WithValue(ctx, mcpInvocationKey{}, MCPInvocation{Tool: tool, AllowRetries: retries, ReadOnly: readOnly, credentials: &mcpCredentials{}})
}
func MCPInvocationFrom(ctx context.Context) (MCPInvocation, bool) {
	v, ok := ctx.Value(mcpInvocationKey{}).(MCPInvocation)
	return v, ok
}

func recordMCPCredentials(ctx context.Context, material CredentialMaterial) {
	inv, ok := MCPInvocationFrom(ctx)
	if !ok || inv.credentials == nil {
		return
	}
	inv.credentials.mu.Lock()
	defer inv.credentials.mu.Unlock()
	for _, values := range material.Header {
		for _, value := range values {
			if value == "" || slices.Contains(inv.credentials.values, value) {
				continue
			}
			if inv.credentials.bytes+len(value)*2 > MCPMaxBytes {
				inv.credentials.overflow = true
				continue
			}
			inv.credentials.bytes += len(value) * 2
			inv.credentials.values = append(inv.credentials.values, value)
			if scheme, token, found := strings.Cut(value, " "); found && (strings.EqualFold(scheme, "Bearer") || strings.EqualFold(scheme, "Basic")) {
				inv.credentials.values = append(inv.credentials.values, token)
			}
		}
	}
}

func MCPResultContainsCredential(ctx context.Context, value any) bool {
	inv, ok := MCPInvocationFrom(ctx)
	if !ok || inv.credentials == nil {
		return false
	}
	inv.credentials.mu.Lock()
	defer inv.credentials.mu.Unlock()
	if inv.credentials.overflow {
		return true
	}
	var check func(any) bool
	check = func(value any) bool {
		switch value := value.(type) {
		case string:
			for _, secret := range inv.credentials.values {
				if value == secret || len(secret) >= 8 && strings.Contains(value, secret) {
					return true
				}
			}
		case map[string]any:
			for key, field := range value {
				if check(key) || check(field) {
					return true
				}
			}
		case []any:
			for _, field := range value {
				if check(field) {
					return true
				}
			}
		}
		return false
	}
	return check(value)
}

type mcpTriggerNode struct {
	id   string
	opts MCPTriggerOptions
}

func newMCPTrigger(id string, o MCPTriggerOptions) graph.Node { return mcpTriggerNode{id: id, opts: o} }
func (n mcpTriggerNode) OutPorts() []string {
	out := make([]string, 0, len(n.opts.Tools))
	for _, t := range n.opts.Tools {
		out = append(out, t.Name)
	}
	return out
}
func (n mcpTriggerNode) Run(ctx context.Context, p graph.Payload) (out graph.Payload, err error) {
	defer func() {
		if err != nil {
			err = graph.NodeError{NodeID: n.id, Status: 500, Err: err}
		}
	}()
	inv, ok := MCPInvocationFrom(ctx)
	env, typed := p.(graph.Envelope)
	if !ok || !typed || !slices.Contains(n.OutPorts(), inv.Tool) {
		return nil, errors.New("MCP invocation is required")
	}
	env.Branch = inv.Tool
	return env, nil
}
func (n mcpTriggerNode) ValidateFlow(fc graph.FlowConfig) error { return validateMCPFlow(fc, n.opts) }
func validateMCPFlow(fc graph.FlowConfig, o MCPTriggerOptions) error {
	nodes := map[string]graph.NodeConfig{}
	root := ""
	for _, n := range fc.Nodes {
		nodes[n.ID] = n
		if n.Type == "mcp_trigger" {
			root = n.ID
		}
	}
	edges, e := graph.ParseEdges(fc.Edges)
	if e != nil {
		return e
	}
	out := map[string][]graph.Edge{}
	for _, e := range edges {
		out[e.From] = append(out[e.From], e)
	}
	for _, t := range o.Tools {
		start := ""
		for _, e := range out[root] {
			if e.FromPort == t.Name {
				start = e.To
			}
		}
		seen := map[string]bool{}
		var walk func(string) error
		walk = func(id string) error {
			if seen[id] {
				return nil
			}
			seen[id] = true
			n := nodes[id]
			switch n.Type {
			case "mcp_result":
				return nil
			case "http_trigger", "respond", "batch", "inspect", "filter", "sample", "no_auth", "auth", "read_body", "envelope":
				return fmt.Errorf("node %s cannot terminate or replace MCP invocation", id)
			case "http_request":
				h, err := decodeOptions[HTTPRequestOptions](n)
				if err != nil {
					return err
				}
				if h.Upstream != nil || urlAuthorityIsTemplated(h.URL) || h.Redirects != nil || h.ForwardMethod || h.Credential != nil && h.Credential.Profile == "" {
					return errors.New("MCP HTTP operations require fixed origins, fixed credential profiles and no redirects")
				}
				for header := range h.Headers {
					if mcpCredentialHeader(header) {
						return errors.New("MCP upstream authentication must use an outbound credential profile")
					}
				}
				u, err := url.Parse(h.URL)
				if err != nil || u.User != nil || u.Fragment != "" || strings.ContainsAny(u.RawQuery, "{}") {
					return errors.New("MCP URL variables are path segments; use request.query for query values and credential profiles for secrets")
				}
				if !t.AllowRetries && h.Retry.MaxAttempts > 1 {
					return errors.New("MCP retries require tool.allow_retries")
				}
				if t.ReadOnly && h.Method != "GET" && h.Method != "HEAD" && h.Method != "OPTIONS" {
					return errors.New("read-only MCP HTTP tools require a safe method")
				}
			case "publish", "register_route":
				if t.ReadOnly {
					return errors.New("read-only MCP tool cannot publish or register routes")
				}
			}
			next := out[id]
			if len(next) == 0 {
				return fmt.Errorf("MCP branch ends at %s without mcp_result", id)
			}
			successes := 0
			for _, e := range next {
				if e.FromPort != "on_error" {
					successes++
				}
			}
			if successes > 1 && n.Type != "switch" && n.Type != "if_else" {
				return errors.New("MCP result branches cannot fan out in parallel")
			}
			for _, e := range next {
				if err := walk(e.To); err != nil {
					return err
				}
			}
			return nil
		}
		if start != "" {
			if e := walk(start); e != nil {
				return fmt.Errorf("tool %s: %w", t.Name, e)
			}
		}
	}
	return nil
}
func newMCPResult(id string, o MCPResultOptions) (graph.Node, error) {
	if !slices.Contains([]string{"body", "upstream", "constant"}, o.Source) || (o.Field != "" && len(o.Fields) > 0) {
		return nil, errors.New("MCP result requires source and either field or fields")
	}
	if o.Wrap != "" && (!vars.ValidName(o.Wrap) || len(o.Wrap) > 128) {
		return nil, errors.New("invalid result wrap field")
	}
	if len(o.EmptyStatuses) > 100 || (len(o.EmptyStatuses) > 0 && o.Source != "upstream") {
		return nil, errors.New("empty_statuses requires upstream source")
	}
	seen := map[int]bool{}
	for _, status := range o.EmptyStatuses {
		if status < 200 || status > 299 || seen[status] {
			return nil, errors.New("empty_statuses requires distinct successful statuses")
		}
		seen[status] = true
	}
	return mcpResultNode{id, o}, nil
}

type mcpResultNode struct {
	id   string
	opts MCPResultOptions
}

func (n mcpResultNode) Run(ctx context.Context, p graph.Payload) (out graph.Payload, err error) {
	defer func() {
		if err != nil {
			err = graph.NodeError{NodeID: n.id, Status: 502, Err: err}
		}
	}()
	if _, ok := MCPInvocationFrom(ctx); !ok {
		return nil, errors.New("MCP result requires invocation")
	}
	env, ok := p.(graph.Envelope)
	if !ok {
		return nil, errors.New("MCP result requires envelope")
	}
	var value any
	switch n.opts.Source {
	case "body":
		value = env.Body
	case "constant":
		value = n.opts.Value
	case "upstream":
		if env.Upstream == nil || env.Upstream.Status < 200 || env.Upstream.Status >= 300 {
			return nil, errors.New("upstream operation failed")
		}
		if len(bytes.TrimSpace(env.Upstream.Body)) == 0 && slices.Contains(n.opts.EmptyStatuses, env.Upstream.Status) {
			value = nil
		} else if e := DecodeMCPJSON(env.Upstream.Body, &value); e != nil {
			return nil, errors.New("invalid upstream JSON")
		}
	}
	if n.opts.Field != "" {
		var found bool
		value, found = dotted(value, n.opts.Field)
		if !found {
			return nil, errors.New("MCP result field missing")
		}
	}
	if len(n.opts.Fields) > 0 {
		m := map[string]any{}
		for key, path := range n.opts.Fields {
			v, found := dotted(value, path)
			if !found {
				return nil, errors.New("MCP result field missing")
			}
			m[key] = v
		}
		value = m
	}
	if n.opts.Wrap != "" {
		value = map[string]any{n.opts.Wrap: value}
	}
	raw, e := json.Marshal(value)
	if e != nil || len(raw) > MCPMaxBytes {
		return nil, errors.New("MCP result exceeds limit")
	}
	return graph.Terminal{Result: &graph.Result{JSON: raw}}, nil
}
func DecodeMCPJSON(raw []byte, out any) error {
	if len(raw) > MCPMaxBytes {
		return errors.New("JSON exceeds limit")
	}
	if e := jsonbudget.Check(raw, jsonbudget.Limits{}); e != nil {
		return e
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err := d.Decode(out); err != nil {
		return err
	}
	if d.Decode(new(any)) != io.EOF {
		return errors.New("trailing JSON")
	}
	return nil
}

// MCPEnvelope constructs only application data; transport headers and tokens are never copied.
func MCPEnvelope(t MCPToolOptions, args any, head graph.RequestHead) (graph.Envelope, error) {
	head.Method = http.MethodPost
	head.Header = http.Header{"Content-Type": []string{"application/json"}}
	head.Query = url.Values{}
	head.PathVars = map[string]string{}
	env := graph.Envelope{Body: args, BodyFormat: "json", Vars: map[string]string{}, V: graph.Verified{Identity: "mcp"}}
	for kind, m := range map[string]map[string]string{"variables": t.Request.Variables, "query": t.Request.Query, "headers": t.Request.Headers, "optional_query": t.Request.OptionalQuery, "optional_headers": t.Request.OptionalHeaders} {
		for key, path := range m {
			v, ok := dotted(args, path)
			if !ok {
				if strings.HasPrefix(kind, "optional_") {
					continue
				}
				return env, errors.New("request mapping field missing")
			}
			var s string
			switch x := v.(type) {
			case string:
				s = x
			case json.Number:
				s = string(x)
			case bool:
				s = fmt.Sprint(x)
			default:
				return env, errors.New("request mapping requires scalar value")
			}
			switch kind {
			case "variables":
				for _, segment := range strings.Split(strings.ReplaceAll(s, "\\", "/"), "/") {
					if segment == "." || segment == ".." {
						return env, errors.New("unsafe path variable")
					}
				}
				env.Vars[key] = s
			case "query", "optional_query":
				head.Query.Set(key, s)
			case "headers", "optional_headers":
				if strings.ContainsAny(s, "\r\n\x00") {
					return env, errors.New("invalid mapped header")
				}
				head.Header.Set(key, s)
			}
		}
	}
	if len(t.Request.Body) > 0 {
		body := map[string]any{}
		for key, path := range t.Request.Body {
			v, ok := dotted(args, path)
			if !ok {
				return env, errors.New("request body mapping field missing")
			}
			body[key] = v
		}
		env.Body = body
	}
	if t.Request.BodySource != "" {
		value, found := dotted(args, t.Request.BodySource)
		if !found && !t.Request.BodyOptional {
			return env, errors.New("request body source missing")
		}
		env.Body = value
		if !found {
			env.V.Raw = graph.RawRequest{Head: head}
			return env, nil
		}
	}
	raw, e := json.Marshal(env.Body)
	env.V.Raw = graph.RawRequest{Head: head, Body: raw}
	return env, e
}

func MCPConfigurationIssues(cfg Config) []config.ValidationIssue {
	var issues []config.ValidationIssue
	for name, fc := range cfg.Flows {
		if _, ok := MCPOptions(fc); !ok {
			continue
		}
		for _, n := range fc.Nodes {
			if n.Type != "publish" {
				continue
			}
			o, e := decodeOptions[PublishOptions](n)
			if e != nil {
				continue
			}
			d := cfg.Destinations[o.Destination]
			if d.Type != "sqs" && d.Type != "sns" && d.Type != "rabbitmq" {
				issues = append(issues, cfgIssue("flows."+name+".nodes."+n.ID, "MCP publication requires SQS, SNS or RabbitMQ confirmed single-attempt delivery; use http_request for REST operations", ""))
			}
			if o.Retry != nil && o.Retry.MaxAttempts > 1 {
				issues = append(issues, cfgIssue("flows."+name+".nodes."+n.ID, "MCP publication cannot automatically retry", ""))
			}
		}
	}
	return issues
}
