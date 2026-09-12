package queue

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
)

// tlsMaterial is the certificate and verification settings shared by every
// TLS-capable destination type.
type tlsMaterial struct {
	CACert             string
	ClientCert         string
	ClientKey          string
	ServerName         string
	InsecureSkipVerify bool
}

// newTLSConfig assembles a *tls.Config from m. label names the destination
// type in error messages.
func newTLSConfig(label string, m tlsMaterial) (*tls.Config, error) {
	cfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if m.ServerName != "" {
		cfg.ServerName = m.ServerName
	}
	if m.InsecureSkipVerify {
		cfg.InsecureSkipVerify = true //nolint:gosec // intentional, user-opted in
	}
	if m.CACert != "" {
		pem, err := os.ReadFile(m.CACert)
		if err != nil {
			return nil, fmt.Errorf("read %s tls ca_cert: %w", label, err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("%s tls ca_cert %q contained no valid certificates", label, m.CACert)
		}
		cfg.RootCAs = pool
	}
	if m.ClientCert != "" || m.ClientKey != "" {
		if m.ClientCert == "" || m.ClientKey == "" {
			return nil, errors.New(label + " tls client_cert and client_key must both be set for mutual TLS")
		}
		cert, err := tls.LoadX509KeyPair(m.ClientCert, m.ClientKey)
		if err != nil {
			return nil, fmt.Errorf("load %s tls client cert/key: %w", label, err)
		}
		cfg.Certificates = []tls.Certificate{cert}
	}
	return cfg, nil
}
