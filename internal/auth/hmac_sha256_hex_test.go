package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"
)

func TestHMACSHA256HexVerifierAcceptsValidSignature(t *testing.T) {
	body := []byte(`{"zen":"Keep it logically awesome."}`)
	now := time.Unix(1710000000, 0)

	if err := (hmacVerifier{secret: "secret", scheme: hmacSHA256HexScheme()}).Verify(hmacSHA256HexHeader("secret", body), body, now); err != nil {
		t.Fatalf("Verify returned error: %v", err)
	}
}

func TestHMACSHA256HexVerifierRejectsInvalidInputs(t *testing.T) {
	body := []byte(`{"zen":"ok"}`)
	now := time.Unix(1710000000, 0)
	valid := hmacSHA256HexHeader("secret", body)
	tests := map[string]string{
		"missing header": "",
		"missing prefix": valid[len("sha256="):],
		"wrong prefix":   "sha1=" + valid[len("sha256="):],
		"bad hex":        "sha256=zzzz",
		"short hmac":     "sha256=abcd",
		"tampered":       valid[:len(valid)-1] + "0",
	}
	for name, header := range tests {
		t.Run(name, func(t *testing.T) {
			if err := (hmacVerifier{secret: "secret", scheme: hmacSHA256HexScheme()}).Verify(header, body, now); err == nil {
				t.Fatal("expected verification error")
			}
		})
	}
}

func TestHMACSHA256HexVerifierFailsClosedWithoutSecret(t *testing.T) {
	body := []byte(`{}`)
	now := time.Unix(1710000000, 0)
	if err := (hmacVerifier{secret: "", scheme: hmacSHA256HexScheme()}).Verify(hmacSHA256HexHeader("secret", body), body, now); err == nil {
		t.Fatal("expected empty secret to fail closed")
	}
}

func TestHMACSHA256HexVerifierBindsToRawBody(t *testing.T) {
	now := time.Unix(1710000000, 0)
	header := hmacSHA256HexHeader("secret", []byte("{\"a\":1}\n"))
	if err := (hmacVerifier{secret: "secret", scheme: hmacSHA256HexScheme()}).Verify(header, []byte(`{"a":1}`), now); err == nil {
		t.Fatal("expected a different raw body to fail")
	}
}

func hmacSHA256HexHeader(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}
