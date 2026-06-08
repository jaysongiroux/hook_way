package logging

import (
	"bytes"
	"log/slog"
	"net/http"
	"strings"
	"testing"
)

func TestRedactHeadersUsesDenylistAndHeuristic(t *testing.T) {
	headers := http.Header{}
	headers.Set("Authorization", "Bearer secret")
	headers.Set("X-Custom-Secret", "secret")
	headers.Set("X-Partner-ID", "partner")

	redacted := RedactHeaders(headers, []string{"x-partner-id"})

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
		Message:        "request handled",
		Method:         http.MethodPost,
		Path:           "/webhooks/paddle/billing",
		Endpoint:       "paddle_billing",
		Queue:          "billing-events",
		Status:         202,
		RequestID:      "req_123",
		TraceID:        "trace_123",
		Headers:        headers,
		HeaderDenylist: []string{"x-api-key"},
	})

	got := buf.String()
	for _, want := range []string{`"request_id":"req_123"`, `"trace_id":"trace_123"`, `"status":202`} {
		if !strings.Contains(got, want) {
			t.Fatalf("log missing %s: %s", want, got)
		}
	}
	if strings.Contains(got, "secret-signature") {
		t.Fatalf("log leaked secret: %s", got)
	}
}
