package auth

import (
	"crypto/subtle"
	"strings"
	"time"
)

const SchemeExact = "exact"

type exactVerifier struct {
	removePrefix string
	secret       string
	removeBearer bool
}

func newExactVerifier(secret string, removeBearer bool) exactVerifier {
	return exactVerifier{secret: secret, removeBearer: removeBearer}
}

func (v exactVerifier) Verify(header string, _ []byte, _ time.Time) error {
	if v.secret == "" || header == "" {
		return ErrUnauthorized
	}
	got := header
	if v.removePrefix != "" {
		got = strings.TrimPrefix(got, v.removePrefix)
	}
	if v.removeBearer {
		got = stripBearerPrefix(got)
		if got == "" {
			return ErrUnauthorized
		}
	}
	if subtle.ConstantTimeCompare([]byte(got), []byte(v.secret)) != 1 {
		return ErrUnauthorized
	}
	return nil
}

// stripBearerPrefix removes a leading "Bearer" token prefix, case-insensitively,
// and any whitespace that follows it.
func stripBearerPrefix(value string) string {
	const prefix = "bearer"
	trimmed := strings.TrimSpace(value)
	if len(trimmed) < len(prefix) || !strings.EqualFold(trimmed[:len(prefix)], prefix) {
		return trimmed
	}
	return strings.TrimLeft(trimmed[len(prefix):], " \t")
}
