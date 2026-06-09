package auth

import (
	"fmt"
	"sort"
	"time"
)

// Verifier validates a provider signature header against the exact raw body.
type Verifier interface {
	Verify(header string, rawBody []byte, now time.Time) error
}

// Built-in signature scheme identifiers. Schemes describe the *layout* of an
// HMAC signature; new providers are added by registering a builder rather than
// branching inside the request path.
const (
	SchemePaddle = "paddle"
	SchemeGitHub = "github"
)

// Spec carries the per-endpoint inputs a scheme needs to build a verifier.
type Spec struct {
	Scheme             string
	Secret             string
	TimestampTolerance time.Duration
}

type schemeDef struct {
	build             func(Spec) (Verifier, error)
	requiresTimestamp bool
}

var registry = map[string]schemeDef{}

// Register adds a signature scheme. It is intended to be called from init().
func Register(scheme string, requiresTimestamp bool, build func(Spec) (Verifier, error)) {
	registry[scheme] = schemeDef{build: build, requiresTimestamp: requiresTimestamp}
}

func init() {
	Register(SchemePaddle, true, func(s Spec) (Verifier, error) {
		return NewHMACSHA256Verifier(s.Secret, s.TimestampTolerance), nil
	})
	Register(SchemeGitHub, false, func(s Spec) (Verifier, error) {
		return newGitHubVerifier(s.Secret), nil
	})
}

// RejectAll is a fail-closed verifier used when an endpoint's scheme cannot be
// built, so a misconfiguration rejects traffic rather than accepting it blindly.
type RejectAll struct{}

func (RejectAll) Verify(string, []byte, time.Time) error { return ErrUnauthorized }

// NewVerifier builds the verifier for a registered scheme.
func NewVerifier(spec Spec) (Verifier, error) {
	def, ok := registry[spec.Scheme]
	if !ok {
		return nil, fmt.Errorf("unknown signature scheme %q (known: %v)", spec.Scheme, Schemes())
	}
	return def.build(spec)
}

// SchemeRegistered reports whether a scheme is known.
func SchemeRegistered(scheme string) bool {
	_, ok := registry[scheme]
	return ok
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
