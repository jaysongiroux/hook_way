package config

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jaysongiroux/hook_way/internal/routing"
)

var safeName = regexp.MustCompile(`^[a-zA-Z0-9._-]{1,128}$`)

// IsSafeName reports whether s is a valid destination id / route target /
// endpoint name: 1-128 characters of letters, digits, dot, underscore, hyphen.
// Exported so the request path can check rendered variable targets.
func IsSafeName(s string) bool { return safeName.MatchString(s) }

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
	Replay       ReplayConfig                 `yaml:"replay"`
	Destinations map[string]DestinationConfig `yaml:"destinations"`
	Endpoints    map[string]EndpointConfig    `yaml:"endpoints"`
}

type ServerConfig struct {
	Addr            string        `yaml:"addr,omitempty"`
	ReadTimeout     time.Duration `yaml:"read_timeout,omitempty"`
	WriteTimeout    time.Duration `yaml:"write_timeout,omitempty"`
	IdleTimeout     time.Duration `yaml:"idle_timeout,omitempty"`
	ShutdownTimeout time.Duration `yaml:"shutdown_timeout,omitempty"`
	// ShutdownDelay withdraws readiness before stopping HTTP admission (default zero).
	ShutdownDelay time.Duration `yaml:"shutdown_delay,omitempty"`
	// ShutdownTotalTimeout bounds withdrawal, request/redrive draining and resource cleanup.
	// Exceeding it terminates the process with an error; unfinished work may be lost.
	ShutdownTotalTimeout time.Duration `yaml:"shutdown_total_timeout,omitempty"`
	// PublishTimeout bounds how long a webhook request's fan-out publish is
	// allowed to run once detached from the inbound request context (see
	// FlowServer publish). Defaults to 10s when omitted,
	// matching the WriteTimeout/ShutdownTimeout defaults so a synchronous
	// publish can't outlive the response write or a graceful shutdown; an
	// explicitly-set non-positive value is rejected at load time. Config
	// validation also rejects a PublishTimeout greater than ShutdownTimeout or
	// WriteTimeout (see ValidationIssues), since either would let a slow sync
	// publish outlive its response write or a graceful shutdown deadline.
	PublishTimeout      time.Duration `yaml:"publish_timeout,omitempty"`
	MaxBodyBytes        int64         `yaml:"max_body_bytes,omitempty"`
	MaxInflightRequests int           `yaml:"max_inflight_requests,omitempty"`
	// MaxPendingRequests bounds HTTP handlers waiting for an execution slot.
	// Both this and AdmissionWaitTimeout must be positive to enable waiting.
	// Bodies are read only after admission; zero retains immediate overload rejection.
	MaxPendingRequests   int           `yaml:"max_pending_requests,omitempty"`
	AdmissionWaitTimeout time.Duration `yaml:"admission_wait_timeout,omitempty"`
	FanoutConcurrency    int           `yaml:"fanout_concurrency,omitempty"`
	// ReadinessInterval is how often the background readiness prober re-checks
	// every destination. Defaults to 10s when omitted; an explicitly-set
	// non-positive value is rejected at load time.
	ReadinessInterval time.Duration `yaml:"readiness_interval,omitempty"`
	// ReadinessTimeout bounds a single readiness probe's destination fan-out.
	// Defaults to 5s when omitted; an explicitly-set non-positive value is
	// rejected at load time.
	ReadinessTimeout time.Duration `yaml:"readiness_timeout,omitempty"`
	// ReadinessRequireAll, when true, makes /ready return 503 if any flow is
	// unready (AND-aggregation). The default is false: /ready stays 200
	// when at least one flow is ready so one bad destination does not take
	// unrelated flows out of rotation (D9).
	ReadinessRequireAll bool `yaml:"readiness_require_all,omitempty"`
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
	// "error". Empty defaults to "info". HOOKWAY_LOG_LEVEL overrides this at
	// process start. See internal/logging.ParseLevel for the values accepted
	// at runtime; ValidationIssues enforces the same enum at config-load time.
	Level string `yaml:"level"`
	// HeaderAllowlist is the request headers that may appear in structured
	// request logs. Empty (the default) logs no request headers. Allowed
	// names are still redacted by the built-in sensitive-header heuristics
	// and security.redacted_header_denylist. Proxy forwarding uses
	// security.forwarded_header_allowlist instead.
	HeaderAllowlist []string `yaml:"header_allowlist"`
}

// Replay defaults.
const (
	DefaultReplayMaxRecordBytes       int64 = 16 << 20
	DefaultReplayControlMaxConcurrent       = 8
)

// ReplayConfig connects core to the optional replay service. With the block
// absent Configured is false, and failure capture and the control listener
// are both off.
type ReplayConfig struct {
	Configured     bool
	URL            string
	CaptureToken   string
	MaxRecordBytes int64 // per artifact; larger artifacts make a metadata-only record
	TLS            ReplayTLSConfig
	Control        ReplayControlConfig
}

// ReplayTLSConfig is core's client TLS for uploads to the replay service.
type ReplayTLSConfig struct {
	CACert             string
	ClientCert         string
	ClientKey          string
	ServerName         string
	InsecureSkipVerify bool
}

// ReplayControlConfig is the private listener replay calls to redrive.
type ReplayControlConfig struct {
	Addr          string
	Token         string
	MaxConcurrent int
}

type rawConfig struct {
	Routing      routing.Config                  `yaml:"routing,omitempty"`
	Server       rawServerConfig                 `yaml:"server"`
	Security     SecurityConfig                  `yaml:"security"`
	RateLimit    RateLimitConfig                 `yaml:"rate_limit"`
	Metrics      MetricsConfig                   `yaml:"metrics"`
	Logging      LoggingConfig                   `yaml:"logging"`
	Replay       *rawReplayConfig                `yaml:"replay"`
	Connections  map[string]rawConnectionConfig  `yaml:"connections"`
	Destinations map[string]rawDestinationConfig `yaml:"destinations"`
	Flows        map[string]flowSchema           `yaml:"flows"`
}

// flowSchema describes one flow for `hookway schema` only; internal/flowconfig
// decodes flows. Node options are per type (hookway schema --nodes).
type flowSchema struct {
	Nodes                 []map[string]any `yaml:"nodes"`
	Edges                 []string         `yaml:"edges"`
	DeliveryFailurePolicy string           `yaml:"delivery_failure_policy,omitempty" jsonschema:"enum=stop_on_failure,enum=skip_failed"`
	CaptureFailures       *bool            `yaml:"capture_failures,omitempty"`
}

// MissingEnvIssues builds one ValidationIssue per referenced-but-unset
// environment variable name (as produced by expandEnvStrict / ExpandEnvLenient
// missing lists). Shared by flowconfig.Load's hard-failure path and by
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

// ExpandEnvStrict is the load-time expander used by LoadBytes
// when environment substitution is requested. Unset variables are reported in
// missing and expanded is empty in that case.
func ExpandEnvStrict(raw string) (expanded string, missing []string) {
	return expandEnvStrict(raw)
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

func ResolvePath(flagValue string) (string, error) {
	if flagValue != "" {
		return flagValue, nil
	}
	if env := os.Getenv("HOOKWAY_CONFIG"); env != "" {
		return env, nil
	}
	return "", errors.New("config path required via --config or HOOKWAY_CONFIG")
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
