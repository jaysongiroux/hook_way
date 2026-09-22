package queue

import (
	"testing"

	"github.com/jaysongiroux/hook_way/internal/config"
)

func TestBuildPostgresTLSConfig_ServerNameAndInsecure(t *testing.T) {
	cfg, err := buildPostgresTLSConfig(config.PostgresTLSConfig{
		ServerName:         "db.internal",
		InsecureSkipVerify: true,
	})
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

func TestBuildPostgresTLSConfig_MissingCAFile(t *testing.T) {
	_, err := buildPostgresTLSConfig(config.PostgresTLSConfig{CACert: "/nonexistent/ca.pem"})
	if err == nil {
		t.Fatal("expected error reading missing ca_cert")
	}
}

func TestBuildPostgresTLSConfig_PartialClientCert(t *testing.T) {
	_, err := buildPostgresTLSConfig(config.PostgresTLSConfig{ClientCert: "only-cert.pem"})
	if err == nil {
		t.Fatal("expected error when client_key missing")
	}
}
