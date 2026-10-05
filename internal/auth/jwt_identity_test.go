package auth

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestJWTIdentityOnlyFromValidatedSelectedClaims(t *testing.T) {
	now := time.Unix(1710000000, 0)
	verifier, err := newJWTHS256Verifier("shared-secret", 0, "issuer", "hookway", "")
	if err != nil {
		t.Fatal(err)
	}
	claims := jwt.MapClaims{"exp": now.Add(time.Hour).Unix(), "iss": "issuer", "aud": "hookway", "sub": "subject", "tenant": "tenant", "private": "never-selected"}
	identityVerifier := verifier.(IdentityVerifier)
	valid := signToken(t, jwt.SigningMethodHS256, []byte("shared-secret"), claims)
	identity, err := identityVerifier.VerifyIdentity(valid, nil, now, "tenant")
	if err != nil || identity != (VerifiedIdentity{Subject: "subject", Tenant: "tenant", Issuer: "issuer"}) {
		t.Fatalf("%+v %v", identity, err)
	}
	for _, token := range []string{signToken(t, jwt.SigningMethodHS256, []byte("wrong-secret"), claims), valid + "corrupt"} {
		identity, err := identityVerifier.VerifyIdentity(token, nil, now, "tenant")
		if err == nil || identity != (VerifiedIdentity{}) {
			t.Fatal("unverified identity escaped")
		}
	}
	claims["aud"] = "different"
	identity, err = identityVerifier.VerifyIdentity(signToken(t, jwt.SigningMethodHS256, []byte("shared-secret"), claims), nil, now, "tenant")
	if err == nil || identity != (VerifiedIdentity{}) {
		t.Fatal("audience mismatch supplied identity")
	}
}
