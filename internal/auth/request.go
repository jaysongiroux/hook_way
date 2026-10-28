package auth

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/x509"
	"encoding/asn1"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"
)

const (
	SchemeHMACSHA256SlackV0      = "hmac_sha256_slack_v0"
	SchemeHMACSHA256HeadersDot   = "hmac_sha256_headers_dot"
	SchemeHMACSHA256TSDotMulti   = "hmac_sha256_ts_dot_multi"
	SchemeHMACSHA256Polar        = "hmac_sha256_polar"
	SchemeECDSAP256SHA256Headers = "ecdsa_p256_sha256_headers"
	MaxCredentialBytes           = 8192
	MaxSignatureCandidates       = 16
)

// Input preserves separate wire inputs and the original bounded request body.
type Input struct {
	cache                           *bodyCache
	Signature, Timestamp, MessageID string
	Body                            []byte
}
type RequestVerifier interface {
	VerifyRequest(context.Context, Input, time.Time) error
}

// ErrVerificationUnavailable identifies temporary computation failure, never
// evidence that a credential is invalid. Callers may return a retryable status.
var ErrVerificationUnavailable = errors.New("authentication verification temporarily unavailable")

// Failure contains only a finite reason, never supplied credentials or keys.
type Failure struct{ Reason string }

func (e Failure) Error() string { return "authentication failed: " + e.Reason }
func (e Failure) Unwrap() error { return ErrUnauthorized }
func fail(reason string) error  { return Failure{Reason: reason} }
func FailureReason(err error) string {
	var f Failure
	if errors.As(err, &f) {
		switch f.Reason {
		case "missing_header", "duplicate_header", "missing_timestamp", "malformed_timestamp", "malformed_message_id", "timestamp_window", "header_too_large", "too_many_signatures", "missing_signature", "malformed_signature", "invalid_body", "digest_mismatch":
			return f.Reason
		}
	}
	return "invalid_signature"
}

func IsRequestScheme(scheme string) bool {
	return scheme == SchemeHMACSHA256SlackV0 || scheme == SchemeHMACSHA256HeadersDot || scheme == SchemeHMACSHA256TSDotMulti || scheme == SchemeECDSAP256SHA256Headers || scheme == SchemeHMACSHA256Polar
}
func SeparateTimestamp(scheme string) bool {
	return scheme == SchemeHMACSHA256SlackV0 || scheme == SchemeHMACSHA256HeadersDot || scheme == SchemeECDSAP256SHA256Headers || scheme == SchemeHMACSHA256Polar
}

// ValidateSpec checks protocol configuration without accessing key material.
func ValidateSpec(s Spec) error {
	if _, ok := registry[s.Scheme]; !ok {
		return fmt.Errorf("unknown signature scheme %q", s.Scheme)
	}
	if !IsRequestScheme(s.Scheme) {
		if s.TimestampUnit != "" || s.TimestampPolicy != "" || s.BodyRepresentation != "" || s.SignatureLabel != "" {
			return errors.New("request signature options require a request signature scheme")
		}
		return nil
	}
	if s.TimestampUnit != "seconds" && s.TimestampUnit != "milliseconds" {
		return errors.New("timestamp_unit must be seconds or milliseconds")
	}
	switch s.TimestampPolicy {
	case "", "symmetric", "age", "unchecked":
	default:
		return errors.New("timestamp_policy must be symmetric, age or unchecked")
	}
	if s.TimestampPolicy != "unchecked" && s.TimestampTolerance <= 0 {
		return errors.New("timestamp_tolerance must be positive")
	}
	if s.BodyRepresentation != "" && s.BodyRepresentation != "raw" && s.BodyRepresentation != "json_js" {
		return errors.New("body_representation must be raw or json_js")
	}
	if s.BodyRepresentation == "json_js" && s.Scheme != SchemeHMACSHA256HeadersDot {
		return errors.New("json_js is supported only by hmac_sha256_headers_dot")
	}
	if s.Scheme == SchemeHMACSHA256SlackV0 && s.TimestampUnit != "seconds" {
		return errors.New("slack timestamps use seconds")
	}
	if s.Scheme == SchemeHMACSHA256Polar && (s.TimestampUnit != "seconds" || s.TimestampPolicy != "" && s.TimestampPolicy != "symmetric") {
		return errors.New("polar signatures require seconds timestamps and a symmetric timestamp policy")
	}
	if s.SignatureLabel != "" {
		if s.Scheme != SchemeHMACSHA256TSDotMulti || len(s.SignatureLabel) > 32 || s.SignatureLabel == "t" {
			return errors.New("signature_label requires a distinct label on the multi-signature scheme")
		}
		for _, c := range s.SignatureLabel {
			valid := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_'
			if !valid {
				return errors.New("signature_label must be alphanumeric or underscore")
			}
		}
	}
	return nil
}

type requestVerifier struct {
	spec      Spec
	key       *ecdsa.PublicKey
	polarKeys [][]byte
}

func newRequestVerifier(s Spec) (Verifier, error) {
	if err := ValidateSpec(s); err != nil {
		return nil, err
	}
	if len(s.Secret) == 0 || len(s.Secret) > 65536 {
		return nil, errors.New("key material must be nonempty and at most 64 KiB")
	}
	v := requestVerifier{spec: s}
	if s.Scheme == SchemeHMACSHA256Polar {
		// Polar's current format decodes the whsec_ payload; legacy endpoints
		// sign with the complete dashboard secret's UTF-8 bytes instead.
		encoded := strings.TrimPrefix(s.Secret, "whsec_")
		if decoded, err := base64.StdEncoding.Strict().DecodeString(encoded); err == nil && len(decoded) > 0 {
			v.polarKeys = append(v.polarKeys, decoded)
		}
		v.polarKeys = append(v.polarKeys, []byte(s.Secret))
	}
	if s.Scheme == SchemeECDSAP256SHA256Headers {
		// Auth secret is a base64 encoded PKIX/SubjectPublicKeyInfo DER public key.
		der, err := base64.StdEncoding.Strict().DecodeString(s.Secret)
		if err != nil {
			return nil, errors.New("ECDSA key must be base64 PKIX DER")
		}
		parsed, err := x509.ParsePKIXPublicKey(der)
		if err != nil {
			return nil, errors.New("invalid PKIX public key")
		}
		key, ok := parsed.(*ecdsa.PublicKey)
		if !ok || key.Curve != elliptic.P256() {
			return nil, errors.New("ECDSA key must use P-256")
		}
		if _, err := key.Bytes(); err != nil {
			return nil, errors.New("invalid P-256 public key")
		}
		v.key = key
	}
	return v, nil
}
func init() {
	Register(AuthTypeHMACSHA256, SchemeHMACSHA256SlackV0, true, newRequestVerifier)
	Register(AuthTypeHMACSHA256, SchemeHMACSHA256HeadersDot, true, newRequestVerifier)
	Register(AuthTypeHMACSHA256, SchemeHMACSHA256TSDotMulti, true, newRequestVerifier)
	Register(AuthTypeHMACSHA256, SchemeHMACSHA256Polar, true, newRequestVerifier)
	Register("ecdsa", SchemeECDSAP256SHA256Headers, true, newRequestVerifier)
}
func (v requestVerifier) Verify(header string, body []byte, now time.Time) error {
	return v.VerifyRequest(context.Background(), Input{Signature: header, Body: body}, now)
}
func (v requestVerifier) VerifyRequest(ctx context.Context, in Input, now time.Time) error {
	if err := ctx.Err(); err != nil {
		return errors.Join(ErrVerificationUnavailable, err)
	}
	if len(in.Signature) == 0 {
		return fail("missing_header")
	}
	if len(in.Signature) > MaxCredentialBytes || len(in.Timestamp) > 32 || len(in.MessageID) > MaxCredentialBytes {
		return fail("header_too_large")
	}
	ts := in.Timestamp
	candidates := []string{in.Signature}
	if v.spec.Scheme == SchemeHMACSHA256SlackV0 {
		signature, ok := strings.CutPrefix(in.Signature, "v0=")
		if !ok {
			return fail("malformed_signature")
		}
		candidates = []string{signature}
	}
	if v.spec.Scheme == SchemeHMACSHA256TSDotMulti {
		candidates = nil
		label := v.spec.SignatureLabel
		if label == "" {
			label = "s"
		}
		parts := strings.Split(in.Signature, ",")
		if len(parts) > MaxSignatureCandidates+8 {
			return fail("too_many_signatures")
		}
		for _, part := range parts {
			name, value, ok := strings.Cut(strings.TrimSpace(part), "=")
			if !ok || value == "" {
				return fail("malformed_signature")
			}
			if name == "t" {
				if ts != "" {
					return fail("malformed_timestamp")
				}
				ts = value
			}
			if name == label {
				candidates = append(candidates, value)
				if len(candidates) > MaxSignatureCandidates {
					return fail("too_many_signatures")
				}
			}
		}
		if len(candidates) == 0 {
			return fail("missing_signature")
		}
	}
	if ts == "" {
		return fail("missing_timestamp")
	}
	if len(ts) > 19 {
		return fail("malformed_timestamp")
	}
	for _, c := range ts {
		if c < '0' || c > '9' {
			return fail("malformed_timestamp")
		}
	}
	n, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return fail("malformed_timestamp")
	}
	if v.spec.TimestampPolicy != "unchecked" {
		// Reject unrepresentable dates before converting to time.Time. A bounded
		// timestamp avoids integer overflow in unix milliseconds / seconds.
		max := int64(253402300799)
		if v.spec.TimestampUnit == "milliseconds" {
			max *= 1000
		}
		if n > max {
			return fail("malformed_timestamp")
		}
		signed := time.Unix(n, 0)
		clock := now
		if v.spec.TimestampUnit == "milliseconds" {
			signed = time.UnixMilli(n)
			// Date.now has millisecond precision; sub-millisecond server clock
			// precision must not reject the exact maximum-age boundary.
			clock = time.UnixMilli(now.UnixMilli())
		}
		if signed.Before(clock.Add(-v.spec.TimestampTolerance)) || (v.spec.TimestampPolicy != "age" && signed.After(clock.Add(v.spec.TimestampTolerance))) {
			return fail("timestamp_window")
		}
	}
	if v.spec.Scheme == SchemeHMACSHA256Polar {
		return v.verifyPolar(in)
	}
	body := in.Body
	if v.spec.BodyRepresentation == "json_js" {
		body, err = in.canonicalBody(ctx)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
				return errors.Join(ErrVerificationUnavailable, err)
			}
			return fail("invalid_body")
		}
	}
	sep := "."
	if v.spec.Scheme == SchemeECDSAP256SHA256Headers {
		sep = ""
	}
	hash := sha256.New()
	if v.key == nil {
		hash = hmac.New(sha256.New, []byte(v.spec.Secret))
	}
	prefix := ts + sep
	if v.spec.Scheme == SchemeHMACSHA256SlackV0 {
		prefix = "v0:" + ts + ":"
	}
	_, _ = hash.Write([]byte(prefix))
	_, _ = hash.Write(body)
	digest := hash.Sum(nil)
	malformed := true
	for _, candidate := range candidates {
		if v.key != nil {
			sig, err := base64.StdEncoding.Strict().DecodeString(candidate)
			if err != nil || len(sig) > 80 {
				continue
			}
			var pair struct{ R, S *big.Int }
			rest, err := asn1.Unmarshal(sig, &pair)
			if err != nil || len(rest) != 0 || pair.R == nil || pair.S == nil || pair.R.Sign() <= 0 || pair.S.Sign() <= 0 {
				continue
			}
			malformed = false
			if ecdsa.VerifyASN1(v.key, digest, sig) {
				return nil
			}
		} else {
			got, err := hex.DecodeString(candidate)
			if err != nil || len(got) != sha256.Size {
				continue
			}
			malformed = false
			if hmac.Equal(digest, got) {
				return nil
			}
		}
	}
	if malformed {
		return fail("malformed_signature")
	}
	return fail("digest_mismatch")
}
