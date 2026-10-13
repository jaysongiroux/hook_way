package auth

import (
	"testing"
	"time"
)

func TestNewVerifierBuildsHMACSHA256TSColonScheme(t *testing.T) {
	body := []byte(`{"hello":"world"}`)
	now := time.Unix(1710000000, 0)
	header := signHeader("secret", now, body, "ts-first")

	v, err := NewVerifier(Spec{AuthType: AuthTypeHMACSHA256, Scheme: SchemeHMACSHA256TSColon, Secret: "secret", TimestampTolerance: 5 * time.Minute})
	if err != nil {
		t.Fatalf("NewVerifier returned error: %v", err)
	}
	if err := v.Verify(header, body, now); err != nil {
		t.Fatalf("hmac_sha256_ts_colon verify failed: %v", err)
	}
}

func TestNewVerifierBuildsHMACSHA256HexScheme(t *testing.T) {
	body := []byte(`{"action":"opened"}`)
	now := time.Unix(1710000000, 0)
	header := hmacSHA256HexHeader("secret", body)

	v, err := NewVerifier(Spec{AuthType: AuthTypeHMACSHA256, Scheme: SchemeHMACSHA256Hex, Secret: "secret"})
	if err != nil {
		t.Fatalf("NewVerifier returned error: %v", err)
	}
	if err := v.Verify(header, body, now); err != nil {
		t.Fatalf("hmac_sha256_hex verify failed: %v", err)
	}
}

func TestNewVerifierBuildsExactSchemeForHeaderExactType(t *testing.T) {
	v, err := NewVerifier(Spec{AuthType: AuthTypeHeaderExact, Scheme: SchemeExact, Secret: "tok"})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	if err := v.Verify("tok", nil, time.Now()); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

func TestNewVerifierRejectsExactSchemeWithHMACType(t *testing.T) {
	if _, err := NewVerifier(Spec{AuthType: AuthTypeHMACSHA256, Scheme: SchemeExact, Secret: "tok"}); err == nil {
		t.Fatal("expected type/scheme mismatch error")
	}
}

func TestNewVerifierRejectsUnknownScheme(t *testing.T) {
	if _, err := NewVerifier(Spec{AuthType: AuthTypeHMACSHA256, Scheme: "nope", Secret: "secret"}); err == nil {
		t.Fatal("expected unknown scheme error")
	}
}

func TestNewVerifierBuildsJWTSchemes(t *testing.T) {
	// HS256 builds from a secret.
	if _, err := NewVerifier(Spec{AuthType: AuthTypeJWT, Scheme: SchemeJWTHS256, Secret: "s"}); err != nil {
		t.Fatalf("jwt_hs256 build: %v", err)
	}
	// RS256/ES256 fail to build on bad PEM (fail-closed at startup).
	if _, err := NewVerifier(Spec{AuthType: AuthTypeJWT, Scheme: SchemeJWTRS256, Secret: "not a pem"}); err == nil {
		t.Fatal("jwt_rs256 with bad PEM should error")
	}
	if _, err := NewVerifier(Spec{AuthType: AuthTypeJWT, Scheme: SchemeJWTES256, Secret: "not a pem"}); err == nil {
		t.Fatal("jwt_es256 with bad PEM should error")
	}
}

func TestJWTSchemeMetadata(t *testing.T) {
	for _, s := range []string{SchemeJWTHS256, SchemeJWTRS256, SchemeJWTES256} {
		if SchemeRequiresTimestamp(s) {
			t.Fatalf("%s must not require a timestamp", s)
		}
		if !SchemeAllowedForAuthType(AuthTypeJWT, s) {
			t.Fatalf("%s should be allowed for jwt type", s)
		}
		if SchemeAllowedForAuthType(AuthTypeHMACSHA256, s) {
			t.Fatalf("%s must not be allowed for hmac_sha256", s)
		}
	}
}

func TestSchemeMetadata(t *testing.T) {
	if !SchemeAllowedForAuthType(AuthTypeHMACSHA256, SchemeHMACSHA256TSColon) ||
		!SchemeAllowedForAuthType(AuthTypeHMACSHA256, SchemeHMACSHA256Hex) ||
		!SchemeAllowedForAuthType(AuthTypeHeaderExact, SchemeExact) {
		t.Fatal("built-in schemes should be registered")
	}
	if SchemeAllowedForAuthType(AuthTypeHMACSHA256, "nope") {
		t.Fatal("unknown scheme should not be registered")
	}
	if !SchemeRequiresTimestamp(SchemeHMACSHA256TSColon) {
		t.Fatal("hmac_sha256_ts_colon scheme should require a timestamp tolerance")
	}
	if SchemeRequiresTimestamp(SchemeHMACSHA256Hex) {
		t.Fatal("hmac_sha256_hex scheme should not require a timestamp tolerance")
	}
	if SchemeRequiresTimestamp(SchemeExact) {
		t.Fatal("exact scheme must not require timestamp")
	}
	if !SchemeAllowedForAuthType(AuthTypeHeaderExact, SchemeExact) {
		t.Fatal("exact scheme should be allowed for header_exact")
	}
	if SchemeAllowedForAuthType(AuthTypeHMACSHA256, SchemeExact) {
		t.Fatal("exact scheme should not be allowed for hmac_sha256")
	}
	if len(Schemes()) < 3 {
		t.Fatalf("expected at least three registered schemes, got %v", Schemes())
	}
}
