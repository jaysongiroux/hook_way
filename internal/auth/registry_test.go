package auth

import (
	"testing"
	"time"
)

func TestNewVerifierBuildsPaddleScheme(t *testing.T) {
	body := []byte(`{"hello":"world"}`)
	now := time.Unix(1710000000, 0)
	header := signHeader("secret", now, body, "ts-first")

	v, err := NewVerifier(Spec{Scheme: SchemePaddle, Secret: "secret", TimestampTolerance: 5 * time.Minute})
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

	v, err := NewVerifier(Spec{Scheme: SchemeGitHub, Secret: "secret"})
	if err != nil {
		t.Fatalf("NewVerifier returned error: %v", err)
	}
	if err := v.Verify(header, body, now); err != nil {
		t.Fatalf("github verify failed: %v", err)
	}
}

func TestNewVerifierRejectsUnknownScheme(t *testing.T) {
	if _, err := NewVerifier(Spec{Scheme: "nope", Secret: "secret"}); err == nil {
		t.Fatal("expected unknown scheme error")
	}
}

func TestSchemeMetadata(t *testing.T) {
	if !SchemeRegistered(SchemePaddle) || !SchemeRegistered(SchemeGitHub) {
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
	if len(Schemes()) < 2 {
		t.Fatalf("expected at least two registered schemes, got %v", Schemes())
	}
}
