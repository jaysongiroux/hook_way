package pgtls

import (
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/jaysongiroux/hook_way/internal/tlsfiles"
)

// With a tls block configured, TLS is mandatory: pgx's default
// sslmode=prefer adds a plaintext fallback per host, and a failed
// verification (for example a man in the middle) would silently use it.
func TestApplyMakesTLSMandatory(t *testing.T) {
	cfg, err := pgconn.ParseConfig("postgres://replay@a.internal:5432,b.internal:5433/replay")
	if err != nil {
		t.Fatal(err)
	}
	plaintext := 0
	for _, fb := range cfg.Fallbacks {
		if fb.TLSConfig == nil {
			plaintext++
		}
	}
	if plaintext == 0 {
		t.Fatal("fixture: expected pgx's sslmode=prefer plaintext fallbacks")
	}
	if err := Apply(cfg, tlsfiles.Files{CACert: ""}, "postgres tls"); err != nil || cfg.Fallbacks == nil {
		t.Fatalf("an empty block must leave the url's TLS alone: %v", err)
	}
	if err := Apply(cfg, tlsfiles.Files{InsecureSkipVerify: true}, "postgres tls"); err != nil {
		t.Fatal(err)
	}
	if cfg.TLSConfig == nil || cfg.TLSConfig.ServerName != "a.internal" {
		t.Fatalf("primary TLS = %+v, want TLS verified against a.internal", cfg.TLSConfig)
	}
	hosts := map[string]bool{}
	for _, fb := range cfg.Fallbacks {
		if fb.TLSConfig == nil {
			t.Fatalf("plaintext fallback to %s:%d survived", fb.Host, fb.Port)
		}
		if fb.TLSConfig.ServerName != fb.Host {
			t.Fatalf("fallback %s verifies against %q", fb.Host, fb.TLSConfig.ServerName)
		}
		hosts[fb.Host] = true
	}
	if !hosts["b.internal"] {
		t.Fatalf("the second host's TLS fallback was dropped: %v", hosts)
	}
}

func TestApplyKeepsAnExplicitServerName(t *testing.T) {
	cfg, err := pgconn.ParseConfig("postgres://replay@10.0.0.5/replay")
	if err != nil {
		t.Fatal(err)
	}
	if err := Apply(cfg, tlsfiles.Files{ServerName: "db.internal"}, "postgres tls"); err != nil {
		t.Fatal(err)
	}
	if cfg.TLSConfig.ServerName != "db.internal" {
		t.Fatalf("ServerName = %q", cfg.TLSConfig.ServerName)
	}
}

func TestApplyReportsBuildErrors(t *testing.T) {
	cfg, err := pgconn.ParseConfig("postgres://replay@db.internal/replay")
	if err != nil {
		t.Fatal(err)
	}
	if err := Apply(cfg, tlsfiles.Files{CACert: "/nonexistent/ca.pem"}, "database tls"); err == nil || !strings.Contains(err.Error(), "database tls") {
		t.Fatalf("Apply = %v, want an error naming the database tls block", err)
	}
}
