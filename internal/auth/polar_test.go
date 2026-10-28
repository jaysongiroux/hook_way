package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"
)

func polarSignature(key []byte, in Input) string {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(in.MessageID + "." + in.Timestamp + "."))
	_, _ = mac.Write(in.Body)
	return "v1," + base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func TestPolarKeyFormats(t *testing.T) {
	key := []byte("independent-32-byte-signing-key!!")
	encoded := base64.StdEncoding.EncodeToString(key)
	now := time.Unix(1710000000, 0)
	for _, tc := range []struct {
		name, secret string
		key          []byte
	}{
		{"standard", "whsec_" + encoded, key},
		{"standard_without_prefix", encoded, key},
		{"legacy_prefixed", "whsec_" + encoded, []byte("whsec_" + encoded)},
		{"legacy_custom", "legacy-not-base64!", []byte("legacy-not-base64!")},
		{"legacy_invalid_encoded_suffix", "whsec_not-base64!", []byte("whsec_not-base64!")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v, err := NewVerifier(Spec{Scheme: SchemeHMACSHA256Polar, Secret: tc.secret, TimestampUnit: "seconds", TimestampTolerance: 5 * time.Minute})
			if err != nil {
				t.Fatal(err)
			}
			in := Input{MessageID: "event-1", Timestamp: strconv.FormatInt(now.Unix(), 10), Body: []byte("{ \"text\": \"café\" }\n")}
			in.Signature = polarSignature(tc.key, in)
			if err := v.(RequestVerifier).VerifyRequest(context.Background(), in, now); err != nil {
				t.Fatal(err)
			}
			if err := v.Verify(in.Signature, in.Body, now); !errors.Is(err, ErrUnauthorized) {
				t.Fatal("header-only verification must fail closed", err)
			}
		})
	}
}

func TestPolarInvalidInputsAndRotationCandidates(t *testing.T) {
	now := time.Unix(1710000000, 0)
	v, err := NewVerifier(Spec{Scheme: SchemeHMACSHA256Polar, Secret: "legacy!", TimestampUnit: "seconds", TimestampTolerance: 5 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	valid := Input{MessageID: "event-1", Timestamp: "1710000000", Body: []byte("{\"text\":\"café\"}\n")}
	valid.Signature = polarSignature([]byte("legacy!"), valid)
	for _, tc := range []struct {
		name   string
		change func(*Input)
	}{
		{"wrong_key", func(in *Input) { in.Signature = polarSignature([]byte("wrong"), *in) }},
		{"changed_body", func(in *Input) { in.Body = []byte("{\"text\":\"café\"}") }},
		{"changed_id", func(in *Input) { in.MessageID = "event-2" }},
		{"changed_timestamp", func(in *Input) { in.Timestamp = "1710000001" }},
		{"missing_signature", func(in *Input) { in.Signature = "" }},
		{"missing_id", func(in *Input) { in.MessageID = "" }},
		{"ambiguous_id", func(in *Input) { in.MessageID = "event.1"; in.Signature = polarSignature([]byte("legacy!"), *in) }},
		{"missing_timestamp", func(in *Input) { in.Timestamp = "" }},
		{"signed_timestamp", func(in *Input) { in.Timestamp = "+1710000000" }},
		{"negative_timestamp", func(in *Input) { in.Timestamp = "-1710000000" }},
		{"overflow_timestamp", func(in *Input) { in.Timestamp = "9999999999999999999" }},
		{"unrepresentable_timestamp", func(in *Input) { in.Timestamp = "253402300800" }},
		{"oversized_timestamp", func(in *Input) { in.Timestamp = strings.Repeat("1", 33) }},
		{"oversized_id", func(in *Input) { in.MessageID = strings.Repeat("a", MaxCredentialBytes+1) }},
		{"oversized_signature", func(in *Input) { in.Signature = strings.Repeat("a", MaxCredentialBytes+1) }},
		{"excessive_candidates", func(in *Input) { in.Signature = strings.Repeat("v2,bad ", MaxSignatureCandidates) + valid.Signature }},
		{"unknown_version", func(in *Input) { in.Signature = strings.Replace(in.Signature, "v1,", "v2,", 1) }},
		{"malformed_base64", func(in *Input) { in.Signature = "v1,%%%" }},
		{"short_digest", func(in *Input) { in.Signature = "v1,YQ==" }},
		{"missing_version", func(in *Input) { in.Signature = strings.TrimPrefix(in.Signature, "v1,") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := valid
			tc.change(&in)
			err := v.(RequestVerifier).VerifyRequest(context.Background(), in, now)
			if !errors.Is(err, ErrUnauthorized) {
				t.Fatalf("expected unauthorized: %v", err)
			}
			if strings.Contains(err.Error(), "legacy!") {
				t.Fatal("secret leaked")
			}
		})
	}
	for _, prefix := range []string{"v2,ignored ", "v1,%%% ", "v1," + base64.StdEncoding.EncodeToString(make([]byte, 32)) + " "} {
		in := valid
		in.Signature = prefix + valid.Signature
		if err := v.(RequestVerifier).VerifyRequest(context.Background(), in, now); err != nil {
			t.Fatal("valid later candidate rejected", err)
		}
	}
	for _, offset := range []time.Duration{-5 * time.Minute, 5 * time.Minute} {
		if err := v.(RequestVerifier).VerifyRequest(context.Background(), valid, now.Add(offset)); err != nil {
			t.Fatal("boundary rejected", err)
		}
	}
	for _, offset := range []time.Duration{-5*time.Minute - time.Nanosecond, 5*time.Minute + time.Nanosecond} {
		if err := v.(RequestVerifier).VerifyRequest(context.Background(), valid, now.Add(offset)); !errors.Is(err, ErrUnauthorized) {
			t.Fatal("out-of-window signature accepted", err)
		}
	}
	// The exact timestamp text, rather than its normalized integer, is signed.
	leadingZero := valid
	leadingZero.Timestamp = "01710000000"
	leadingZero.Signature = polarSignature([]byte("legacy!"), leadingZero)
	if err := v.(RequestVerifier).VerifyRequest(context.Background(), leadingZero, now); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := v.(RequestVerifier).VerifyRequest(ctx, valid, now); !errors.Is(err, ErrVerificationUnavailable) {
		t.Fatal(err)
	}
}

func TestPolarSpecValidation(t *testing.T) {
	for _, tc := range []Spec{
		{TimestampUnit: "milliseconds"},
		{TimestampUnit: "seconds", TimestampPolicy: "age"},
		{TimestampUnit: "seconds", TimestampPolicy: "unchecked"},
		{TimestampUnit: "seconds", BodyRepresentation: "json_js"},
		{TimestampUnit: "seconds", SignatureLabel: "s"},
	} {
		tc.Scheme = SchemeHMACSHA256Polar
		tc.Secret = "secret"
		tc.TimestampTolerance = time.Minute
		if _, err := NewVerifier(tc); err == nil {
			t.Fatalf("accepted incompatible spec: %+v", tc)
		}
	}
	for _, secret := range []string{"", strings.Repeat("x", 65537)} {
		if _, err := NewVerifier(Spec{Scheme: SchemeHMACSHA256Polar, Secret: secret, TimestampUnit: "seconds", TimestampTolerance: time.Minute}); err == nil {
			t.Fatal("invalid key size accepted")
		}
	}
}
