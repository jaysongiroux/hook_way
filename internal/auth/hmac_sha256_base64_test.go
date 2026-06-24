package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"testing"
	"time"
)

func hmacSHA256Base64Header(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func TestHMACSHA256Base64VerifierAcceptsValidSignature(t *testing.T) {
	body := []byte(`{"id":12345}`)
	now := time.Unix(1710000000, 0)
	v, err := NewVerifier(Spec{AuthType: AuthTypeHMACSHA256, Scheme: SchemeHMACSHA256Base64, Secret: "shpss"})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	if err := v.Verify(hmacSHA256Base64Header("shpss", body), body, now); err != nil {
		t.Fatalf("valid hmac_sha256_base64 signature rejected: %v", err)
	}
}

func TestHMACSHA256Base64VerifierRejectsInvalidInputs(t *testing.T) {
	body := []byte(`{"id":12345}`)
	now := time.Unix(1710000000, 0)
	valid := hmacSHA256Base64Header("shpss", body)
	tests := map[string]string{
		"missing header": "",
		"hex not base64": hex.EncodeToString([]byte("0123456789abcdef0123456789abcdef")),
		"not base64":     "!!!!notbase64!!!!",
		"tampered":       valid[:len(valid)-2] + "AA",
		"wrong secret":   hmacSHA256Base64Header("attacker", body),
	}
	for name, header := range tests {
		t.Run(name, func(t *testing.T) {
			v, _ := NewVerifier(Spec{AuthType: AuthTypeHMACSHA256, Scheme: SchemeHMACSHA256Base64, Secret: "shpss"})
			if err := v.Verify(header, body, now); err == nil {
				t.Fatal("expected verification error")
			}
		})
	}
}

func TestHMACSHA256Base64VerifierBindsToRawBodyAndSecret(t *testing.T) {
	now := time.Unix(1710000000, 0)
	header := hmacSHA256Base64Header("shpss", []byte("{\"a\":1}\n"))
	v, _ := NewVerifier(Spec{AuthType: AuthTypeHMACSHA256, Scheme: SchemeHMACSHA256Base64, Secret: "shpss"})
	if err := v.Verify(header, []byte(`{"a":1}`), now); err == nil {
		t.Fatal("different raw body should fail")
	}
	empty, _ := NewVerifier(Spec{AuthType: AuthTypeHMACSHA256, Scheme: SchemeHMACSHA256Base64, Secret: ""})
	if err := empty.Verify(header, []byte("{\"a\":1}\n"), now); err == nil {
		t.Fatal("empty secret should fail closed")
	}
}
