package queue

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"path"
	"strings"
	"unicode"
)

// ObjectPayloadModes destinations that can store decoded bytes instead of the
// JSON Message envelope. Other destination types reject publish.payload: object
// at config validation time.
var ObjectPayloadModes = map[string]struct{}{
	"s3":       {},
	"redis_kv": {},
}

// SupportsObjectPayload reports whether destination type can honor
// publish.payload: object.
func SupportsObjectPayload(destType string) bool {
	_, ok := ObjectPayloadModes[destType]
	return ok
}

// ObjectFromBody extracts bytes for an object-mode publish from a parsed
// envelope body, or from the raw request when the body was left empty
// (body_format: raw).
//
// Accepted body shapes:
//   - map with base64 "content" (multipart file object after a code reshape)
//   - nil / empty body with RawBody or RawBodyBase64 set
//
// content_type and filename are optional metadata used by S3 for Content-Type
// and the object key suffix.
func ObjectFromBody(body any, rawBody []byte, rawBodyBase64 *string, headers http.Header) (data []byte, contentType, filename string, err error) {
	if m, ok := body.(map[string]any); ok {
		return objectFromFileMap(m)
	}
	if body != nil {
		return nil, "", "", fmt.Errorf("object payload requires a file object body {content, filename?, content_type?} or a raw (empty) body")
	}
	switch {
	case len(rawBody) > 0:
		data = append([]byte(nil), rawBody...)
	case rawBodyBase64 != nil && *rawBodyBase64 != "":
		data, err = base64.StdEncoding.DecodeString(*rawBodyBase64)
		if err != nil {
			return nil, "", "", fmt.Errorf("object payload: raw_body_base64: %w", err)
		}
	default:
		return nil, "", "", fmt.Errorf("object payload: no file content on the envelope")
	}
	if headers != nil {
		contentType = headers.Get("Content-Type")
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	return data, contentType, "", nil
}

func objectFromFileMap(m map[string]any) ([]byte, string, string, error) {
	raw, ok := m["content"]
	if !ok {
		if omitted, _ := m["content_omitted"].(bool); omitted {
			return nil, "", "", fmt.Errorf("object payload: file content was omitted (include_content: false)")
		}
		return nil, "", "", fmt.Errorf("object payload: body.content (base64) is required")
	}
	encoded, ok := raw.(string)
	if !ok {
		return nil, "", "", fmt.Errorf("object payload: body.content must be a base64 string, got %T", raw)
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, "", "", fmt.Errorf("object payload: body.content: %w", err)
	}
	contentType, _ := m["content_type"].(string)
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	filename, _ := m["filename"].(string)
	return data, contentType, filename, nil
}

// SanitizeObjectFilename returns a single path segment safe for an S3 key
// suffix, or "" when the input is empty / unusable. Path separators and
// control characters are stripped; length is capped.
func SanitizeObjectFilename(name string) string {
	name = path.Base(strings.ReplaceAll(name, "\\", "/"))
	if name == "." || name == ".." {
		return ""
	}
	var b strings.Builder
	for _, r := range name {
		switch {
		case r < 32 || r == 127:
			continue
		case r == '/' || r == '\\':
			continue
		case unicode.IsSpace(r):
			b.WriteByte('_')
		default:
			b.WriteRune(r)
		}
	}
	out := b.String()
	if len(out) > 200 {
		out = out[:200]
	}
	return out
}
