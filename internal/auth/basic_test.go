package auth

import (
	"encoding/base64"
	"testing"
	"time"
)

func basicHeader(creds string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(creds))
}

func TestBasicVerifierAcceptsMatchingCredentials(t *testing.T) {
	v, err := NewVerifier(Spec{AuthType: AuthTypeHeaderExact, Scheme: SchemeBasic, Secret: "user:pass"})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	for _, h := range []string{basicHeader("user:pass"), "basic " + base64.StdEncoding.EncodeToString([]byte("user:pass"))} {
		if err := v.Verify(h, nil, time.Now()); err != nil {
			t.Fatalf("Verify(%q): %v", h, err)
		}
	}
}

func TestBasicVerifierRejectsInvalidInputs(t *testing.T) {
	tests := map[string]string{
		"missing header":   "",
		"no basic prefix":  base64.StdEncoding.EncodeToString([]byte("user:pass")),
		"bearer prefix":    "Bearer " + base64.StdEncoding.EncodeToString([]byte("user:pass")),
		"bad base64":       "Basic !!!!",
		"wrong password":   basicHeader("user:wrong"),
		"wrong user":       basicHeader("nope:pass"),
		"empty credential": basicHeader(""),
	}
	for name, header := range tests {
		t.Run(name, func(t *testing.T) {
			v, _ := NewVerifier(Spec{AuthType: AuthTypeHeaderExact, Scheme: SchemeBasic, Secret: "user:pass"})
			if err := v.Verify(header, nil, time.Now()); err == nil {
				t.Fatal("expected verification error")
			}
		})
	}
}

func TestBasicVerifierFailsClosedWithoutSecret(t *testing.T) {
	v, _ := NewVerifier(Spec{AuthType: AuthTypeHeaderExact, Scheme: SchemeBasic, Secret: ""})
	if err := v.Verify(basicHeader("user:pass"), nil, time.Now()); err == nil {
		t.Fatal("empty configured credential should fail closed")
	}
}

func TestCutBasicPrefixRequiresWhitespaceSeparator(t *testing.T) {
	creds := base64.StdEncoding.EncodeToString([]byte("user:pass"))
	// No separator between the keyword and the value must be rejected.
	if _, ok := cutBasicPrefix("Basic" + creds); ok {
		t.Fatal("expected header with no separator to be rejected")
	}
	// The well-formed forms must still parse to the encoded value.
	for _, h := range []string{"Basic " + creds, "basic " + creds, "Basic  " + creds, "\tBasic\t" + creds} {
		got, ok := cutBasicPrefix(h)
		if !ok || got != creds {
			t.Fatalf("cutBasicPrefix(%q) = %q, %v; want %q, true", h, got, ok, creds)
		}
	}
}
