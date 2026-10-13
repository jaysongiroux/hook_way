package logging

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// ParseLevel parses a case-insensitive log level name into a slog.Level. An
// empty string defaults to "info" so an omitted logging.level config field is
// valid. Supported values are "debug", "info", "warn", and "error"; anything
// else is an error naming the unsupported value.
func ParseLevel(level string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "", "info":
		return slog.LevelInfo, nil
	case "debug":
		return slog.LevelDebug, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("unsupported log level %q: use debug, info, warn, or error", level)
	}
}

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
	if !logger.Enabled(context.Background(), level) {
		return
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
		"headers", LogHeaders(attrs.Headers, attrs.HeaderAllowlist, attrs.HeaderDenylist),
	)
}

// LogHeaders copies headers for structured request logs. An empty allowlist
// logs no request headers (fail-closed). Explicitly allowed names are still
// filtered by the built-in sensitive-header heuristics and the configured
// denylist. Use FilterHeaders for proxy forwarding, which stays fail-open
// when its allowlist is empty.
func LogHeaders(headers http.Header, allowlist, denylist []string) http.Header {
	if len(allowlist) == 0 {
		return http.Header{}
	}
	return FilterHeaders(headers, allowlist, denylist)
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
	return strings.Contains(key, "secret") || strings.Contains(key, "token") || strings.Contains(key, "signature") ||
		strings.Contains(key, "key") || strings.Contains(key, "auth")
}
