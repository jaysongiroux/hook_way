package auth

import (
	"strings"
	"testing"
	"time"
)

func TestHMACSHA256RawHexIndependentVectors(t *testing.T) {
	// Python hmac.new(secret.encode(), body.encode(), hashlib.sha256).hexdigest().
	for _, tc := range []struct{ body, signature string }{
		{"{ \"action\": \"triggered\", \"title\": \"café\" }\n", "d45295e3c6554fafa9d124009fd8955b9a223f253531731de6513139190edc26"},
		{"", "f19f8ebca134e4b52ed473a791c83188828ba89b33c647b886a9cdf176fd4211"},
	} {
		v, err := NewVerifier(Spec{Scheme: SchemeHMACSHA256RawHex, Secret: "sentry-test-secret"})
		if err != nil {
			t.Fatal(err)
		}
		if err := v.Verify(tc.signature, []byte(tc.body), time.Time{}); err != nil {
			t.Fatal(err)
		}
		for _, signature := range []string{"", "sha256=" + tc.signature, tc.signature[:62], tc.signature + "00", strings.Repeat("z", 64), strings.Repeat("0", MaxCredentialBytes+1)} {
			if v.Verify(signature, []byte(tc.body), time.Now()) == nil {
				t.Fatal("accepted malformed signature")
			}
		}
		if v.Verify(tc.signature, []byte(tc.body+" "), time.Now()) == nil {
			t.Fatal("accepted changed raw bytes")
		}
		for _, secret := range []string{"", "wrong"} {
			wrong, err := NewVerifier(Spec{Scheme: SchemeHMACSHA256RawHex, Secret: secret})
			if err != nil {
				t.Fatal(err)
			}
			if wrong.Verify(tc.signature, []byte(tc.body), time.Now()) == nil {
				t.Fatal("accepted wrong or empty secret")
			}
		}
		legacy, err := NewVerifier(Spec{Scheme: SchemeHMACSHA256Hex, Secret: "sentry-test-secret"})
		if err != nil {
			t.Fatal(err)
		}
		if legacy.Verify(tc.signature, []byte(tc.body), time.Now()) == nil {
			t.Fatal("legacy prefixed scheme accepted bare hex")
		}
	}
	if SchemeRequiresTimestamp(SchemeHMACSHA256RawHex) || !SchemeAllowedForAuthType(AuthTypeHMACSHA256, SchemeHMACSHA256RawHex) {
		t.Fatal("incorrect raw hex scheme metadata")
	}
}
