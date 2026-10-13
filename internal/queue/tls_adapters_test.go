package queue

import (
	"crypto/tls"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jaysongiroux/hook_way/internal/config"
)

func TestTLSAdaptersShareCertificateLoadingAndLabels(t *testing.T) {
	ca, cert, key := writeTestKeypair(t)
	dir := t.TempDir()
	caPath, certPath, keyPath := filepath.Join(dir, "ca.pem"), filepath.Join(dir, "client.pem"), filepath.Join(dir, "client.key")
	mustWrite(t, caPath, ca)
	mustWrite(t, certPath, cert)
	mustWrite(t, keyPath, key)
	invalidCA := filepath.Join(dir, "invalid.pem")
	mustWrite(t, invalidCA, []byte("not a certificate"))
	for label, build := range tlsAdapterBuilders() {
		t.Run(label, func(t *testing.T) {
			cfg, err := build(tlsMaterial{CACert: caPath, ClientCert: certPath, ClientKey: keyPath, ServerName: "service.internal", InsecureSkipVerify: true})
			if err != nil {
				t.Fatal(err)
			}
			if cfg.MinVersion != tls.VersionTLS12 || cfg.RootCAs == nil || len(cfg.Certificates) != 1 || cfg.ServerName != "service.internal" || !cfg.InsecureSkipVerify {
				t.Fatalf("loaded TLS = %+v", cfg)
			}
			for name, tc := range map[string]struct {
				material tlsMaterial
				want     string
			}{
				"missing CA":       {tlsMaterial{CACert: "/missing/ca.pem"}, "read " + label + " tls ca_cert:"},
				"invalid CA":       {tlsMaterial{CACert: invalidCA}, label + " tls ca_cert "},
				"cert without key": {tlsMaterial{ClientCert: certPath}, label + " tls client_cert and client_key must both be set for mutual TLS"},
				"key without cert": {tlsMaterial{ClientKey: keyPath}, label + " tls client_cert and client_key must both be set for mutual TLS"},
				"invalid pair":     {tlsMaterial{ClientCert: invalidCA, ClientKey: invalidCA}, "load " + label + " tls client cert/key:"},
			} {
				t.Run(name, func(t *testing.T) {
					if _, err := build(tc.material); err == nil || !strings.Contains(err.Error(), tc.want) {
						t.Fatalf("error = %v, want %q", err, tc.want)
					}
				})
			}
		})
	}
}

// Each adapter keeps its own TLS activation rules; these builders exercise
// certificate material and labels after that boundary.
func tlsAdapterBuilders() map[string]func(tlsMaterial) (*tls.Config, error) {
	return map[string]func(tlsMaterial) (*tls.Config, error){
		"kafka":    func(m tlsMaterial) (*tls.Config, error) { return newTLSConfig("kafka", m) },
		"temporal": func(m tlsMaterial) (*tls.Config, error) { return newTLSConfig("temporal", m) },
		"rabbitmq": func(m tlsMaterial) (*tls.Config, error) { return buildTLSConfig(RabbitMQTLSConfig(m)) },
		"mongodb":  func(m tlsMaterial) (*tls.Config, error) { return buildMongoTLSConfig(config.MongoTLSConfig(m)) },
		"webhook":  func(m tlsMaterial) (*tls.Config, error) { return buildWebhookTLSConfig(config.WebhookTLSConfig(m)) },
		"redis":    func(m tlsMaterial) (*tls.Config, error) { return redisTLSConfigForTest(redisTLSMaterial(m)) },
	}
}

func redisTLSMaterial(m tlsMaterial) config.RedisTLSConfig {
	return config.RedisTLSConfig{Enabled: true, CACert: m.CACert, ClientCert: m.ClientCert, ClientKey: m.ClientKey, ServerName: m.ServerName, InsecureSkipVerify: m.InsecureSkipVerify}
}

func TestTLSAdaptersPreserveSymlinkParentCAPath(t *testing.T) {
	// link/../ca.pem resolves relative to the symlink target, not link's parent.
	// Queue adapters historically used the former trust file; preserve it.
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.MkdirAll(filepath.Join(target, "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	ca, _, _ := writeTestKeypair(t)
	mustWrite(t, filepath.Join(target, "ca.pem"), ca)
	mustWrite(t, filepath.Join(dir, "ca.pem"), []byte("incorrect trust file selected by Clean"))
	if err := os.Symlink(filepath.Join(target, "nested"), filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	caPath := dir + string(os.PathSeparator) + "link/../ca.pem"
	for label, build := range tlsAdapterBuilders() {
		t.Run(label, func(t *testing.T) {
			cfg, err := build(tlsMaterial{CACert: caPath})
			if err != nil || cfg.RootCAs == nil {
				t.Fatalf("raw CA path was not preserved: %+v, %v", cfg, err)
			}
		})
	}
}

func TestTLSAdapterGatesKeepTransportDefaults(t *testing.T) {
	if mongoTLSSet(config.MongoTLSConfig{}) || webhookTLSSet(config.WebhookTLSConfig{}) {
		t.Fatal("empty blocks must leave Mongo URI and webhook transport TLS defaults intact")
	}
	for _, m := range []tlsMaterial{{CACert: "ca"}, {ClientCert: "cert"}, {ClientKey: "key"}, {ServerName: "service.internal"}, {InsecureSkipVerify: true}} {
		if !mongoTLSSet(config.MongoTLSConfig(m)) || !webhookTLSSet(config.WebhookTLSConfig(m)) {
			t.Fatalf("set TLS material not detected: %+v", m)
		}
	}
	client, err := newWebhookHTTPClient(config.WebhookConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(client.Transport.(*http.Transport).TLSClientConfig, http.DefaultTransport.(*http.Transport).Clone().TLSClientConfig) {
		t.Fatal("empty webhook TLS overrides cloned transport defaults")
	}
}
