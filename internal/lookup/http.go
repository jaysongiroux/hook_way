package lookup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jaysongiroux/hook_way/internal/vars"
)

// HTTPOptions configures an HTTP lookup. URL contains exactly one {key}
// placeholder (URL-escaped at request time). GET sends no body; POST sends
// {"key": "<key>"} as JSON. ValuePath is a dotted path into the JSON response.
// A 404 is a clean miss; any other non-2xx status, an unparsable body, or a
// missing value path is an error.
type HTTPOptions struct {
	URL           string
	Method        string // GET (default) | POST
	Headers       map[string]string
	ValuePath     string
	Timeout       time.Duration // transport timeout; the caller also bounds via context
	Client        *http.Client  // optional server-owned transport with DNS egress protection
	SecretHeaders func(context.Context) (http.Header, error)
}

// maxHTTPResponse caps the body read so a misbehaving resolver cannot exhaust memory.
const maxHTTPResponse = 1 << 20

type httpProvider struct {
	opts   HTTPOptions
	client *http.Client
}

func newHTTP(opts HTTPOptions) (*httpProvider, error) {
	if !strings.HasPrefix(opts.URL, "http://") && !strings.HasPrefix(opts.URL, "https://") {
		return nil, errors.New("http: url must be http or https")
	}
	if strings.Count(opts.URL, "{key}") != 1 {
		return nil, errors.New("http: url must contain exactly one {key} placeholder")
	}
	parsed, err := url.Parse(strings.ReplaceAll(opts.URL, "{key}", "LOOKUPKEY"))
	authority := strings.SplitN(strings.TrimPrefix(strings.TrimPrefix(opts.URL, "https://"), "http://"), "/", 2)[0]
	authority = strings.SplitN(strings.SplitN(authority, "?", 2)[0], "#", 2)[0]
	if err != nil || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" || strings.Contains(authority, "{key}") {
		return nil, errors.New("http: url requires a fixed host, no userinfo or fragment, and {key} in the path or query")
	}
	switch strings.ToUpper(opts.Method) {
	case "", "GET":
		opts.Method = http.MethodGet
	case "POST":
		opts.Method = http.MethodPost
	default:
		return nil, fmt.Errorf("http: unsupported method %q", opts.Method)
	}
	if opts.ValuePath == "" {
		return nil, errors.New("http: value_path is required")
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	client := &http.Client{}
	if opts.Client != nil {
		copy := *opts.Client
		client = &copy
	}
	client.Timeout = timeout
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &httpProvider{opts: opts, client: client}, nil
}

// escapeKey percent-encodes every byte outside the unreserved set so the key
// is safe in both the path and the query component. url.PathEscape leaves
// & = + ; unescaped, which let a request-derived key add or override query
// parameters when {key} sits in the query string.
func escapeKey(key string) string {
	return strings.ReplaceAll(url.QueryEscape(key), "+", "%20")
}

func (p *httpProvider) Lookup(ctx context.Context, key string) (string, bool, error) {
	target := strings.Replace(p.opts.URL, "{key}", escapeKey(key), 1)
	var body io.Reader
	if p.opts.Method == http.MethodPost {
		b, _ := json.Marshal(map[string]string{"key": key})
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, p.opts.Method, target, body)
	if err != nil {
		return "", false, fmt.Errorf("http: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range p.opts.Headers {
		req.Header.Set(k, v)
	}
	if p.opts.SecretHeaders != nil {
		headers, err := p.opts.SecretHeaders(ctx)
		if err != nil {
			return "", false, errors.New("http: lookup credentials unavailable")
		}
		for name, values := range headers {
			req.Header[name] = values
		}
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return "", false, errors.New("http: lookup request failed")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return "", false, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", false, fmt.Errorf("http: status %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxHTTPResponse+1))
	if err != nil {
		return "", false, fmt.Errorf("http: read body: %w", err)
	}
	if len(raw) > maxHTTPResponse {
		return "", false, errors.New("http: response exceeds 1 MiB")
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return "", false, fmt.Errorf("http: response is not JSON: %w", err)
	}
	value, ok := vars.BodyString(decoded, p.opts.ValuePath)
	if !ok {
		return "", false, fmt.Errorf("http: value_path %q not found in response", p.opts.ValuePath)
	}
	if value == "" {
		return "", false, nil
	}
	return value, true, nil
}

func (p *httpProvider) Close() error {
	p.client.CloseIdleConnections()
	return nil
}
