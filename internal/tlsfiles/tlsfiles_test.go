package tlsfiles

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfiguredReportsAnySetOption(t *testing.T) {
	if (Files{}).Configured() {
		t.Fatal("an empty tls block must leave TLS to the connection's own defaults")
	}
	for name, f := range map[string]Files{
		"ca_cert": {CACert: "ca.pem"}, "client_cert": {ClientCert: "c.pem"}, "client_key": {ClientKey: "k.pem"},
		"server_name": {ServerName: "db.internal"}, "insecure_skip_verify": {InsecureSkipVerify: true},
	} {
		if !f.Configured() {
			t.Errorf("%s set but Configured = false", name)
		}
	}
}

func TestBuildServerNameAndInsecure(t *testing.T) {
	cfg, err := Files{ServerName: "db.internal", InsecureSkipVerify: true}.Build("postgres tls")
	if err != nil || cfg.ServerName != "db.internal" || !cfg.InsecureSkipVerify {
		t.Fatalf("cfg = %+v, %v", cfg, err)
	}
}

func TestBuildErrorsNameTheBlock(t *testing.T) {
	for name, f := range map[string]Files{
		"missing ca":          {CACert: "/nonexistent/ca.pem"},
		"partial client cert": {ClientCert: "only-cert.pem"},
	} {
		_, err := f.Build("core tls")
		if err == nil || !strings.Contains(err.Error(), "core tls") {
			t.Errorf("%s: Build = %v, want an error naming the core tls block", name, err)
		}
	}
}

func TestBuildPreservesExistingAndAdapterCAPathSemantics(t *testing.T) {
	path := t.TempDir() + "/./missing-ca.pem"
	for _, tc := range []struct {
		name     string
		options  []BuildOptions
		wantPath string
	}{
		{"existing consumers clean", nil, filepath.Clean(path)},
		{"adapters keep exact path", []BuildOptions{{PreserveCAPath: true}}, path},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := (Files{CACert: path}).Build("client tls", tc.options...)
			var pathErr *os.PathError
			if !errors.As(err, &pathErr) || pathErr.Path != tc.wantPath {
				t.Fatalf("Build error = %v, want filesystem path %q", err, tc.wantPath)
			}
		})
	}
}
