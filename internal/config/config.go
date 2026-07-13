package config

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

var safeName = regexp.MustCompile(`^[a-zA-Z0-9._-]{1,128}$`)
var safeHeaderName = regexp.MustCompile("^[!#$%&'*+\\-.^_`|~0-9A-Za-z]+$")

// LoopHopsHeader is the HTTP header Hookway stamps on outbound webhook
// deliveries and reads on inbound requests to detect delivery loops (a webhook
// destination that points back at Hookway). Each Hookway hop increments the
// count; an inbound request whose count reaches an endpoint's max is rejected.
const LoopHopsHeader = "X-Hookway-Hops"

// defaultLoopMaxHops is the hop ceiling applied when loop protection is enabled
// but max_hops is not set. An inbound request is rejected when its hop count is
// greater than or equal to this value.
const defaultLoopMaxHops = 3

type Config struct {
	Server       ServerConfig                 `yaml:"server"`
	Security     SecurityConfig               `yaml:"security"`
	RateLimit    RateLimitConfig              `yaml:"rate_limit"`
	Metrics      MetricsConfig                `yaml:"metrics"`
	Destinations map[string]DestinationConfig `yaml:"destinations"`
	Endpoints    map[string]EndpointConfig    `yaml:"endpoints"`
}

type ServerConfig struct {
	Addr                string
	ReadTimeout         time.Duration
	WriteTimeout        time.Duration
	IdleTimeout         time.Duration
	ShutdownTimeout     time.Duration
	MaxBodyBytes        int64
	MaxInflightRequests int
	FanoutConcurrency   int
}

type SecurityConfig struct {
	// APIKey, when set, requires callers of the metrics and config-API endpoints
	// to send "Authorization: Bearer <APIKey>". Empty disables that auth. May be
	// an ${ENV} reference (expanded on file load).
	APIKey                   string               `yaml:"api_key"`
	TrustedProxyHeaders      bool                 `yaml:"trusted_proxy_headers"`
	TrustedProxies           []string             `yaml:"trusted_proxies"`
	RequestIDHeader          string               `yaml:"request_id_header"`
	RedactedHeaderDenylist   []string             `yaml:"redacted_header_denylist"`
	ForwardedHeaderAllowlist []string             `yaml:"forwarded_header_allowlist"`
	LoopProtection           LoopProtectionConfig `yaml:"loop_protection"`
}

// LoopProtectionConfig configures detection of webhook delivery loops via the
// LoopHopsHeader hop counter. Enabled defaults to true; MaxHops defaults to
// defaultLoopMaxHops.
type LoopProtectionConfig struct {
	Enabled *bool `yaml:"enabled"`
	MaxHops int   `yaml:"max_hops"`
}

// LoopProtectionEnabled reports whether inbound loop rejection is active.
// Defaults to true unless explicitly disabled.
func (s SecurityConfig) LoopProtectionEnabled() bool {
	return s.LoopProtection.Enabled == nil || *s.LoopProtection.Enabled
}

// LoopMaxHops returns the configured hop ceiling, or the default when unset.
func (s SecurityConfig) LoopMaxHops() int {
	if s.LoopProtection.MaxHops <= 0 {
		return defaultLoopMaxHops
	}
	return s.LoopProtection.MaxHops
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

type rawConfig struct {
	Server struct {
		Addr                string `yaml:"addr"`
		ReadTimeout         string `yaml:"read_timeout"`
		WriteTimeout        string `yaml:"write_timeout"`
		IdleTimeout         string `yaml:"idle_timeout"`
		ShutdownTimeout     string `yaml:"shutdown_timeout"`
		MaxBodyBytes        int64  `yaml:"max_body_bytes"`
		MaxInflightRequests int    `yaml:"max_inflight_requests"`
		FanoutConcurrency   int    `yaml:"fanout_concurrency"`
	} `yaml:"server"`
	Security     SecurityConfig                  `yaml:"security"`
	RateLimit    RateLimitConfig                 `yaml:"rate_limit"`
	Metrics      MetricsConfig                   `yaml:"metrics"`
	Destinations map[string]rawDestinationConfig `yaml:"destinations"`
	Endpoints    map[string]struct {
		Enabled               bool                           `yaml:"enabled"`
		Path                  string                         `yaml:"path"`
		DeliveryFailurePolicy string                         `yaml:"delivery_failure_policy"`
		Destinations          []rawEndpointDestinationConfig `yaml:"destinations"`
		MaxBodyBytes          int64                          `yaml:"max_body_bytes"`
		RateLimit             EndpointRateLimitConfig        `yaml:"rate_limit"`
		ReplayProtection      struct {
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
			RemoveBearer       bool   `yaml:"remove_bearer"`
			ExpectedIssuer     string `yaml:"expected_issuer"`
			ExpectedAudience   string `yaml:"expected_audience"`
			BodyHashClaim      string `yaml:"body_hash_claim"`
		} `yaml:"auth"`
		Filters        []rawFilterConditionConfig `yaml:"filters"`
		FilterResponse struct {
			Status int `yaml:"status"`
		} `yaml:"filter_response"`
	} `yaml:"endpoints"`
}

func Load(path string) (Config, error) {
	return NewService().Load(path)
}

type Service struct{}

func NewService() Service {
	return Service{}
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
	return s.loadBytes(rawBytes, path, true)
}

// loadBytes parses, converts, and validates config from raw YAML bytes. source
// names the origin (a file path, or "config" for in-memory bytes) for error
// text. When expandEnv is true, ${VAR} references are expanded against the
// process environment before parsing; when false, they are left as opaque
// literals (see ValidateBytes).
func (s Service) loadBytes(rawBytes []byte, source string, expandEnv bool) (Config, error) {
	raw := string(rawBytes)
	if expandEnv {
		raw = os.ExpandEnv(raw)
	}
	var config rawConfig
	decoder := yaml.NewDecoder(bytes.NewBufferString(raw))
	decoder.KnownFields(true)
	if err := decoder.Decode(&config); err != nil {
		if strings.Contains(err.Error(), "field") && strings.Contains(err.Error(), "not found") {
			return Config{}, fmt.Errorf("unknown or invalid YAML field in %s: %w", source, err)
		}
		return Config{}, fmt.Errorf("invalid YAML syntax in %s: %w", source, err)
	}
	cfg, issues := convert(config)
	if len(issues) > 0 {
		return Config{}, ConfigValidationError{Issues: issues}
	}
	if issues := cfg.ValidationIssues(); len(issues) > 0 {
		return Config{}, ConfigValidationError{Issues: issues}
	}
	return cfg, nil
}

// ValidateBytes validates raw YAML config bytes and returns structured issues.
// A nil slice means the config is valid. Unlike Load, this path does NOT
// expand ${VAR} references against the process environment: the bytes are
// treated as untrusted candidate config (e.g. from the HTTP /validate
// endpoint), and ${VAR} is left as an opaque literal so validating it cannot
// leak process-environment secrets.
func (s Service) ValidateBytes(data []byte) []ValidationIssue {
	_, err := s.loadBytes(data, "config", false)
	return issuesFromError(err)
}

// issuesFromError converts a load error into structured issues. Structured
// ConfigValidationError issues pass through; any other error (YAML syntax,
// unknown field) becomes a single "config" issue carrying the message.
func issuesFromError(err error) []ValidationIssue {
	if err == nil {
		return nil
	}
	var cve ConfigValidationError
	if errors.As(err, &cve) {
		return cve.Issues
	}
	return []ValidationIssue{{Field: "config", Message: err.Error()}}
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
			MaxInflightRequests: raw.Server.MaxInflightRequests, FanoutConcurrency: raw.Server.FanoutConcurrency,
		},
		Security:     raw.Security,
		RateLimit:    raw.RateLimit,
		Metrics:      raw.Metrics,
		Destinations: map[string]DestinationConfig{},
		Endpoints:    map[string]EndpointConfig{},
	}
	for id, rawDest := range raw.Destinations {
		dest, destIssues := convertDestination("destinations."+id, rawDest)
		issues = append(issues, destIssues...)
		cfg.Destinations[id] = dest
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
		destinations, destinationIssues := convertEndpointDestinations("endpoints."+name+".destinations", endpoint.Destinations)
		issues = append(issues, destinationIssues...)
		cfg.Endpoints[name] = EndpointConfig{
			Enabled:               endpoint.Enabled,
			Path:                  endpoint.Path,
			DeliveryFailurePolicy: deliveryFailurePolicy(endpoint.DeliveryFailurePolicy),
			Destinations:          destinations,
			MaxBodyBytes:          endpoint.MaxBodyBytes,
			RateLimit:             endpoint.RateLimit,
			ReplayProtection: ReplayProtectionConfig{
				Enabled: endpoint.ReplayProtection.Enabled, TTL: replayTTL, MaxEntries: endpoint.ReplayProtection.MaxEntries,
			},
			IPAllowlist: endpoint.IPAllowlist,
			Auth: AuthConfig{Type: endpoint.Auth.Type, Scheme: authScheme(endpoint.Auth.Scheme), Secret: endpoint.Auth.Secret,
				SignatureHeader:    endpoint.Auth.SignatureHeader,
				TimestampTolerance: tolerance,
				IdempotencyHeader:  endpoint.Auth.IdempotencyHeader,
				RemoveBearer:       endpoint.Auth.RemoveBearer,
				ExpectedIssuer:     endpoint.Auth.ExpectedIssuer,
				ExpectedAudience:   endpoint.Auth.ExpectedAudience,
				BodyHashClaim:      endpoint.Auth.BodyHashClaim},
			Filters:        convertFilters(endpoint.Filters),
			FilterResponse: FilterResponseConfig{Status: endpoint.FilterResponse.Status},
		}
	}
	return cfg, issues
}
