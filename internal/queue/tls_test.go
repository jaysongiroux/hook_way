package queue

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jaysongiroux/hook_way/internal/config"
)

func TestBuildTLSConfigEmptyReturnsNil(t *testing.T) {
	cfg, err := buildTLSConfig(RabbitMQTLSConfig{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg != nil {
		t.Fatal("expected nil tls config when no options are set")
	}
}

func TestBuildTLSConfigLoadsCAAndClientCert(t *testing.T) {
	dir := t.TempDir()
	caPEM, certPEM, keyPEM := writeTestKeypair(t)
	caPath := filepath.Join(dir, "ca.pem")
	certPath := filepath.Join(dir, "client.crt")
	keyPath := filepath.Join(dir, "client.key")
	mustWrite(t, caPath, caPEM)
	mustWrite(t, certPath, certPEM)
	mustWrite(t, keyPath, keyPEM)

	cfg, err := buildTLSConfig(RabbitMQTLSConfig{
		CACert: caPath, ClientCert: certPath, ClientKey: keyPath, ServerName: "broker.internal",
	})
	if err != nil {
		t.Fatalf("buildTLSConfig error: %v", err)
	}
	if cfg.RootCAs == nil {
		t.Fatal("expected RootCAs to be set")
	}
	if len(cfg.Certificates) != 1 {
		t.Fatalf("expected 1 client certificate, got %d", len(cfg.Certificates))
	}
	if cfg.ServerName != "broker.internal" {
		t.Fatalf("server name = %q", cfg.ServerName)
	}
}

func TestBuildTLSConfigRejectsUnreadableCAAndHalfMTLS(t *testing.T) {
	if _, err := buildTLSConfig(RabbitMQTLSConfig{CACert: "/nope/missing.pem"}); err == nil {
		t.Fatal("expected error for unreadable ca_cert")
	}
	if _, err := buildTLSConfig(RabbitMQTLSConfig{ClientCert: "/only/cert.pem"}); err == nil {
		t.Fatal("expected error when client_key is missing")
	}
}

func TestBuildTLSConfigInsecureSkipVerify(t *testing.T) {
	cfg, err := buildTLSConfig(RabbitMQTLSConfig{InsecureSkipVerify: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg == nil || !cfg.InsecureSkipVerify {
		t.Fatal("expected InsecureSkipVerify to be set")
	}
}

func mustWrite(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// ---------------------------------------------------------------------------
// buildRedisTLSConfig tests
// ---------------------------------------------------------------------------

func TestBuildRedisTLSConfigCACert(t *testing.T) {
	dir := t.TempDir()
	caPEM, _, _ := writeTestKeypair(t)
	caPath := filepath.Join(dir, "redis-ca.pem")
	mustWrite(t, caPath, caPEM)

	cfg, err := buildRedisTLSConfig(config.RedisTLSConfig{
		Enabled: true,
		CACert:  caPath,
	})
	if err != nil {
		t.Fatalf("buildRedisTLSConfig error: %v", err)
	}
	if cfg.RootCAs == nil {
		t.Fatal("expected RootCAs to be non-nil")
	}
	if cfg.MinVersion != tls.VersionTLS12 {
		t.Errorf("MinVersion = %v, want TLS 1.2", cfg.MinVersion)
	}
}

func TestBuildRedisTLSConfigClientCert(t *testing.T) {
	dir := t.TempDir()
	_, certPEM, keyPEM := writeTestKeypair(t)
	certPath := filepath.Join(dir, "client.crt")
	keyPath := filepath.Join(dir, "client.key")
	mustWrite(t, certPath, certPEM)
	mustWrite(t, keyPath, keyPEM)

	cfg, err := buildRedisTLSConfig(config.RedisTLSConfig{
		Enabled:    true,
		ClientCert: certPath,
		ClientKey:  keyPath,
	})
	if err != nil {
		t.Fatalf("buildRedisTLSConfig error: %v", err)
	}
	if len(cfg.Certificates) != 1 {
		t.Fatalf("expected 1 client certificate, got %d", len(cfg.Certificates))
	}
}

func TestBuildRedisTLSConfigServerNameAndInsecure(t *testing.T) {
	cfg, err := buildRedisTLSConfig(config.RedisTLSConfig{
		Enabled:            true,
		ServerName:         "redis.internal",
		InsecureSkipVerify: true,
	})
	if err != nil {
		t.Fatalf("buildRedisTLSConfig error: %v", err)
	}
	if cfg.ServerName != "redis.internal" {
		t.Errorf("ServerName = %q, want %q", cfg.ServerName, "redis.internal")
	}
	if !cfg.InsecureSkipVerify {
		t.Fatal("expected InsecureSkipVerify to be true")
	}
}

func TestBuildRedisTLSConfigBadCACert(t *testing.T) {
	_, err := buildRedisTLSConfig(config.RedisTLSConfig{
		Enabled: true,
		CACert:  "/nope/no-such-ca.pem",
	})
	if err == nil {
		t.Fatal("expected error for unreadable ca_cert, got nil")
	}
}

func TestBuildRedisTLSConfigHalfMTLSErrors(t *testing.T) {
	// client_cert set without client_key → error.
	if _, err := buildRedisTLSConfig(config.RedisTLSConfig{
		Enabled:    true,
		ClientCert: "/some/cert.pem",
	}); err == nil {
		t.Fatal("expected error when client_key is missing")
	}
	// client_key set without client_cert → error.
	if _, err := buildRedisTLSConfig(config.RedisTLSConfig{
		Enabled:   true,
		ClientKey: "/some/key.pem",
	}); err == nil {
		t.Fatal("expected error when client_cert is missing")
	}
}

// writeTestKeypair returns a self-signed CA PEM plus a cert/key PEM pair usable
// as a client certificate, all freshly generated for the test.
func writeTestKeypair(t *testing.T) (caPEM, certPEM, keyPEM []byte) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "hookway-test"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	return certPEM, certPEM, keyPEM
}
