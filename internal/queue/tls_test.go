package queue

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
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
