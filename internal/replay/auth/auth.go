// Package auth guards the replay API with named, scoped bearer tokens. The
// key name is the audit identity recorded on runs and history events.
package auth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"fmt"
	"net/http"
	"strings"

	"github.com/jaysongiroux/hook_way/internal/replay/problem"
)

// Scope is one permission a key can hold.
type Scope string

const (
	ScopeCapture Scope = "capture"
	ScopeRead    Scope = "read"
	ScopePayload Scope = "payload"
	ScopeRetry   Scope = "retry"
	ScopeMetrics Scope = "metrics"
)

// Scopes lists every scope in documentation order.
func Scopes() []Scope {
	return []Scope{ScopeCapture, ScopeRead, ScopePayload, ScopeRetry, ScopeMetrics}
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
	ReasonMissing = "missing"
	ReasonInvalid = "invalid"
	ReasonScope   = "scope"
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
}

// New builds an Authenticator. onFailure, when non-nil, is called with a
// Reason* constant for every rejected request.
func New(keys []Key, onFailure func(reason string)) *Authenticator {
	a := &Authenticator{onFailure: onFailure}
	for _, k := range keys {
		c := credential{name: k.Name, digest: sha256.Sum256([]byte(k.Token)), scopes: map[Scope]bool{}}
		for _, s := range k.Scopes {
			c.scopes[s] = true
		}
		a.keys = append(a.keys, c)
	}
	return a
}

type actorKey struct{}

// Actor returns the name of the key that authenticated the request, or ""
// outside Require.
func Actor(ctx context.Context) string {
	name, _ := ctx.Value(actorKey{}).(string)
	return name
}

// AnyKeyHas reports whether at least one key holds scope.
func (a *Authenticator) AnyKeyHas(scope Scope) bool {
	for _, k := range a.keys {
		if k.scopes[scope] {
			return true
		}
	}
	return false
}

// Require admits a request whose bearer token belongs to a key holding scope.
// Tokens are compared as SHA-256 digests in constant time against every key,
// so neither the match position nor the token length leaks through timing.
func (a *Authenticator) Require(scope Scope, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := bearerToken(r.Header.Get("Authorization"))
		if !ok {
			a.deny(w, ReasonMissing, http.StatusUnauthorized, "unauthorized", "a bearer token is required")
			return
		}
		key := a.match(token)
		if key == nil {
			a.deny(w, ReasonInvalid, http.StatusUnauthorized, "unauthorized", "the bearer token is not recognized")
			return
		}
		if !key.scopes[scope] {
			a.deny(w, ReasonScope, http.StatusForbidden, "forbidden", fmt.Sprintf("key %q lacks the %s scope", key.name, scope))
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), actorKey{}, key.name)))
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
	if status == http.StatusUnauthorized {
		w.Header().Set("WWW-Authenticate", "Bearer")
	} else {
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
	return token, token != ""
}
