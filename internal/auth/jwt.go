package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// jwtVerifier validates a Bearer JWT against a single statically-configured key
// and a single pinned signing algorithm. It never selects key material from the
// token's own `alg` header, which is what makes it immune to algorithm-confusion.
type jwtVerifier struct {
	key              any      // []byte (HS256), *rsa.PublicKey (RS256), *ecdsa.PublicKey (ES256)
	validMethods     []string // exactly one algorithm
	leeway           time.Duration
	expectedIssuer   string
	expectedAudience string
	bodyHashClaim    string
}

func (v jwtVerifier) Verify(header string, rawBody []byte, now time.Time) error {
	_, err := v.VerifyIdentity(header, rawBody, now, "")
	return err
}

// VerifiedIdentity contains only selected claims from a successfully verified
// JWT. Quota callers never parse tokens a second time or inspect untrusted claims.
type VerifiedIdentity struct{ Subject, Tenant, Issuer string }
type IdentityVerifier interface {
	VerifyIdentity(string, []byte, time.Time, string) (VerifiedIdentity, error)
}

func (v jwtVerifier) VerifyIdentity(header string, rawBody []byte, now time.Time, tenantClaim string) (VerifiedIdentity, error) {
	failed := VerifiedIdentity{}
	if v.key == nil {
		return failed, ErrUnauthorized
	}
	tokenStr := stripBearerPrefix(header)
	if tokenStr == "" {
		return failed, ErrUnauthorized
	}

	opts := []jwt.ParserOption{
		jwt.WithValidMethods(v.validMethods),
		jwt.WithExpirationRequired(),
		jwt.WithLeeway(v.leeway),
		jwt.WithTimeFunc(func() time.Time { return now }),
	}
	if v.expectedIssuer != "" {
		opts = append(opts, jwt.WithIssuer(v.expectedIssuer))
	}
	if v.expectedAudience != "" {
		opts = append(opts, jwt.WithAudience(v.expectedAudience))
	}

	claims := jwt.MapClaims{}
	keyFunc := func(*jwt.Token) (any, error) { return v.key, nil }
	if _, err := jwt.NewParser(opts...).ParseWithClaims(tokenStr, claims, keyFunc); err != nil {
		return failed, ErrUnauthorized
	}

	if v.bodyHashClaim != "" {
		want, _ := claims[v.bodyHashClaim].(string)
		sum := sha256.Sum256(rawBody)
		got := hex.EncodeToString(sum[:])
		if want == "" || subtle.ConstantTimeCompare([]byte(strings.ToLower(want)), []byte(got)) != 1 {
			return failed, ErrUnauthorized
		}
	}
	subject, _ := claims["sub"].(string)
	issuer, _ := claims["iss"].(string)
	tenant, _ := claims[tenantClaim].(string)
	return VerifiedIdentity{Subject: subject, Tenant: tenant, Issuer: issuer}, nil
}

func newJWTHS256Verifier(secret string, leeway time.Duration, iss, aud, bodyHashClaim string) (Verifier, error) {
	if secret == "" {
		return nil, errors.New("jwt hs256 requires a non-empty secret")
	}
	return jwtVerifier{key: []byte(secret), validMethods: []string{"HS256"}, leeway: leeway, expectedIssuer: iss, expectedAudience: aud, bodyHashClaim: bodyHashClaim}, nil
}

func newJWTRS256Verifier(pemPublicKey string, leeway time.Duration, iss, aud, bodyHashClaim string) (Verifier, error) {
	key, err := jwt.ParseRSAPublicKeyFromPEM([]byte(pemPublicKey))
	if err != nil {
		return nil, fmt.Errorf("parse rsa public key: %w", err)
	}
	return jwtVerifier{key: key, validMethods: []string{"RS256"}, leeway: leeway, expectedIssuer: iss, expectedAudience: aud, bodyHashClaim: bodyHashClaim}, nil
}

func newJWTES256Verifier(pemPublicKey string, leeway time.Duration, iss, aud, bodyHashClaim string) (Verifier, error) {
	key, err := jwt.ParseECPublicKeyFromPEM([]byte(pemPublicKey))
	if err != nil {
		return nil, fmt.Errorf("parse ec public key: %w", err)
	}
	return jwtVerifier{key: key, validMethods: []string{"ES256"}, leeway: leeway, expectedIssuer: iss, expectedAudience: aud, bodyHashClaim: bodyHashClaim}, nil
}
