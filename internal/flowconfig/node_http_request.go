package flowconfig

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jaysongiroux/hook_way/internal/config"
	"github.com/jaysongiroux/hook_way/internal/graph"
	"github.com/jaysongiroux/hook_way/internal/retry"
	"github.com/jaysongiroux/hook_way/internal/routing"
	"github.com/jaysongiroux/hook_way/internal/transform"
	"github.com/jaysongiroux/hook_way/internal/vars"
)

const defaultHTTPRequestTimeout = 10 * time.Second
const maxUpstreamBodyBytes = 10 << 20

var errProxyRedirect = errors.New("upstream redirect denied")

// Keep separate pools for each egress policy: a guarded request must never
// reuse a connection opened without validating the resolved address.
var proxyHTTPClient = newProxyHTTPClient(false)
var guardedProxyHTTPClient = newProxyHTTPClient(true)

func newProxyHTTPClient(enforce bool) *http.Client {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.DisableCompression = true
	// A forward proxy would bypass the guarded target dial and DNS pinning.
	if enforce {
		t.Proxy = nil
	}
	t.MaxIdleConns = 100
	t.MaxIdleConnsPerHost = 8
	t.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		return egressDialContext(withEgressGuard(ctx, enforce), network, addr)
	}
	return &http.Client{
		Transport: t,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// egressGuardKey carries the transport's fixed policy to the dialer.
type egressGuardKey struct{}

func withEgressGuard(ctx context.Context, enforce bool) context.Context {
	return context.WithValue(ctx, egressGuardKey{}, enforce)
}

func egressGuarded(ctx context.Context) bool {
	enforce, _ := ctx.Value(egressGuardKey{}).(bool)
	return enforce
}

var egressDialer = &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}

// egressDialContext closes the DNS-rebinding hole the static host check
// cannot: a name that passes validation can still resolve to 169.254.169.254,
// and a second lookup between check and connect can return a different answer
// than the first. When the guard is on, the address is resolved here, every
// candidate IP is classified, and the connection is made to a specific
// vetted IP -- so the address that was checked is the address that is dialed.
func egressDialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	if !egressGuarded(ctx) {
		return egressDialer.DialContext(ctx, network, addr)
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	lastErr := fmt.Errorf("no allowed address for %q", host)
	for _, ip := range ips {
		if reason := blockedIPReason(ip.IP); reason != "" {
			lastErr = fmt.Errorf("host %q resolves to %s, which is not an allowed egress target (%s)", host, ip.IP, reason)
			continue
		}
		conn, err := egressDialer.DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

type httpRequestNode struct {
	registry          *routing.Registry
	credentials       map[string]CredentialProvider
	id                string
	opts              HTTPRequestOptions
	template          vars.Template
	authorityTemplate vars.Template
	// hostTemplated records that the URL authority contains a placeholder,
	// so the destination host is request-derived rather than operator-chosen.
	// That is what turns the egress policy on; see validateEgressURL.
	hostTemplated bool
	timeout       time.Duration
	retry         retry.Policy
	forward       map[string]bool
	allowlist     map[string]bool
	denylist      map[string]bool
	explicitAuth  bool
}

// enforceEgressPolicy reports whether this node's destination must be checked
// against the private-network block list. It fails closed: the opt-out is an
// explicit allow_private_targets on the node.
func (n httpRequestNode) enforceEgressPolicy() bool {
	return (n.hostTemplated || n.opts.Upstream != nil || n.opts.Credential != nil) && !n.opts.AllowPrivateTargets
}

func newHTTPRequestNode(id string, opts HTTPRequestOptions, allowlist, denylist []string) (graph.Node, error) {
	templateText := opts.URL
	if opts.Upstream != nil {
		templateText = opts.Upstream.Path
		if templateText == "" {
			templateText = "/"
		}
	}
	tpl, err := vars.Parse(templateText)
	if err != nil {
		return nil, err
	}
	authorityTemplate, err := vars.Parse(urlAuthority(opts.URL))
	if err != nil {
		return nil, err
	}
	timeout := defaultHTTPRequestTimeout
	if opts.Timeout != "" {
		if d, err := time.ParseDuration(opts.Timeout); err == nil && d > 0 {
			timeout = d
		}
	}
	// Retry defaults mirror the webhook destination (internal/queue/webhook.go)
	// so operators learn one set of numbers. They only apply once the operator
	// has opted in with max_attempts >= 2.
	policy := retry.Policy{MaxAttempts: opts.Retry.MaxAttempts}
	if policy.Attempts() > 1 {
		policy.InitialBackoff = 500 * time.Millisecond
		policy.MaxBackoff = 30 * time.Second
		if d, err := time.ParseDuration(opts.Retry.InitialBackoff); err == nil && d > 0 {
			policy.InitialBackoff = d
		}
		if d, err := time.ParseDuration(opts.Retry.MaxBackoff); err == nil && d > 0 {
			policy.MaxBackoff = d
		}
	}
	forward := map[string]bool{}
	explicitAuth := false
	for _, name := range opts.ForwardHeaders {
		canon := http.CanonicalHeaderKey(name)
		forward[canon] = true
		if canon == "Authorization" {
			explicitAuth = true
		}
	}
	allow := map[string]bool{}
	for _, name := range allowlist {
		allow[http.CanonicalHeaderKey(name)] = true
	}
	deny := map[string]bool{}
	for _, name := range denylist {
		deny[http.CanonicalHeaderKey(name)] = true
	}
	return httpRequestNode{
		id: id, opts: opts, template: tpl, authorityTemplate: authorityTemplate, hostTemplated: urlAuthorityIsTemplated(opts.URL),
		timeout: timeout, retry: policy,
		forward: forward, allowlist: allow, denylist: deny, explicitAuth: explicitAuth,
	}, nil
}

func (n httpRequestNode) Run(ctx context.Context, in graph.Payload) (graph.Payload, error) {
	env, ok := in.(graph.Envelope)
	if !ok {
		return nil, graph.NodeError{
			NodeID: n.id, Status: http.StatusInternalServerError,
			Err: fmt.Errorf("http_request %q: expected Envelope, got %T", n.id, in),
		}
	}
	for _, name := range n.opts.RequireHeaders {
		v := env.V.Raw.Head.Header.Values(name)
		if len(v) != 1 || strings.TrimSpace(v[0]) == "" {
			return nil, graph.NodeError{NodeID: n.id, Status: 400, Err: errors.New("missing or ambiguous required caller header")}
		}
	}
	for _, path := range n.opts.RequireBodyFields {
		v, found := dotted(env.Body, path)
		text, isText := v.(string)
		if !found || v == nil || (isText && strings.TrimSpace(text) == "") {
			return nil, graph.NodeError{NodeID: n.id, Status: 400, Err: errors.New("missing required request field")}
		}
	}
	target, err := n.target(env)
	if err != nil {
		return nil, graph.NodeError{NodeID: n.id, Status: http.StatusBadRequest, Err: proxyBoundaryError{message: "upstream target invalid", cause: err}}
	}
	if err := validateEgressURL(target, n.enforceEgressPolicy()); err != nil {
		return nil, graph.NodeError{NodeID: n.id, Status: http.StatusBadRequest, Err: proxyBoundaryError{message: "upstream target denied", cause: err}}
	}
	target = cleanOutboundPath(target)
	method := n.opts.Method
	if method == "" {
		method = http.MethodPost
	}
	if n.opts.ForwardMethod && env.V.Raw.Head.Method != "" {
		method = env.V.Raw.Head.Method
	}
	body, contentType, err := n.requestBody(env, method)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, errBodyProjection) {
			status = http.StatusBadRequest
		}
		return nil, graph.NodeError{NodeID: n.id, Status: status, Err: err}
	}
	if n.opts.Retry.TotalTimeout != "" {
		d, _ := time.ParseDuration(n.opts.Retry.TotalTimeout)
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, d)
		defer cancel()
	}
	attempts := n.retry.Attempts()
	if len(n.opts.Retry.Methods) > 0 && !slices.Contains(n.opts.Retry.Methods, method) {
		attempts = 1
	}
	delay := n.retry.InitialBackoff
	if delay <= 0 {
		delay = retry.BackoffFloor
	}
	var credential CredentialMaterial
	provider, err := n.credentialProvider(env)
	if err != nil {
		return nil, graph.NodeError{NodeID: n.id, Status: 403, Err: errors.New("credential environment or profile denied")}
	}
	if provider != nil {
		// Check destination admission before acquiring any credential.
		if _, err := provider.Target(CredentialMaterial{}, target); err != nil {
			return nil, graph.NodeError{NodeID: n.id, Status: 403, Err: errors.New("credential target denied")}
		}
		credential, err = provider.Acquire(ctx)
		if err != nil {
			return nil, graph.NodeError{NodeID: n.id, Status: 502, Err: errors.New("outbound credential acquisition failed")}
		}
		target, err = provider.Target(credential, target)
		if err != nil {
			return nil, graph.NodeError{NodeID: n.id, Status: 403, Err: errors.New("credential instance denied")}
		}
	}
	var res attemptResult
	sessionRetried := false
	for attempt := 1; ; attempt++ {
		res = n.send(ctx, method, target, body, contentType, env, credential)
		if provider != nil && !sessionRetried && res.err == nil && provider.Reject(credential, res.status, res.body) {
			sessionRetried = true
			credential, err = provider.Acquire(ctx)
			if err != nil {
				return nil, graph.NodeError{NodeID: n.id, Status: 502, Err: errors.New("outbound credential refresh failed")}
			}
			original, e := n.target(env)
			if e != nil {
				return nil, graph.NodeError{NodeID: n.id, Status: 403, Err: errors.New("credential target denied")}
			}
			target, err = provider.Target(credential, original)
			if err != nil {
				return nil, graph.NodeError{NodeID: n.id, Status: 403, Err: errors.New("credential instance denied")}
			}
			res = n.send(ctx, method, target, body, contentType, env, credential)
		}
		if attempt >= attempts || !res.retryable || ctx.Err() != nil {
			break
		}
		// A 429's Retry-After overrides the computed backoff for this wait,
		// capped by max_backoff so a hostile or mistaken header cannot park
		// the flow for hours.
		wait := delay
		if res.retryAfter > 0 {
			wait = res.retryAfter
			if n.retry.MaxBackoff > 0 && wait > n.retry.MaxBackoff {
				wait = n.retry.MaxBackoff
			}
		}
		if err := retry.Sleep(ctx, wait); err != nil {
			return nil, graph.NodeError{
				NodeID: n.id, Status: http.StatusGatewayTimeout,
				Err: fmt.Errorf("http_request %q: retry aborted after %d attempts: %w", n.id, attempt, err),
			}
		}
		delay = retry.NextBackoff(delay, n.retry.MaxBackoff)
	}
	if res.err != nil {
		return nil, *res.err
	}
	if matchErrorOnStatus(res.status, n.opts.ErrorOnStatus) {
		return nil, graph.NodeError{
			NodeID: n.id, Status: http.StatusBadGateway,
			Err: fmt.Errorf("http_request %q: upstream status %d", n.id, res.status),
		}
	}
	env.Upstream = &graph.UpstreamResponse{Status: res.status, Header: res.header, Body: res.body}
	n.bindResponse(&env)
	return env, nil
}

// attemptResult is the outcome of one upstream attempt. Exactly one of err or
// a response (status/header/body) is meaningful.
type attemptResult struct {
	status     int
	header     http.Header
	body       []byte
	err        *graph.NodeError
	retryable  bool
	retryAfter time.Duration
}

// send performs one attempt. It builds a fresh *http.Request every call: the
// body is an io.Reader, so reusing a request across attempts would send an
// empty body on the second one.
func (n httpRequestNode) send(ctx context.Context, method, target string, body []byte, contentType string, env graph.Envelope, credentials ...CredentialMaterial) attemptResult {
	nctx, cancel := context.WithTimeout(ctx, n.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(nctx, method, target, bytes.NewReader(body))
	if err != nil {
		return attemptResult{err: &graph.NodeError{NodeID: n.id, Status: http.StatusBadRequest, Err: proxyBoundaryError{message: "upstream request invalid", cause: err}}}
	}
	if n.opts.ForwardQuery {
		req.URL.RawQuery = projectedQuery(env.V.Raw.Head.Query, n.opts.QueryMode, n.opts.RemoveQuery).Encode()
	}
	if n.opts.HeaderMode != "replace" {
		n.copyForwardedHeaders(req, env.V.Raw.Head.Header)
	}
	for _, key := range n.opts.RemoveHeaders {
		req.Header.Del(key)
	}
	if n.opts.Body == "transformed" || (len(n.opts.BodyMethods) > 0 && !slices.Contains(n.opts.BodyMethods, method)) {
		for _, key := range []string{"Content-Type", "Content-Encoding", "Content-MD5", "Digest", "Content-Digest", "Repr-Digest"} {
			req.Header.Del(key)
		}
	}
	if contentType != "" && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", contentType)
	}
	for k, v := range n.opts.Headers {
		req.Header.Set(k, v)
	}
	for name := range connectionHeaders(req.Header) {
		req.Header.Del(name)
	}
	for name := range req.Header {
		if isHopByHop(name) {
			req.Header.Del(name)
		}
	}
	// Framing always reflects the actual projected bytes.
	req.Header.Del("Content-Length")
	req.Header.Set(config.LoopHopsHeader, strconv.Itoa(env.V.Raw.Head.Hops+1))
	if len(credentials) > 0 {
		for k, v := range credentials[0].Header {
			req.Header[k] = slices.Clone(v)
		}
	}

	client := proxyHTTPClient
	if n.enforceEgressPolicy() || (len(credentials) > 0 && credentials[0].Guard) {
		client = guardedProxyHTTPClient
	}
	if n.opts.Redirects != nil {
		cloned := *client
		cloned.CheckRedirect = func(next *http.Request, via []*http.Request) error {
			if len(via) > n.opts.Redirects.MaxHops {
				return fmt.Errorf("%w: limit exceeded", errProxyRedirect)
			}
			origin := via[0].URL
			if next.URL.Scheme != origin.Scheme || !strings.EqualFold(next.URL.Host, origin.Host) || next.URL.User != nil {
				return fmt.Errorf("%w: must remain on the same origin", errProxyRedirect)
			}
			if err := validateEgressURL(next.URL.String(), n.enforceEgressPolicy()); err != nil {
				return fmt.Errorf("%w: %w", errProxyRedirect, err)
			}
			cleaned, err := url.Parse(cleanOutboundPath(next.URL.String()))
			if err != nil {
				return err
			}
			next.URL = cleaned
			return nil
		}
		client = &cloned
	}
	resp, err := client.Do(req)
	if err != nil {
		status := http.StatusBadGateway
		retryable := !errors.Is(err, errProxyRedirect) && (nctx.Err() == nil || isTimeout(nctx, err))
		message := "upstream transport failed"
		if isTimeout(nctx, err) {
			status = http.StatusGatewayTimeout
			message = "upstream timeout"
		} else if errors.Is(err, errProxyRedirect) {
			message = "upstream redirect denied"
		}
		// net/http wraps errors with the complete outbound URL. It may contain
		// caller query credentials or a private resource path; keep diagnostics
		// bounded while retaining the cause for errors.Is/As and retry policy.
		err = proxyBoundaryError{message: fmt.Sprintf("http_request %q: %s", n.id, message), cause: err}
		// A transport failure is the retryable case: the upstream may simply
		// have been unreachable for a moment.
		return attemptResult{retryable: retryable, err: &graph.NodeError{NodeID: n.id, Status: status, Err: err}}
	}
	defer func() { _ = resp.Body.Close() }()
	limit := n.opts.MaxResponseBytes
	if limit == 0 {
		limit = maxUpstreamBodyBytes
	}
	limited := io.LimitReader(resp.Body, limit+1)
	respBody, err := io.ReadAll(limited)
	if err != nil {
		status := http.StatusBadGateway
		if isTimeout(nctx, err) {
			status = http.StatusGatewayTimeout
		}
		return attemptResult{retryable: true, err: &graph.NodeError{NodeID: n.id, Status: status, Err: proxyBoundaryError{message: "upstream response read failed", cause: err}}}
	}
	if int64(len(respBody)) > limit {
		return attemptResult{err: &graph.NodeError{
			NodeID: n.id, Status: http.StatusBadGateway,
			Err: fmt.Errorf("http_request %q: upstream body exceeds %d bytes", n.id, limit),
		}}
	}
	res := attemptResult{status: resp.StatusCode, header: resp.Header.Clone(), body: respBody}
	if resp.Uncompressed {
		res.header.Del("Content-Encoding")
		res.header.Del("Content-Length")
	}
	if n.opts.Retry.On != "network" && retry.RetryableStatus(resp.StatusCode) {
		res.retryable = true
		if resp.StatusCode == http.StatusTooManyRequests {
			res.retryAfter = retry.ParseRetryAfter(resp.Header.Get("Retry-After"), time.Now())
		}
	}
	return res
}

type proxyBoundaryError struct {
	message string
	cause   error
}

func (e proxyBoundaryError) Error() string { return e.message }
func (e proxyBoundaryError) Unwrap() error { return e.cause }

func (n httpRequestNode) copyForwardedHeaders(req *http.Request, src http.Header) {
	nominated := connectionHeaders(src)
	for k, vs := range src {
		if nominated[http.CanonicalHeaderKey(k)] || !n.shouldForward(k) {
			continue
		}
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
}

func (n httpRequestNode) shouldForward(name string) bool {
	canon := http.CanonicalHeaderKey(name)
	if isHopByHop(canon) || canon == "Content-Length" {
		return false
	}
	// Creation profiles explicitly require and forward caller credentials. Their
	// redaction affects scripts/capture; native forwarding uses the private view.
	callerCredential := false
	for _, h := range n.opts.RequireHeaders {
		if http.CanonicalHeaderKey(h) == canon && n.forward[canon] {
			callerCredential = true
		}
	}
	if n.denylist[canon] && !callerCredential {
		return false
	}
	if canon == "Authorization" && !n.explicitAuth {
		return false
	}
	// Cookie authenticates the caller to Hookway's own origin, so forwarding
	// it by default would hand a session credential to a third-party
	// upstream. Opt in explicitly via forward_headers if an upstream really
	// shares the caller's session.
	if canon == "Cookie" && !n.forward[canon] {
		return false
	}
	if len(n.forward) > 0 {
		return n.forward[canon]
	}
	if len(n.allowlist) > 0 {
		return n.allowlist[canon]
	}
	return true
}

func outboundBody(env graph.Envelope, mode string) ([]byte, string, error) {
	rawType := env.V.Raw.Head.Header.Get("Content-Type")
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", "raw":
		return env.V.Raw.Body, rawType, nil
	case "transformed":
		switch body := env.Body.(type) {
		case transform.Document:
			raw, err := transform.SerializeXML(body)
			if err != nil {
				return nil, "", err
			}
			if rawType == "" {
				rawType = "application/xml"
			}
			return raw, rawType, nil
		case *transform.Document:
			if body == nil {
				return env.V.Raw.Body, rawType, nil
			}
			raw, err := transform.SerializeXML(*body)
			if err != nil {
				return nil, "", err
			}
			if rawType == "" {
				rawType = "application/xml"
			}
			return raw, rawType, nil
		case nil:
			return env.V.Raw.Body, rawType, nil
		default:
			b, err := json.Marshal(env.Body)
			if err != nil {
				return nil, "", err
			}
			return b, "application/json", nil
		}
	default:
		return nil, "", fmt.Errorf("http_request: unknown body mode %q", mode)
	}
}

// cleanOutboundPath normalizes the rendered upstream path, resolving any "."
// and ".." segments. Defense in depth: UnsafePathVar already rejects traversal
// in captured path variables at the trigger, but a hand-written url template
// could also introduce them, and an upstream must never receive a path whose
// meaning depends on its own normalization behavior.
//
// It deliberately works on the *escaped* path. Cleaning u.Path instead would
// read an already-decoded path, so a capture containing the literal text
// "%2e%2e%2f" -- which is not traversal, just a percent sign and some
// characters -- would be decoded to "../" and then resolved away, turning an
// inert value into a real traversal on the way out. Percent-encoded dots stay
// percent-encoded; only literal "." and ".." segments are resolved.
func cleanOutboundPath(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	escaped := u.EscapedPath()
	if escaped == "" {
		return rawURL
	}
	cleaned := path.Clean(escaped)
	if cleaned == "." {
		cleaned = "/"
	}
	if !strings.HasPrefix(cleaned, "/") {
		cleaned = "/" + cleaned
	}
	// path.Clean drops a meaningful trailing slash; restore it.
	if strings.HasSuffix(escaped, "/") && !strings.HasSuffix(cleaned, "/") {
		cleaned += "/"
	}
	if cleaned == escaped {
		return rawURL
	}
	decoded, err := url.PathUnescape(cleaned)
	if err != nil {
		// Unreachable for a path that already parsed, but bail out rather
		// than emit a URL whose Path and RawPath disagree.
		return rawURL
	}
	u.Path = decoded
	u.RawPath = cleaned
	return u.String()
}

func matchErrorOnStatus(code int, patterns []string) bool {
	for _, p := range patterns {
		switch strings.ToLower(strings.TrimSpace(p)) {
		case "5xx":
			if code >= 500 && code <= 599 {
				return true
			}
		case "4xx":
			if code >= 400 && code <= 499 {
				return true
			}
		default:
			if n, err := strconv.Atoi(p); err == nil && n == code {
				return true
			}
		}
	}
	return false
}

func isTimeout(ctx context.Context, err error) bool {
	if errors.Is(err, context.DeadlineExceeded) || ctx.Err() == context.DeadlineExceeded {
		return true
	}
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}
