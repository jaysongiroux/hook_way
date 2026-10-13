// Package tlsfiles builds a TLS client configuration from PEM file paths and
// options — the shape of every tls block in Hookway's configuration. Native
// destination adapters and service clients share certificate loading here.
package tlsfiles

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"path/filepath"
)

// Files is one tls block.
type Files struct {
	CACert             string
	ClientCert         string
	ClientKey          string
	ServerName         string
	InsecureSkipVerify bool
}

// Configured reports whether any option is set. When none is, the caller
// keeps its connection's own TLS defaults (for PostgreSQL, the url's sslmode).
func (f Files) Configured() bool {
	return f.CACert != "" || f.ClientCert != "" || f.ClientKey != "" || f.ServerName != "" || f.InsecureSkipVerify
}

// BuildOptions preserves path handling for adapters that historically passed
// CA paths directly to the filesystem. It is an internal compatibility option,
// not part of an authored TLS configuration.
type BuildOptions struct {
	PreserveCAPath bool
}

// Build assembles a *tls.Config. label names the block in errors, for
// example "postgres tls" or "core tls".
func (f Files) Build(label string, options ...BuildOptions) (*tls.Config, error) {
	cfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if f.ServerName != "" {
		cfg.ServerName = f.ServerName
	}
	if f.InsecureSkipVerify {
		cfg.InsecureSkipVerify = true //nolint:gosec // intentional, user-opted in
	}
	if f.CACert != "" {
		caPath := filepath.Clean(f.CACert)
		if len(options) > 0 && options[0].PreserveCAPath {
			// Cleaning symlink/../ca.pem can select a different trust file.
			caPath = f.CACert
		}
		pem, err := os.ReadFile(caPath) // #nosec G304 -- explicitly configured local certificate path
		if err != nil {
			return nil, fmt.Errorf("read %s ca_cert: %w", label, err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("%s ca_cert %q contained no valid certificates", label, f.CACert)
		}
		cfg.RootCAs = pool
	}
	if f.ClientCert != "" || f.ClientKey != "" {
		if f.ClientCert == "" || f.ClientKey == "" {
			return nil, fmt.Errorf("%s client_cert and client_key must both be set for mutual TLS", label)
		}
		cert, err := tls.LoadX509KeyPair(f.ClientCert, f.ClientKey)
		if err != nil {
			return nil, fmt.Errorf("load %s client cert/key: %w", label, err)
		}
		cfg.Certificates = []tls.Certificate{cert}
	}
	return cfg, nil
}
