package queue

import (
	"testing"

	"github.com/jaysongiroux/hook_way/internal/config"
)

func TestBuildMongoTLSConfig_ServerNameAndInsecure(t *testing.T) {
	cfg, err := buildMongoTLSConfig(config.MongoTLSConfig{ServerName: "db.internal", InsecureSkipVerify: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.ServerName != "db.internal" {
		t.Fatalf("ServerName = %q", cfg.ServerName)
	}
	if !cfg.InsecureSkipVerify {
		t.Fatal("expected InsecureSkipVerify true")
	}
}

func TestBuildMongoTLSConfig_MissingCAFile(t *testing.T) {
	if _, err := buildMongoTLSConfig(config.MongoTLSConfig{CACert: "/nonexistent/ca.pem"}); err == nil {
		t.Fatal("expected error reading missing ca_cert")
	}
}

func TestBuildMongoTLSConfig_PartialClientCert(t *testing.T) {
	if _, err := buildMongoTLSConfig(config.MongoTLSConfig{ClientCert: "only-cert.pem"}); err == nil {
		t.Fatal("expected error when client_key missing")
	}
}
