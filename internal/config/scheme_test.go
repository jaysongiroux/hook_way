package config

import (
	"strings"
	"testing"
)

func TestRabbitMQMutualTLSRequiresBothCertAndKey(t *testing.T) {
	t.Setenv("RABBITMQ_URL", "amqps://guest:guest@broker.example.com/")
	body := replace(validSharedYAML(),
		"      declare_queues: true",
		"      declare_queues: true\n      tls:\n        client_cert: \"/etc/hookway/tls.crt\"")

	_, err := loadSharedYAML(t, body)
	if err == nil || !strings.Contains(err.Error(), "tls.client_cert") {
		t.Fatalf("expected mutual TLS pairing error, got %v", err)
	}
}

func TestRabbitMQTLSCABlockIsValid(t *testing.T) {
	t.Setenv("RABBITMQ_URL", "amqps://guest:guest@broker.example.com/")
	body := replace(validSharedYAML(),
		"      declare_queues: true",
		"      declare_queues: true\n      tls:\n        ca_cert: \"/etc/hookway/ca.pem\"\n        server_name: \"broker.example.com\"")

	cfg, err := loadSharedYAML(t, body)
	if err != nil {
		t.Fatalf("ca-only tls block should be valid: %v", err)
	}
	if cfg.Destinations["billing"].RabbitMQ.TLS.CACert != "/etc/hookway/ca.pem" {
		t.Fatalf("ca_cert = %q", cfg.Destinations["billing"].RabbitMQ.TLS.CACert)
	}
}

// TestEndpointAuthSchemeValidation covers the per-endpoint auth scheme rules
// reached through validationIssues. The v1 `endpoints:` YAML loader is gone, so
// the endpoint is built directly.
func TestEndpointAuthSchemeValidation(t *testing.T) {
	mk := func(auth AuthConfig) Config {
		return endpointConfigFor(
			map[string]DestinationConfig{"billing": {Type: "rabbitmq", RabbitMQ: RabbitMQConfig{URL: "amqp://x", Exchange: "e", ExchangeType: "direct"}}},
			EndpointConfig{Enabled: true, Path: "/webhooks/paddle/billing", Auth: auth,
				Destinations: []EndpointDestinationConfig{{Destination: "billing", Target: "billing-events"}}})
	}
	base := AuthConfig{Type: "hmac_sha256", Secret: "pdl_secret", SignatureHeader: "Paddle-Signature"}

	// A prefixed-hex scheme carries no signed timestamp, so no tolerance is required.
	hexAuth := base
	hexAuth.Scheme = "hmac_sha256_hex"
	hexAuth.SignatureHeader = "X-Hub-Signature-256"
	for _, iss := range validationIssues(mk(hexAuth)) {
		if strings.Contains(iss.Field, "auth") {
			t.Fatalf("prefixed-hex scheme without timestamp_tolerance should be valid: %#v", iss)
		}
	}

	// An unknown scheme is rejected.
	unknown := base
	unknown.Scheme = "mystery"
	unknown.TimestampTolerance = 5 * 1e9
	if issues := validationIssues(mk(unknown)); !hasIssue(issues, "endpoints.e.auth.scheme") {
		t.Fatalf("expected auth.scheme validation error, got %#v", issues)
	}

	// A scheme that does not belong to the declared auth type is rejected.
	mismatched := base
	mismatched.Scheme = "exact"
	if issues := validationIssues(mk(mismatched)); !hasIssue(issues, "endpoints.e.auth.scheme") {
		t.Fatalf("expected exact-under-hmac_sha256 to be rejected, got %#v", issues)
	}
}
