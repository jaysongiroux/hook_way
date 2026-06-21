package auth

import (
	"testing"
	"time"
)

func TestNewVerifierBuildsPaddleScheme(t *testing.T) {
	body := []byte(`{"hello":"world"}`)
	now := time.Unix(1710000000, 0)
	header := signHeader("secret", now, body, "ts-first")

	v, err := NewVerifier(Spec{AuthType: AuthTypeHMACSHA256, Scheme: SchemePaddle, Secret: "secret", TimestampTolerance: 5 * time.Minute})
	if err != nil {
		t.Fatalf("NewVerifier returned error: %v", err)
	}
	if err := v.Verify(header, body, now); err != nil {
		t.Fatalf("paddle verify failed: %v", err)
	}
}

func TestNewVerifierBuildsGitHubScheme(t *testing.T) {
	body := []byte(`{"action":"opened"}`)
	now := time.Unix(1710000000, 0)
	header := githubHeader("secret", body)

	v, err := NewVerifier(Spec{AuthType: AuthTypeHMACSHA256, Scheme: SchemeGitHub, Secret: "secret"})
	if err != nil {
		t.Fatalf("NewVerifier returned error: %v", err)
	}
	if err := v.Verify(header, body, now); err != nil {
		t.Fatalf("github verify failed: %v", err)
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

func TestSchemeMetadata(t *testing.T) {
	if !SchemeRegistered(SchemePaddle) || !SchemeRegistered(SchemeGitHub) || !SchemeRegistered(SchemeExact) {
		t.Fatal("built-in schemes should be registered")
	}
	if SchemeRegistered("nope") {
		t.Fatal("unknown scheme should not be registered")
	}
	if !SchemeRequiresTimestamp(SchemePaddle) {
		t.Fatal("paddle scheme should require a timestamp tolerance")
	}
	if SchemeRequiresTimestamp(SchemeGitHub) {
		t.Fatal("github scheme should not require a timestamp tolerance")
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
