package configapi

import (
	"encoding/json"
	"testing"
)

func TestValidate_ValidConfigMarshalsEmptyIssuesArray(t *testing.T) {
	resp := Validate([]byte(`server:
  addr: ":8080"
  max_body_bytes: 1048576
destinations:
  billing:
    type: "rabbitmq"
    rabbitmq:
      url: "amqp://guest:guest@localhost:5672/"
      exchange: "webhooks"
      exchange_type: "direct"
      durable: true
endpoints:
  test:
    enabled: true
    path: "/test"
    destinations:
      - destination: "billing"
        target: "test-target"
        create: true
    auth:
      type: "hmac_sha256"
      secret: "test-secret"
      signature_header: "X-Signature"
`))
	out, _ := json.Marshal(resp)
	if string(out) != `{"issues":[]}` {
		t.Fatalf("expected empty issues array, got %s", out)
	}
}

func TestValidate_InvalidConfigSetsErrorSeverity(t *testing.T) {
	resp := Validate([]byte("server:\n  max_body_bytes: 1048576\n"))
	if len(resp.Issues) == 0 {
		t.Fatal("expected at least one issue")
	}
	for _, iss := range resp.Issues {
		if iss.Severity != "error" {
			t.Errorf("expected severity error, got %q", iss.Severity)
		}
	}
}

func TestSchema_ReturnsValidJSON(t *testing.T) {
	data, err := Schema()
	if err != nil {
		t.Fatalf("Schema error: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("schema not valid JSON: %v", err)
	}
}
