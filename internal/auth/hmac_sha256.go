package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

var ErrUnauthorized = errors.New("unauthorized")

type HMACSHA256Verifier struct {
	secret    string
	tolerance time.Duration
}

// NewHMACSHA256Verifier builds a verifier for timestamped HMAC-SHA256 headers.
func NewHMACSHA256Verifier(secret string, tolerance time.Duration) HMACSHA256Verifier {
	return HMACSHA256Verifier{secret: secret, tolerance: tolerance}
}

// Verify checks a header shaped like `ts=<unix>;h1=<hex_hmac>` against the exact raw body bytes.
func (v HMACSHA256Verifier) Verify(header string, rawBody []byte, now time.Time) error {
	if v.secret == "" || v.tolerance <= 0 {
		return ErrUnauthorized
	}
	ts, signature, err := parseSignature(header)
	if err != nil {
		return ErrUnauthorized
	}
	signedAt := time.Unix(ts, 0)
	if signedAt.Before(now.Add(-v.tolerance)) || signedAt.After(now.Add(v.tolerance)) {
		return ErrUnauthorized
	}
	payload := fmt.Sprintf("%d:%s", ts, rawBody)
	mac := hmac.New(sha256.New, []byte(v.secret))
	mac.Write([]byte(payload))
	expected := mac.Sum(nil)
	got, err := hex.DecodeString(signature)
	if err != nil {
		return ErrUnauthorized
	}
	if len(got) != sha256.Size {
		return ErrUnauthorized
	}
	if !hmac.Equal(expected, got) {
		return ErrUnauthorized
	}
	return nil
}

func parseSignature(header string) (int64, string, error) {
	if header == "" {
		return 0, "", errors.New("missing signature")
	}
	parts := strings.Split(header, ";")
	values := map[string]string{}
	for _, part := range parts {
		key, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok || key == "" || value == "" {
			return 0, "", errors.New("malformed signature")
		}
		if _, exists := values[key]; exists {
			return 0, "", errors.New("duplicate signature key")
		}
		values[key] = value
	}
	tsValue, ok := values["ts"]
	if !ok {
		return 0, "", errors.New("missing ts")
	}
	h1, ok := values["h1"]
	if !ok {
		return 0, "", errors.New("missing h1")
	}
	ts, err := strconv.ParseInt(tsValue, 10, 64)
	if err != nil {
		return 0, "", err
	}
	return ts, h1, nil
}
