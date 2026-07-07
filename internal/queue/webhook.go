package queue

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/jasongiroux/hookway/internal/config"
)

type WebhookTarget struct{ Name string }

// WebhookPublisher enqueues deliveries onto the shared engine. targets is
// written once in the constructor; read-only in Publish (safe for fan-out).
type WebhookPublisher struct {
	engine   *webhookDeliveryEngine
	destID   string
	delivery map[string]*webhookDelivery // per target
	metrics  WebhookMetrics              // may be nil
	logger   *slog.Logger
}

func NewWebhookPublisher(engine *webhookDeliveryEngine, cfg config.WebhookConfig, destID string, targets []WebhookTarget, m WebhookMetrics, logger *slog.Logger) (*WebhookPublisher, error) {
	if logger == nil {
		logger = slog.Default()
	}
	client, err := newWebhookHTTPClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("webhook %q: %w", destID, err)
	}
	mode := "envelope"
	if cfg.Auth.Type == "proxy" {
		mode = "proxy"
	}
	// Apply retry defaults: unset (zero) config values become the documented defaults
	// so that an empty retry block still retries. Do NOT change the config layer which
	// intentionally preserves 0 as "unset".
	retryMaxAttempts := cfg.Retry.MaxAttempts
	if retryMaxAttempts <= 0 {
		retryMaxAttempts = 3
	}
	retryInitialBackoff := cfg.Retry.InitialBackoff
	if retryInitialBackoff <= 0 {
		retryInitialBackoff = 500 * time.Millisecond
	}
	retryMaxBackoff := cfg.Retry.MaxBackoff
	if retryMaxBackoff <= 0 {
		retryMaxBackoff = 30 * time.Second
	}

	deliveries := make(map[string]*webhookDelivery, len(targets))
	for _, t := range targets {
		deliveries[t.Name] = &webhookDelivery{
			destID: destID, target: t.Name, url: joinWebhookURL(cfg.URL, t.Name), mode: mode,
			client: client,
			retry: RetryPolicy{
				MaxAttempts:    retryMaxAttempts,
				InitialBackoff: retryInitialBackoff,
				MaxBackoff:     retryMaxBackoff,
			},
			auth: webhookAuth{
				Type:          cfg.Auth.Type,
				Header:        cfg.Auth.Header,
				Value:         cfg.Auth.Value,
				Secret:        cfg.Auth.Secret,
				Scheme:        cfg.Auth.Scheme,
				EventIDHeader: cfg.Auth.EventIDHeader,
			},
			logger: logger,
		}
	}
	return &WebhookPublisher{engine: engine, destID: destID, delivery: deliveries, metrics: m, logger: logger}, nil
}

func (p *WebhookPublisher) Publish(ctx context.Context, destinationQueueID, target string, msg Message) error {
	d, ok := p.delivery[target]
	if !ok {
		return fmt.Errorf("webhook %q: target %q is not configured", p.destID, target)
	}
	if err := p.engine.Enqueue(webhookJob{delivery: d, msg: msg}); err != nil {
		if p.metrics != nil {
			p.metrics.WebhookDropped(p.destID, target)
		}
		return fmt.Errorf("webhook %q: enqueue: %w", p.destID, err)
	}
	return nil
}

// Ready is a no-op: the target is external; do not probe it.
func (p *WebhookPublisher) Ready(ctx context.Context) error { return nil }

// Close drains the shared engine (idempotent; safe if multiple webhook
// publishers share one engine).
func (p *WebhookPublisher) Close() error { return p.engine.Close() }

func newWebhookHTTPClient(cfg config.WebhookConfig) (*http.Client, error) {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if webhookTLSSet(cfg.TLS) {
		tlsCfg, err := buildWebhookTLSConfig(cfg.TLS)
		if err != nil {
			return nil, err
		}
		transport.TLSClientConfig = tlsCfg
	}
	return &http.Client{Timeout: timeout, Transport: transport}, nil
}

func webhookTLSSet(c config.WebhookTLSConfig) bool {
	return c.CACert != "" || c.ClientCert != "" || c.ClientKey != "" || c.ServerName != "" || c.InsecureSkipVerify
}

// buildWebhookTLSConfig mirrors buildTLSConfig (rabbitmq) / buildPostgresTLSConfig.
func buildWebhookTLSConfig(c config.WebhookTLSConfig) (*tls.Config, error) {
	cfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if c.ServerName != "" {
		cfg.ServerName = c.ServerName
	}
	if c.InsecureSkipVerify {
		cfg.InsecureSkipVerify = true //nolint:gosec // intentional, user-opted in
	}
	if c.CACert != "" {
		pem, err := os.ReadFile(c.CACert)
		if err != nil {
			return nil, fmt.Errorf("read webhook tls ca_cert: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("webhook tls ca_cert %q contained no valid certificates", c.CACert)
		}
		cfg.RootCAs = pool
	}
	if c.ClientCert != "" || c.ClientKey != "" {
		if c.ClientCert == "" || c.ClientKey == "" {
			return nil, errors.New("webhook tls client_cert and client_key must both be set for mutual TLS")
		}
		cert, err := tls.LoadX509KeyPair(c.ClientCert, c.ClientKey)
		if err != nil {
			return nil, fmt.Errorf("load webhook tls client cert/key: %w", err)
		}
		cfg.Certificates = []tls.Certificate{cert}
	}
	return cfg, nil
}

// joinWebhookURL appends target as a path segment to base (single slash join).
func joinWebhookURL(base, target string) string {
	if target == "" {
		return base
	}
	return strings.TrimRight(base, "/") + "/" + strings.TrimLeft(target, "/")
}
