package auth

import (
	"fmt"
	"sort"
	"time"
)

// Verifier validates a provider auth header against the exact raw body when the
// scheme requires it.
type Verifier interface {
	Verify(header string, rawBody []byte, now time.Time) error
}

const (
	AuthTypeHMACSHA256  = "hmac_sha256"
	AuthTypeHeaderExact = "header_exact"

	SchemePaddle = "paddle"
	SchemeGitHub = "github"
)

// Spec carries the per-endpoint inputs a scheme needs to build a verifier.
type Spec struct {
	AuthType           string
	Scheme             string
	Secret             string
	TimestampTolerance time.Duration
	RemoveBearer       bool
}

type schemeDef struct {
	authType          string
	build             func(Spec) (Verifier, error)
	requiresTimestamp bool
}

var registry = map[string]schemeDef{}

// Register adds a signature scheme. It is intended to be called from init().
func Register(authType, scheme string, requiresTimestamp bool, build func(Spec) (Verifier, error)) {
	registry[scheme] = schemeDef{authType: authType, build: build, requiresTimestamp: requiresTimestamp}
}

func init() {
	Register(AuthTypeHMACSHA256, SchemePaddle, true, func(s Spec) (Verifier, error) {
		return NewHMACSHA256Verifier(s.Secret, s.TimestampTolerance), nil
	})
	Register(AuthTypeHMACSHA256, SchemeGitHub, false, func(s Spec) (Verifier, error) {
		return newGitHubVerifier(s.Secret), nil
	})
	Register(AuthTypeHeaderExact, SchemeExact, false, func(s Spec) (Verifier, error) {
		return newExactVerifier(s.Secret, s.RemoveBearer), nil
	})
}

// RejectAll is a fail-closed verifier used when an endpoint's scheme cannot be
// built, so a misconfiguration rejects traffic rather than accepting it blindly.
type RejectAll struct{}

func (RejectAll) Verify(string, []byte, time.Time) error { return ErrUnauthorized }

func authTypeOrDefault(authType string) string {
	if authType == "" {
		return AuthTypeHMACSHA256
	}
	return authType
}

// NewVerifier builds the verifier for a registered scheme.
func NewVerifier(spec Spec) (Verifier, error) {
	def, ok := registry[spec.Scheme]
	if !ok {
		return nil, fmt.Errorf("unknown signature scheme %q (known: %v)", spec.Scheme, Schemes())
	}
	if def.authType != authTypeOrDefault(spec.AuthType) {
		return nil, fmt.Errorf("scheme %q is not valid for auth type %q", spec.Scheme, authTypeOrDefault(spec.AuthType))
	}
	return def.build(spec)
}

// SchemeRegistered reports whether a scheme is known.
func SchemeRegistered(scheme string) bool {
	_, ok := registry[scheme]
	return ok
}

// SchemeAllowedForAuthType reports whether scheme is registered for authType.
func SchemeAllowedForAuthType(authType, scheme string) bool {
	def, ok := registry[scheme]
	if !ok {
		return false
	}
	return def.authType == authTypeOrDefault(authType)
}

// SchemeRequiresTimestamp reports whether a scheme verifies a signed timestamp
// (and therefore needs a positive timestamp tolerance).
func SchemeRequiresTimestamp(scheme string) bool {
	return registry[scheme].requiresTimestamp
}

// Schemes returns the sorted list of registered scheme identifiers.
func Schemes() []string {
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
