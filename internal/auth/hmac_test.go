package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"testing"
	"time"
)

func TestSigEncodingDecode(t *testing.T) {
	raw := []byte{0xde, 0xad, 0xbe, 0xef}
	if got, err := encHex.decode(hex.EncodeToString(raw)); err != nil || string(got) != string(raw) {
		t.Fatalf("hex decode = %x, %v", got, err)
	}
	if got, err := encBase64.decode(base64.StdEncoding.EncodeToString(raw)); err != nil || string(got) != string(raw) {
		t.Fatalf("base64 decode = %x, %v", got, err)
	}
	if _, err := encHex.decode("zzzz"); err == nil {
		t.Fatal("expected invalid hex to error")
	}
	if _, err := encBase64.decode("!!!!"); err == nil {
		t.Fatal("expected invalid base64 to error")
	}
}

// A body-only sha256 hex scheme proves the engine end-to-end without depending
// on any provider-specific header layout.
func TestHMACVerifierEngineBodyOnly(t *testing.T) {
	body := []byte(`{"a":1}`)
	now := time.Unix(1710000000, 0)
	mac := hmac.New(sha256.New, []byte("secret"))
	mac.Write(body)
	header := hex.EncodeToString(mac.Sum(nil))

	v := hmacVerifier{secret: "secret", scheme: hmacScheme{
		newHash: sha256.New, encoding: encHex, requiresTimestamp: false,
		parseHeader: rawHeaderParser, buildPayload: bodyOnlyPayload,
	}}
	if err := v.Verify(header, body, now); err != nil {
		t.Fatalf("valid body-only signature rejected: %v", err)
	}
	if err := v.Verify(header, []byte(`{"a":2}`), now); err == nil {
		t.Fatal("tampered body should fail")
	}
	if err := v.Verify("", body, now); err == nil {
		t.Fatal("empty header should fail")
	}
	if (hmacVerifier{secret: "", scheme: v.scheme}).Verify(header, body, now) == nil {
		t.Fatal("empty secret should fail closed")
	}
}
