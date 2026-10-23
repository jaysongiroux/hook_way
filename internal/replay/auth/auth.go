// Package auth guards the replay API with named, scoped bearer tokens. The
// key name is the audit identity recorded on runs and history events.
package auth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/jaysongiroux/hook_way/internal/problem"
)

// Scope is one permission a key can hold.
type Scope string

const (
	// MaxTokenBytes bounds credentials in configuration and incoming requests.
	MaxTokenBytes            = 256
	ScopeEventsRead    Scope = "events_read"
	ScopeEventsPayload Scope = "events_payload"
	ScopeEventsRetry   Scope = "events_retry"
	ScopeEventsManage  Scope = "events_manage"
	ScopeCapture       Scope = "capture"
	ScopeRead          Scope = "read"
	ScopePayload       Scope = "payload"
	ScopeRetry         Scope = "retry"
	ScopeMetrics       Scope = "metrics"
)

// Scopes lists every scope in documentation order.
func Scopes() []Scope {
	return []Scope{ScopeCapture, ScopeRead, ScopePayload, ScopeRetry, ScopeMetrics, ScopeEventsRead, ScopeEventsPayload, ScopeEventsRetry, ScopeEventsManage}
}

// Key is one configured credential.
type Key struct {
	Name   string
	Token  string
	Scopes []Scope
}

// Failure reasons passed to the onFailure callback (the auth_failures_total
// reason label).
const (
	ReasonMissing   = "missing"
	ReasonInvalid   = "invalid"
	ReasonScope     = "scope"
	ReasonThrottled = "throttled"
)

type credential struct {
	name   string
	digest [sha256.Size]byte
	scopes map[Scope]bool
}

// Authenticator checks bearer tokens against the configured keys.
type Authenticator struct {
	keys      []credential
	onFailure func(reason string)
	failures  *failureLimiter
}

// New builds an Authenticator. onFailure, when non-nil, is called with a
// Reason* constant for every rejected request.
func New(keys []Key, onFailure func(reason string)) *Authenticator {
	a := &Authenticator{onFailure: onFailure, failures: newFailureLimiter()}
	for _, k := range keys {
		if len(k.Token) == 0 || len(k.Token) > MaxTokenBytes {
			continue
		}
		c := credential{name: k.Name, digest: sha256.Sum256([]byte(k.Token)), scopes: map[Scope]bool{}}
		for _, s := range k.Scopes {
			c.scopes[s] = true
		}
		a.keys = append(a.keys, c)
	}
	return a
}

type actorKey struct{}

// Identity is the authenticated key identity, never the bearer credential.
type Identity struct {
	Name   string
	Scopes []Scope
}

// Principal returns a copy so handlers cannot mutate authenticated scopes.
func Principal(ctx context.Context) Identity {
	identity, _ := ctx.Value(actorKey{}).(Identity)
	identity.Scopes = append([]Scope{}, identity.Scopes...)
	return identity
}

// Actor returns the name of the key that authenticated the request, or ""
// outside Require.
func Actor(ctx context.Context) string {
	return Principal(ctx).Name
}

// Require admits a request whose bearer token belongs to a key holding scope.
// Tokens are compared as SHA-256 digests in constant time against every key,
// so comparisons do not leak stored secret contents, lengths or match position.
// Parsing and hashing the bounded submitted token can depend on its length.
func (a *Authenticator) Require(scope Scope, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := bearerToken(r.Header.Get("Authorization"))
		if len(r.Header.Values("Authorization")) != 1 {
			ok = false
		}
		// Always scan all configured digests before consulting failure limits.
		key := a.match(token)
		if !ok || key == nil {
			reason := ReasonInvalid
			if !ok {
				reason = ReasonMissing
			}
			if wait := a.failures.reject(r.RemoteAddr); wait > 0 {
				w.Header().Set("Retry-After", retryAfter(wait))
				a.deny(w, ReasonThrottled, http.StatusTooManyRequests, "auth_throttled", "too many failed authentication attempts")
				return
			}
			if a.onFailure != nil {
				a.onFailure(reason)
			}
			w.Header().Set("WWW-Authenticate", "Bearer")
			problem.Write(w, http.StatusUnauthorized, "unauthorized", "authentication required")
			return
		}
		if !key.scopes[scope] {
			a.deny(w, ReasonScope, http.StatusForbidden, "forbidden", "insufficient permission")
			return
		}
		identity := Identity{Name: key.name, Scopes: []Scope{}}
		for _, s := range Scopes() {
			if key.scopes[s] {
				identity.Scopes = append(identity.Scopes, s)
			}
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), actorKey{}, identity)))
	})
}

func (a *Authenticator) match(token string) *credential {
	digest := sha256.Sum256([]byte(token))
	var found *credential
	for i := range a.keys {
		if subtle.ConstantTimeCompare(digest[:], a.keys[i].digest[:]) == 1 {
			found = &a.keys[i]
		}
	}
	return found
}

func (a *Authenticator) deny(w http.ResponseWriter, reason string, status int, code, detail string) {
	if a.onFailure != nil {
		a.onFailure(reason)
	}
	switch status {
	case http.StatusUnauthorized:
		w.Header().Set("WWW-Authenticate", "Bearer")
	case http.StatusForbidden:
		w.Header().Set("WWW-Authenticate", `Bearer error="insufficient_scope"`)
	}
	problem.Write(w, status, code, detail)
}

// bearerToken extracts the token from an Authorization header whose scheme is
// a case-insensitive "Bearer".
func bearerToken(header string) (string, bool) {
	const prefix = "bearer "
	if len(header) < len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return "", false
	}
	token := strings.TrimSpace(header[len(prefix):])
	if token == "" || len(token) > MaxTokenBytes || strings.ContainsAny(token, " \t\r\n") {
		return "", false
	}
	return token, true
}
