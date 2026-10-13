package logging

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

var errorURL = regexp.MustCompile(`[a-zA-Z][a-zA-Z0-9+.-]*://[^\s"<>]*`)
var errorSecret = regexp.MustCompile(`(?i)\b(password|passwd|pwd|secret|token|api[_-]?key|access[_-]?key|client[_-]?secret)\s*=\s*('[^']*'|"[^"]*"|[^\s&;]+)`)

// ErrorText keeps useful error classes and destination hosts while removing URL
// userinfo, paths, queries, fragments and credential assignments from diagnostic
// text. It never changes the error itself or its retry/classification behavior.
// Payload bytes and arbitrary upstream response bodies must not be logged.
func ErrorText(err error) string {
	if err == nil {
		return ""
	}
	message := err.Error()
	// net/http quotes URLs in its error text. Replace that exact representation
	// first, so even unusual credentials containing quotes cannot split the
	// fallback URL matcher and leave a credential suffix behind.
	pending := []error{err}
	for inspected := 0; len(pending) > 0; inspected++ {
		if inspected >= 128 {
			return "diagnostic error chain exceeds limit"
		}
		current := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if current == nil {
			continue
		}
		if transport, ok := current.(*url.Error); ok { //nolint:errorlint // Inspect each member of the bounded traversal; recursive errors.As can follow cycles.
			message = strings.ReplaceAll(message, strconv.Quote(transport.URL), strconv.Quote(errorOrigin(transport.URL)))
		}
		switch wrapped := current.(type) { //nolint:errorlint // Direct unwrapping keeps joined and cyclic error traversal bounded.
		case interface{ Unwrap() []error }:
			pending = append(pending, wrapped.Unwrap()...)
		case interface{ Unwrap() error }:
			pending = append(pending, wrapped.Unwrap())
		}
	}
	message = errorURL.ReplaceAllStringFunc(message, errorOrigin)
	message = errorSecret.ReplaceAllString(message, "$1=[redacted]")
	return strings.ReplaceAll(strings.ToValidUTF8(message, "�"), "\x00", "")
}

func errorOrigin(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "[redacted URL]"
	}
	return u.Scheme + "://" + u.Host
}
