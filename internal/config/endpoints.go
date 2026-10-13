package config

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jaysongiroux/hook_way/internal/vars"
)

const (
	DeliveryFailurePolicyStopOnFailure = "stop_on_failure"
	DeliveryFailurePolicySkipFailed    = "skip_failed"
)

type SQSEndpointOptions struct {
	QueueOwnerAccountID string
	VisibilityTimeout   time.Duration
	MessageRetention    time.Duration
}

type SNSEndpointOptions struct {
	TopicARN string
	Subject  string
}

// RedisEndpointOptions are per-route options for redis_* destinations.
// Not all fields apply to all redis types — validation enforces the coupling.
type RedisEndpointOptions struct {
	MaxLen        int64
	ConsumerGroup string
	TTL           time.Duration
	KeyTemplate   string
}

// KafkaEndpointOptions are per-route options for kafka destinations.
type KafkaEndpointOptions struct {
	PartitionKey      KafkaPartitionKeyConfig
	Partitions        int
	ReplicationFactor int
}

// KafkaPartitionKeyConfig derives the Kafka message key (which selects the
// partition). Mirrors the FIFO group_id shape: source is static | endpoint |
// header | body, with an optional fallback ("endpoint" or a literal).
type KafkaPartitionKeyConfig struct {
	Source   string
	Value    string
	Path     string
	Fallback string
}

// TemporalEndpointOptions are per-route options for temporal destinations.
// The struct is compared with != in validation, so it must stay comparable.
// Input "" means envelope; IDReusePolicy "" means the server default.
type TemporalEndpointOptions struct {
	WorkflowType     string
	Input            string // "" | envelope | body
	WorkflowID       TemporalWorkflowIDConfig
	IDReusePolicy    string // "" | allow_duplicate | allow_duplicate_failed_only | reject_duplicate | terminate_if_running
	ExecutionTimeout time.Duration
	RunTimeout       time.Duration
	TaskTimeout      time.Duration
}

// TemporalWorkflowIDConfig derives the workflow id. Source mirrors the FIFO
// group_id shape (static | endpoint | header | body); an empty Source or an
// unresolved value falls back to the hookway event id. Prefix is prepended to
// whatever was resolved.
type TemporalWorkflowIDConfig struct {
	Source string
	Value  string
	Path   string
	Prefix string
}

type FIFOConfig struct {
	GroupID FIFOGroupIDConfig
	Dedup   FIFODedupConfig
}

type FIFOGroupIDConfig struct {
	Source   string // static | endpoint | header | body
	Value    string // used when Source == "static"
	Path     string // used when Source == "header" | "body"
	Fallback string // literal value, or "endpoint"
}

type FIFODedupConfig struct {
	UseIdempotencyKey    bool
	ContentBasedFallback bool
}

type EndpointConfig struct {
	Enabled               bool                        `yaml:"enabled"`
	Path                  string                      `yaml:"path"`
	DeliveryFailurePolicy string                      `yaml:"delivery_failure_policy"`
	Destinations          []EndpointDestinationConfig `yaml:"destinations"`
	MaxBodyBytes          int64                       `yaml:"max_body_bytes"`
	RateLimit             EndpointRateLimitConfig     `yaml:"rate_limit"`
	ReplayProtection      ReplayProtectionConfig      `yaml:"replay_protection"`
	IPAllowlist           []string                    `yaml:"ip_allowlist"`
	Auth                  AuthConfig                  `yaml:"auth"`
	Filters               []FilterConditionConfig     `yaml:"filters"`
	FilterResponse        FilterResponseConfig        `yaml:"filter_response"`
	Variables             map[string]VariableConfig   `yaml:"variables"`
	OnUnresolvedVariable  *FallbackRouteConfig        `yaml:"on_unresolved_variable"`
}

// VariableConfig declares one per-request routing variable. Exactly one of
// Values or Pattern is set (validation enforces). Path is the header name,
// dotted body path, query key, or endpoint path segment name; empty means the
// variable's own name.
type VariableConfig struct {
	Source  string
	Path    string
	Values  []string
	Pattern string
	Default string
	// Lookup is set when Source is "lookup": the key extraction and provider.
	Lookup *LookupConfig
}

// LookupConfig configures a lookup variable: how the key is read from the
// request, which provider resolves it, and how results are cached.
type LookupConfig struct {
	Key      VariableKeyConfig
	Provider string // dynamodb | http | postgres
	Timeout  time.Duration
	Cache    LookupCacheConfig
	DynamoDB *LookupDynamoDBConfig
	HTTP     *LookupHTTPConfig
	Postgres *LookupPostgresConfig
}

// VariableKeyConfig extracts the lookup key from the request (same source
// model as a plain variable, minus lookup itself).
type VariableKeyConfig struct {
	Source string // header | body | query | path
	Path   string
}

type LookupCacheConfig struct {
	TTL         time.Duration
	NegativeTTL time.Duration
	MaxEntries  int
}

type LookupDynamoDBConfig struct {
	Table           string
	Index           string
	KeyAttribute    string
	FilterAttribute string
	FilterValue     string
	ValueAttribute  string
	ConsistentRead  bool
	Region          string
	Endpoint        string
	AccessKeyID     string
	SecretAccessKey string
}

type LookupHTTPConfig struct {
	URL       string
	Method    string
	Headers   map[string]string
	ValuePath string
}

type LookupPostgresConfig struct {
	URL   string
	Query string
}

// FallbackRouteConfig is the endpoint-level sink for events whose variables
// could not be resolved. Target is static (no placeholders).
type FallbackRouteConfig struct {
	Destination string
	Target      string
}

// ReservedVariableNames are the redis_kv key_template tokens; a variable may
// not reuse them so operators never confuse the two substitution layers.
var ReservedVariableNames = map[string]struct{}{
	"prefix": {}, "target": {}, "endpoint": {}, "id": {}, "idempotency_key": {},
}

// MaxExpandedRoutes caps how many concrete routes one endpoint may expand to
// (Cartesian product of its enumerated variables, plus the fallback route).
const MaxExpandedRoutes = 256

// ExpandedRoute is one provisionable route derived from an authored
// destination after enumerated variables are substituted. The embedded
// EndpointDestinationConfig has Target rendered for enumerated bindings;
// Dynamic routes still contain {pattern-variable} placeholders and are
// rendered per request.
type ExpandedRoute struct {
	EndpointDestinationConfig
	Index    int               // index into EndpointConfig.Destinations; -1 for the fallback route
	Template string            // authored target (metrics `queue` label)
	Bindings map[string]string // enumerated variable -> value fixed by this expansion; nil when none
	Dynamic  bool              // Target still contains placeholders
}

type EndpointDestinationConfig struct {
	BrokerAttributes map[string]string       `yaml:"broker_attributes,omitempty"`
	Destination      string                  `yaml:"destination"`
	Target           string                  `yaml:"target"`
	Create           bool                    `yaml:"create"`
	QueueOptions     RabbitMQQueueOptions    `yaml:"queue_options"`
	SQSOptions       SQSEndpointOptions      `yaml:"sqs_options"`
	SNSOptions       SNSEndpointOptions      `yaml:"sns_options"`
	RedisOptions     RedisEndpointOptions    `yaml:"redis_options"`
	KafkaOptions     KafkaEndpointOptions    `yaml:"kafka_options"`
	TemporalOptions  TemporalEndpointOptions `yaml:"temporal_options"`
	FIFO             *FIFOConfig             `yaml:"fifo"`
	Filters          []FilterConditionConfig `yaml:"filters"`
}

// FilterConditionConfig is one condition in an endpoint or route filter chain.
// All conditions in a chain must pass (AND) for the payload to proceed. Value
// carries the raw YAML scalar (string/int/float/bool/nil); the filter package
// decides the comparison type from the operator and value.
type FilterConditionConfig struct {
	Source   string `yaml:"source"`
	Path     string `yaml:"path"`
	Operator string `yaml:"operator"`
	Value    any    `yaml:"value"`
}

// FilterResponseConfig controls how an endpoint responds when its filter chain
// rejects a payload. Status 0 (the default) means a silent 202 ack with no
// destinations; a non-2xx status (e.g. 422) is returned to the sender instead.
type FilterResponseConfig struct {
	Status int `yaml:"status"`
}

// defaultFilterResponseStatus is returned when no filter_response.status is set.
const defaultFilterResponseStatus = 202

// FilterResponseStatus returns the configured non-match status, or 202 default.
func (e EndpointConfig) FilterResponseStatus() int {
	if e.FilterResponse.Status == 0 {
		return defaultFilterResponseStatus
	}
	return e.FilterResponse.Status
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
	RemoveBearer       bool
	ExpectedIssuer     string
	ExpectedAudience   string
	BodyHashClaim      string
	AllowUnboundBody   bool
}

// VariableSpecs compiles the endpoint's variables into vars.Spec values sorted
// by name. It errors only when a pattern fails to compile; validation reports
// that earlier, so callers that bypass Load must treat an error as fail-closed.
func (e EndpointConfig) VariableSpecs() ([]vars.Spec, error) {
	names := make([]string, 0, len(e.Variables))
	for name := range e.Variables {
		names = append(names, name)
	}
	sort.Strings(names)
	specs := make([]vars.Spec, 0, len(names))
	for _, name := range names {
		v := e.Variables[name]
		spec := vars.Spec{Name: name, Source: vars.Source(v.Source), Path: v.Path, Values: v.Values, Default: v.Default}
		if spec.Path == "" {
			spec.Path = name
		}
		if v.Lookup != nil {
			spec.KeySource = vars.Source(v.Lookup.Key.Source)
			spec.KeyPath = v.Lookup.Key.Path
		}
		if v.Pattern != "" {
			re, err := regexp.Compile(v.Pattern)
			if err != nil {
				return nil, fmt.Errorf("variable %q: %w", name, err)
			}
			spec.Pattern = re
		}
		specs = append(specs, spec)
	}
	return specs, nil
}

// lookupAnyPattern is the implicit pattern of a lookup variable that declares
// neither values nor pattern: the provider is trusted, so any value the
// request-time resolver accepts (a safe name) is a valid target segment.
const lookupAnyPattern = "^[a-zA-Z0-9._-]{1,128}$"

// PatternSources returns the regex source of every open-set variable, keyed
// by name: declared patterns, plus the implicit safe-name pattern of a lookup
// variable with neither values nor pattern. The queue layer uses it to compile
// target matchers for dynamic routes.
func (e EndpointConfig) PatternSources() map[string]string {
	var out map[string]string
	for name, v := range e.Variables {
		src := v.Pattern
		if src == "" {
			if v.Source != string(vars.SourceLookup) || len(v.Values) > 0 {
				continue
			}
			src = lookupAnyPattern
		}
		if out == nil {
			out = map[string]string{}
		}
		out[name] = src
	}
	return out
}

// FallbackRoute returns the on_unresolved_variable route, when configured.
func (e EndpointConfig) FallbackRoute() (EndpointDestinationConfig, bool) {
	if e.OnUnresolvedVariable == nil {
		return EndpointDestinationConfig{}, false
	}
	return EndpointDestinationConfig{Destination: e.OnUnresolvedVariable.Destination, Target: e.OnUnresolvedVariable.Target}, true
}

// DestinationRoutes returns every provisionable route of the endpoint:
// authored destinations with enumerated variables expanded (Cartesian
// product, declaration order of values, variables in order of appearance in
// the target), followed by the fallback route with Index -1. Without
// variables the result is one plain route per authored destination.
func (e EndpointConfig) DestinationRoutes() []ExpandedRoute {
	routes := make([]ExpandedRoute, 0, len(e.Destinations)+1)
	for i, d := range e.Destinations {
		tpl, err := vars.Parse(d.Target)
		if err != nil || !tpl.HasPlaceholders() {
			routes = append(routes, ExpandedRoute{EndpointDestinationConfig: d, Index: i, Template: d.Target})
			continue
		}
		var enumerated []string
		for _, name := range tpl.Names() {
			if len(e.Variables[name].Values) > 0 {
				enumerated = append(enumerated, name)
			}
		}
		for _, combo := range e.cartesian(enumerated) {
			partial := tpl.RenderPartial(combo)
			r := d
			r.Target = partial.String()
			routes = append(routes, ExpandedRoute{EndpointDestinationConfig: r, Index: i, Template: d.Target, Bindings: combo, Dynamic: partial.HasPlaceholders()})
			if len(routes) > MaxExpandedRoutes {
				return routes
			}
		}
	}
	if fb, ok := e.FallbackRoute(); ok {
		routes = append(routes, ExpandedRoute{EndpointDestinationConfig: fb, Index: -1, Template: fb.Target})
	}
	return routes
}

// cartesian enumerates every combination of values for the named enumerated
// variables. With no names it yields a single nil binding so a template with
// only pattern variables still produces one (dynamic) route.
func (e EndpointConfig) cartesian(names []string) []map[string]string {
	combos := []map[string]string{nil}
	for _, name := range names {
		values := e.Variables[name].Values
		next := make([]map[string]string, 0, len(combos)*len(values))
		for _, base := range combos {
			for _, v := range values {
				m := make(map[string]string, len(base)+1)
				for k, bv := range base {
					m[k] = bv
				}
				m[name] = v
				next = append(next, m)
			}
		}
		combos = next
	}
	return combos
}

func (e EndpointConfig) EffectiveDeliveryFailurePolicy() string {
	if e.DeliveryFailurePolicy == "" {
		return DeliveryFailurePolicyStopOnFailure
	}
	return e.DeliveryFailurePolicy
}

func (e EndpointConfig) DestinationIDs() []string {
	ids := make([]string, 0, len(e.Destinations))
	for _, destination := range e.Destinations {
		ids = append(ids, destination.Destination)
	}
	return ids
}

func (e EndpointConfig) DestinationLabel() string {
	return strings.Join(e.DestinationIDs(), ",")
}

func (e EndpointConfig) TargetLabel() string {
	targets := make([]string, 0, len(e.Destinations))
	for _, destination := range e.Destinations {
		targets = append(targets, destination.Target)
	}
	return strings.Join(targets, ",")
}

func (s ServerConfig) FanoutConcurrencyLimit() int {
	if s.FanoutConcurrency <= 0 {
		return 8
	}
	return s.FanoutConcurrency
}
