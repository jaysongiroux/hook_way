package queue

import (
	"encoding/base64"
	"fmt"
	"mime"
	"path"
	"strings"
	"unicode"
)

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
func ObjectFromBody(body any, rawBody []byte, rawBodyBase64 *string, rawContentType string) (data []byte, contentType, filename string, err error) {
	if m, ok := body.(map[string]any); ok {
		return objectFromFileMap(m)
	}
	if body != nil {
		return nil, "", "", fmt.Errorf("object payload requires a file object body {content, filename?, content_type?} or a raw (empty) body")
	}
	switch {
	case len(rawBody) > 0:
		data = append([]byte(nil), rawBody...)
	case rawBodyBase64 != nil:
		data, err = base64.StdEncoding.DecodeString(*rawBodyBase64)
		if err != nil {
			return nil, "", "", fmt.Errorf("object payload: raw_body_base64: %w", err)
		}
	default:
		return nil, "", "", fmt.Errorf("object payload: no file content on the envelope")
	}
	contentType, err = normalizeObjectContentType(rawContentType)
	if err != nil {
		return nil, "", "", err
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
	contentType, err := optionalObjectString(m, "content_type")
	if err != nil {
		return nil, "", "", err
	}
	contentType, err = normalizeObjectContentType(contentType)
	if err != nil {
		return nil, "", "", err
	}
	filename, err := optionalObjectString(m, "filename")
	if err != nil {
		return nil, "", "", err
	}
	return data, contentType, filename, nil
}

func optionalObjectString(m map[string]any, key string) (string, error) {
	value, ok := m[key]
	if !ok || value == nil {
		return "", nil
	}
	text, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("object payload: body.%s must be a string, got %T", key, value)
	}
	return text, nil
}

func normalizeObjectContentType(value string) (string, error) {
	if value == "" {
		return "application/octet-stream", nil
	}
	if strings.ContainsAny(value, "\r\n") {
		return "", fmt.Errorf("object payload: content_type contains a line break")
	}
	if _, _, err := mime.ParseMediaType(value); err != nil {
		return "", fmt.Errorf("object payload: content_type %q is invalid: %w", value, err)
	}
	return value, nil
}

// SanitizeObjectFilename returns a single path segment safe for an S3 key
// suffix, or "" when the input is empty / unusable. Path separators and
// control and Unicode format characters are stripped; length is capped.
func SanitizeObjectFilename(name string) string {
	name = strings.ToValidUTF8(name, "")
	name = path.Base(strings.ReplaceAll(name, "\\", "/"))
	if name == "." || name == ".." {
		return ""
	}
	var b strings.Builder
	for _, r := range name {
		switch {
		case unicode.IsControl(r) || unicode.In(r, unicode.Cf):
			continue
		case r == '/' || r == '\\':
			continue
		case unicode.IsSpace(r):
			if b.Len()+1 > 200 {
				return b.String()
			}
			b.WriteByte('_')
		default:
			if b.Len()+len(string(r)) > 200 {
				return b.String()
			}
			b.WriteRune(r)
		}
	}
	return b.String()
}
