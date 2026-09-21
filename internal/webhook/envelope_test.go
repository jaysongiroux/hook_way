package webhook

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestBuildEnvelopeParsesJSONAndRedactsHeaders(t *testing.T) {
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, "/webhooks/paddle/billing?x=1", nil)
	req.RemoteAddr = "203.0.113.42:1234"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Paddle-Signature", "secret")
	req.Header.Set("X-Api-Token", "secret")
	req.Header.Set("X-Api-Key", "secret")

	env, err := BuildEnvelope(req, "paddle_billing", "billing-events", []byte(`{"event_type":"subscription.created"}`), time.Unix(0, 0), "203.0.113.42")
	if err != nil {
		t.Fatalf("BuildEnvelope returned error: %v", err)
	}
	if env.ID == "" || env.RawBodyBase64 != nil {
		t.Fatalf("bad envelope: %#v", env)
	}
	paddle := "paddle-signature"
	token := "x-api-token"
	apiKey := "x-api-key"
	if env.Headers[paddle][0] != "redacted" || env.Headers[token][0] != "redacted" || env.Headers[apiKey][0] != "redacted" {
		t.Fatalf("headers not redacted: %#v", env.Headers)
	}
	if env.Body == nil {
		t.Fatal("expected parsed JSON body")
	}
}

func TestBuildEnvelopeIncludesPublishedContractFields(t *testing.T) {
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, "/webhooks/paddle/billing?x=1", nil)
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

// TestBuildEnvelopeCarriesTraceparent covers C4: EnvelopeOptions.Traceparent
// (computed by the httpapi layer) must land verbatim on the envelope and be
// serialized under the additive "traceparent" field, omitted when empty so
// schema_version stays 1.0 for callers that never set it.
func TestBuildEnvelopeCarriesTraceparent(t *testing.T) {
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, "/webhooks/paddle/billing", nil)
	want := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"

	env, err := BuildEnvelopeWithOptions(req, EnvelopeOptions{
		Endpoint: "paddle_billing", Queue: "billing-events",
		RawBody: []byte(`{}`), ReceivedAt: time.Unix(0, 0),
		TraceID: "4bf92f3577b34da6a3ce929d0e0e4736", Traceparent: want,
	})
	if err != nil {
		t.Fatalf("BuildEnvelopeWithOptions returned error: %v", err)
	}
	if env.Traceparent != want {
		t.Fatalf("env.Traceparent = %q, want %q", env.Traceparent, want)
	}
	got, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), `"traceparent":"`+want+`"`) {
		t.Fatalf("envelope JSON missing traceparent: %s", got)
	}

	// Omitted when unset (additive field, schema_version stays 1.0).
	envNoTP, err := BuildEnvelopeWithOptions(req, EnvelopeOptions{
		Endpoint: "paddle_billing", Queue: "billing-events",
		RawBody: []byte(`{}`), ReceivedAt: time.Unix(0, 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	gotNoTP, _ := json.Marshal(envNoTP)
	if strings.Contains(string(gotNoTP), "traceparent") {
		t.Fatalf("expected traceparent omitted when unset: %s", gotNoTP)
	}
}

func TestBuildEnvelopeEncodesNonJSONBody(t *testing.T) {
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, "/webhooks/paddle/billing", nil)
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

func TestEnvelopeCarriesRawBodyAndOrigHeadersInMemoryOnly(t *testing.T) {
	body := []byte(`{"a":1}`)
	req := httptest.NewRequestWithContext(context.Background(), "POST", "/x", bytes.NewReader(body))
	req.Header.Set("X-Vendor-Signature", "sig123")
	env, err := BuildEnvelopeWithOptions(req, EnvelopeOptions{Endpoint: "e", Queue: "orders", RawBody: body, ReceivedAt: time.Now()})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if string(env.RawBody) != string(body) {
		t.Fatalf("RawBody not retained: %q", env.RawBody)
	}
	if env.OrigHeaders.Get("X-Vendor-Signature") != "sig123" {
		t.Fatalf("OrigHeaders not retained (unredacted): %v", env.OrigHeaders)
	}
	// json.Marshal MUST NOT include raw body / orig headers
	j, _ := json.Marshal(env)
	if bytes.Contains(j, []byte("X-Vendor-Signature")) || bytes.Contains(j, []byte("raw_body")) && bytes.Contains(j, []byte("YQ==")) {
		t.Fatalf("in-memory fields leaked into JSON: %s", j)
	}
	if bytes.Contains(j, []byte("OrigHeaders")) || bytes.Contains(j, []byte("RawBody")) {
		t.Fatalf("json:\"-\" not honored: %s", j)
	}
}
