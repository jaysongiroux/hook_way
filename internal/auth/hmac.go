package auth

import (
	"crypto/hmac"
	"crypto/sha1" // #nosec G505 -- HMAC-SHA1 is a configured inbound webhook scheme
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"strconv"
	"strings"
	"time"
)

type sigEncoding int

const (
	encHex sigEncoding = iota
	encBase64
)

func (e sigEncoding) decode(s string) ([]byte, error) {
	if e == encBase64 {
		return base64.StdEncoding.DecodeString(s)
	}
	return hex.DecodeString(s)
}

// parsedSig is what a scheme extracts from its signature header.
type parsedSig struct {
	signature    string
	candidates   []string
	timestamp    int64
	hasTimestamp bool
}

// hmacScheme captures every per-provider difference between HMAC schemes.
type hmacScheme struct {
	newHash           func() hash.Hash
	encoding          sigEncoding
	requiresTimestamp bool
	parseHeader       func(header string) (parsedSig, error)
	buildPayload      func(p parsedSig, rawBody []byte) []byte
}

type hmacVerifier struct {
	secret    string
	tolerance time.Duration
	scheme    hmacScheme
}

func (v hmacVerifier) Verify(header string, rawBody []byte, now time.Time) error {
	if v.secret == "" || len(header) > MaxCredentialBytes {
		return ErrUnauthorized
	}
	p, err := v.scheme.parseHeader(header)
	if err != nil {
		return ErrUnauthorized
	}
	if v.scheme.requiresTimestamp {
		if v.tolerance <= 0 || !p.hasTimestamp {
			return ErrUnauthorized
		}
		signedAt := time.Unix(p.timestamp, 0)
		if signedAt.Before(now.Add(-v.tolerance)) || signedAt.After(now.Add(v.tolerance)) {
			return ErrUnauthorized
		}
	}
	mac := hmac.New(v.scheme.newHash, []byte(v.secret))
	mac.Write(v.scheme.buildPayload(p, rawBody))
	expected := mac.Sum(nil)
	candidates := p.candidates
	if len(candidates) == 0 {
		candidates = []string{p.signature}
	}
	for _, candidate := range candidates {
		got, err := v.scheme.encoding.decode(candidate)
		if err == nil && len(got) == v.scheme.newHash().Size() && hmac.Equal(expected, got) {
			return nil
		}
	}
	return ErrUnauthorized
}

// bodyOnlyPayload signs the exact raw body, used by the non-timestamped schemes.
func bodyOnlyPayload(_ parsedSig, rawBody []byte) []byte { return rawBody }

// rawHeaderParser treats the trimmed header value as the whole signature, with
// no prefix and no timestamp (used by raw hex and base64 schemes).
func rawHeaderParser(header string) (parsedSig, error) {
	sig := strings.TrimSpace(header)
	if sig == "" {
		return parsedSig{}, errors.New("missing signature")
	}
	return parsedSig{signature: sig}, nil
}

// prefixHeaderParser strips a fixed prefix such as "sha256=" from the header
// before decoding the signature (used by the prefixed hex schemes).
func prefixHeaderParser(prefix string) func(string) (parsedSig, error) {
	return func(header string) (parsedSig, error) {
		sig, ok := strings.CutPrefix(strings.TrimSpace(header), prefix)
		if !ok || sig == "" {
			return parsedSig{}, errors.New("missing or malformed signature")
		}
		return parsedSig{signature: sig}, nil
	}
}

// parseTimestampedHeader accepts bounded signature candidates during provider
// key rotation. The timestamp must occur exactly once; repeated signatures
// are valid, repeated timestamps are ambiguous and fail closed.
func parseTimestampedHeader(header, separator, timestampLabel, signatureLabel string) (parsedSig, error) {
	if len(header) == 0 || len(header) > MaxCredentialBytes {
		return parsedSig{}, errors.New("missing or oversized signature")
	}
	parts := strings.Split(header, separator)
	if len(parts) > MaxSignatureCandidates+8 {
		return parsedSig{}, errors.New("too many signature fields")
	}
	var timestamp string
	var candidates []string
	for _, part := range parts {
		key, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok || key == "" || value == "" {
			return parsedSig{}, errors.New("malformed signature")
		}
		if key == timestampLabel {
			if timestamp != "" {
				return parsedSig{}, errors.New("duplicate timestamp")
			}
			timestamp = value
		}
		if key == signatureLabel {
			candidates = append(candidates, value)
		}
	}
	if timestamp == "" || len(candidates) == 0 || len(candidates) > MaxSignatureCandidates {
		return parsedSig{}, errors.New("missing timestamp or invalid signature count")
	}
	for _, c := range timestamp {
		if c < '0' || c > '9' {
			return parsedSig{}, errors.New("malformed timestamp")
		}
	}
	ts, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil || ts > 253402300799 {
		return parsedSig{}, errors.New("timestamp out of range")
	}
	return parsedSig{signature: candidates[0], candidates: candidates, timestamp: ts, hasTimestamp: true}, nil
}

func parseTSColonHeader(header string) (parsedSig, error) {
	return parseTimestampedHeader(header, ";", "ts", "h1")
}

func hmacSHA256TSColonScheme() hmacScheme {
	return hmacScheme{
		newHash: sha256New, encoding: encHex, requiresTimestamp: true,
		parseHeader: parseTSColonHeader,
		buildPayload: func(p parsedSig, rawBody []byte) []byte {
			return []byte(fmt.Sprintf("%d:%s", p.timestamp, rawBody))
		},
	}
}

func hmacSHA256HexScheme() hmacScheme {
	return hmacScheme{
		newHash: sha256New, encoding: encHex, requiresTimestamp: false,
		parseHeader: prefixHeaderParser("sha256="), buildPayload: bodyOnlyPayload,
	}
}

func hmacSHA256RawHexScheme() hmacScheme {
	return hmacScheme{
		newHash: sha256New, encoding: encHex,
		parseHeader: rawHeaderParser, buildPayload: bodyOnlyPayload,
	}
}

// parseTSDotHeader accepts every v1 signature during Stripe secret rotation.
func parseTSDotHeader(header string) (parsedSig, error) {
	return parseTimestampedHeader(header, ",", "t", "v1")
}

func hmacSHA256TSDotScheme() hmacScheme {
	return hmacScheme{
		newHash: sha256New, encoding: encHex, requiresTimestamp: true,
		parseHeader: parseTSDotHeader,
		buildPayload: func(p parsedSig, rawBody []byte) []byte {
			return []byte(fmt.Sprintf("%d.%s", p.timestamp, rawBody))
		},
	}
}

func hmacSHA256Base64Scheme() hmacScheme {
	return hmacScheme{
		newHash: sha256New, encoding: encBase64, requiresTimestamp: false,
		parseHeader: rawHeaderParser, buildPayload: bodyOnlyPayload,
	}
}

func sha1New() hash.Hash { return sha1.New() } // #nosec G401 -- HMAC-SHA1 scheme

func hmacSHA1HexScheme() hmacScheme {
	return hmacScheme{
		newHash: sha1New, encoding: encHex, requiresTimestamp: false,
		parseHeader: prefixHeaderParser("sha1="), buildPayload: bodyOnlyPayload,
	}
}
