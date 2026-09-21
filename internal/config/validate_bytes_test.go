package config

import "testing"

func TestValidateBytes_ValidConfigReturnsNoIssues(t *testing.T) {
	yaml := `
server:
  addr: ":8080"
  max_body_bytes: 1048576
destinations:
  billing:
    type: "rabbitmq"
    rabbitmq:
      url: "amqp://localhost:5672/"
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
`
	issues := NewService().ValidateBytes([]byte(yaml))
	if len(issues) != 0 {
		t.Fatalf("expected no issues, got %+v", issues)
	}
}

func TestValidateBytes_MissingAddrReturnsFieldIssue(t *testing.T) {
	yaml := `
server:
  max_body_bytes: 1048576
`
	issues := NewService().ValidateBytes([]byte(yaml))
	found := false
	for _, iss := range issues {
		if iss.Field == "server.addr" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a server.addr issue, got %+v", issues)
	}
}

func TestValidateBytes_MalformedYAMLReturnsSingleIssue(t *testing.T) {
	issues := NewService().ValidateBytes([]byte("server: : :\n  bad"))
	if len(issues) != 1 || issues[0].Field != "config" {
		t.Fatalf("expected one 'config' issue, got %+v", issues)
	}
}
