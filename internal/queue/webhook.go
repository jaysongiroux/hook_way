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
	"github.com/jasongiroux/hookway/internal/vars"
)

type WebhookTarget struct {
	Name string
	// Template is set for dynamic routes (target contains {variable}
	// placeholders); rendered targets the matcher accepts are delivered to
	// base url + "/" + rendered target.
	Template vars.Matcher
}

// WebhookPublisherOptions carries the cross-cutting dependencies a webhook
// publisher needs to build (and, in async mode, own) its delivery engine.
type WebhookPublisherOptions struct {
	Workers      int           // async worker-pool size (server.fanout_concurrency)
	DefaultDrain time.Duration // fallback drain when cfg.DrainTimeout is 0
	Metrics      WebhookMetrics
	Logger       *slog.Logger
}

// WebhookPublisher delivers to a single webhook destination. In async mode it
// owns a per-destination delivery engine; in sync mode Publish delivers inline
// on the caller's goroutine and returns the delivery result. targets is written
// once in the constructor; read-only in Publish (safe for fan-out).
type WebhookPublisher struct {
	engine   *webhookDeliveryEngine // nil in sync mode
	syncMode bool
	destID   string
	delivery *targetResolver[*webhookDelivery] // per target, static and dynamic
	metrics  WebhookMetrics                    // may be nil
	logger   *slog.Logger
	// urlTemplate is the destination url parsed for {variable} placeholders.
	// When it has any, the base url is rendered per message from
	// msg.Variables; otherwise baseURL is used verbatim.
	urlTemplate vars.Template
	baseURL     string
}

func NewWebhookPublisher(cfg config.WebhookConfig, destID string, targets []WebhookTarget, opts WebhookPublisherOptions) (*WebhookPublisher, error) {
	logger := opts.Logger
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

	syncMode := cfg.DeliveryMode == "sync"
	// DLQ is async-only; validation guarantees no dead_letter with sync mode.
	dlqDestination, dlqTarget := "", ""
	if !syncMode {
		dlqDestination = cfg.DeadLetter.Destination
		dlqTarget = cfg.DeadLetter.Target
	}

	urlTemplate, err := vars.Parse(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("webhook %q: url template: %w", destID, err)
	}
	deliveries := newTargetResolver[*webhookDelivery](0, logger)
	for _, t := range targets {
		d := &webhookDelivery{
			destID: destID, target: t.Name, metricTarget: t.Name, url: joinWebhookURL(cfg.URL, t.Name), mode: mode,
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
			logger:         logger,
			dlqDestination: dlqDestination,
			dlqTarget:      dlqTarget,
		}
		if t.Template.IsTemplate() {
			d.metricTarget = t.Template.Raw()
			deliveries.addTemplate(t.Template, d)
		} else {
			deliveries.addStatic(t.Name, d)
		}
	}

	p := &WebhookPublisher{syncMode: syncMode, destID: destID, delivery: deliveries, metrics: opts.Metrics, logger: logger, urlTemplate: urlTemplate, baseURL: cfg.URL}
	if !syncMode {
		drain := cfg.DrainTimeout
		if drain <= 0 {
			drain = opts.DefaultDrain
		}
		p.engine = newWebhookDeliveryEngine(destID, opts.Workers, cfg.MaxQueue, drain, opts.Metrics, logger)
		if opts.Metrics != nil {
			opts.Metrics.WebhookQueueCapacity(destID, p.engine.capacity())
		}
	}
	return p, nil
}

// provisionDelivery clones a dynamic route's delivery settings for a rendered
// target; the url is base + "/" + rendered target, exactly as for static ones.
func (p *WebhookPublisher) provisionDelivery(_ context.Context, base *webhookDelivery, rendered string) (*webhookDelivery, error) {
	d := *base
	d.target = rendered
	d.url = joinWebhookURL(p.baseURL, rendered)
	return &d, nil
}

func (p *WebhookPublisher) Publish(ctx context.Context, destinationQueueID, target string, msg Message) error {
	d, err := p.delivery.resolve(ctx, target, p.provisionDelivery)
	if err != nil {
		return fmt.Errorf("webhook %q: target %q is not configured", p.destID, target)
	}
	if p.urlTemplate.HasPlaceholders() {
		// Templated destination url: render the base from the event's routing
		// variables. A missing binding is terminal for this event (it can
		// never succeed on retry with the same envelope).
		// The intake path already enforces this; re-check here so a replayed
		// or tampered envelope can never steer the delivery host.
		for _, n := range p.urlTemplate.Names() {
			if v, ok := msg.Variables[n]; ok && !config.IsSafeName(v) {
				return fmt.Errorf("webhook %q: variable %q has an unsafe value for a url", p.destID, n)
			}
		}
		base, err := p.urlTemplate.Render(msg.Variables)
		if err != nil {
			return fmt.Errorf("webhook %q: render url: %w", p.destID, err)
		}
		rendered := *d
		rendered.url = joinWebhookURL(base, d.target)
		d = &rendered
	}
	if p.syncMode {
		_, _, err := d.run(ctx, msg, p.metrics)
		if err != nil {
			return fmt.Errorf("webhook %q: %w", p.destID, err)
		}
		return nil
	}
	if err := p.engine.Enqueue(webhookJob{delivery: d, msg: msg}); err != nil {
		reason := "engine_closing"
		if errors.Is(err, errWebhookQueueFull) {
			reason = "queue_full"
		}
		if p.metrics != nil {
			p.metrics.WebhookDropped(p.destID, d.metricTarget, reason)
		}
		return fmt.Errorf("webhook %q: enqueue: %w", p.destID, err)
	}
	return nil
}

// setDeadLetterPublisher wires the dead-letter resolver onto the owned engine.
// No-op in sync mode (no engine, and DLQ is async-only).
func (p *WebhookPublisher) setDeadLetterPublisher(fn deadLetterFunc) {
	if p.engine != nil {
		p.engine.setDeadLetter(fn)
	}
}

// Ready reports saturation for async delivery. Sync mode is always ready (there
// is no queue). It never probes the external target.
func (p *WebhookPublisher) Ready(ctx context.Context) error {
	if p.syncMode || p.engine == nil {
		return nil
	}
	if depth, capacity, isSaturated := p.engine.saturated(); isSaturated {
		return fmt.Errorf("webhook %q: delivery queue saturated (%d/%d)", p.destID, depth, capacity)
	}
	return nil
}

// Close drains the owned engine (idempotent). Sync mode has no engine.
func (p *WebhookPublisher) Close() error {
	if p.engine != nil {
		return p.engine.Close()
	}
	return nil
}

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
