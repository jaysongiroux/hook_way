package webhook

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestBuildEnvelopeParsesJSONAndRedactsHeaders(t *testing.T) {
	req, _ := http.NewRequest(http.MethodPost, "/webhooks/paddle/billing?x=1", nil)
	req.RemoteAddr = "203.0.113.42:1234"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Paddle-Signature", "secret")
	req.Header.Set("X-Api-Token", "secret")

	env, err := BuildEnvelope(req, "paddle_billing", "billing-events", []byte(`{"event_type":"subscription.created"}`), time.Unix(0, 0), "203.0.113.42")
	if err != nil {
		t.Fatalf("BuildEnvelope returned error: %v", err)
	}
	if env.ID == "" || env.RawBodyBase64 != nil {
		t.Fatalf("bad envelope: %#v", env)
	}
	if env.Headers["paddle-signature"][0] != "redacted" || env.Headers["x-api-token"][0] != "redacted" {
		t.Fatalf("headers not redacted: %#v", env.Headers)
	}
	if env.Body == nil {
		t.Fatal("expected parsed JSON body")
	}
}

func TestBuildEnvelopeIncludesPublishedContractFields(t *testing.T) {
	req, _ := http.NewRequest(http.MethodPost, "/webhooks/paddle/billing?x=1", nil)
	req.Header.Set("X-Request-ID", "req_123")
	req.Header.Set("Traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-00")
	req.Header.Set("X-Partner-Secret", "secret")

	env, err := BuildEnvelopeWithOptions(req, EnvelopeOptions{
		Endpoint:           "paddle_billing",
		DestinationQueueID: "billing",
		Queue:              "billing-events",
		RawBody:            []byte(`{"event_type":"subscription.created"}`),
		ReceivedAt:         time.Unix(0, 0),
		SourceIP:           "203.0.113.42",
		RequestID:          "req_123",
		TraceID:            "4bf92f3577b34da6a3ce929d0e0e4736",
		HeaderDenylist:     []string{"x-partner-secret"},
	})
	if err != nil {
		t.Fatalf("BuildEnvelopeWithOptions returned error: %v", err)
	}
	got, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"schema_version":"1.0"`,
		`"request_id":"req_123"`,
		`"trace_id":"4bf92f3577b34da6a3ce929d0e0e4736"`,
		`"destination_queue_id":"billing"`,
		`"x-partner-secret":["redacted"]`,
	} {
		if !strings.Contains(string(got), want) {
			t.Fatalf("envelope JSON missing %s: %s", want, got)
		}
	}
}

func TestBuildEnvelopeEncodesNonJSONBody(t *testing.T) {
	req, _ := http.NewRequest(http.MethodPost, "/webhooks/paddle/billing", nil)
	raw := []byte("not-json")
	env, err := BuildEnvelope(req, "paddle_billing", "billing-events", raw, time.Unix(0, 0), "127.0.0.1")
	if err != nil {
		t.Fatalf("BuildEnvelope returned error: %v", err)
	}
	if env.Body != nil {
		t.Fatal("expected nil body")
	}
	if env.RawBodyBase64 == nil || *env.RawBodyBase64 != base64.StdEncoding.EncodeToString(raw) {
		t.Fatalf("raw body not encoded: %#v", env.RawBodyBase64)
	}
}
