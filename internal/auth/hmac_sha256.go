package auth

import (
	"crypto/sha256"
	"errors"
	"hash"
	"time"
)

var ErrUnauthorized = errors.New("unauthorized")

// sha256New is a named alias so scheme tables can reference it by value.
func sha256New() hash.Hash { return sha256.New() }

// NewHMACSHA256Verifier builds a verifier for the timestamped HMAC-SHA256
// scheme (SchemeHMACSHA256TSColon): a labeled `ts=<unix>;h1=<hex>` header
// validated over the payload `ts + ":" + raw_body`.
func NewHMACSHA256Verifier(secret string, tolerance time.Duration) Verifier {
	return hmacVerifier{secret: secret, tolerance: tolerance, scheme: hmacSHA256TSColonScheme()}
}
