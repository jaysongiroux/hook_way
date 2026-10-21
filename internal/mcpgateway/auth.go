// Package mcpgateway adapts authenticated MCP requests to compiled native flows.
package mcpgateway

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jaysongiroux/hook_way/internal/flowconfig"
	"github.com/jaysongiroux/hook_way/internal/secretref"
)

type principal struct {
	Audience      string
	Issuer        string
	ExpiresAt     time.Time
	ID, Key       string
	Scopes, Roles []string
	Anonymous     bool
}
type key struct {
	name     string
	resolver secretref.Resolver
	expires  time.Time
}
type authenticator struct {
	opts                   flowconfig.MCPAuthOptions
	allowOAuth             func() bool
	keys                   []key
	client                 *http.Client
	clientID, clientSecret secretref.Resolver
	mu                     sync.Mutex
	jwks                   map[string]any
	expires, refreshAfter  time.Time
}

var errOAuthRateLimited = errors.New("OAuth validation rate limited")

func resolve(ref flowconfig.SecretRefOptions, apiKey bool) (secretref.Resolver, error) {
	refresh, _ := time.ParseDuration(ref.Refresh)
	overlap, _ := time.ParseDuration(ref.Overlap)
	r, e := secretref.New(secretref.Ref{Provider: ref.Provider, Name: ref.Name, JSONField: ref.JSONField, Selection: ref.Selection, Refresh: refresh, Overlap: overlap, Validate: func(v []byte) error {
		if len(v) == 0 || len(v) > 8192 || strings.ContainsAny(string(v), "\r\n\x00") || apiKey && (len(v) < 32 || len(v) > 256) {
			return errors.New("invalid credential")
		}
		return nil
	}}, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	if e != nil {
		return nil, errors.New("MCP credential unavailable")
	}
	return r, nil
}
func newAuthenticator(o flowconfig.MCPAuthOptions) (*authenticator, error) {
	a := &authenticator{opts: o, jwks: map[string]any{}}
	for name, k := range o.APIKeys {
		r, e := resolve(k.SecretRef, true)
		if e != nil {
			_ = a.Close()
			return nil, e
		}
		exp, _ := time.Parse(time.RFC3339, k.ExpiresAt)
		a.keys = append(a.keys, key{name, r, exp})
	}
	if c := o.OAuth; c != nil {
		a.client = flowconfig.NewCredentialHTTPClient(!c.AllowPrivateTargets)
		if c.IntrospectionURL != "" {
			var e error
			a.clientID, e = resolve(*c.ClientID, false)
			if e == nil {
				a.clientSecret, e = resolve(*c.ClientSecret, false)
			}
			if e != nil {
				_ = a.Close()
				return nil, e
			}
		}
	}
	return a, nil
}
func (a *authenticator) Close() error {
	for _, k := range a.keys {
		_ = k.resolver.Close()
	}
	if a.clientID != nil {
		_ = a.clientID.Close()
	}
	if a.clientSecret != nil {
		_ = a.clientSecret.Close()
	}
	if a.client != nil {
		a.client.CloseIdleConnections()
	}
	return nil
}
func (a *authenticator) verify(ctx context.Context, r *http.Request) (principal, error) {
	headers := r.Header.Values("Authorization")
	if a.opts.NoAuth {
		if len(headers) > 0 {
			return principal{}, errors.New("unexpected credentials")
		}
		return principal{ID: "anonymous", Anonymous: true}, nil
	}
	if len(headers) != 1 || len(headers[0]) > 8192 {
		return principal{}, errors.New("invalid credentials")
	}
	parts := strings.Fields(headers[0])
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return principal{}, errors.New("invalid credentials")
	}
	token := parts[1]
	hash := sha256.Sum256([]byte(token))
	matched := ""
	ambiguous := false
	for _, k := range a.keys {
		values, e := k.resolver.Secrets(ctx)
		if e != nil {
			continue
		}
		for _, v := range values {
			expected := sha256.Sum256(v)
			equal := subtle.ConstantTimeCompare(hash[:], expected[:])
			if equal == 1 && (k.expires.IsZero() || time.Now().Before(k.expires)) {
				if matched != "" && matched != k.name {
					ambiguous = true
				}
				matched = k.name
			}
		}
	}
	if ambiguous {
		return principal{}, errors.New("ambiguous API key")
	}
	if matched != "" {
		for _, k := range a.keys {
			if k.name == matched {
				return principal{ID: "key:" + matched, Key: matched, ExpiresAt: k.expires}, nil
			}
		}
	}
	if a.opts.OAuth == nil {
		return principal{}, errors.New("invalid credentials")
	}
	if a.allowOAuth != nil && !a.allowOAuth() {
		return principal{}, errOAuthRateLimited
	}
	var claims jwt.MapClaims
	if a.opts.OAuth.IntrospectionURL == "" {
		parsed, e := jwt.Parse(token, func(t *jwt.Token) (any, error) { kid, _ := t.Header["kid"].(string); return a.publicKey(ctx, kid) }, jwt.WithValidMethods([]string{"RS256", "ES256"}), jwt.WithIssuer(a.opts.OAuth.Issuer), jwt.WithAudience(a.opts.OAuth.Audience), jwt.WithExpirationRequired(), jwt.WithIssuedAt())
		if e != nil || !parsed.Valid {
			return principal{}, errors.New("invalid access token")
		}
		claims = parsed.Claims.(jwt.MapClaims)
	} else {
		var e error
		claims, e = a.introspect(ctx, token)
		if e != nil {
			return principal{}, e
		}
	}
	sub, e := claims.GetSubject()
	if e != nil || sub == "" || len(sub) > 1024 {
		return principal{}, errors.New("access token subject missing")
	}
	// Tool grants are checked separately from the verified issuer and subject.
	expiry, _ := claims.GetExpirationTime()
	p := principal{Audience: a.opts.OAuth.Audience, Issuer: a.opts.OAuth.Issuer, ExpiresAt: expiry.Time, ID: fmt.Sprintf("oauth:%x", sha256.Sum256([]byte(a.opts.OAuth.Issuer+"\x00"+sub))), Scopes: stringList(claims["scope"])}
	if len(p.Scopes) == 0 {
		p.Scopes = stringList(claims["scp"])
	}
	if c := a.opts.OAuth.RolesClaim; c != "" {
		p.Roles = stringList(claims[c])
	}
	return p, nil
}
func stringList(v any) []string {
	switch x := v.(type) {
	case string:
		return strings.Fields(x)
	case []any:
		out := []string{}
		for _, v := range x {
			if s, ok := v.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}
func (a *authenticator) fetch(ctx context.Context, target string, out any) error {
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if e != nil {
		return errors.New("OAuth metadata unavailable")
	}
	return a.do(req, out)
}
func (a *authenticator) do(req *http.Request, out any) error {
	ctx, cancel := context.WithTimeout(req.Context(), 5*time.Second)
	defer cancel()
	res, e := a.client.Do(req.WithContext(ctx))
	if e != nil {
		return errors.New("OAuth validation unavailable")
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != 200 {
		return errors.New("OAuth validation unavailable")
	}
	raw, e := io.ReadAll(io.LimitReader(res.Body, 1<<20+1))
	if e != nil || len(raw) > 1<<20 {
		return errors.New("OAuth response exceeds limit")
	}
	if flowconfig.DecodeMCPJSON(raw, out) != nil {
		return errors.New("invalid OAuth response")
	}
	return nil
}
func (a *authenticator) publicKey(ctx context.Context, kid string) (any, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	if k := a.jwks[kid]; k != nil && now.Before(a.expires) {
		return k, nil
	}
	if now.Before(a.refreshAfter) {
		return nil, errors.New("OAuth key unavailable")
	}
	a.refreshAfter = now.Add(30 * time.Second)
	c := a.opts.OAuth
	target := c.JWKSURL
	if target == "" {
		var meta struct {
			Issuer string `json:"issuer"`
			JWKS   string `json:"jwks_uri"`
		}
		if e := a.fetch(ctx, strings.TrimRight(c.Issuer, "/")+"/.well-known/openid-configuration", &meta); e != nil {
			return nil, e
		}
		u, e := url.Parse(meta.JWKS)
		issuer, _ := url.Parse(c.Issuer)
		if e != nil || meta.Issuer != c.Issuer || u.Host != issuer.Host || u.Scheme != issuer.Scheme || u.User != nil || u.Fragment != "" {
			return nil, errors.New("invalid OAuth discovery")
		}
		target = meta.JWKS
	}
	var doc struct {
		Keys []struct{ Kty, Kid, Use, Alg, N, E, X, Y, Crv string }
	}
	if e := a.fetch(ctx, target, &doc); e != nil {
		return nil, e
	}
	if len(doc.Keys) > 64 {
		return nil, errors.New("OAuth key limit exceeded")
	}
	keys := map[string]any{}
	decode := base64.RawURLEncoding.DecodeString
	for _, k := range doc.Keys {
		if k.Use != "" && k.Use != "sig" {
			continue
		}
		if k.Kid == "" || len(k.Kid) > 256 {
			continue
		}
		if k.Kty == "RSA" && (k.Alg == "" || k.Alg == "RS256") {
			n, ne := decode(k.N)
			e, ee := decode(k.E)
			if ne == nil && ee == nil && len(n) >= 256 && len(n) <= 1024 && len(e) <= 4 {
				ex := new(big.Int).SetBytes(e).Int64()
				if ex >= 3 && ex%2 == 1 {
					keys[k.Kid] = &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(ex)}
				}
			}
		}
		if k.Kty == "EC" && k.Crv == "P-256" && (k.Alg == "" || k.Alg == "ES256") {
			x, xe := decode(k.X)
			y, ye := decode(k.Y)
			if xe == nil && ye == nil && len(x) == 32 && len(y) == 32 {
				encoded := append([]byte{4}, x...)
				encoded = append(encoded, y...)
				if public, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), encoded); err == nil {
					keys[k.Kid] = public
				}
			}
		}
	}
	a.jwks = keys
	a.expires = now.Add(5 * time.Minute)
	if k := keys[kid]; k != nil {
		return k, nil
	}
	return nil, errors.New("OAuth key unavailable")
}
func (a *authenticator) introspect(ctx context.Context, token string) (jwt.MapClaims, error) {
	c := a.opts.OAuth
	if c.IntrospectionURL == "" {
		return nil, errors.New("opaque token unsupported")
	}
	id, e := a.clientID.Secrets(ctx)
	if e != nil {
		return nil, e
	}
	secret, e := a.clientSecret.Secrets(ctx)
	if e != nil {
		return nil, e
	}
	body := url.Values{"token": []string{token}, "token_type_hint": []string{"access_token"}}
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, c.IntrospectionURL, strings.NewReader(body.Encode()))
	if e != nil {
		return nil, e
	}
	req.SetBasicAuth(url.QueryEscape(string(id[0])), url.QueryEscape(string(secret[0])))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	var claims jwt.MapClaims
	if e = a.do(req, &claims); e != nil {
		return nil, e
	}
	active, _ := claims["active"].(bool)
	if !active {
		return nil, errors.New("inactive token")
	}
	// DecodeMCPJSON preserves numbers; JWT claim helpers require float64 or json.Number.
	if e = jwt.NewValidator(jwt.WithIssuer(c.Issuer), jwt.WithAudience(c.Audience), jwt.WithExpirationRequired()).Validate(claims); e != nil {
		return nil, errors.New("invalid introspection claims")
	}
	return claims, nil
}
