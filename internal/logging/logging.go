package logging

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

type RequestAttrs struct {
	Message            string
	Method             string
	Path               string
	Endpoint           string
	DestinationQueueID string
	Queue              string
	Status             int
	Duration           time.Duration
	RequestID          string
	TraceID            string
	Headers            http.Header
	HeaderDenylist     []string
	HeaderAllowlist    []string
}

func Request(logger *slog.Logger, attrs RequestAttrs) {
	if logger == nil {
		logger = slog.Default()
	}
	msg := attrs.Message
	if msg == "" {
		msg = "request handled"
	}
	level := slog.LevelInfo
	if attrs.Status >= http.StatusInternalServerError {
		level = slog.LevelError
	} else if attrs.Status >= http.StatusBadRequest {
		level = slog.LevelWarn
	}
	logger.Log(context.Background(), level, msg,
		"method", attrs.Method,
		"path", attrs.Path,
		"endpoint", attrs.Endpoint,
		"destination_queue_id", attrs.DestinationQueueID,
		"queue", attrs.Queue,
		"status", attrs.Status,
		"duration", attrs.Duration.String(),
		"request_id", attrs.RequestID,
		"trace_id", attrs.TraceID,
		"headers", FilterHeaders(attrs.Headers, attrs.HeaderAllowlist, attrs.HeaderDenylist),
	)
}

// RedactHeaders redacts denylisted and known-sensitive headers, passing
// everything else through. It is a denylist (fail-open) filter.
func RedactHeaders(headers http.Header, denylist []string) http.Header {
	return FilterHeaders(headers, nil, denylist)
}

// FilterHeaders returns a safe copy of headers for logging and forwarding.
//
// When allowlist is non-empty it fails safe: only allowlisted headers are kept
// and everything else is dropped entirely. The denylist and built-in sensitive
// rules still apply on top, so an allowlisted-but-sensitive header is redacted
// rather than passed through. When allowlist is empty, behaviour is the legacy
// denylist (fail-open) filter.
func FilterHeaders(headers http.Header, allowlist, denylist []string) http.Header {
	allow := map[string]struct{}{}
	for _, name := range allowlist {
		allow[strings.ToLower(name)] = struct{}{}
	}
	deny := map[string]struct{}{}
	for _, name := range denylist {
		deny[strings.ToLower(name)] = struct{}{}
	}
	out := http.Header{}
	for name, values := range headers {
		key := strings.ToLower(name)
		if len(allow) > 0 {
			if _, ok := allow[key]; !ok {
				continue
			}
		}
		if _, ok := deny[key]; ok || SensitiveHeader(key) {
			out[key] = []string{"redacted"}
			continue
		}
		out[key] = append([]string(nil), values...)
	}
	return out
}

func SensitiveHeader(key string) bool {
	key = strings.ToLower(key)
	if key == "authorization" || key == "cookie" || key == "set-cookie" || key == "paddle-signature" {
		return true
	}
	return strings.Contains(key, "secret") || strings.Contains(key, "token") || strings.Contains(key, "signature")
}
