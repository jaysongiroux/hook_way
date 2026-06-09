package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"
)

// githubVerifier validates GitHub's `X-Hub-Signature-256: sha256=<hex>` header,
// an HMAC-SHA256 over the exact raw request body with no signed timestamp.
type githubVerifier struct {
	secret string
}

func newGitHubVerifier(secret string) githubVerifier {
	return githubVerifier{secret: secret}
}

func (v githubVerifier) Verify(header string, rawBody []byte, _ time.Time) error {
	if v.secret == "" {
		return ErrUnauthorized
	}
	hexSig, ok := strings.CutPrefix(strings.TrimSpace(header), "sha256=")
	if !ok || hexSig == "" {
		return ErrUnauthorized
	}
	got, err := hex.DecodeString(hexSig)
	if err != nil {
		return ErrUnauthorized
	}
	if len(got) != sha256.Size {
		return ErrUnauthorized
	}
	mac := hmac.New(sha256.New, []byte(v.secret))
	mac.Write(rawBody)
	if !hmac.Equal(mac.Sum(nil), got) {
		return ErrUnauthorized
	}
	return nil
}
