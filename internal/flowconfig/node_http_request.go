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
	"strconv"
	"strings"
	"time"

	"github.com/jasongiroux/hookway/internal/config"
	"github.com/jasongiroux/hookway/internal/graph"
	"github.com/jasongiroux/hookway/internal/transform"
	"github.com/jasongiroux/hookway/internal/vars"
)

const defaultHTTPRequestTimeout = 10 * time.Second
const maxUpstreamBodyBytes = 10 << 20

// proxyHTTPClient is shared across requests so a proxy under load reuses
// connections instead of opening a socket per event.
var proxyHTTPClient = &http.Client{
	Transport: func() http.RoundTripper {
		t := http.DefaultTransport.(*http.Transport).Clone()
		t.MaxIdleConns = 100
		t.MaxIdleConnsPerHost = 8
		return t
	}(),
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

type httpRequestNode struct {
	id           string
	opts         HTTPRequestOptions
	template     vars.Template
	timeout      time.Duration
	forward      map[string]bool
	allowlist    map[string]bool
	denylist     map[string]bool
	explicitAuth bool
}

func newHTTPRequestNode(id string, opts HTTPRequestOptions, allowlist, denylist []string) graph.Node {
	tpl, err := vars.Parse(opts.URL)
	if err != nil {
		tpl, _ = vars.Parse("")
	}
	timeout := defaultHTTPRequestTimeout
	if opts.Timeout != "" {
		if d, err := time.ParseDuration(opts.Timeout); err == nil && d > 0 {
			timeout = d
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
		id: id, opts: opts, template: tpl, timeout: timeout,
		forward: forward, allowlist: allow, denylist: deny, explicitAuth: explicitAuth,
	}
}

func (n httpRequestNode) Run(ctx context.Context, in graph.Payload) (graph.Payload, error) {
	env, ok := in.(graph.Envelope)
	if !ok {
		return nil, graph.NodeError{
			NodeID: n.id, Status: http.StatusInternalServerError,
			Err: fmt.Errorf("http_request %q: expected Envelope, got %T", n.id, in),
		}
	}
	target, err := n.template.Render(env.Vars)
	if err != nil {
		return nil, graph.NodeError{NodeID: n.id, Status: http.StatusBadRequest, Err: err}
	}
	target = cleanOutboundPath(collapseDoubleSlashInPath(target))
	method := http.MethodPost
	if n.opts.ForwardMethod && env.V.Raw.Head.Method != "" {
		method = env.V.Raw.Head.Method
	}
	body, contentType, err := outboundBody(env, n.opts.Body)
	if err != nil {
		return nil, graph.NodeError{NodeID: n.id, Status: http.StatusInternalServerError, Err: err}
	}
	nctx, cancel := context.WithTimeout(ctx, n.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(nctx, method, target, bytes.NewReader(body))
	if err != nil {
		return nil, graph.NodeError{NodeID: n.id, Status: http.StatusBadRequest, Err: err}
	}
	if n.opts.ForwardQuery {
		req.URL.RawQuery = env.V.Raw.Head.Query.Encode()
	}
	n.copyForwardedHeaders(req, env.V.Raw.Head.Header)
	if contentType != "" && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", contentType)
	}
	for k, v := range n.opts.Headers {
		req.Header.Set(k, v)
	}
	req.Header.Set(config.LoopHopsHeader, strconv.Itoa(env.V.Raw.Head.Hops+1))

	resp, err := proxyHTTPClient.Do(req)
	if err != nil {
		status := http.StatusBadGateway
		if isTimeout(nctx, err) {
			status = http.StatusGatewayTimeout
			err = fmt.Errorf("http_request %q: upstream timeout", n.id)
		}
		return nil, graph.NodeError{NodeID: n.id, Status: status, Err: err}
	}
	defer resp.Body.Close()
	limited := io.LimitReader(resp.Body, maxUpstreamBodyBytes+1)
	respBody, err := io.ReadAll(limited)
	if err != nil {
		return nil, graph.NodeError{NodeID: n.id, Status: http.StatusBadGateway, Err: err}
	}
	if int64(len(respBody)) > maxUpstreamBodyBytes {
		return nil, graph.NodeError{
			NodeID: n.id, Status: http.StatusBadGateway,
			Err: fmt.Errorf("http_request %q: upstream body exceeds %d bytes", n.id, maxUpstreamBodyBytes),
		}
	}
	if matchErrorOnStatus(resp.StatusCode, n.opts.ErrorOnStatus) {
		return nil, graph.NodeError{
			NodeID: n.id, Status: http.StatusBadGateway,
			Err: fmt.Errorf("http_request %q: upstream status %d", n.id, resp.StatusCode),
		}
	}
	env.Upstream = &graph.UpstreamResponse{
		Status: resp.StatusCode,
		Header: resp.Header.Clone(),
		Body:   respBody,
	}
	return env, nil
}

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
	if isHopByHop(canon) || n.denylist[canon] {
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
func cleanOutboundPath(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Path == "" {
		return rawURL
	}
	cleaned := path.Clean(u.Path)
	if cleaned == "." {
		cleaned = "/"
	}
	if !strings.HasPrefix(cleaned, "/") {
		cleaned = "/" + cleaned
	}
	// path.Clean drops a meaningful trailing slash; restore it.
	if strings.HasSuffix(u.Path, "/") && !strings.HasSuffix(cleaned, "/") {
		cleaned += "/"
	}
	u.Path = cleaned
	return u.String()
}

func collapseDoubleSlashInPath(rawURL string) string {
	scheme, rest, ok := strings.Cut(rawURL, "://")
	if !ok {
		return rawURL
	}
	host, path, found := strings.Cut(rest, "/")
	if !found {
		return rawURL
	}
	path = strings.TrimLeft(path, "/")
	for strings.Contains(path, "//") {
		path = strings.ReplaceAll(path, "//", "/")
	}
	if path == "" {
		return scheme + "://" + host
	}
	return scheme + "://" + host + "/" + path
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
