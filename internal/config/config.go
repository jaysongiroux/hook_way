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
)

var safeName = regexp.MustCompile(`^[a-zA-Z0-9._-]{1,128}$`)
var safeHeaderName = regexp.MustCompile("^[!#$%&'*+\\-.^_`|~0-9A-Za-z]+$")

type Config struct {
	Server       ServerConfig              `yaml:"server"`
	Security     SecurityConfig            `yaml:"security"`
	RateLimit    RateLimitConfig           `yaml:"rate_limit"`
	Metrics      MetricsConfig             `yaml:"metrics"`
	MessageQueue MessageQueueConfig        `yaml:"message_queue"`
	Endpoints    map[string]EndpointConfig `yaml:"endpoints"`
}

type ServerConfig struct {
	Addr            string
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	IdleTimeout     time.Duration
	ShutdownTimeout time.Duration
	MaxBodyBytes    int64
}

type SecurityConfig struct {
	TrustedProxyHeaders    bool     `yaml:"trusted_proxy_headers"`
	RequestIDHeader        string   `yaml:"request_id_header"`
	RedactedHeaderDenylist []string `yaml:"redacted_header_denylist"`
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
}

type RabbitMQConfig struct {
	URL            string
	Exchange       string
	ExchangeType   string
	Durable        bool
	PublishTimeout time.Duration
	DeclareQueues  bool
	DeadLetter     RabbitMQDeadLetterConfig
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

type MessageQueueConfig struct {
	Type     string
	RabbitMQ RabbitMQConfig
}

type EndpointConfig struct {
	Enabled          bool                    `yaml:"enabled"`
	Path             string                  `yaml:"path"`
	Queue            string                  `yaml:"queue"`
	CreateQueue      bool                    `yaml:"create_queue"`
	MaxBodyBytes     int64                   `yaml:"max_body_bytes"`
	RateLimit        EndpointRateLimitConfig `yaml:"rate_limit"`
	ReplayProtection ReplayProtectionConfig  `yaml:"replay_protection"`
	IPAllowlist      []string                `yaml:"ip_allowlist"`
	Auth             AuthConfig              `yaml:"auth"`
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
	Secret             string
	SignatureHeader    string
	TimestampTolerance time.Duration
}

type rawConfig struct {
	Server struct {
		Addr            string `yaml:"addr"`
		ReadTimeout     string `yaml:"read_timeout"`
		WriteTimeout    string `yaml:"write_timeout"`
		IdleTimeout     string `yaml:"idle_timeout"`
		ShutdownTimeout string `yaml:"shutdown_timeout"`
		MaxBodyBytes    int64  `yaml:"max_body_bytes"`
	} `yaml:"server"`
	Security     SecurityConfig  `yaml:"security"`
	RateLimit    RateLimitConfig `yaml:"rate_limit"`
	Metrics      MetricsConfig   `yaml:"metrics"`
	MessageQueue struct {
		Type     string `yaml:"type"`
		RabbitMQ struct {
			URL            string `yaml:"url"`
			Exchange       string `yaml:"exchange"`
			ExchangeType   string `yaml:"exchange_type"`
			Durable        bool   `yaml:"durable"`
			PublishTimeout string `yaml:"publish_timeout"`
			DeclareQueues  bool   `yaml:"declare_queues"`
			DeadLetter     struct {
				Enabled      bool   `yaml:"enabled"`
				Exchange     string `yaml:"exchange"`
				ExchangeType string `yaml:"exchange_type"`
				Queue        string `yaml:"queue"`
				RoutingKey   string `yaml:"routing_key"`
				Durable      bool   `yaml:"durable"`
				CreateQueue  bool   `yaml:"create_queue"`
			} `yaml:"dead_letter"`
		} `yaml:"rabbitmq"`
	} `yaml:"message_queue"`
	Endpoints map[string]struct {
		Enabled          bool                    `yaml:"enabled"`
		Path             string                  `yaml:"path"`
		Queue            string                  `yaml:"queue"`
		CreateQueue      bool                    `yaml:"create_queue"`
		MaxBodyBytes     int64                   `yaml:"max_body_bytes"`
		RateLimit        EndpointRateLimitConfig `yaml:"rate_limit"`
		ReplayProtection struct {
			Enabled    bool   `yaml:"enabled"`
			TTL        string `yaml:"ttl"`
			MaxEntries int    `yaml:"max_entries"`
		} `yaml:"replay_protection"`
		IPAllowlist []string `yaml:"ip_allowlist"`
		Auth        struct {
			Type               string `yaml:"type"`
			Secret             string `yaml:"secret"`
			SignatureHeader    string `yaml:"signature_header"`
			TimestampTolerance string `yaml:"timestamp_tolerance"`
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
	for i, header := range c.Security.RedactedHeaderDenylist {
		if !safeHeaderName.MatchString(header) {
			issues = append(issues, validationError(fmt.Sprintf("security.redacted_header_denylist[%d]", i), "header name is empty or invalid", "use HTTP header names such as \"Authorization\" or \"X-Signature\""))
		}
	}
	if c.MessageQueue.Type == "" {
		issues = append(issues, validationError("message_queue.type", "message queue backend type is required", "set message_queue.type to \"rabbitmq\""))
	} else if c.MessageQueue.Type != "rabbitmq" {
		issues = append(issues, validationError("message_queue.type", fmt.Sprintf("%q is not supported", c.MessageQueue.Type), "supported value for MVP is \"rabbitmq\""))
	}
	if c.MessageQueue.Type == "rabbitmq" || c.MessageQueue.Type == "" {
		if c.MessageQueue.RabbitMQ.URL == "" {
			issues = append(issues, validationError("message_queue.rabbitmq.url", "AMQP URL is required", "set url to ${RABBITMQ_URL} and export RABBITMQ_URL"))
		}
		if c.MessageQueue.RabbitMQ.Exchange == "" {
			issues = append(issues, validationError("message_queue.rabbitmq.exchange", "exchange name is required", "set a direct exchange such as \"hookway.webhooks\""))
		}
		if c.MessageQueue.RabbitMQ.ExchangeType == "" {
			issues = append(issues, validationError("message_queue.rabbitmq.exchange_type", "exchange type is required", "set exchange_type to \"direct\""))
		}
		if c.MessageQueue.RabbitMQ.DeadLetter.Enabled {
			if !safeName.MatchString(c.MessageQueue.RabbitMQ.DeadLetter.Exchange) {
				issues = append(issues, validationError("message_queue.rabbitmq.dead_letter.exchange", "exchange name contains unsupported characters or is empty", "use 1-128 letters, numbers, dot, underscore, or hyphen"))
			}
			if c.MessageQueue.RabbitMQ.DeadLetter.ExchangeType == "" {
				issues = append(issues, validationError("message_queue.rabbitmq.dead_letter.exchange_type", "exchange type is required", "set exchange_type to \"direct\""))
			}
			if c.MessageQueue.RabbitMQ.DeadLetter.CreateQueue && !safeName.MatchString(c.MessageQueue.RabbitMQ.DeadLetter.Queue) {
				issues = append(issues, validationError("message_queue.rabbitmq.dead_letter.queue", "queue name contains unsupported characters or is empty", "use 1-128 letters, numbers, dot, underscore, or hyphen"))
			}
			if c.MessageQueue.RabbitMQ.DeadLetter.RoutingKey == "" {
				issues = append(issues, validationError("message_queue.rabbitmq.dead_letter.routing_key", "routing key is required", "set a routing key such as \"webhooks.dead\""))
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
	for name, endpoint := range c.Endpoints {
		fieldPrefix := "endpoints." + name
		if !safeName.MatchString(name) {
			issues = append(issues, validationError(fieldPrefix, "endpoint name contains unsupported characters", "use letters, numbers, dot, underscore, or hyphen only"))
		}
		if !endpoint.Enabled {
			continue
		}
		if endpoint.Path == "" {
			issues = append(issues, validationError(fieldPrefix+".path", "path is required for enabled endpoints", "set a path such as \"/webhooks/paddle/billing\""))
		} else if endpoint.Path[0] != '/' {
			issues = append(issues, validationError(fieldPrefix+".path", "path must start with /", "set a path such as \"/webhooks/paddle/billing\""))
		} else if other, ok := paths[endpoint.Path]; ok {
			issues = append(issues, validationError(fieldPrefix+".path", "path duplicates endpoint "+other, "each enabled endpoint must use a unique path"))
		} else {
			paths[endpoint.Path] = name
		}
		if !safeName.MatchString(endpoint.Queue) {
			issues = append(issues, validationError(fieldPrefix+".queue", "queue name contains unsupported characters or is empty", "use 1-128 letters, numbers, dot, underscore, or hyphen"))
		}
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
		if endpoint.Auth.Secret == "" {
			issues = append(issues, validationError(fieldPrefix+".auth.secret", "secret is required", "reference an environment variable such as ${PADDLE_WEBHOOK_SECRET}"))
		}
		if endpoint.Auth.SignatureHeader == "" {
			issues = append(issues, validationError(fieldPrefix+".auth.signature_header", "signature header is required", "for Paddle, set \"Paddle-Signature\""))
		}
		if endpoint.Auth.TimestampTolerance <= 0 {
			issues = append(issues, validationError(fieldPrefix+".auth.timestamp_tolerance", "must be a positive duration", "set a value such as \"5m\""))
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
	publish, issue := parseDuration("message_queue.rabbitmq.publish_timeout", raw.MessageQueue.RabbitMQ.PublishTimeout, 5*time.Second)
	if issue.Field != "" {
		issues = append(issues, issue)
	}
	cfg := Config{
		Server: ServerConfig{
			Addr: raw.Server.Addr, ReadTimeout: read, WriteTimeout: write,
			IdleTimeout: idle, ShutdownTimeout: shutdown, MaxBodyBytes: raw.Server.MaxBodyBytes,
		},
		Security:  raw.Security,
		RateLimit: raw.RateLimit,
		Metrics:   raw.Metrics,
		MessageQueue: MessageQueueConfig{
			Type: raw.MessageQueue.Type,
			RabbitMQ: RabbitMQConfig{
				URL: raw.MessageQueue.RabbitMQ.URL, Exchange: raw.MessageQueue.RabbitMQ.Exchange,
				ExchangeType: raw.MessageQueue.RabbitMQ.ExchangeType, Durable: raw.MessageQueue.RabbitMQ.Durable,
				PublishTimeout: publish, DeclareQueues: raw.MessageQueue.RabbitMQ.DeclareQueues,
				DeadLetter: RabbitMQDeadLetterConfig{
					Enabled: raw.MessageQueue.RabbitMQ.DeadLetter.Enabled, Exchange: raw.MessageQueue.RabbitMQ.DeadLetter.Exchange,
					ExchangeType: raw.MessageQueue.RabbitMQ.DeadLetter.ExchangeType, Queue: raw.MessageQueue.RabbitMQ.DeadLetter.Queue,
					RoutingKey: raw.MessageQueue.RabbitMQ.DeadLetter.RoutingKey, Durable: raw.MessageQueue.RabbitMQ.DeadLetter.Durable,
					CreateQueue: raw.MessageQueue.RabbitMQ.DeadLetter.CreateQueue,
				},
			},
		},
		Endpoints: map[string]EndpointConfig{},
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
		cfg.Endpoints[name] = EndpointConfig{
			Enabled:      endpoint.Enabled,
			Path:         endpoint.Path,
			Queue:        endpoint.Queue,
			CreateQueue:  endpoint.CreateQueue,
			MaxBodyBytes: endpoint.MaxBodyBytes,
			RateLimit:    endpoint.RateLimit,
			ReplayProtection: ReplayProtectionConfig{
				Enabled: endpoint.ReplayProtection.Enabled, TTL: replayTTL, MaxEntries: endpoint.ReplayProtection.MaxEntries,
			},
			IPAllowlist: endpoint.IPAllowlist,
			Auth: AuthConfig{Type: endpoint.Auth.Type, Secret: endpoint.Auth.Secret,
				SignatureHeader:    endpoint.Auth.SignatureHeader,
				TimestampTolerance: tolerance},
		}
	}
	return cfg, issues
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

func (c Config) EndpointForPath(path string) (string, EndpointConfig, bool) {
	for name, endpoint := range c.Endpoints {
		if endpoint.Enabled && endpoint.Path == path {
			return name, endpoint, true
		}
	}
	return "", EndpointConfig{}, false
}
