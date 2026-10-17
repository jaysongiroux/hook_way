package mcpgateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jaysongiroux/hook_way/internal/flowconfig"
	"github.com/jaysongiroux/hook_way/internal/graph"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
	"golang.org/x/time/rate"
)

type Runner func(context.Context, graph.Envelope) (graph.Terminal, error)
type Options struct {
	Config  flowconfig.MCPTriggerOptions
	Run     Runner
	Observe func(tool, outcome string, duration time.Duration)
	Logger  *slog.Logger
}
type compiledTool struct {
	opts          flowconfig.MCPToolOptions
	input, output *jsonschema.Schema
	slots         chan struct{}
}
type bucket struct {
	limiter *rate.Limiter
	seen    time.Time
}
type Gateway struct {
	opts           Options
	auth           *authenticator
	tools          map[string]*compiledTool
	slots, control chan struct{}
	handler        http.Handler
	mu             sync.Mutex
	limits         map[string]*bucket
	overflow       *rate.Limiter
}

func New(o Options) (*Gateway, error) {
	if err := o.Config.Validate(); err != nil {
		return nil, err
	}
	if o.Run == nil {
		return nil, errors.New("MCP workflow runner required")
	}
	a, e := newAuthenticator(o.Config.Auth)
	if e != nil {
		return nil, e
	}
	n := o.Config.MaxInflight
	if n == 0 {
		n = 32
	}
	// Keep room for discovery and authentication while all execution slots are
	// occupied. The transport bound must not cap configured tool concurrency.
	g := &Gateway{opts: o, auth: a, tools: map[string]*compiledTool{}, slots: make(chan struct{}, n), control: make(chan struct{}, n+32), limits: map[string]*bucket{}, overflow: rate.NewLimiter(1, 4)}
	a.allowOAuth = func() bool { return g.budget("oauth_validation", 600) }
	for _, t := range o.Config.Tools {
		in, e := flowconfig.CompileMCPSchema(t.InputSchema)
		if e != nil {
			_ = a.Close()
			return nil, e
		}
		out, e := flowconfig.CompileMCPSchema(t.OutputSchema)
		if e != nil {
			_ = a.Close()
			return nil, e
		}
		g.tools[t.Name] = &compiledTool{t, in, out, make(chan struct{}, t.Concurrency())}
	}
	g.handler = mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		p, ok := r.Context().Value(principalKey{}).(principal)
		if !ok {
			return nil
		}
		s := mcp.NewServer(&mcp.Implementation{Name: "Hookway", Version: "1"}, &mcp.ServerOptions{Capabilities: &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{}}, SetCacheable: func(_ context.Context, _ mcp.Request, c *mcp.Cacheable) { c.TTLMs = 0; c.CacheScope = "private" }})
		names := make([]string, 0, len(g.tools))
		for name := range g.tools {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			t := g.tools[name]
			if !allowed(p, t.opts) {
				continue
			}
			s.AddTool(&mcp.Tool{Name: name, Description: t.opts.Description, InputSchema: t.opts.InputSchema, OutputSchema: t.opts.OutputSchema, Annotations: &mcp.ToolAnnotations{ReadOnlyHint: t.opts.ReadOnly}}, func(ctx context.Context, r *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return g.call(ctx, p, t, r)
			})
		}
		return s
	}, &mcp.StreamableHTTPOptions{Stateless: true, PropagateRequestCancellation: true, MaxRequestBodyBytes: flowconfig.MCPMaxBytes})
	return g, nil
}
func (g *Gateway) Close() error { return g.auth.Close() }

type principalKey struct{}
type requestKey struct{}
type startedKey struct{}
type admittedToolKey struct{}

func allowed(p principal, t flowconfig.MCPToolOptions) bool {
	if !t.ReadOnly && !t.Access.AllowMutation {
		return false
	}
	a := t.Access
	if p.Anonymous {
		return a.Anonymous
	}
	if p.Key != "" {
		return slices.Contains(a.APIKeys, p.Key)
	}
	if len(a.Scopes)+len(a.Roles) == 0 {
		return false
	}
	for _, s := range a.Scopes {
		if !slices.Contains(p.Scopes, s) {
			return false
		}
	}
	if len(a.Roles) > 0 {
		match := false
		for _, r := range a.Roles {
			match = match || slices.Contains(p.Roles, r)
		}
		if !match {
			return false
		}
	}
	return true
}
func (g *Gateway) observe(tool, outcome string, d time.Duration) {
	if g.opts.Observe != nil {
		g.opts.Observe(tool, outcome, d)
	}
}
func (g *Gateway) budget(key string, rpm int) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := time.Now()
	b := g.limits[key]
	if b == nil {
		if len(g.limits) >= 4096 {
			for k, v := range g.limits {
				if now.Sub(v.seen) > 15*time.Minute {
					delete(g.limits, k)
				}
			}
		}
		if len(g.limits) >= 4096 {
			return g.overflow.Allow()
		}
		b = &bucket{limiter: rate.NewLimiter(rate.Limit(float64(rpm)/60), max(1, min(rpm, 10)))}
		g.limits[key] = b
	}
	b.seen = now
	return b.limiter.Allow()
}
func (g *Gateway) challenge(w http.ResponseWriter, status int, t *compiledTool, invalidToken bool) {
	if g.opts.Config.Auth.OAuth != nil {
		value := fmt.Sprintf(`Bearer resource_metadata=%q`, g.metadataURL())
		if invalidToken {
			value += `, error="invalid_token"`
		}
		if status == 401 && len(g.opts.Config.Auth.OAuth.DiscoveryScopes) > 0 {
			value += fmt.Sprintf(", scope=%q", strings.Join(g.opts.Config.Auth.OAuth.DiscoveryScopes, " "))
		}
		if status == 403 {
			value += `, error="insufficient_scope"`
			if t != nil && len(t.opts.Access.Scopes) > 0 {
				value += fmt.Sprintf(", scope=%q", strings.Join(t.opts.Access.Scopes, " "))
			}
		}
		w.Header().Set("WWW-Authenticate", value)
	} else {
		w.Header().Set("WWW-Authenticate", `Bearer realm="Hookway MCP"`)
	}
	http.Error(w, http.StatusText(status), status)
}
func (g *Gateway) metadataURL() string {
	u, _ := url.Parse(g.opts.Config.PublicURL)
	return u.Scheme + "://" + u.Host + "/.well-known/oauth-protected-resource" + g.opts.Config.Path
}
func (g *Gateway) MetadataPath() string {
	return "/.well-known/oauth-protected-resource" + g.opts.Config.Path
}
func (g *Gateway) Metadata(w http.ResponseWriter, r *http.Request) {
	if !g.checkOrigin(w, r) {
		return
	}
	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodGet {
		w.WriteHeader(405)
		return
	}
	if g.opts.Config.Auth.OAuth == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	metadata := map[string]any{"resource": g.opts.Config.PublicURL, "authorization_servers": []string{g.opts.Config.Auth.OAuth.Issuer}, "bearer_methods_supported": []string{"header"}}
	if len(g.opts.Config.Auth.OAuth.DiscoveryScopes) > 0 {
		metadata["scopes_supported"] = g.opts.Config.Auth.OAuth.DiscoveryScopes
	}
	_ = json.NewEncoder(w).Encode(metadata)
}

func (g *Gateway) checkOrigin(w http.ResponseWriter, r *http.Request) bool {
	if len(r.Header.Values("Origin")) > 1 {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return false
	}
	origin := r.Header.Get("Origin")
	if origin != "" {
		u, _ := url.Parse(g.opts.Config.PublicURL)
		if origin != u.Scheme+"://"+u.Host && !slices.Contains(g.opts.Config.AllowedOrigins, origin) {
			g.observe("", "origin_denied", 0)
			http.Error(w, "Forbidden", http.StatusForbidden)
			return false
		}
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Vary", "Origin")
		w.Header().Set("Access-Control-Expose-Headers", "WWW-Authenticate, X-Request-ID, MCP-Protocol-Version")
	}
	return true
}

func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if !g.checkOrigin(w, r) {
		return
	}
	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Accept, MCP-Protocol-Version, MCP-Method, MCP-Name")
		w.WriteHeader(204)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST, OPTIONS")
		w.WriteHeader(405)
		return
	}
	select {
	case g.control <- struct{}{}:
		defer func() { <-g.control }()
	default:
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(503)
		return
	}
	p, e := g.auth.verify(r.Context(), r)
	if errors.Is(e, errOAuthRateLimited) {
		w.Header().Set("Retry-After", "1")
		g.observe("", "auth_rate_limited", time.Since(start))
		w.WriteHeader(429)
		return
	}
	if e != nil {
		peer, _, _ := net.SplitHostPort(r.RemoteAddr)
		if !g.budget("auth:"+peer, 10) {
			w.Header().Set("Retry-After", "6")
			w.WriteHeader(429)
		} else {
			g.challenge(w, 401, nil, len(r.Header.Values("Authorization")) > 0)
		}
		g.observe("", "auth_denied", time.Since(start))
		return
	}
	if !g.budget("transport:"+p.ID, 600) {
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(429)
		return
	}
	raw, e := io.ReadAll(http.MaxBytesReader(w, r.Body, flowconfig.MCPMaxBytes))
	if e != nil {
		http.Error(w, "Request exceeds limit", http.StatusRequestEntityTooLarge)
		return
	}
	invalidRequest := func() {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0", "id": nil,
			"error": map[string]any{"code": -32600, "message": "Expected a single JSON-RPC request"},
		})
	}
	// Keep admission and SDK dispatch on the same exact field names. The SDK
	// ignores differently cased fields, while encoding/json struct decoding
	// accepts them. Reject batches so every call passes this admission path.
	var msg map[string]json.RawMessage
	var method string
	if json.Unmarshal(raw, &msg) != nil || msg == nil || json.Unmarshal(msg["method"], &method) != nil {
		invalidRequest()
		return
	}
	var admitted *compiledTool
	if method == "tools/call" {
		var params map[string]json.RawMessage
		var name string
		if json.Unmarshal(msg["params"], &params) != nil || params == nil || json.Unmarshal(params["name"], &name) != nil {
			invalidRequest()
			return
		}
		if t := g.tools[name]; t != nil {
			if !allowed(p, t.opts) {
				g.observe(t.opts.Name, "forbidden", 0)
				g.challenge(w, 403, t, false)
				return
			}
			rpm := t.opts.RequestsPerMinute
			if rpm == 0 {
				rpm = 60
			}
			if !g.budget("tool:"+p.ID+":"+t.opts.Name, rpm) {
				w.Header().Set("Retry-After", "1")
				g.observe(t.opts.Name, "rate_limited", 0)
				w.WriteHeader(429)
				return
			}
			// Reject saturation before executing any operation. A transport-level
			// retry of this response cannot duplicate a mutation.
			select {
			case g.slots <- struct{}{}:
			default:
				g.observe(t.opts.Name, "server_busy", 0)
				w.Header().Set("Retry-After", "1")
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			select {
			case t.slots <- struct{}{}:
				admitted = t
				defer func() { <-t.slots; <-g.slots }()
			default:
				<-g.slots
				g.observe(t.opts.Name, "tool_busy", 0)
				w.Header().Set("Retry-After", "1")
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
		}
	}
	id := w.Header().Get("X-Request-ID")
	if id == "" {
		id = uuid.NewString()
		w.Header().Set("X-Request-ID", id)
	}
	ctx := context.WithValue(r.Context(), principalKey{}, p)
	ctx = context.WithValue(ctx, requestKey{}, id)
	ctx = context.WithValue(ctx, startedKey{}, start)
	if admitted != nil {
		ctx = context.WithValue(ctx, admittedToolKey{}, admitted)
	}
	r = r.WithContext(ctx)
	r.Body = io.NopCloser(bytes.NewReader(raw))
	_ = http.NewResponseController(w).SetWriteDeadline(start.Add(125 * time.Second))
	g.handler.ServeHTTP(w, r)
}
func (g *Gateway) call(ctx context.Context, p principal, t *compiledTool, r *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	start := time.Now()
	outcome := "error"
	id, _ := ctx.Value(requestKey{}).(string)
	defer func() {
		g.observe(t.opts.Name, outcome, time.Since(start))
		if g.opts.Logger != nil {
			g.opts.Logger.Info("MCP tool completed", "tool", t.opts.Name, "principal", p.ID, "request_id", id, "outcome", outcome, "duration_ms", time.Since(start).Milliseconds())
		}
	}()
	meta := mcp.Meta{"io.hookway/request_id": id}
	fail := func(code string) (*mcp.CallToolResult, error) {
		outcome = code
		return &mcp.CallToolResult{Meta: meta, IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: code + " (request " + id + ")"}}}, nil
	}
	if !allowed(p, t.opts) {
		return fail("forbidden")
	}
	began, _ := ctx.Value(startedKey{}).(time.Time)
	if began.IsZero() {
		began = start
	}
	ctx, cancel := context.WithDeadline(ctx, began.Add(t.opts.Deadline()))
	defer cancel()
	select {
	case <-ctx.Done():
		return fail("deadline_exceeded")
	default:
	}
	var args any
	raw := r.Params.Arguments
	if len(raw) == 0 {
		raw = []byte("{}")
	}
	if flowconfig.DecodeMCPJSON(raw, &args) != nil || t.input.Validate(args) != nil {
		return fail("invalid_arguments")
	}
	if ctx.Value(admittedToolKey{}) != t {
		select {
		case g.slots <- struct{}{}:
			defer func() { <-g.slots }()
		default:
			return fail("server_busy")
		}
		select {
		case t.slots <- struct{}{}:
			defer func() { <-t.slots }()
		default:
			return fail("tool_busy")
		}
	}
	env, e := flowconfig.MCPEnvelope(t.opts, args, graph.RequestHead{Path: g.opts.Config.Path, RequestID: id, EventID: uuid.NewString()})
	if e != nil {
		return fail("invalid_request_mapping")
	}
	env.V.CredentialID = p.Key
	env.V.Subject = p.ID
	env.V.TrustScope = "mcp:" + g.opts.Config.PublicURL
	ctx = flowconfig.WithMCPInvocation(ctx, t.opts.Name, t.opts.AllowRetries, t.opts.ReadOnly)
	term, e := g.opts.Run(ctx, env)
	if ctx.Err() != nil {
		if term.SideEffects && !t.opts.ReadOnly {
			return fail("deadline_or_cancellation_unknown_outcome")
		}
		return fail("deadline_or_cancellation")
	}
	if e != nil {
		if term.SideEffects && !t.opts.ReadOnly {
			return fail("execution_failed_unknown_outcome")
		}
		return fail("execution_failed")
	}
	if term.Result == nil {
		return fail("result_missing")
	}
	var value any
	if flowconfig.DecodeMCPJSON(term.Result.JSON, &value) != nil || t.output.Validate(value) != nil {
		return fail("invalid_result")
	}
	if flowconfig.MCPResultContainsCredential(ctx, value) {
		return fail("credential_exposure_blocked")
	}
	if ctx.Err() != nil {
		return fail("deadline_exceeded")
	}
	outcome = "success"
	return &mcp.CallToolResult{Meta: meta, StructuredContent: json.RawMessage(term.Result.JSON), Content: []mcp.Content{&mcp.TextContent{Text: string(term.Result.JSON)}}}, nil
}
