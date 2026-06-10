package config

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/jasongiroux/hookway/internal/auth"
)

// authScheme defaults the per-endpoint signature scheme to Paddle for
// backward compatibility when the field is omitted.
func authScheme(scheme string) string {
	if scheme == "" {
		return auth.SchemePaddle
	}
	return scheme
}

var safeName = regexp.MustCompile(`^[a-zA-Z0-9._-]{1,128}$`)
var safeHeaderName = regexp.MustCompile("^[!#$%&'*+\\-.^_`|~0-9A-Za-z]+$")

type Config struct {
	Server        ServerConfig                  `yaml:"server"`
	Security      SecurityConfig                `yaml:"security"`
	RateLimit     RateLimitConfig               `yaml:"rate_limit"`
	Metrics       MetricsConfig                 `yaml:"metrics"`
	MessageQueues map[string]MessageQueueConfig `yaml:"message_queues"`
	Endpoints     map[string]EndpointConfig     `yaml:"endpoints"`
}

type ServerConfig struct {
	Addr                string
	ReadTimeout         time.Duration
	WriteTimeout        time.Duration
	IdleTimeout         time.Duration
	ShutdownTimeout     time.Duration
	MaxBodyBytes        int64
	MaxInflightRequests int
}

type SecurityConfig struct {
	TrustedProxyHeaders      bool     `yaml:"trusted_proxy_headers"`
	TrustedProxies           []string `yaml:"trusted_proxies"`
	RequestIDHeader          string   `yaml:"request_id_header"`
	RedactedHeaderDenylist   []string `yaml:"redacted_header_denylist"`
	ForwardedHeaderAllowlist []string `yaml:"forwarded_header_allowlist"`
}

type RateLimitConfig struct {
	Enabled           bool   `yaml:"enabled"`
	RequestsPerMinute int    `yaml:"requests_per_minute"`
	Burst             int    `yaml:"burst"`
	KeyBy             string `yaml:"key_by"`
}

type MetricsConfig struct {
	Enabled bool   `yaml:"enabled"`
	Path    string `yaml:"path"`
	// Addr, when set, serves metrics on a dedicated listener (e.g. ":9090")
	// instead of the public webhook port, so /metrics is not internet-exposed.
	Addr string `yaml:"addr"`
	// RuntimeMetrics toggles the standard Go runtime and process collectors
	// (goroutines, heap/GC, CPU, resident memory). Defaults to true when omitted.
	RuntimeMetrics *bool `yaml:"runtime_metrics"`
}

// RuntimeMetricsEnabled reports whether Go runtime/process collectors should be
// registered. Defaults to true unless explicitly disabled.
func (m MetricsConfig) RuntimeMetricsEnabled() bool {
	return m.RuntimeMetrics == nil || *m.RuntimeMetrics
}

type RabbitMQConfig struct {
	URL               string
	Exchange          string
	ExchangeType      string
	Durable           bool
	PublishTimeout    time.Duration
	DeclareQueues     bool
	PublisherChannels int
	TLS               RabbitMQTLSConfig
	DeadLetter        RabbitMQDeadLetterConfig
	QueueDefaults     RabbitMQQueueOptions
}

// RabbitMQTLSConfig configures TLS for amqps:// connections. All fields are
// optional: with an empty block, amqps:// still works against brokers whose
// certificates chain to the system trust store (e.g. CloudAMQP). Set these only
// for private CAs, mutual TLS, or a non-default server name.
type RabbitMQTLSConfig struct {
	CACert             string
	ClientCert         string
	ClientKey          string
	ServerName         string
	InsecureSkipVerify bool
}

type RabbitMQDeadLetterConfig struct {
	Enabled      bool
	Exchange     string
	ExchangeType string
	Queue        string
	RoutingKey   string
	Durable      bool
	CreateQueue  bool
}

type RabbitMQQueueOptions struct {
	MessageTTL           time.Duration
	Expires              time.Duration
	MaxLength            int64
	Overflow             string
	DeadLetterRoutingKey string
}

type MessageQueueConfig struct {
	Type     string
	RabbitMQ RabbitMQConfig
}

type EndpointConfig struct {
	Enabled            bool                    `yaml:"enabled"`
	Path               string                  `yaml:"path"`
	DestinationQueueID string                  `yaml:"destination_queue_id"`
	Queue              string                  `yaml:"queue"`
	CreateQueue        bool                    `yaml:"create_queue"`
	QueueOptions       RabbitMQQueueOptions    `yaml:"queue_options"`
	MaxBodyBytes       int64                   `yaml:"max_body_bytes"`
	RateLimit          EndpointRateLimitConfig `yaml:"rate_limit"`
	ReplayProtection   ReplayProtectionConfig  `yaml:"replay_protection"`
	IPAllowlist        []string                `yaml:"ip_allowlist"`
	Auth               AuthConfig              `yaml:"auth"`
}

type EndpointRateLimitConfig struct {
	Enabled           *bool  `yaml:"enabled"`
	RequestsPerMinute int    `yaml:"requests_per_minute"`
	Burst             int    `yaml:"burst"`
	KeyBy             string `yaml:"key_by"`
}

type ReplayProtectionConfig struct {
	Enabled    bool
	TTL        time.Duration
	MaxEntries int
}

type AuthConfig struct {
	Type               string
	Scheme             string
	Secret             string
	SignatureHeader    string
	TimestampTolerance time.Duration
	IdempotencyHeader  string
}

type rawRabbitMQQueueOptions struct {
	MessageTTL           string `yaml:"message_ttl"`
	Expires              string `yaml:"expires"`
	MaxLength            int64  `yaml:"max_length"`
	Overflow             string `yaml:"overflow"`
	DeadLetterRoutingKey string `yaml:"dead_letter_routing_key"`
}

type rawMessageQueueConfig struct {
	Type     string `yaml:"type"`
	RabbitMQ struct {
		URL               string                  `yaml:"url"`
		Exchange          string                  `yaml:"exchange"`
		ExchangeType      string                  `yaml:"exchange_type"`
		Durable           bool                    `yaml:"durable"`
		PublishTimeout    string                  `yaml:"publish_timeout"`
		DeclareQueues     bool                    `yaml:"declare_queues"`
		PublisherChannels int                     `yaml:"publisher_channels"`
		QueueDefaults     rawRabbitMQQueueOptions `yaml:"queue_defaults"`
		TLS               struct {
			CACert             string `yaml:"ca_cert"`
			ClientCert         string `yaml:"client_cert"`
			ClientKey          string `yaml:"client_key"`
			ServerName         string `yaml:"server_name"`
			InsecureSkipVerify bool   `yaml:"insecure_skip_verify"`
		} `yaml:"tls"`
		DeadLetter struct {
			Enabled      bool   `yaml:"enabled"`
			Exchange     string `yaml:"exchange"`
			ExchangeType string `yaml:"exchange_type"`
			Queue        string `yaml:"queue"`
			RoutingKey   string `yaml:"routing_key"`
			Durable      bool   `yaml:"durable"`
			CreateQueue  bool   `yaml:"create_queue"`
		} `yaml:"dead_letter"`
	} `yaml:"rabbitmq"`
}

type rawConfig struct {
	Server struct {
		Addr                string `yaml:"addr"`
		ReadTimeout         string `yaml:"read_timeout"`
		WriteTimeout        string `yaml:"write_timeout"`
		IdleTimeout         string `yaml:"idle_timeout"`
		ShutdownTimeout     string `yaml:"shutdown_timeout"`
		MaxBodyBytes        int64  `yaml:"max_body_bytes"`
		MaxInflightRequests int    `yaml:"max_inflight_requests"`
	} `yaml:"server"`
	Security      SecurityConfig                   `yaml:"security"`
	RateLimit     RateLimitConfig                  `yaml:"rate_limit"`
	Metrics       MetricsConfig                    `yaml:"metrics"`
	MessageQueues map[string]rawMessageQueueConfig `yaml:"message_queues"`
	Endpoints     map[string]struct {
		Enabled            bool                    `yaml:"enabled"`
		Path               string                  `yaml:"path"`
		DestinationQueueID string                  `yaml:"destination_queue_id"`
		Queue              string                  `yaml:"queue"`
		CreateQueue        bool                    `yaml:"create_queue"`
		QueueOptions       rawRabbitMQQueueOptions `yaml:"queue_options"`
		MaxBodyBytes       int64                   `yaml:"max_body_bytes"`
		RateLimit          EndpointRateLimitConfig `yaml:"rate_limit"`
		ReplayProtection   struct {
			Enabled    bool   `yaml:"enabled"`
			TTL        string `yaml:"ttl"`
			MaxEntries int    `yaml:"max_entries"`
		} `yaml:"replay_protection"`
		IPAllowlist []string `yaml:"ip_allowlist"`
		Auth        struct {
			Type               string `yaml:"type"`
			Scheme             string `yaml:"scheme"`
			Secret             string `yaml:"secret"`
			SignatureHeader    string `yaml:"signature_header"`
			TimestampTolerance string `yaml:"timestamp_tolerance"`
			IdempotencyHeader  string `yaml:"idempotency_header"`
		} `yaml:"auth"`
	} `yaml:"endpoints"`
}

func Load(path string) (Config, error) {
	return NewService().Load(path)
}

type Service struct{}

func NewService() Service {
	return Service{}
}

type ValidationIssue struct {
	Field      string
	Message    string
	Suggestion string
}

type ConfigValidationError struct {
	Issues []ValidationIssue
}

func (e ConfigValidationError) Error() string {
	var b strings.Builder
	b.WriteString("config validation failed")
	for _, issue := range e.Issues {
		b.WriteString("\n- ")
		b.WriteString(issue.Field)
		b.WriteString(": ")
		b.WriteString(issue.Message)
		if issue.Suggestion != "" {
			b.WriteString(" Suggestion: ")
			b.WriteString(issue.Suggestion)
		}
	}
	return b.String()
}

func (e ConfigValidationError) Is(target error) bool {
	_, ok := target.(ConfigValidationError)
	return ok
}

func (s Service) Load(path string) (Config, error) {
	if path == "" {
		return Config{}, ConfigValidationError{Issues: []ValidationIssue{{
			Field: "config", Message: "path is required", Suggestion: "set --config ./config.yaml or HOOKWAY_CONFIG",
		}}}
	}
	rawBytes, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	expanded := os.ExpandEnv(string(rawBytes))
	var raw rawConfig
	decoder := yaml.NewDecoder(bytes.NewBufferString(expanded))
	decoder.KnownFields(true)
	if err := decoder.Decode(&raw); err != nil {
		if strings.Contains(err.Error(), "field") && strings.Contains(err.Error(), "not found") {
			return Config{}, fmt.Errorf("unknown or invalid YAML field in %s: %w", path, err)
		}
		return Config{}, fmt.Errorf("invalid YAML syntax in %s: %w", path, err)
	}
	cfg, issues := convert(raw)
	if len(issues) > 0 {
		return Config{}, ConfigValidationError{Issues: issues}
	}
	if issues := cfg.ValidationIssues(); len(issues) > 0 {
		return Config{}, ConfigValidationError{Issues: issues}
	}
	return cfg, nil
}

func (s Service) Validate(path string) error {
	_, err := s.Load(path)
	return err
}

func MustLoad(path string) Config {
	cfg, err := Load(path)
	if err != nil {
		panic(err)
	}
	return cfg
}

func validationError(field, message, suggestion string) ValidationIssue {
	return ValidationIssue{Field: field, Message: message, Suggestion: suggestion}
}

func validationErrors(issues ...ValidationIssue) []ValidationIssue {
	var out []ValidationIssue
	for _, issue := range issues {
		if issue.Field != "" {
			out = append(out, issue)
		}
	}
	return out
}

func configValidationError(issues []ValidationIssue) error {
	if len(issues) == 0 {
		return nil
	}
	return ConfigValidationError{Issues: issues}
}

func (c Config) Validate() error {
	return configValidationError(c.ValidationIssues())
}

func (c Config) ValidationIssues() []ValidationIssue {
	var issues []ValidationIssue
	if c.Server.Addr == "" {
		issues = append(issues, validationError("server.addr", "listen address is required", "set server.addr to a host:port such as \":8080\""))
	}
	if c.Server.MaxBodyBytes <= 0 {
		issues = append(issues, validationError("server.max_body_bytes", "must be a positive integer", "set a safe byte limit, for example 1048576"))
	}
	if c.Server.MaxInflightRequests < 0 {
		issues = append(issues, validationError("server.max_inflight_requests", "must be zero (unlimited) or a positive integer", "set a bound such as 256 to apply backpressure under load"))
	}
	for i, header := range c.Security.RedactedHeaderDenylist {
		if !safeHeaderName.MatchString(header) {
			issues = append(issues, validationError(fmt.Sprintf("security.redacted_header_denylist[%d]", i), "header name is empty or invalid", "use HTTP header names such as \"Authorization\" or \"X-Signature\""))
		}
	}
	for i, header := range c.Security.ForwardedHeaderAllowlist {
		if !safeHeaderName.MatchString(header) {
			issues = append(issues, validationError(fmt.Sprintf("security.forwarded_header_allowlist[%d]", i), "header name is empty or invalid", "use HTTP header names such as \"X-Request-ID\" or \"Content-Type\""))
		}
	}
	for i, entry := range c.Security.TrustedProxies {
		if net.ParseIP(entry) == nil {
			if _, _, err := net.ParseCIDR(entry); err != nil {
				issues = append(issues, validationError(fmt.Sprintf("security.trusted_proxies[%d]", i), "must be an IP address or CIDR range", "use values such as \"10.0.0.0/8\" or \"203.0.113.10\""))
			}
		}
	}
	if len(c.MessageQueues) == 0 {
		issues = append(issues, validationError("message_queues", "at least one message queue is required", "add message_queues.billing with type \"rabbitmq\""))
	}
	for id, mq := range c.MessageQueues {
		fieldPrefix := "message_queues." + id
		if !safeName.MatchString(id) {
			issues = append(issues, validationError(fieldPrefix, "message queue id contains unsupported characters", "use letters, numbers, dot, underscore, or hyphen only"))
		}
		if mq.Type == "" {
			issues = append(issues, validationError(fieldPrefix+".type", "message queue backend type is required", "set type to \"rabbitmq\""))
		} else if mq.Type != "rabbitmq" {
			issues = append(issues, validationError(fieldPrefix+".type", fmt.Sprintf("%q is not supported", mq.Type), "supported value for MVP is \"rabbitmq\""))
		}
		if mq.Type == "rabbitmq" || mq.Type == "" {
			rabbitPrefix := fieldPrefix + ".rabbitmq"
			if mq.RabbitMQ.URL == "" {
				issues = append(issues, validationError(rabbitPrefix+".url", "AMQP URL is required", "set url to ${RABBITMQ_URL} and export RABBITMQ_URL"))
			}
			if mq.RabbitMQ.Exchange == "" {
				issues = append(issues, validationError(rabbitPrefix+".exchange", "exchange name is required", "set a direct exchange such as \"hookway.webhooks\""))
			}
			if mq.RabbitMQ.ExchangeType == "" {
				issues = append(issues, validationError(rabbitPrefix+".exchange_type", "exchange type is required", "set exchange_type to \"direct\""))
			}
			if mq.RabbitMQ.PublisherChannels < 0 {
				issues = append(issues, validationError(rabbitPrefix+".publisher_channels", "must be zero (default) or a positive integer", "set publish concurrency such as 8"))
			}
			issues = append(issues, queueOptionValidationIssues(rabbitPrefix+".queue_defaults", mq.RabbitMQ.QueueDefaults)...)
			tls := mq.RabbitMQ.TLS
			if (tls.ClientCert == "") != (tls.ClientKey == "") {
				issues = append(issues, validationError(rabbitPrefix+".tls.client_cert", "client_cert and client_key must both be set for mutual TLS", "set both paths, or neither"))
			}
			if mq.RabbitMQ.DeadLetter.Enabled {
				if !safeName.MatchString(mq.RabbitMQ.DeadLetter.Exchange) {
					issues = append(issues, validationError(rabbitPrefix+".dead_letter.exchange", "exchange name contains unsupported characters or is empty", "use 1-128 letters, numbers, dot, underscore, or hyphen"))
				}
				if mq.RabbitMQ.DeadLetter.ExchangeType == "" {
					issues = append(issues, validationError(rabbitPrefix+".dead_letter.exchange_type", "exchange type is required", "set exchange_type to \"direct\""))
				}
				if mq.RabbitMQ.DeadLetter.CreateQueue && !safeName.MatchString(mq.RabbitMQ.DeadLetter.Queue) {
					issues = append(issues, validationError(rabbitPrefix+".dead_letter.queue", "queue name contains unsupported characters or is empty", "use 1-128 letters, numbers, dot, underscore, or hyphen"))
				}
				if mq.RabbitMQ.DeadLetter.RoutingKey == "" {
					issues = append(issues, validationError(rabbitPrefix+".dead_letter.routing_key", "routing key is required", "set a routing key such as \"webhooks.dead\""))
				}
			}
		}
	}
	if len(c.Endpoints) == 0 {
		issues = append(issues, validationError("endpoints", "at least one endpoint is required", "add an endpoint such as endpoints.paddle_billing"))
	}
	if c.Metrics.Enabled && (c.Metrics.Path == "" || c.Metrics.Path[0] != '/') {
		issues = append(issues, validationError("metrics.path", "must start with / when metrics are enabled", "set metrics.path to \"/metrics\""))
	}
	if c.RateLimit.Enabled {
		if c.RateLimit.RequestsPerMinute <= 0 {
			issues = append(issues, validationError("rate_limit.requests_per_minute", "must be positive when rate limiting is enabled", "set a value such as 120"))
		}
		if c.RateLimit.Burst <= 0 {
			issues = append(issues, validationError("rate_limit.burst", "must be positive when rate limiting is enabled", "set a value such as 20"))
		}
		if c.RateLimit.KeyBy != "ip" {
			issues = append(issues, validationError("rate_limit.key_by", "only ip is supported for MVP", "set rate_limit.key_by to \"ip\""))
		}
	}
	paths := map[string]string{}
	queueOptions := map[string]RabbitMQQueueOptions{}
	for name, endpoint := range c.Endpoints {
		fieldPrefix := "endpoints." + name
		if !safeName.MatchString(name) {
			issues = append(issues, validationError(fieldPrefix, "endpoint name contains unsupported characters", "use letters, numbers, dot, underscore, or hyphen only"))
		}
		if !endpoint.Enabled {
			continue
		}
		if endpoint.DestinationQueueID == "" {
			issues = append(issues, validationError(fieldPrefix+".destination_queue_id", "destination queue id is required for enabled endpoints", "set destination_queue_id to a key under message_queues"))
		} else if _, ok := c.MessageQueues[endpoint.DestinationQueueID]; !ok {
			issues = append(issues, validationError(fieldPrefix+".destination_queue_id", "destination queue id is not configured", "reference an existing message_queues entry"))
		}
		if endpoint.Path == "" {
			issues = append(issues, validationError(fieldPrefix+".path", "path is required for enabled endpoints", "set a path such as \"/webhooks/paddle/billing\""))
		} else if endpoint.Path[0] != '/' {
			issues = append(issues, validationError(fieldPrefix+".path", "path must start with /", "set a path such as \"/webhooks/paddle/billing\""))
		} else if other, ok := paths[endpoint.Path]; ok {
			issues = append(issues, validationError(fieldPrefix+".path", "path duplicates endpoint "+other, "each enabled endpoint must use a unique path"))
		} else {
			for otherPath, otherName := range paths {
				if endpointPathsOverlap(endpoint.Path, otherPath) {
					issues = append(issues, validationError(fieldPrefix+".path", "path overlaps endpoint "+otherName, "avoid parent/child paths such as \"/webhooks/paddle\" and \"/webhooks/paddle/billing\""))
					break
				}
			}
			paths[endpoint.Path] = name
		}
		if !safeName.MatchString(endpoint.Queue) {
			issues = append(issues, validationError(fieldPrefix+".queue", "queue name contains unsupported characters or is empty", "use 1-128 letters, numbers, dot, underscore, or hyphen"))
		}
		queueOptionKey := endpoint.DestinationQueueID + "\x00" + endpoint.Queue
		if previous, ok := queueOptions[queueOptionKey]; ok && previous != endpoint.QueueOptions {
			issues = append(issues, validationError(fieldPrefix+".queue_options", "cannot differ between endpoints that share a RabbitMQ queue", "use one queue per endpoint when queue arguments need to differ"))
		} else {
			queueOptions[queueOptionKey] = endpoint.QueueOptions
		}
		issues = append(issues, queueOptionValidationIssues(fieldPrefix+".queue_options", endpoint.QueueOptions)...)
		if endpoint.MaxBodyBytes < 0 {
			issues = append(issues, validationError(fieldPrefix+".max_body_bytes", "must be positive when set", "omit to inherit server.max_body_bytes or set a positive byte limit"))
		}
		if endpoint.RateLimit.Enabled != nil && *endpoint.RateLimit.Enabled {
			if endpoint.RateLimit.RequestsPerMinute <= 0 {
				issues = append(issues, validationError(fieldPrefix+".rate_limit.requests_per_minute", "must be positive when endpoint rate limiting is enabled", "set a value such as 60"))
			}
			if endpoint.RateLimit.Burst <= 0 {
				issues = append(issues, validationError(fieldPrefix+".rate_limit.burst", "must be positive when endpoint rate limiting is enabled", "set a value such as 10"))
			}
			if endpoint.RateLimit.KeyBy != "ip" {
				issues = append(issues, validationError(fieldPrefix+".rate_limit.key_by", "only ip is supported for MVP", "set rate_limit.key_by to \"ip\""))
			}
		}
		if endpoint.ReplayProtection.Enabled {
			if endpoint.ReplayProtection.TTL <= 0 {
				issues = append(issues, validationError(fieldPrefix+".replay_protection.ttl", "must be a positive duration", "set a value such as \"10m\""))
			}
			if endpoint.ReplayProtection.MaxEntries <= 0 {
				issues = append(issues, validationError(fieldPrefix+".replay_protection.max_entries", "must be positive when replay protection is enabled", "set a bounded size such as 50000"))
			}
		}
		for i, entry := range endpoint.IPAllowlist {
			if net.ParseIP(entry) == nil {
				if _, _, err := net.ParseCIDR(entry); err != nil {
					issues = append(issues, validationError(fmt.Sprintf("%s.ip_allowlist[%d]", fieldPrefix, i), "must be an IP address or CIDR range", "use values such as \"203.0.113.10\" or \"198.51.100.0/24\""))
				}
			}
		}
		if endpoint.Auth.Type == "" {
			issues = append(issues, validationError(fieldPrefix+".auth.type", "auth type is required", "set auth.type to \"hmac_sha256\""))
		} else if endpoint.Auth.Type != "hmac_sha256" {
			issues = append(issues, validationError(fieldPrefix+".auth.type", fmt.Sprintf("%q is not supported", endpoint.Auth.Type), "supported value for MVP is \"hmac_sha256\""))
		}
		if !auth.SchemeRegistered(endpoint.Auth.Scheme) {
			issues = append(issues, validationError(fieldPrefix+".auth.scheme", fmt.Sprintf("%q is not a known signature scheme", endpoint.Auth.Scheme), fmt.Sprintf("use one of %v", auth.Schemes())))
		}
		if endpoint.Auth.Secret == "" {
			issues = append(issues, validationError(fieldPrefix+".auth.secret", "secret is required", "reference an environment variable such as ${PADDLE_WEBHOOK_SECRET}"))
		}
		if endpoint.Auth.SignatureHeader == "" {
			issues = append(issues, validationError(fieldPrefix+".auth.signature_header", "signature header is required", "for Paddle, set \"Paddle-Signature\""))
		}
		if auth.SchemeRequiresTimestamp(endpoint.Auth.Scheme) && endpoint.Auth.TimestampTolerance <= 0 {
			issues = append(issues, validationError(fieldPrefix+".auth.timestamp_tolerance", "must be a positive duration for timestamped schemes", "set a value such as \"5m\""))
		}
	}
	return issues
}

func ResolvePath(flagValue string) (string, error) {
	if flagValue != "" {
		return flagValue, nil
	}
	if env := os.Getenv("HOOKWAY_CONFIG"); env != "" {
		return env, nil
	}
	return "", errors.New("config path required via --config or HOOKWAY_CONFIG")
}

func ConfigFlag() *string {
	return flag.String("config", "", "path to Hookway YAML config")
}

func convert(raw rawConfig) (Config, []ValidationIssue) {
	var issues []ValidationIssue
	read, issue := parseDuration("server.read_timeout", raw.Server.ReadTimeout, 5*time.Second)
	if issue.Field != "" {
		issues = append(issues, issue)
	}
	write, issue := parseDuration("server.write_timeout", raw.Server.WriteTimeout, 10*time.Second)
	if issue.Field != "" {
		issues = append(issues, issue)
	}
	idle, issue := parseDuration("server.idle_timeout", raw.Server.IdleTimeout, 60*time.Second)
	if issue.Field != "" {
		issues = append(issues, issue)
	}
	shutdown, issue := parseDuration("server.shutdown_timeout", raw.Server.ShutdownTimeout, 10*time.Second)
	if issue.Field != "" {
		issues = append(issues, issue)
	}
	cfg := Config{
		Server: ServerConfig{
			Addr: raw.Server.Addr, ReadTimeout: read, WriteTimeout: write,
			IdleTimeout: idle, ShutdownTimeout: shutdown, MaxBodyBytes: raw.Server.MaxBodyBytes,
			MaxInflightRequests: raw.Server.MaxInflightRequests,
		},
		Security:      raw.Security,
		RateLimit:     raw.RateLimit,
		Metrics:       raw.Metrics,
		MessageQueues: map[string]MessageQueueConfig{},
		Endpoints:     map[string]EndpointConfig{},
	}
	for id, rawMQ := range raw.MessageQueues {
		mq, mqIssues := convertMessageQueue("message_queues."+id, rawMQ)
		issues = append(issues, mqIssues...)
		cfg.MessageQueues[id] = mq
	}
	for name, endpoint := range raw.Endpoints {
		tolerance, issue := parseDuration("endpoints."+name+".auth.timestamp_tolerance", endpoint.Auth.TimestampTolerance, 5*time.Minute)
		if issue.Field != "" {
			issues = append(issues, issue)
		}
		replayTTL, issue := parseDuration("endpoints."+name+".replay_protection.ttl", endpoint.ReplayProtection.TTL, 0)
		if issue.Field != "" {
			issues = append(issues, issue)
		}
		queueOptions, queueOptionIssues := convertQueueOptions("endpoints."+name+".queue_options", endpoint.QueueOptions)
		issues = append(issues, queueOptionIssues...)
		cfg.Endpoints[name] = EndpointConfig{
			Enabled:            endpoint.Enabled,
			Path:               endpoint.Path,
			DestinationQueueID: endpoint.DestinationQueueID,
			Queue:              endpoint.Queue,
			CreateQueue:        endpoint.CreateQueue,
			QueueOptions:       queueOptions,
			MaxBodyBytes:       endpoint.MaxBodyBytes,
			RateLimit:          endpoint.RateLimit,
			ReplayProtection: ReplayProtectionConfig{
				Enabled: endpoint.ReplayProtection.Enabled, TTL: replayTTL, MaxEntries: endpoint.ReplayProtection.MaxEntries,
			},
			IPAllowlist: endpoint.IPAllowlist,
			Auth: AuthConfig{Type: endpoint.Auth.Type, Scheme: authScheme(endpoint.Auth.Scheme), Secret: endpoint.Auth.Secret,
				SignatureHeader:    endpoint.Auth.SignatureHeader,
				TimestampTolerance: tolerance,
				IdempotencyHeader:  endpoint.Auth.IdempotencyHeader},
		}
	}
	return cfg, issues
}

func convertMessageQueue(fieldPrefix string, raw rawMessageQueueConfig) (MessageQueueConfig, []ValidationIssue) {
	var issues []ValidationIssue
	publish, issue := parseDuration(fieldPrefix+".rabbitmq.publish_timeout", raw.RabbitMQ.PublishTimeout, 5*time.Second)
	if issue.Field != "" {
		issues = append(issues, issue)
	}
	queueDefaults, queueDefaultIssues := convertQueueOptions(fieldPrefix+".rabbitmq.queue_defaults", raw.RabbitMQ.QueueDefaults)
	issues = append(issues, queueDefaultIssues...)
	return MessageQueueConfig{
		Type: raw.Type,
		RabbitMQ: RabbitMQConfig{
			URL: raw.RabbitMQ.URL, Exchange: raw.RabbitMQ.Exchange,
			ExchangeType: raw.RabbitMQ.ExchangeType, Durable: raw.RabbitMQ.Durable,
			PublishTimeout: publish, DeclareQueues: raw.RabbitMQ.DeclareQueues,
			PublisherChannels: raw.RabbitMQ.PublisherChannels,
			TLS: RabbitMQTLSConfig{
				CACert: raw.RabbitMQ.TLS.CACert, ClientCert: raw.RabbitMQ.TLS.ClientCert,
				ClientKey: raw.RabbitMQ.TLS.ClientKey, ServerName: raw.RabbitMQ.TLS.ServerName,
				InsecureSkipVerify: raw.RabbitMQ.TLS.InsecureSkipVerify,
			},
			DeadLetter: RabbitMQDeadLetterConfig{
				Enabled: raw.RabbitMQ.DeadLetter.Enabled, Exchange: raw.RabbitMQ.DeadLetter.Exchange,
				ExchangeType: raw.RabbitMQ.DeadLetter.ExchangeType, Queue: raw.RabbitMQ.DeadLetter.Queue,
				RoutingKey: raw.RabbitMQ.DeadLetter.RoutingKey, Durable: raw.RabbitMQ.DeadLetter.Durable,
				CreateQueue: raw.RabbitMQ.DeadLetter.CreateQueue,
			},
			QueueDefaults: queueDefaults,
		},
	}, issues
}

func parseDuration(field, value string, fallback time.Duration) (time.Duration, ValidationIssue) {
	if value == "" {
		return fallback, ValidationIssue{}
	}
	d, err := time.ParseDuration(value)
	if err != nil {
		return 0, validationError(field, fmt.Sprintf("%q is not a valid Go duration", value), "use values like \"5s\", \"10s\", or \"5m\"")
	}
	return d, ValidationIssue{}
}

func convertQueueOptions(fieldPrefix string, raw rawRabbitMQQueueOptions) (RabbitMQQueueOptions, []ValidationIssue) {
	var issues []ValidationIssue
	messageTTL, issue := parsePositiveDuration(fieldPrefix+".message_ttl", raw.MessageTTL)
	if issue.Field != "" {
		issues = append(issues, issue)
	}
	expires, issue := parsePositiveDuration(fieldPrefix+".expires", raw.Expires)
	if issue.Field != "" {
		issues = append(issues, issue)
	}
	return RabbitMQQueueOptions{
		MessageTTL:           messageTTL,
		Expires:              expires,
		MaxLength:            raw.MaxLength,
		Overflow:             raw.Overflow,
		DeadLetterRoutingKey: raw.DeadLetterRoutingKey,
	}, issues
}

func parsePositiveDuration(field, value string) (time.Duration, ValidationIssue) {
	if value == "" {
		return 0, ValidationIssue{}
	}
	d, err := time.ParseDuration(value)
	if err != nil {
		return 0, validationError(field, fmt.Sprintf("%q is not a valid Go duration", value), "use values like \"1h\", \"24h\", or \"168h\"")
	}
	if d <= 0 {
		return 0, validationError(field, "must be a positive duration when set", "omit it or set a value such as \"24h\"")
	}
	return d, ValidationIssue{}
}

func queueOptionValidationIssues(fieldPrefix string, opts RabbitMQQueueOptions) []ValidationIssue {
	var issues []ValidationIssue
	if opts.MaxLength < 0 {
		issues = append(issues, validationError(fieldPrefix+".max_length", "must be zero (omitted) or a positive integer", "set a queue cap such as 100000"))
	}
	switch opts.Overflow {
	case "", "reject-publish", "reject-publish-dlx":
	default:
		issues = append(issues, validationError(fieldPrefix+".overflow", fmt.Sprintf("%q is not supported", opts.Overflow), "use \"reject-publish\" or \"reject-publish-dlx\" for webhook queues"))
	}
	if opts.DeadLetterRoutingKey != "" && !safeName.MatchString(opts.DeadLetterRoutingKey) {
		issues = append(issues, validationError(fieldPrefix+".dead_letter_routing_key", "routing key contains unsupported characters", "use 1-128 letters, numbers, dot, underscore, or hyphen"))
	}
	return issues
}

func endpointPathsOverlap(a, b string) bool {
	shorter, longer := a, b
	if len(shorter) > len(longer) {
		shorter, longer = longer, shorter
	}
	return strings.HasPrefix(longer, shorter+"/")
}

func (c Config) EndpointForPath(path string) (string, EndpointConfig, bool) {
	for name, endpoint := range c.Endpoints {
		if endpoint.Enabled && endpoint.Path == path {
			return name, endpoint, true
		}
	}
	return "", EndpointConfig{}, false
}
