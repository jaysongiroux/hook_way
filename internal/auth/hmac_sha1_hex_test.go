package auth

import (
	"crypto/hmac"
	"crypto/sha1" // #nosec G505 -- HMAC-SHA1 fixture matching configured scheme
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"
)

func hmacSHA1HexHeader(secret string, body []byte) string {
	mac := hmac.New(sha1.New, []byte(secret))
	mac.Write(body)
	return "sha1=" + hex.EncodeToString(mac.Sum(nil))
}

func TestHMACSHA1HexVerifierAcceptsValidSignature(t *testing.T) {
	body := []byte(`{"zen":"ok"}`)
	now := time.Unix(1710000000, 0)
	v, err := NewVerifier(Spec{AuthType: AuthTypeHMACSHA256, Scheme: SchemeHMACSHA1Hex, Secret: "secret"})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	if err := v.Verify(hmacSHA1HexHeader("secret", body), body, now); err != nil {
		t.Fatalf("valid sha1 signature rejected: %v", err)
	}
}

func TestHMACSHA1HexVerifierRejectsInvalidInputs(t *testing.T) {
	body := []byte(`{"zen":"ok"}`)
	now := time.Unix(1710000000, 0)
	valid := hmacSHA1HexHeader("secret", body)
	// A SHA-256 hex digest is 32 bytes; the SHA-1 scheme must reject it on length.
	sha256mac := hmac.New(sha256.New, []byte("secret"))
	sha256mac.Write(body)
	sha256Hex := "sha1=" + hex.EncodeToString(sha256mac.Sum(nil))
	tests := map[string]string{
		"missing header":       "",
		"wrong prefix":         "sha256=" + valid[len("sha1="):],
		"bad hex":              "sha1=zzzz",
		"sha256-length digest": sha256Hex,
		"tampered":             valid[:len(valid)-1] + "0",
		"wrong secret":         hmacSHA1HexHeader("attacker", body),
	}
	for name, header := range tests {
		t.Run(name, func(t *testing.T) {
			v, _ := NewVerifier(Spec{AuthType: AuthTypeHMACSHA256, Scheme: SchemeHMACSHA1Hex, Secret: "secret"})
			if err := v.Verify(header, body, now); err == nil {
				t.Fatal("expected verification error")
			}
		})
	}
}
