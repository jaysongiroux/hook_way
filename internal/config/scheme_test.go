package config

import (
	"strings"
	"testing"
)

func TestHMACSHA256HexSchemeValidWithoutTimestampTolerance(t *testing.T) {
	t.Setenv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/")
	t.Setenv("PADDLE_SECRET", "pdl_secret")
	body := replace(validYAML(), "      timestamp_tolerance: \"5s\"\n", "")
	body = replace(body,
		"signature_header: \"Paddle-Signature\"",
		"scheme: \"hmac_sha256_hex\"\n      signature_header: \"X-Hub-Signature-256\"")

	cfg, err := Load(writeConfig(t, body))
	if err != nil {
		t.Fatalf("prefixed-hex scheme without timestamp_tolerance should be valid: %v", err)
	}
	if cfg.Endpoints["paddle_billing"].Auth.Scheme != "hmac_sha256_hex" {
		t.Fatalf("scheme = %q", cfg.Endpoints["paddle_billing"].Auth.Scheme)
	}
}

func TestOmittedSchemeDefaultsToTSColon(t *testing.T) {
	t.Setenv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/")
	t.Setenv("PADDLE_SECRET", "pdl_secret")

	cfg, err := Load(writeConfig(t, validYAML()))
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.Endpoints["paddle_billing"].Auth.Scheme != "hmac_sha256_ts_colon" {
		t.Fatalf("default scheme = %q, want hmac_sha256_ts_colon", cfg.Endpoints["paddle_billing"].Auth.Scheme)
	}
}

func TestRabbitMQMutualTLSRequiresBothCertAndKey(t *testing.T) {
	t.Setenv("RABBITMQ_URL", "amqps://guest:guest@broker.example.com/")
	t.Setenv("PADDLE_SECRET", "pdl_secret")
	body := replace(validYAML(),
		"      declare_queues: true",
		"      declare_queues: true\n      tls:\n        client_cert: \"/etc/hookway/tls.crt\"")

	_, err := Load(writeConfig(t, body))
	if err == nil || !strings.Contains(err.Error(), "tls.client_cert") {
		t.Fatalf("expected mutual TLS pairing error, got %v", err)
	}
}

func TestRabbitMQTLSCABlockIsValid(t *testing.T) {
	t.Setenv("RABBITMQ_URL", "amqps://guest:guest@broker.example.com/")
	t.Setenv("PADDLE_SECRET", "pdl_secret")
	body := replace(validYAML(),
		"      declare_queues: true",
		"      declare_queues: true\n      tls:\n        ca_cert: \"/etc/hookway/ca.pem\"\n        server_name: \"broker.example.com\"")

	cfg, err := Load(writeConfig(t, body))
	if err != nil {
		t.Fatalf("ca-only tls block should be valid: %v", err)
	}
	if cfg.MessageQueues["billing"].RabbitMQ.TLS.CACert != "/etc/hookway/ca.pem" {
		t.Fatalf("ca_cert = %q", cfg.MessageQueues["billing"].RabbitMQ.TLS.CACert)
	}
}

func TestUnknownSchemeRejected(t *testing.T) {
	t.Setenv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/")
	t.Setenv("PADDLE_SECRET", "pdl_secret")
	body := replace(validYAML(),
		"signature_header: \"Paddle-Signature\"",
		"scheme: \"mystery\"\n      signature_header: \"Paddle-Signature\"")

	_, err := Load(writeConfig(t, body))
	if err == nil || !strings.Contains(err.Error(), "auth.scheme") {
		t.Fatalf("expected auth.scheme validation error, got %v", err)
	}
}
