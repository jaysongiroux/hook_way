// Package outboundcred owns native credentials. Only HTTP header material crosses
// its boundary; credentials never become graph variables or persisted snapshots.
package outboundcred

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jaysongiroux/hook_way/internal/flowconfig"
	"github.com/jaysongiroux/hook_way/internal/secretref"
)

type flight struct {
	done  chan struct{}
	value flowconfig.CredentialMaterial
	err   error
}
type Provider struct {
	config     flowconfig.CredentialProfile
	name       string
	observe    func(string, string)
	secrets    map[string]secretref.Resolver
	client     *http.Client
	ctx        context.Context
	cancel     context.CancelFunc
	mu         sync.Mutex
	closed     bool
	cache      flowconfig.CredentialMaterial
	expires    time.Time
	generation uint64
	pending    *flight
	wg         sync.WaitGroup
	closeOnce  sync.Once
}

// New validates and resolves secret references once. Token acquisition is lazy.
func New(name string, c flowconfig.CredentialProfile, logger *slog.Logger, observe func(string, string)) (*Provider, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	if logger == nil {
		logger = slog.Default()
	}
	ctx, cancel := context.WithCancel(context.Background())
	p := &Provider{name: name, config: c, observe: observe, secrets: map[string]secretref.Resolver{}, ctx: ctx, cancel: cancel, client: flowconfig.NewCredentialHTTPClient(!c.AllowPrivateTargets)}
	bind := func(key string, o flowconfig.SecretRefOptions) error {
		selection := o.Selection
		if o.Provider == "aws_secrets_manager" && selection == "" {
			selection = "exact"
		}
		r := secretref.Ref{Provider: o.Provider, Name: o.Name, JSONField: o.JSONField, Selection: selection, Validate: validValue}
		if o.Refresh != "" {
			r.Refresh, _ = time.ParseDuration(o.Refresh)
		}
		if o.Overlap != "" {
			r.Overlap, _ = time.ParseDuration(o.Overlap)
		}
		// Secret-provider errors can contain backing data. Log a finite class only.
		resolver, e := secretref.New(r, slog.New(slog.NewTextHandler(io.Discard, nil)), func() {
			p.result("secret_refresh_failed")
			logger.Warn("outbound credential refresh failed; keeping validated value", "profile", name)
		})
		if e != nil {
			return errors.New("credential secret resolution failed")
		}
		p.secrets[key] = resolver
		return nil
	}
	fail := func(e error) (*Provider, error) { _ = p.Close(); return nil, e }
	for name, h := range c.Headers {
		if e := bind("header:"+name, h.SecretRef); e != nil {
			return fail(e)
		}
	}
	if c.ClientID != nil {
		if e := bind("client_id", *c.ClientID); e != nil {
			return fail(e)
		}
	}
	if c.ClientSecret != nil {
		if e := bind("client_secret", *c.ClientSecret); e != nil {
			return fail(e)
		}
	}
	for name, r := range c.SecretParameters {
		if e := bind("parameter:"+name, r); e != nil {
			return fail(e)
		}
	}
	return p, nil
}
func validValue(v []byte) error {
	if len(v) == 0 || len(v) > 8192 || bytes.ContainsAny(v, "\r\n\x00") {
		return errors.New("invalid credential value")
	}
	return nil
}
func (p *Provider) result(result string) {
	if p.observe != nil {
		p.observe(p.name, result)
	}
}
func (p *Provider) secret(ctx context.Context, key string) (string, error) {
	r := p.secrets[key]
	if r == nil {
		return "", errors.New("credential reference missing")
	}
	values, e := r.Secrets(ctx)
	if e != nil || len(values) == 0 {
		return "", errors.New("credential reference unavailable")
	}
	return string(values[0]), nil
}
func (p *Provider) headers(ctx context.Context) (http.Header, error) {
	h := http.Header{}
	for name, c := range p.config.Headers {
		v, e := p.secret(ctx, "header:"+name)
		if e != nil {
			return nil, e
		}
		v = c.Prefix + v
		if validValue([]byte(v)) != nil {
			return nil, errors.New("invalid managed header value")
		}
		h.Set(name, v)
	}
	return h, nil
}
func clone(c flowconfig.CredentialMaterial) flowconfig.CredentialMaterial {
	c.Header = c.Header.Clone()
	return c
}
func (p *Provider) Acquire(ctx context.Context) (flowconfig.CredentialMaterial, error) {
	if ctx.Err() != nil {
		return flowconfig.CredentialMaterial{}, ctx.Err()
	}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return flowconfig.CredentialMaterial{}, errors.New("credential provider closed")
	}
	if p.config.Kind == "static" {
		p.mu.Unlock()
		h, e := p.headers(ctx)
		if e == nil {
			p.result("cache_hit")
		} else {
			p.result("acquisition_failed")
		}
		return flowconfig.CredentialMaterial{Header: h, Guard: !p.config.AllowPrivateTargets}, e
	}
	if p.config.Kind == "token_exchange" {
		// Track lifetime under the same lock as Close; each request owns its exchange.
		p.wg.Add(1)
		p.mu.Unlock()
		defer p.wg.Done()
		exchangeCtx, cancel := context.WithCancel(ctx)
		stop := context.AfterFunc(p.ctx, cancel)
		defer stop()
		defer cancel()
		c, _, e := p.acquire(exchangeCtx)
		if e != nil {
			p.result("acquisition_failed")
		} else {
			p.result("acquired")
		}
		return c, e
	}
	if p.cache.Generation != 0 && time.Now().Before(p.expires) {
		value := clone(p.cache)
		p.mu.Unlock()
		p.result("cache_hit")
		return value, nil
	}
	f := p.pending
	if f == nil {
		f = &flight{done: make(chan struct{})}
		p.pending = f
		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			value, expiry, e := p.acquire(p.ctx)
			p.mu.Lock()
			if p.closed {
				e = errors.New("credential provider closed")
				value = flowconfig.CredentialMaterial{}
			}
			if e == nil && !p.closed {
				p.generation++
				value.Generation = p.generation
				p.cache = clone(value)
				p.expires = expiry
			}
			f.value = value
			f.err = e
			p.pending = nil
			close(f.done)
			p.mu.Unlock()
			if e != nil {
				p.result("acquisition_failed")
			} else {
				p.result("acquired")
			}
		}()
	}
	p.mu.Unlock()
	select {
	case <-ctx.Done():
		return flowconfig.CredentialMaterial{}, ctx.Err()
	case <-p.ctx.Done():
		return flowconfig.CredentialMaterial{}, errors.New("credential provider closed")
	case <-f.done:
		return clone(f.value), f.err
	}
}
func (p *Provider) acquire(ctx context.Context) (flowconfig.CredentialMaterial, time.Time, error) {
	timeout := 10 * time.Second
	if p.config.Timeout != "" {
		timeout, _ = time.ParseDuration(p.config.Timeout)
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	fields := url.Values{}
	for k, v := range p.config.Parameters {
		fields.Set(k, v)
	}
	for k := range p.config.SecretParameters {
		v, e := p.secret(ctx, "parameter:"+k)
		if e != nil {
			return flowconfig.CredentialMaterial{}, time.Time{}, e
		}
		fields.Set(k, v)
	}
	var clientID, clientSecret string
	if p.config.Kind == "oauth_client_credentials" {
		fields.Set("grant_type", "client_credentials")
		if p.config.Scope != "" {
			fields.Set("scope", p.config.Scope)
		}
		var e error
		clientID, e = p.secret(ctx, "client_id")
		if e != nil {
			return flowconfig.CredentialMaterial{}, time.Time{}, e
		}
		clientSecret, e = p.secret(ctx, "client_secret")
		if e != nil {
			return flowconfig.CredentialMaterial{}, time.Time{}, e
		}
		if p.config.ClientAuth == "form" {
			fields.Set("client_id", clientID)
			fields.Set("client_secret", clientSecret)
		}
	}
	contentType := "application/x-www-form-urlencoded"
	body := []byte(fields.Encode())
	if p.config.RequestFormat == "json" {
		object := map[string]string{}
		for k := range fields {
			object[k] = fields.Get(k)
		}
		body, _ = json.Marshal(object)
		contentType = "application/json"
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, p.config.TokenURL, bytes.NewReader(body))
	if e != nil {
		return flowconfig.CredentialMaterial{}, time.Time{}, errors.New("invalid token request")
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Accept", "application/json")
	if p.config.Kind == "oauth_client_credentials" && p.config.ClientAuth != "form" {
		req.SetBasicAuth(url.QueryEscape(clientID), url.QueryEscape(clientSecret))
	}
	resp, e := p.client.Do(req)
	if e != nil {
		return flowconfig.CredentialMaterial{}, time.Time{}, errors.New("token endpoint unavailable")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return flowconfig.CredentialMaterial{}, time.Time{}, errors.New("token endpoint rejected acquisition")
	}
	b, e := io.ReadAll(io.LimitReader(resp.Body, (64<<10)+1))
	if e != nil || len(b) > 64<<10 {
		return flowconfig.CredentialMaterial{}, time.Time{}, errors.New("token response exceeds limit or is unreadable")
	}
	var object any
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.UseNumber()
	if decoder.Decode(&object) != nil {
		return flowconfig.CredentialMaterial{}, time.Time{}, errors.New("invalid token JSON")
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		return flowconfig.CredentialMaterial{}, time.Time{}, errors.New("invalid token JSON")
	}
	tokenPath := p.config.TokenPath
	if tokenPath == "" {
		tokenPath = "access_token"
	}
	token, ok := selectValue(object, tokenPath).(string)
	if !ok || validValue([]byte(token)) != nil {
		return flowconfig.CredentialMaterial{}, time.Time{}, errors.New("token missing or invalid")
	}
	if p.config.Kind == "oauth_client_credentials" {
		if tokenType := selectValue(object, "token_type"); tokenType != nil {
			s, ok := tokenType.(string)
			if !ok || !strings.EqualFold(s, "Bearer") {
				return flowconfig.CredentialMaterial{}, time.Time{}, errors.New("unsupported token type")
			}
		}
	}
	h, e := p.headers(ctx)
	if e != nil {
		return flowconfig.CredentialMaterial{}, time.Time{}, e
	}
	h.Set("Authorization", "Bearer "+token)
	value := flowconfig.CredentialMaterial{Header: h, Guard: !p.config.AllowPrivateTargets}
	if p.config.InstancePath != "" {
		instance, ok := selectValue(object, p.config.InstancePath).(string)
		if !ok {
			return flowconfig.CredentialMaterial{}, time.Time{}, errors.New("instance missing")
		}
		origin, e := flowconfig.CredentialOrigin(instance, p.config.AllowPrivateTargets)
		if e != nil || !p.allowed(origin) {
			return flowconfig.CredentialMaterial{}, time.Time{}, errors.New("instance origin denied")
		}
		value.Origin = origin
	}
	ttl := 5 * time.Minute
	if p.config.TTL != "" {
		ttl, _ = time.ParseDuration(p.config.TTL)
	}
	if p.config.Kind == "oauth_client_credentials" {
		path := p.config.ExpiryPath
		if path == "" {
			path = "expires_in"
		}
		if v := selectValue(object, path); v != nil {
			n, ok := v.(json.Number)
			if !ok {
				return flowconfig.CredentialMaterial{}, time.Time{}, errors.New("invalid token expiry")
			}
			seconds, e := strconv.ParseFloat(n.String(), 64)
			if e != nil || seconds <= 0 || seconds > 86400 {
				return flowconfig.CredentialMaterial{}, time.Time{}, errors.New("invalid token expiry")
			}
			ttl = time.Duration(seconds * float64(time.Second))
		}
	}
	ttl -= min(5*time.Second, ttl/10)
	return value, time.Now().Add(ttl), nil
}
func selectValue(value any, path string) any {
	for _, part := range strings.Split(path, ".") {
		switch object := value.(type) {
		case map[string]any:
			value = object[part]
		case []any:
			i, e := strconv.Atoi(part)
			if e != nil || i < 0 || i >= len(object) {
				return nil
			}
			value = object[i]
		default:
			return nil
		}
	}
	return value
}
func (p *Provider) allowed(origin string) bool {
	for _, s := range p.config.AllowedOrigins {
		o, e := flowconfig.CredentialOrigin(s, p.config.AllowPrivateTargets)
		if e == nil && o == origin {
			return true
		}
	}
	return false
}
func (p *Provider) Target(c flowconfig.CredentialMaterial, target string) (string, error) {
	u, e := url.Parse(target)
	if e != nil || u.User != nil || u.Fragment != "" {
		return "", errors.New("credential target denied")
	}
	origin, e := flowconfig.CredentialOrigin(u.Scheme+"://"+u.Host, p.config.AllowPrivateTargets)
	if e != nil || !p.allowed(origin) {
		return "", errors.New("credential target denied")
	}
	if c.Origin != "" {
		if !p.allowed(c.Origin) {
			return "", errors.New("credential instance denied")
		}
		o, e := url.Parse(c.Origin)
		if e != nil {
			return "", errors.New("credential instance denied")
		}
		u.Scheme = o.Scheme
		u.Host = o.Host
	}
	return u.String(), nil
}
func (p *Provider) Reject(c flowconfig.CredentialMaterial, status int, body []byte) bool {
	marker := p.config.InvalidSession
	if p.config.Kind != "oauth_client_credentials" || status != 401 || marker == nil || len(body) > 64<<10 {
		return false
	}
	var value any
	if json.Unmarshal(body, &value) != nil {
		return false
	}
	text, ok := selectValue(value, marker.Path).(string)
	if !ok || text != marker.Value {
		return false
	}
	p.mu.Lock()
	if c.Generation != 0 && p.cache.Generation == c.Generation {
		p.cache = flowconfig.CredentialMaterial{}
		p.expires = time.Time{}
	}
	p.mu.Unlock()
	p.result("session_rejected")
	return true
}
func (p *Provider) Close() error {
	p.closeOnce.Do(func() {
		p.mu.Lock()
		p.closed = true
		p.cancel()
		p.mu.Unlock()
		p.wg.Wait()
		for _, r := range p.secrets {
			_ = r.Close()
		}
		p.client.CloseIdleConnections()
	})
	return nil
}
