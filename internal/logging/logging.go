package logging

import (
	"log/slog"
	"net/http"
	"strings"
	"time"
)

type RequestAttrs struct {
	Message        string
	Method         string
	Path           string
	Endpoint       string
	Queue          string
	Status         int
	Duration       time.Duration
	RequestID      string
	TraceID        string
	Headers        http.Header
	HeaderDenylist []string
}

func Request(logger *slog.Logger, attrs RequestAttrs) {
	if logger == nil {
		logger = slog.Default()
	}
	msg := attrs.Message
	if msg == "" {
		msg = "request handled"
	}
	logger.Info(msg,
		"method", attrs.Method,
		"path", attrs.Path,
		"endpoint", attrs.Endpoint,
		"queue", attrs.Queue,
		"status", attrs.Status,
		"duration", attrs.Duration.String(),
		"request_id", attrs.RequestID,
		"trace_id", attrs.TraceID,
		"headers", RedactHeaders(attrs.Headers, attrs.HeaderDenylist),
	)
}

func RedactHeaders(headers http.Header, denylist []string) http.Header {
	deny := map[string]struct{}{}
	for _, name := range denylist {
		deny[strings.ToLower(name)] = struct{}{}
	}
	redacted := http.Header{}
	for name, values := range headers {
		key := strings.ToLower(name)
		if _, ok := deny[key]; ok || SensitiveHeader(key) {
			redacted[key] = []string{"redacted"}
			continue
		}
		redacted[key] = append([]string(nil), values...)
	}
	return redacted
}

func SensitiveHeader(key string) bool {
	key = strings.ToLower(key)
	if key == "authorization" || key == "cookie" || key == "set-cookie" || key == "paddle-signature" {
		return true
	}
	return strings.Contains(key, "secret") || strings.Contains(key, "token") || strings.Contains(key, "signature")
}
