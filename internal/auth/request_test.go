package auth

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func requestMAC(secret, ts, body string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + "." + body))
	return hex.EncodeToString(mac.Sum(nil))
}
func requestV(t *testing.T, s Spec) RequestVerifier {
	t.Helper()
	v, err := NewVerifier(s)
	if err != nil {
		t.Fatal(err)
	}
	return v.(RequestVerifier)
}
func TestHeadersHMACJavaScriptRepresentationAndTimePolicies(t *testing.T) {
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	spec := Spec{Scheme: SchemeHMACSHA256HeadersDot, Secret: "synthetic", TimestampTolerance: 5 * time.Minute, TimestampUnit: "milliseconds", TimestampPolicy: "age", BodyRepresentation: "json_js"}
	v := requestV(t, spec)
	raw := []byte(`{ "b": 2, "1": 1, "a": 9007199254740993, "u":"\ud800" }`)
	canonical := `{"1":1,"b":2,"a":9007199254740992,"u":"\ud800"}`
	original := string(raw)
	for _, tc := range []struct {
		delta time.Duration
		valid bool
	}{{0, true}, {-5 * time.Minute, true}, {-5*time.Minute - time.Millisecond, false}, {10 * time.Minute, true}} {
		ts := strconv.FormatInt(now.Add(tc.delta).UnixMilli(), 10)
		in := Input{Timestamp: ts, Signature: requestMAC(spec.Secret, ts, canonical), Body: raw}
		if err := v.VerifyRequest(context.Background(), in, now); (err == nil) != tc.valid {
			t.Errorf("delta=%s: %v", tc.delta, err)
		}
	}
	if string(raw) != original {
		t.Fatal("verification mutated original body")
	}
	// Millisecond protocol clocks must not acquire nanosecond precision.
	boundary := strconv.FormatInt(now.Add(-5*time.Minute).UnixMilli(), 10)
	if err := v.VerifyRequest(context.Background(), Input{Timestamp: boundary, Signature: requestMAC(spec.Secret, boundary, canonical), Body: raw}, now.Add(999999*time.Nanosecond)); err != nil {
		t.Fatalf("millisecond boundary: %v", err)
	}
	spec.TimestampPolicy = "symmetric"
	v = requestV(t, spec)
	for _, tc := range []struct {
		delta time.Duration
		valid bool
	}{{5 * time.Minute, true}, {5*time.Minute + time.Millisecond, false}} {
		ts := strconv.FormatInt(now.Add(tc.delta).UnixMilli(), 10)
		if err := v.VerifyRequest(context.Background(), Input{Timestamp: ts, Signature: requestMAC(spec.Secret, ts, canonical), Body: raw}, now); (err == nil) != tc.valid {
			t.Errorf("future=%s: %v", tc.delta, err)
		}
	}
	ts := strconv.FormatInt(now.UnixMilli(), 10)
	if err := v.VerifyRequest(context.Background(), Input{Timestamp: ts, Signature: requestMAC(spec.Secret, ts, original), Body: raw}, now); FailureReason(err) != "digest_mismatch" {
		t.Fatalf("raw fallback allowed: %v", err)
	}
	if err := v.VerifyRequest(context.Background(), Input{Timestamp: ts, Signature: requestMAC(spec.Secret, ts, canonical), Body: []byte("bad")}, now); FailureReason(err) != "invalid_body" {
		t.Fatalf("invalid JSON: %v", err)
	}
}
func TestMultiSignatureCandidatesAndBounds(t *testing.T) {
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	ts := strconv.FormatInt(now.Unix(), 10)
	spec := Spec{Scheme: SchemeHMACSHA256TSDotMulti, Secret: "new", TimestampTolerance: 5 * time.Minute, TimestampUnit: "seconds"}
	v := requestV(t, spec)
	body := "raw body"
	good := requestMAC("new", ts, body)
	for _, tc := range []struct{ header, reason string }{
		{"t=" + ts + ",s=bad,s=" + good, ""},
		{"s=" + good + ",t=" + ts, ""},
		{"t=" + ts + ",t=" + ts + ",s=" + good, "malformed_timestamp"},
		{"t=" + ts, "missing_signature"},
		{"t=" + ts + ",s=zz", "malformed_signature"},
		{"t=" + ts + ",s=" + strings.Repeat("00", 32), "digest_mismatch"},
		{"t=" + ts + strings.Repeat(",s="+good, 17), "too_many_signatures"},
		{"t=9999999999999999999,s=" + good, "malformed_timestamp"},
		{"t=+123,s=" + good, "malformed_timestamp"},
		{strings.Repeat("s", MaxCredentialBytes+1), "header_too_large"},
	} {
		err := v.VerifyRequest(context.Background(), Input{Signature: tc.header, Body: []byte(body)}, now)
		if tc.reason == "" {
			if err != nil {
				t.Error(err)
			}
		} else if FailureReason(err) != tc.reason || !errors.Is(err, ErrUnauthorized) {
			t.Errorf("want %s, got %v", tc.reason, err)
		}
	}
	for _, delta := range []time.Duration{-5*time.Minute - time.Second, 5*time.Minute + time.Second} {
		stale := strconv.FormatInt(now.Add(delta).Unix(), 10)
		err := v.VerifyRequest(context.Background(), Input{Signature: "t=" + stale + ",s=" + requestMAC("new", stale, body), Body: []byte(body)}, now)
		if FailureReason(err) != "timestamp_window" {
			t.Fatal(err)
		}
	}
}
func TestECDSAP256DERSeparateHeaders(t *testing.T) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	ts := strconv.FormatInt(now.Unix(), 10)
	body := []byte("{ \"k\": 1 }")
	sign := func(ts string, body []byte) string {
		hash := sha256.Sum256(append([]byte(ts), body...))
		sig, err := ecdsa.SignASN1(rand.Reader, priv, hash[:])
		if err != nil {
			t.Fatal(err)
		}
		return base64.StdEncoding.EncodeToString(sig)
	}
	spec := Spec{Scheme: SchemeECDSAP256SHA256Headers, Secret: base64.StdEncoding.EncodeToString(der), TimestampTolerance: 5 * time.Minute, TimestampUnit: "seconds"}
	v := requestV(t, spec)
	good := sign(ts, body)
	if err := v.VerifyRequest(context.Background(), Input{Signature: good, Timestamp: ts, Body: body}, now); err != nil {
		t.Fatal(err)
	}
	for _, in := range []Input{
		{Signature: good, Timestamp: ts, Body: []byte(`{"k":1}`)},
		{Signature: "not base64", Timestamp: ts, Body: body},
		{Signature: base64.StdEncoding.EncodeToString(make([]byte, 64)), Timestamp: ts, Body: body},
		{Signature: good, Timestamp: ts + " ", Body: body},
	} {
		if err := v.VerifyRequest(context.Background(), in, now); err == nil {
			t.Fatal("accepted modified ECDSA input")
		}
	}
	for _, tc := range []struct {
		delta time.Duration
		valid bool
	}{{-5 * time.Minute, true}, {5 * time.Minute, true}, {-5*time.Minute - time.Second, false}, {5*time.Minute + time.Second, false}} {
		stamp := strconv.FormatInt(now.Add(tc.delta).Unix(), 10)
		if err := v.VerifyRequest(context.Background(), Input{Signature: sign(stamp, body), Timestamp: stamp, Body: body}, now); (err == nil) != tc.valid {
			t.Errorf("delta=%s: %v", tc.delta, err)
		}
	}
	spec.TimestampPolicy = "unchecked"
	v = requestV(t, spec)
	stamp := strconv.FormatInt(now.Add(-24*time.Hour).Unix(), 10)
	if err := v.VerifyRequest(context.Background(), Input{Signature: sign(stamp, body), Timestamp: stamp, Body: body}, now); err != nil {
		t.Fatal(err)
	}
	p384, _ := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	wrong, _ := x509.MarshalPKIXPublicKey(&p384.PublicKey)
	for _, key := range []string{"invalid", base64.StdEncoding.EncodeToString([]byte("DER invalid")), base64.StdEncoding.EncodeToString(wrong)} {
		spec.Secret = key
		if _, err := NewVerifier(spec); err == nil {
			t.Fatal("invalid key accepted")
		}
	}
}
func TestRequestSpecRejectsUnsupportedOptions(t *testing.T) {
	for _, spec := range []Spec{
		{Scheme: "unknown"},
		{Scheme: SchemeHMACSHA256Hex, TimestampUnit: "seconds"},
		{Scheme: SchemeHMACSHA256HeadersDot, Secret: "s", TimestampTolerance: time.Minute},
		{Scheme: SchemeECDSAP256SHA256Headers, Secret: "s", TimestampUnit: "seconds", BodyRepresentation: "json_js", TimestampTolerance: time.Minute},
		{Scheme: SchemeHMACSHA256TSDotMulti, Secret: "s", TimestampUnit: "seconds", TimestampPolicy: "anything", TimestampTolerance: time.Minute},
	} {
		if _, err := NewVerifier(spec); err == nil {
			t.Fatalf("accepted %+v", spec)
		}
	}
}

func TestRequestLocalRepresentationReusedAcrossRotationAndBoundToBody(t *testing.T) {
	now := time.Now()
	ts := strconv.FormatInt(now.UnixMilli(), 10)
	body := []byte(`{ "b":2, "a":9007199254740993 }`)
	spec := Spec{Scheme: SchemeHMACSHA256HeadersDot, Secret: "previous", TimestampUnit: "milliseconds", TimestampPolicy: "age", TimestampTolerance: time.Minute, BodyRepresentation: "json_js"}
	in := Input{Signature: requestMAC("current", ts, `{"b":2,"a":9007199254740992}`), Timestamp: ts, Body: body}.WithCanonicalCache()
	if in.PreparedRepresentation() != nil {
		t.Fatal("cache eagerly canonicalized before verification")
	}
	if err := requestV(t, spec).VerifyRequest(context.Background(), in, now); FailureReason(err) != "digest_mismatch" {
		t.Fatal(err)
	}
	prepared := in.PreparedRepresentation()
	if prepared == nil {
		t.Fatal("signature attempt did not prepare body")
	}
	spec.Secret = "current"
	if err := requestV(t, spec).VerifyRequest(context.Background(), in, now); err != nil {
		t.Fatal(err)
	}
	if in.PreparedRepresentation() != prepared {
		t.Fatal("rotation rebuilt representation")
	}
	in.Body = []byte(`{ "b":3, "a":9007199254740993 }`)
	if err := requestV(t, spec).VerifyRequest(context.Background(), in, now); FailureReason(err) != "digest_mismatch" {
		t.Fatal("stale body authenticated", err)
	}
	if in.PreparedRepresentation() == prepared {
		t.Fatal("changed input retained stale representation")
	}
	missing := Input{Body: body}.WithCanonicalCache()
	if err := requestV(t, spec).VerifyRequest(context.Background(), missing, now); FailureReason(err) != "missing_header" || missing.PreparedRepresentation() != nil {
		t.Fatal("credential checks moved after canonicalization", err)
	}
}

func TestRepresentationDeadlineIsTemporaryNotInvalidCredentials(t *testing.T) {
	now := time.Now()
	ts := strconv.FormatInt(now.UnixMilli(), 10)
	spec := Spec{Scheme: SchemeHMACSHA256HeadersDot, Secret: "synthetic", TimestampTolerance: time.Minute, TimestampUnit: "milliseconds", TimestampPolicy: "age", BodyRepresentation: "json_js"}
	v := requestV(t, spec)
	for _, reason := range []error{context.Canceled, context.DeadlineExceeded} {
		ctx, cancel := context.WithCancelCause(context.Background())
		cancel(reason)
		// A canceled context cannot produce authentication proof or reusable bytes.
		in := (Input{Timestamp: ts, Signature: requestMAC(spec.Secret, ts, `{}`), Body: []byte(`{}`)}).WithCanonicalCache()
		err := v.VerifyRequest(ctx, in, now)
		if !errors.Is(err, ErrVerificationUnavailable) || errors.Is(err, ErrUnauthorized) || in.PreparedRepresentation() != nil {
			t.Fatalf("temporary verification failure misclassified: %v", err)
		}
	}
	// A deadline context exercises the actual deadline identity, not cancel cause.
	ctx, cancel := context.WithDeadline(context.Background(), now.Add(-time.Second))
	defer cancel()
	if err := v.VerifyRequest(ctx, Input{Timestamp: ts, Signature: requestMAC(spec.Secret, ts, `{}`), Body: []byte(`{}`)}, now); !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, ErrVerificationUnavailable) {
		t.Fatalf("deadline identity lost: %v", err)
	}
}

type cancelAfterVerificationCheck struct {
	context.Context
	cancel context.CancelFunc
	once   sync.Once
}

func (c *cancelAfterVerificationCheck) Err() error {
	err := c.Context.Err()
	c.once.Do(c.cancel)
	return err
}
func TestCanonicalBodyCancellationIsRetryable(t *testing.T) {
	now := time.Now()
	ts := strconv.FormatInt(now.UnixMilli(), 10)
	spec := Spec{Scheme: SchemeHMACSHA256HeadersDot, Secret: "synthetic", TimestampTolerance: time.Minute, TimestampUnit: "milliseconds", TimestampPolicy: "age", BodyRepresentation: "json_js"}
	for _, cached := range []bool{false, true} {
		base, cancel := context.WithCancel(context.Background())
		defer cancel()
		ctx := &cancelAfterVerificationCheck{Context: base, cancel: cancel}
		in := Input{Timestamp: ts, Signature: requestMAC(spec.Secret, ts, `{}`), Body: []byte(`{}`)}
		if cached {
			in = in.WithCanonicalCache()
		}
		err := requestV(t, spec).VerifyRequest(ctx, in, now)
		if !errors.Is(err, ErrVerificationUnavailable) || !errors.Is(err, context.Canceled) || errors.Is(err, ErrUnauthorized) || in.PreparedRepresentation() != nil {
			t.Fatalf("body computation cancellation = %v", err)
		}
	}
}
