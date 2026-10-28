package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"unicode"
)

// verifyPolar runs after the shared timestamp and input-size checks. The
// original timestamp spelling and body bytes are part of the signed message.
func (v requestVerifier) verifyPolar(in Input) error {
	if in.MessageID == "" {
		return fail("missing_header")
	}
	// Dots would make the signed ID/timestamp/body framing ambiguous.
	if strings.Contains(in.MessageID, ".") || strings.IndexFunc(in.MessageID, unicode.IsSpace) >= 0 {
		return fail("malformed_message_id")
	}
	parts := strings.Fields(in.Signature)
	if len(parts) > MaxSignatureCandidates {
		return fail("too_many_signatures")
	}
	var candidates [][]byte
	for _, part := range parts {
		version, encoded, ok := strings.Cut(part, ",")
		if !ok || version != "v1" {
			continue
		}
		signature, err := base64.StdEncoding.Strict().DecodeString(encoded)
		if err == nil && len(signature) == sha256.Size {
			candidates = append(candidates, signature)
		}
	}
	if len(candidates) == 0 {
		return fail("malformed_signature")
	}
	for _, key := range v.polarKeys {
		mac := hmac.New(sha256.New, key)
		_, _ = mac.Write([]byte(in.MessageID + "." + in.Timestamp + "."))
		_, _ = mac.Write(in.Body)
		expected := mac.Sum(nil)
		for _, candidate := range candidates {
			if hmac.Equal(expected, candidate) {
				return nil
			}
		}
	}
	return fail("digest_mismatch")
}
