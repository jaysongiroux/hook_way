package config

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"os"
	"regexp"
	"sort"
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
	Logging      LoggingConfig                `yaml:"logging"`
	Destinations map[string]DestinationConfig `yaml:"destinations"`
	Endpoints    map[string]EndpointConfig    `yaml:"endpoints"`
}

type ServerConfig struct {
	Addr            string
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	IdleTimeout     time.Duration
	ShutdownTimeout time.Duration
	// PublishTimeout bounds how long a webhook request's fan-out publish is
	// allowed to run once detached from the inbound request context (see
	// internal/httpapi Server.handleWebhook). Defaults to 10s when omitted,
	// matching the WriteTimeout/ShutdownTimeout defaults so a synchronous
	// publish can't outlive the response write or a graceful shutdown; an
	// explicitly-set non-positive value is rejected at load time. Config
	// validation also rejects a PublishTimeout greater than ShutdownTimeout or
	// WriteTimeout (see ValidationIssues), since either would let a slow sync
	// publish outlive its response write or a graceful shutdown deadline.
	PublishTimeout      time.Duration
	MaxBodyBytes        int64
	MaxInflightRequests int
	FanoutConcurrency   int
	// ReadinessInterval is how often the background readiness prober re-checks
	// every destination. Defaults to 10s when omitted; an explicitly-set
	// non-positive value is rejected at load time.
	ReadinessInterval time.Duration
	// ReadinessTimeout bounds a single readiness probe's destination fan-out.
	// Defaults to 5s when omitted; an explicitly-set non-positive value is
	// rejected at load time.
	ReadinessTimeout time.Duration
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

// LoggingConfig configures the process-wide structured logger.
type LoggingConfig struct {
	// Level is the minimum severity logged: "debug", "info", "warn", or
	// "error". Empty defaults to "info". See internal/logging.ParseLevel for
	// the values accepted at runtime; ValidationIssues enforces the same enum
	// at config-load time.
	Level string `yaml:"level"`
}

type rawConfig struct {
	Server struct {
		Addr                string `yaml:"addr"`
		ReadTimeout         string `yaml:"read_timeout"`
		WriteTimeout        string `yaml:"write_timeout"`
		IdleTimeout         string `yaml:"idle_timeout"`
		ShutdownTimeout     string `yaml:"shutdown_timeout"`
		PublishTimeout      string `yaml:"publish_timeout"`
		MaxBodyBytes        int64  `yaml:"max_body_bytes"`
		MaxInflightRequests int    `yaml:"max_inflight_requests"`
		FanoutConcurrency   int    `yaml:"fanout_concurrency"`
		ReadinessInterval   string `yaml:"readiness_interval"`
		ReadinessTimeout    string `yaml:"readiness_timeout"`
	} `yaml:"server"`
	Security     SecurityConfig                  `yaml:"security"`
	RateLimit    RateLimitConfig                 `yaml:"rate_limit"`
	Metrics      MetricsConfig                   `yaml:"metrics"`
	Logging      LoggingConfig                   `yaml:"logging"`
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
		expanded, missing := expandEnvStrict(raw)
		if len(missing) > 0 {
			return Config{}, ConfigValidationError{Issues: MissingEnvIssues(missing)}
		}
		raw = expanded
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

// MissingEnvIssues builds one ValidationIssue per referenced-but-unset
// environment variable name (as produced by expandEnvStrict / ExpandEnvLenient
// missing lists). Shared by Load's hard-failure path and by
// internal/configapi's expand-and-downgrade-to-warning path, so both surfaces
// report the exact same field/message/suggestion text for the same problem.
func MissingEnvIssues(missing []string) []ValidationIssue {
	issues := make([]ValidationIssue, len(missing))
	for i, name := range missing {
		issues[i] = validationError(
			"config.env."+name,
			fmt.Sprintf("environment variable %s is referenced (as ${%s} or $%s) but is not set", name, name, name),
			fmt.Sprintf("export %s before starting Hookway, or remove the reference if it was a typo", name),
		)
	}
	return issues
}

// dollarEscapeSentinel stands in for a literal "$$" escape while
// expandEnvStrict runs os.Expand, then is restored to a single "$" afterward.
// It is built from NUL bytes, which cannot occur in a YAML text file, so it
// can never collide with anything a real config could contain.
const dollarEscapeSentinel = "\x00\x00hookway-literal-dollar\x00\x00"

// expandEnvStrict expands ${VAR} and $VAR references in raw against the
// process environment, and reports every referenced variable that is unset.
//
// This differs from os.ExpandEnv (the previous behavior) in two ways:
//   - An unset variable is a load error, not a silent expansion to "". A
//     typo'd env var name should never result in an empty secret/URL/etc.
//     making it into a running config.
//   - "$$" is treated as an escape for a literal "$", matching shell
//     convention, so values that legitimately contain a dollar sign don't
//     need to route around expansion.
//
// A variable that is set but empty is allowed and expands to "" -- that is
// the operator's explicit choice, not a missing reference.
//
// missing is nil when every reference resolved; expanded is only meaningful
// when missing is empty.
func expandEnvStrict(raw string) (expanded string, missing []string) {
	out, missing := expandEnvCollect(raw, true)
	if len(missing) > 0 {
		return "", missing
	}
	return out, nil
}

// ExpandEnvLenient expands ${VAR} and $VAR references the same way
// expandEnvStrict does, but never fails: every SET variable is substituted
// normally, while a referenced-but-unset variable is left as the opaque
// literal "${NAME}" (rather than expanding to "") AND is reported by name in
// missing, so a caller can decide how to treat it (e.g. a warning instead of
// a hard load error).
//
// Leaving missing references as a non-empty literal — instead of ""  — matters:
// it means a config that is otherwise complete except for one unset secret
// still passes every *other* structural/required-field check, instead of
// tripping a cascade of unrelated "field is required" errors caused by the
// substitution itself. The caller is expected to already know (via missing)
// that the field's real value is unresolved.
//
// It exists for validation paths — such as `validate --json` — that want to
// run every other structural check even when production secrets aren't set in
// the local environment. It is exported for internal/configapi to build an
// expand-then-validate flow on top of internal/config's non-expanding
// ValidateBytes, without ValidateBytes itself ever touching the process
// environment (see ValidateBytes's doc comment).
func ExpandEnvLenient(raw string) (expanded string, missing []string) {
	return expandEnvCollect(raw, false)
}

// expandEnvCollect does the actual ${VAR}/$VAR substitution against the
// process environment. When zeroMissing is true, a missing variable expands
// to "" and the returned expanded string is only meaningful when missing is
// empty (expandEnvStrict's contract). When false, a missing variable is left
// as the literal "${NAME}" and expanded is always meaningful (ExpandEnvLenient's
// contract). Either way, missing is the sorted list of variable names that
// were referenced but unset.
func expandEnvCollect(raw string, zeroMissing bool) (expanded string, missing []string) {
	escaped := strings.ReplaceAll(raw, "$$", dollarEscapeSentinel)

	missingSet := map[string]struct{}{}
	out := os.Expand(escaped, func(name string) string {
		val, ok := os.LookupEnv(name)
		if ok {
			return val
		}
		missingSet[name] = struct{}{}
		if zeroMissing {
			return ""
		}
		return "${" + name + "}"
	})
	out = strings.ReplaceAll(out, dollarEscapeSentinel, "$")
	if len(missingSet) == 0 {
		return out, nil
	}
	names := make([]string, 0, len(missingSet))
	for name := range missingSet {
		names = append(names, name)
	}
	sort.Strings(names)
	return out, names
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

// defaultMaxInflightRequests is the concurrent-request cap applied when
// server.max_inflight_requests is omitted or explicitly 0, so a fresh install
// gets bounded backpressure instead of the historical unbounded behavior.
const defaultMaxInflightRequests = 256

// maxInflightRequestsUnlimited is the sentinel raw value (and the value
// stored on ServerConfig) that disables the in-flight semaphore entirely.
// It is <= 0, so internal/httpapi.Server's `> 0` semaphore-creation check
// continues to work unmodified.
const maxInflightRequestsUnlimited = -1

// maxInflightRequests resolves the raw server.max_inflight_requests value to
// the value stored on ServerConfig: 0 (omitted) defaults to
// defaultMaxInflightRequests; maxInflightRequestsUnlimited (-1) is passed
// through unchanged to disable the limit; any other value (including other
// negatives, which ValidationIssues rejects) passes through as-is.
func maxInflightRequests(raw int) int {
	if raw == 0 {
		return defaultMaxInflightRequests
	}
	return raw
}

func convert(raw rawConfig) (Config, []ValidationIssue) {
	var issues []ValidationIssue
	read, issue := parsePositiveDefaultDuration("server.read_timeout", raw.Server.ReadTimeout, 5*time.Second)
	if issue.Field != "" {
		issues = append(issues, issue)
	}
	write, issue := parsePositiveDefaultDuration("server.write_timeout", raw.Server.WriteTimeout, 10*time.Second)
	if issue.Field != "" {
		issues = append(issues, issue)
	}
	idle, issue := parsePositiveDefaultDuration("server.idle_timeout", raw.Server.IdleTimeout, 60*time.Second)
	if issue.Field != "" {
		issues = append(issues, issue)
	}
	shutdown, issue := parsePositiveDefaultDuration("server.shutdown_timeout", raw.Server.ShutdownTimeout, 10*time.Second)
	if issue.Field != "" {
		issues = append(issues, issue)
	}
	publish, issue := parsePositiveDefaultDuration("server.publish_timeout", raw.Server.PublishTimeout, 10*time.Second)
	if issue.Field != "" {
		issues = append(issues, issue)
	}
	readinessInterval, issue := parsePositiveDefaultDuration("server.readiness_interval", raw.Server.ReadinessInterval, 10*time.Second)
	if issue.Field != "" {
		issues = append(issues, issue)
	}
	readinessTimeout, issue := parsePositiveDefaultDuration("server.readiness_timeout", raw.Server.ReadinessTimeout, 5*time.Second)
	if issue.Field != "" {
		issues = append(issues, issue)
	}
	cfg := Config{
		Server: ServerConfig{
			Addr: raw.Server.Addr, ReadTimeout: read, WriteTimeout: write,
			IdleTimeout: idle, ShutdownTimeout: shutdown, PublishTimeout: publish, MaxBodyBytes: raw.Server.MaxBodyBytes,
			MaxInflightRequests: maxInflightRequests(raw.Server.MaxInflightRequests), FanoutConcurrency: raw.Server.FanoutConcurrency,
			ReadinessInterval: readinessInterval, ReadinessTimeout: readinessTimeout,
		},
		Security:     raw.Security,
		RateLimit:    raw.RateLimit,
		Metrics:      raw.Metrics,
		Logging:      raw.Logging,
		Destinations: map[string]DestinationConfig{},
		Endpoints:    map[string]EndpointConfig{},
	}
	for id, rawDest := range raw.Destinations {
		dest, destIssues := convertDestination("destinations."+id, rawDest)
		issues = append(issues, destIssues...)
		cfg.Destinations[id] = dest
	}
	for name, endpoint := range raw.Endpoints {
		tolerance, issue := parsePositiveDefaultDuration("endpoints."+name+".auth.timestamp_tolerance", endpoint.Auth.TimestampTolerance, 5*time.Minute)
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
