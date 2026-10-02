package auth

import (
	"testing"
	"time"
)

func TestExactVerifierAcceptsMatchingHeader(t *testing.T) {
	v, err := NewVerifier(Spec{AuthType: AuthTypeHeaderExact, Scheme: SchemeExact, Secret: "Bearer rc-secret"})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	if err := v.Verify("Bearer rc-secret", []byte(`{"id":"evt_1"}`), time.Now()); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestExactVerifierAcceptsBearerHeaderWhenRemoveBearerEnabled(t *testing.T) {
	v, err := NewVerifier(Spec{AuthType: AuthTypeHeaderExact, Scheme: SchemeExact, Secret: "rc-secret", RemoveBearer: true})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	for _, header := range []string{"Bearer rc-secret", "bearer rc-secret", "Bearer  rc-secret"} {
		if err := v.Verify(header, nil, time.Now()); err != nil {
			t.Fatalf("Verify(%q): %v", header, err)
		}
	}
}

func TestExactVerifierRejectsBearerHeaderWhenRemoveBearerDisabled(t *testing.T) {
	v, err := NewVerifier(Spec{AuthType: AuthTypeHeaderExact, Scheme: SchemeExact, Secret: "rc-secret"})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	if err := v.Verify("Bearer rc-secret", nil, time.Now()); err == nil {
		t.Fatal("expected unauthorized without remove_bearer")
	}
}

func TestExactVerifierRejectsMismatch(t *testing.T) {
	v, err := NewVerifier(Spec{AuthType: AuthTypeHeaderExact, Scheme: SchemeExact, Secret: "secret"})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	if err := v.Verify("wrong", nil, time.Now()); err == nil {
		t.Fatal("expected unauthorized")
	}
}

func TestExactVerifierRejectsEmptyHeaderOrSecret(t *testing.T) {
	v, err := NewVerifier(Spec{AuthType: AuthTypeHeaderExact, Scheme: SchemeExact, Secret: "secret"})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	if err := v.Verify("", nil, time.Now()); err == nil {
		t.Fatal("expected unauthorized for empty header")
	}
	v2, err := NewVerifier(Spec{AuthType: AuthTypeHeaderExact, Scheme: SchemeExact, Secret: ""})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	if err := v2.Verify("secret", nil, time.Now()); err == nil {
		t.Fatal("expected unauthorized for empty secret")
	}
}

func TestStripBearerPrefix(t *testing.T) {
	tests := map[string]string{
		"Bearer token":  "token",
		"bearer token":  "token",
		"BEARER token":  "token",
		"Bearer  token": "token",
		"token":         "token",
		"Basic token":   "Basic token",
	}
	for input, want := range tests {
		if got := stripBearerPrefix(input); got != want {
			t.Fatalf("stripBearerPrefix(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestExactOptionalPrefixIsCaseSensitive(t *testing.T) {
	verifier, err := NewVerifier(Spec{AuthType: AuthTypeHeaderExact, Scheme: SchemeExact, Secret: "synthetic-secret", RemovePrefix: "Api-Key "})
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{"synthetic-secret", "Api-Key synthetic-secret"} {
		if err = verifier.Verify(token, nil, time.Now()); err != nil {
			t.Fatalf("valid token rejected: %v", err)
		}
	}
	for _, token := range []string{"api-key synthetic-secret", "Api-Key  synthetic-secret", "Bearer synthetic-secret", "Api-Key "} {
		if err = verifier.Verify(token, nil, time.Now()); err == nil {
			t.Fatal("invalid token accepted")
		}
	}
}
