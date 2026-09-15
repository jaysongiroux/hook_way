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
	AuthTypeNoAuth      = "no_auth"
	AuthTypeJWT         = "jwt"

	// HMAC schemes. Names describe the wire format (digest, encoding, and
	// whether the header carries a signed timestamp), not any provider.
	SchemeHMACSHA256TSColon = "hmac_sha256_ts_colon" // labeled timestamp header, payload "<ts>:<body>"
	SchemeHMACSHA256TSDot   = "hmac_sha256_ts_dot"   // labeled timestamp header, payload "<ts>.<body>"
	SchemeHMACSHA256Hex     = "hmac_sha256_hex"      // prefixed hex digest over the raw body
	SchemeHMACSHA256Base64  = "hmac_sha256_base64"   // raw base64 digest over the raw body
	SchemeHMACSHA1Hex       = "hmac_sha1_hex"        // prefixed hex SHA-1 digest over the raw body

	SchemeJWTHS256 = "jwt_hs256"
	SchemeJWTRS256 = "jwt_rs256"
	SchemeJWTES256 = "jwt_es256"
)

// Spec carries the per-endpoint inputs a scheme needs to build a verifier.
type Spec struct {
	AuthType           string
	Scheme             string
	Secret             string
	TimestampTolerance time.Duration // reused as JWT leeway for jwt_* schemes
	RemoveBearer       bool
	ExpectedIssuer     string
	ExpectedAudience   string
	BodyHashClaim      string
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
	Register(AuthTypeHMACSHA256, SchemeHMACSHA256TSColon, true, func(s Spec) (Verifier, error) {
		return NewHMACSHA256Verifier(s.Secret, s.TimestampTolerance), nil
	})
	Register(AuthTypeHMACSHA256, SchemeHMACSHA256Hex, false, func(s Spec) (Verifier, error) {
		return hmacVerifier{secret: s.Secret, scheme: hmacSHA256HexScheme()}, nil
	})
	Register(AuthTypeHMACSHA256, SchemeHMACSHA256TSDot, true, func(s Spec) (Verifier, error) {
		return hmacVerifier{secret: s.Secret, tolerance: s.TimestampTolerance, scheme: hmacSHA256TSDotScheme()}, nil
	})
	Register(AuthTypeHMACSHA256, SchemeHMACSHA256Base64, false, func(s Spec) (Verifier, error) {
		return hmacVerifier{secret: s.Secret, scheme: hmacSHA256Base64Scheme()}, nil
	})
	Register(AuthTypeHMACSHA256, SchemeHMACSHA1Hex, false, func(s Spec) (Verifier, error) {
		return hmacVerifier{secret: s.Secret, scheme: hmacSHA1HexScheme()}, nil
	})
	Register(AuthTypeHeaderExact, SchemeExact, false, func(s Spec) (Verifier, error) {
		return newExactVerifier(s.Secret, s.RemoveBearer), nil
	})
	Register(AuthTypeHeaderExact, SchemeBasic, false, func(s Spec) (Verifier, error) {
		return newBasicVerifier(s.Secret), nil
	})
	Register(AuthTypeNoAuth, SchemeNoAuth, false, func(Spec) (Verifier, error) {
		return newNoAuthVerifier(), nil
	})
	Register(AuthTypeJWT, SchemeJWTHS256, false, func(s Spec) (Verifier, error) {
		return newJWTHS256Verifier(s.Secret, s.TimestampTolerance, s.ExpectedIssuer, s.ExpectedAudience, s.BodyHashClaim)
	})
	Register(AuthTypeJWT, SchemeJWTRS256, false, func(s Spec) (Verifier, error) {
		return newJWTRS256Verifier(s.Secret, s.TimestampTolerance, s.ExpectedIssuer, s.ExpectedAudience, s.BodyHashClaim)
	})
	Register(AuthTypeJWT, SchemeJWTES256, false, func(s Spec) (Verifier, error) {
		return newJWTES256Verifier(s.Secret, s.TimestampTolerance, s.ExpectedIssuer, s.ExpectedAudience, s.BodyHashClaim)
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

// NewVerifier builds the verifier for a registered scheme. An empty AuthType
// is inferred from the scheme so V2 auth nodes can name only the scheme.
func NewVerifier(spec Spec) (Verifier, error) {
	def, ok := registry[spec.Scheme]
	if !ok {
		return nil, fmt.Errorf("unknown signature scheme %q (known: %v)", spec.Scheme, Schemes())
	}
	if spec.AuthType != "" && def.authType != spec.AuthType {
		return nil, fmt.Errorf("scheme %q is not valid for auth type %q", spec.Scheme, spec.AuthType)
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
