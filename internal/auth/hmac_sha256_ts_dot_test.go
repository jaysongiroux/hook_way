package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"
	"time"
)

func hmacSHA256TSDotHeader(secret string, ts time.Time, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = fmt.Fprintf(mac, "%d.%s", ts.Unix(), body)
	return fmt.Sprintf("t=%d,v1=%s", ts.Unix(), hex.EncodeToString(mac.Sum(nil)))
}

func TestHMACSHA256TSDotVerifierAcceptsValidSignature(t *testing.T) {
	body := []byte(`{"id":"evt_1"}`)
	now := time.Unix(1710000000, 0)
	v, err := NewVerifier(Spec{AuthType: AuthTypeHMACSHA256, Scheme: SchemeHMACSHA256TSDot, Secret: "whsec", TimestampTolerance: 5 * time.Minute})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	if err := v.Verify(hmacSHA256TSDotHeader("whsec", now, body), body, now); err != nil {
		t.Fatalf("valid hmac_sha256_ts_dot signature rejected: %v", err)
	}
}

func TestHMACSHA256TSDotVerifierToleratesExtraSchemePairs(t *testing.T) {
	body := []byte(`{"id":"evt_1"}`)
	now := time.Unix(1710000000, 0)
	valid := hmacSHA256TSDotHeader("whsec", now, body)
	withExtra := valid + ",v0=ignoredolderscheme"
	v, _ := NewVerifier(Spec{AuthType: AuthTypeHMACSHA256, Scheme: SchemeHMACSHA256TSDot, Secret: "whsec", TimestampTolerance: 5 * time.Minute})
	if err := v.Verify(withExtra, body, now); err != nil {
		t.Fatalf("extra v0 pair should be ignored, got: %v", err)
	}
}

func TestHMACSHA256TSDotVerifierRejectsInvalidInputs(t *testing.T) {
	body := []byte(`{"id":"evt_1"}`)
	now := time.Unix(1710000000, 0)
	valid := hmacSHA256TSDotHeader("whsec", now, body)
	tests := map[string]string{
		"missing header": "",
		"missing t":      "v1=" + valid[len(fmt.Sprintf("t=%d,v1=", now.Unix())):],
		"missing v1":     fmt.Sprintf("t=%d", now.Unix()),
		"non-numeric t":  "t=notanumber,v1=" + valid[len(fmt.Sprintf("t=%d,v1=", now.Unix())):],
		"bad hex":        fmt.Sprintf("t=%d,v1=zzzz", now.Unix()),
		"tampered":       valid[:len(valid)-1] + "0",
		"expired":        hmacSHA256TSDotHeader("whsec", now.Add(-10*time.Minute), body),
		"future":         hmacSHA256TSDotHeader("whsec", now.Add(10*time.Minute), body),
		"wrong secret":   hmacSHA256TSDotHeader("attacker", now, body),
	}
	for name, header := range tests {
		t.Run(name, func(t *testing.T) {
			v, _ := NewVerifier(Spec{AuthType: AuthTypeHMACSHA256, Scheme: SchemeHMACSHA256TSDot, Secret: "whsec", TimestampTolerance: 5 * time.Minute})
			if err := v.Verify(header, body, now); err == nil {
				t.Fatal("expected verification error")
			}
		})
	}
}

func TestHMACSHA256TSDotVerifierRejectsTamperedBodyAndZeroTolerance(t *testing.T) {
	body := []byte(`{"id":"evt_1"}`)
	now := time.Unix(1710000000, 0)
	header := hmacSHA256TSDotHeader("whsec", now, body)
	v, _ := NewVerifier(Spec{AuthType: AuthTypeHMACSHA256, Scheme: SchemeHMACSHA256TSDot, Secret: "whsec", TimestampTolerance: 5 * time.Minute})
	if err := v.Verify(header, []byte(`{"id":"evt_2"}`), now); err == nil {
		t.Fatal("tampered body should fail")
	}
	// A timestamped scheme with no tolerance must fail closed.
	v0, _ := NewVerifier(Spec{AuthType: AuthTypeHMACSHA256, Scheme: SchemeHMACSHA256TSDot, Secret: "whsec", TimestampTolerance: 0})
	if err := v0.Verify(header, body, now); err == nil {
		t.Fatal("zero tolerance should fail closed")
	}
}
