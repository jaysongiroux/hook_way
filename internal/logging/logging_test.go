package logging

import (
	"bytes"
	"log/slog"
	"net/http"
	"strings"
	"testing"
)

func TestFilterHeadersUsesDenylistAndHeuristic(t *testing.T) {
	headers := http.Header{}
	headers.Set("Authorization", "Bearer secret")
	headers.Set("X-Custom-Secret", "secret")
	headers.Set("X-Partner-ID", "partner")

	redacted := FilterHeaders(headers, nil, []string{"x-partner-id"})

	for _, key := range []string{"authorization", "x-custom-secret", "x-partner-id"} {
		if got := redacted[key]; len(got) != 1 || got[0] != "redacted" {
			t.Fatalf("%s not redacted: %#v", key, redacted)
		}
	}
}

func TestStructuredRequestLogDoesNotLeakSecrets(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	headers := http.Header{}
	headers.Set("Paddle-Signature", "secret-signature")
	headers.Set("X-Request-ID", "req_123")

	Request(logger, RequestAttrs{
		Message:            "request handled",
		Method:             http.MethodPost,
		Path:               "/webhooks/paddle/billing",
		Endpoint:           "paddle_billing",
		DestinationQueueID: "billing",
		Queue:              "billing-events",
		Status:             202,
		RequestID:          "req_123",
		TraceID:            "trace_123",
		Headers:            headers,
		HeaderDenylist:     []string{"x-api-key"},
	})

	got := buf.String()
	for _, want := range []string{`"request_id":"req_123"`, `"trace_id":"trace_123"`, `"destination_queue_id":"billing"`, `"status":202`} {
		if !strings.Contains(got, want) {
			t.Fatalf("log missing %s: %s", want, got)
		}
	}
	if strings.Contains(got, "secret-signature") {
		t.Fatalf("log leaked secret: %s", got)
	}
}

func TestSensitiveHeaderRedactsKeyAndAuthSubstrings(t *testing.T) {
	headers := http.Header{}
	headers.Set("X-Api-Key", "topsecret")
	headers.Set("X-Auth-User", "alice")
	headers.Set("Accept-Language", "en-US")
	headers.Set("Content-Type", "application/json")
	headers.Set("X-GitHub-Delivery", "delivery-123")

	redacted := FilterHeaders(headers, nil, nil)

	for _, key := range []string{"x-api-key", "x-auth-user"} {
		if got := redacted[key]; len(got) != 1 || got[0] != "redacted" {
			t.Fatalf("%s not redacted: %#v", key, redacted)
		}
	}
	for _, key := range []string{"accept-language", "content-type", "x-github-delivery"} {
		if got := redacted[key]; len(got) == 0 || got[0] == "redacted" {
			t.Fatalf("benign header %s unexpectedly redacted: %#v", key, redacted)
		}
	}
}

func TestParseLevel(t *testing.T) {
	for _, tt := range []struct {
		name    string
		input   string
		want    slog.Level
		wantErr bool
	}{
		{name: "empty defaults to info", input: "", want: slog.LevelInfo},
		{name: "debug", input: "debug", want: slog.LevelDebug},
		{name: "info", input: "info", want: slog.LevelInfo},
		{name: "warn", input: "warn", want: slog.LevelWarn},
		{name: "error", input: "error", want: slog.LevelError},
		{name: "case insensitive", input: "DEBUG", want: slog.LevelDebug},
		{name: "unsupported value", input: "trace", wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseLevel(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseLevel(%q) error = nil, want error", tt.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseLevel(%q) unexpected error: %v", tt.input, err)
			}
			if got != tt.want {
				t.Fatalf("ParseLevel(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestLevelVarGatesDebugOutput(t *testing.T) {
	var buf bytes.Buffer
	levelVar := new(slog.LevelVar)
	levelVar.Set(slog.LevelInfo)
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: levelVar}))

	logger.Debug("debug line should be suppressed")
	if strings.Contains(buf.String(), "debug line") {
		t.Fatalf("debug line leaked at info level: %s", buf.String())
	}

	buf.Reset()
	levelVar.Set(slog.LevelDebug)
	logger.Debug("debug line should be emitted")
	if !strings.Contains(buf.String(), "debug line") {
		t.Fatalf("debug line missing at debug level: %s", buf.String())
	}
}

func TestLogHeadersEmptyAllowlistLogsNoHeaders(t *testing.T) {
	headers := http.Header{}
	headers.Set("X-Request-ID", "req_123")
	headers.Set("Content-Type", "application/json")
	if got := LogHeaders(headers, nil, nil); len(got) != 0 {
		t.Fatalf("empty logging allowlist = %#v, want no headers", got)
	}
	forwarded := FilterHeaders(headers, nil, nil)
	reqID, contentType := "x-request-id", "content-type"
	if len(forwarded[reqID]) != 1 || forwarded[reqID][0] != "req_123" ||
		len(forwarded[contentType]) != 1 || forwarded[contentType][0] != "application/json" {
		t.Fatalf("empty forwarding allowlist dropped headers: %#v", forwarded)
	}
}

func TestLogHeadersAllowlistStillRedactsSensitive(t *testing.T) {
	headers := http.Header{}
	headers.Set("Authorization", "Bearer secret")
	headers.Set("X-Request-ID", "req_123")
	got := LogHeaders(headers, []string{"Authorization", "X-Request-ID"}, nil)
	auth, reqID := "authorization", "x-request-id"
	if len(got[auth]) != 1 || got[auth][0] != "redacted" {
		t.Fatalf("allowlisted sensitive header = %#v", got)
	}
	if len(got[reqID]) != 1 || got[reqID][0] != "req_123" {
		t.Fatalf("allowlisted header missing: %#v", got)
	}
}

func TestRequestLogLevelFollowsHTTPStatus(t *testing.T) {
	for _, tt := range []struct {
		name   string
		status int
		level  string
	}{
		{name: "success", status: http.StatusAccepted, level: `"level":"INFO"`},
		{name: "client error", status: http.StatusUnauthorized, level: `"level":"WARN"`},
		{name: "server error", status: http.StatusServiceUnavailable, level: `"level":"ERROR"`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&buf, nil))

			Request(logger, RequestAttrs{Status: tt.status})

			if got := buf.String(); !strings.Contains(got, tt.level) {
				t.Fatalf("log level missing %s: %s", tt.level, got)
			}
		})
	}
}
