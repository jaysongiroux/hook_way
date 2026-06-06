package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"
	"time"
)

func TestHMACSHA256VerifierAcceptsValidSignature(t *testing.T) {
	body := []byte(`{"hello":"world"}`)
	now := time.Unix(1710000000, 0)
	header := signHeader("secret", now, body, "ts-first")

	err := NewHMACSHA256Verifier("secret", 5*time.Minute).Verify(header, body, now)
	if err != nil {
		t.Fatalf("Verify returned error: %v", err)
	}
}

func TestHMACSHA256VerifierRejectsInvalidInputs(t *testing.T) {
	body := []byte(`{"hello":"world"}`)
	now := time.Unix(1710000000, 0)
	valid := signHeader("secret", now, body, "ts-first")
	tests := map[string]string{
		"missing header": "",
		"malformed":      "bad",
		"missing ts":     "h1=abc",
		"missing h1":     "ts=1710000000",
		"duplicate ts":   fmt.Sprintf("ts=%d;ts=%d;h1=%s", now.Unix(), now.Add(time.Second).Unix(), valid[len("ts=1710000000;h1="):]),
		"duplicate h1":   valid + ";h1=" + valid[len("ts=1710000000;h1="):],
		"invalid hmac":   valid[:len(valid)-1] + "0",
		"short hmac":     "ts=1710000000;h1=abcd",
		"expired":        signHeader("secret", now.Add(-10*time.Minute), body, "ts-first"),
		"future":         signHeader("secret", now.Add(10*time.Minute), body, "ts-first"),
	}
	for name, header := range tests {
		t.Run(name, func(t *testing.T) {
			err := NewHMACSHA256Verifier("secret", 5*time.Minute).Verify(header, body, now)
			if err == nil {
				t.Fatal("expected verification error")
			}
		})
	}
}

func TestHMACSHA256VerifierRejectsUnsafeConfiguration(t *testing.T) {
	body := []byte(`{"hello":"world"}`)
	now := time.Unix(1710000000, 0)
	header := signHeader("", now, body, "ts-first")

	if err := NewHMACSHA256Verifier("", 5*time.Minute).Verify(header, body, now); err == nil {
		t.Fatal("expected empty secret to fail closed")
	}
	if err := NewHMACSHA256Verifier("secret", 0).Verify(signHeader("secret", now, body, "ts-first"), body, now); err == nil {
		t.Fatal("expected non-positive tolerance to fail closed")
	}
}

func TestHMACSHA256VerifierUsesRawBodyExactlyAndAcceptsAnyHeaderOrder(t *testing.T) {
	body := []byte("{\"hello\":\"world\"}\n")
	now := time.Unix(1710000000, 0)
	header := signHeader("secret", now, body, "h1-first")

	if err := NewHMACSHA256Verifier("secret", 5*time.Minute).Verify(header, body, now); err != nil {
		t.Fatalf("Verify returned error: %v", err)
	}
	if err := NewHMACSHA256Verifier("secret", 5*time.Minute).Verify(header, []byte(`{"hello":"world"}`), now); err == nil {
		t.Fatal("expected different raw body to fail")
	}
}

func signHeader(secret string, ts time.Time, body []byte, order string) string {
	payload := fmt.Sprintf("%d:%s", ts.Unix(), body)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	sum := hex.EncodeToString(mac.Sum(nil))
	if order == "h1-first" {
		return fmt.Sprintf("h1=%s;ts=%d", sum, ts.Unix())
	}
	return fmt.Sprintf("ts=%d;h1=%s", ts.Unix(), sum)
}
