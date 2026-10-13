package auth

import (
	"testing"
	"time"
)

func TestNoAuthVerifierAcceptsEverything(t *testing.T) {
	v := newNoAuthVerifier()
	if err := v.Verify("", nil, time.Now()); err != nil {
		t.Fatalf("expected no_auth to accept an empty request, got %v", err)
	}
	if err := v.Verify("anything", []byte(`{"hello":"world"}`), time.Now()); err != nil {
		t.Fatalf("expected no_auth to accept any header and body, got %v", err)
	}
}

func TestNewVerifierBuildsNoAuthScheme(t *testing.T) {
	v, err := NewVerifier(Spec{AuthType: AuthTypeNoAuth, Scheme: SchemeNoAuth})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	if err := v.Verify("", nil, time.Now()); err != nil {
		t.Fatalf("no_auth verify failed: %v", err)
	}
}

func TestNoAuthSchemeMetadata(t *testing.T) {
	if SchemeRequiresTimestamp(SchemeNoAuth) {
		t.Fatal("no_auth scheme must not require a timestamp")
	}
	if !SchemeAllowedForAuthType(AuthTypeNoAuth, SchemeNoAuth) {
		t.Fatal("no_auth scheme should be allowed for no_auth auth type")
	}
	if SchemeAllowedForAuthType(AuthTypeHMACSHA256, SchemeNoAuth) {
		t.Fatal("no_auth scheme should not be allowed for hmac_sha256")
	}
}
