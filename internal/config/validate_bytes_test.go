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
`
	issues := sharedIssues(yaml)
	if len(issues) != 0 {
		t.Fatalf("expected no issues, got %+v", issues)
	}
}

func TestValidateBytes_MissingAddrReturnsFieldIssue(t *testing.T) {
	yaml := `
server:
  max_body_bytes: 1048576
`
	issues := sharedIssues(yaml)
	if !hasIssue(issues, "server.addr") {
		t.Fatalf("expected a server.addr issue, got %+v", issues)
	}
}

func TestValidateBytes_MalformedYAMLReturnsSingleIssue(t *testing.T) {
	_, issues := DecodeSharedYAML([]byte("server: : :\n  bad"), "config")
	if len(issues) != 1 || issues[0].Field != "config" {
		t.Fatalf("expected one 'config' issue, got %+v", issues)
	}
}

func TestValidateBytes_NonMappingDocumentIsRejected(t *testing.T) {
	_, issues := DecodeSharedYAML([]byte("- just\n- a list\n"), "config")
	if len(issues) != 1 || issues[0].Field != "config" {
		t.Fatalf("expected one 'config' issue for a non-mapping document, got %+v", issues)
	}
}
