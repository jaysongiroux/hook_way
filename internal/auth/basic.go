package auth

import (
	"crypto/subtle"
	"encoding/base64"
	"strings"
	"time"
)

const SchemeBasic = "basic"

// basicVerifier checks an `Authorization: Basic <base64(user:pass)>` header
// against a configured `user:pass` credential string in constant time. The body
// is not verified.
type basicVerifier struct {
	creds string // expected "user:pass"
}

func newBasicVerifier(creds string) basicVerifier { return basicVerifier{creds: creds} }

func (v basicVerifier) Verify(header string, _ []byte, _ time.Time) error {
	if v.creds == "" || header == "" {
		return ErrUnauthorized
	}
	encoded, ok := cutBasicPrefix(header)
	if !ok || encoded == "" {
		return ErrUnauthorized
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return ErrUnauthorized
	}
	if subtle.ConstantTimeCompare(decoded, []byte(v.creds)) != 1 {
		return ErrUnauthorized
	}
	return nil
}

// cutBasicPrefix strips a leading case-insensitive "Basic" token and following
// whitespace, returning the base64 remainder.
func cutBasicPrefix(value string) (string, bool) {
	const prefix = "basic"
	trimmed := strings.TrimSpace(value)
	// Require the keyword followed by at least one whitespace separator.
	if len(trimmed) <= len(prefix) || !strings.EqualFold(trimmed[:len(prefix)], prefix) {
		return "", false
	}
	if trimmed[len(prefix)] != ' ' && trimmed[len(prefix)] != '\t' {
		return "", false
	}
	rest := strings.TrimLeft(trimmed[len(prefix):], " \t")
	if rest == "" {
		return "", false
	}
	return rest, true
}
