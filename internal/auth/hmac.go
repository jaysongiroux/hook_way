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
	if v.secret == "" {
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
	got, err := v.scheme.encoding.decode(p.signature)
	if err != nil {
		return ErrUnauthorized
	}
	if len(got) != v.scheme.newHash().Size() {
		return ErrUnauthorized
	}
	if !hmac.Equal(expected, got) {
		return ErrUnauthorized
	}
	return nil
}

// bodyOnlyPayload signs the exact raw body, used by the non-timestamped schemes.
func bodyOnlyPayload(_ parsedSig, rawBody []byte) []byte { return rawBody }

// rawHeaderParser treats the trimmed header value as the whole signature, with
// no prefix and no timestamp (used by the base64 scheme).
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

// parseTSColonHeader parses a labeled `ts=<unix>;h1=<hex>` header
// (semicolon-delimited, order-free), rejecting duplicate keys.
func parseTSColonHeader(header string) (parsedSig, error) {
	if header == "" {
		return parsedSig{}, errors.New("missing signature")
	}
	values := map[string]string{}
	for _, part := range strings.Split(header, ";") {
		key, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok || key == "" || value == "" {
			return parsedSig{}, errors.New("malformed signature")
		}
		if _, exists := values[key]; exists {
			return parsedSig{}, errors.New("duplicate signature key")
		}
		values[key] = value
	}
	tsValue, ok := values["ts"]
	if !ok {
		return parsedSig{}, errors.New("missing ts")
	}
	h1, ok := values["h1"]
	if !ok {
		return parsedSig{}, errors.New("missing h1")
	}
	ts, err := strconv.ParseInt(tsValue, 10, 64)
	if err != nil {
		return parsedSig{}, err
	}
	return parsedSig{signature: h1, timestamp: ts, hasTimestamp: true}, nil
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

// parseTSDotHeader parses a labeled `t=<unix>,v1=<hex>,...` comma-delimited
// header. Only `t` and the first `v1` are used; any other pairs are ignored.
// A missing `t` or `v1` is an error.
func parseTSDotHeader(header string) (parsedSig, error) {
	if strings.TrimSpace(header) == "" {
		return parsedSig{}, errors.New("missing signature")
	}
	var tsValue, v1 string
	for _, part := range strings.Split(header, ",") {
		key, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok || value == "" {
			continue
		}
		switch key {
		case "t":
			tsValue = value
		case "v1":
			if v1 == "" {
				v1 = value
			}
		}
	}
	if tsValue == "" || v1 == "" {
		return parsedSig{}, errors.New("missing t or v1")
	}
	ts, err := strconv.ParseInt(tsValue, 10, 64)
	if err != nil {
		return parsedSig{}, err
	}
	return parsedSig{signature: v1, timestamp: ts, hasTimestamp: true}, nil
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
