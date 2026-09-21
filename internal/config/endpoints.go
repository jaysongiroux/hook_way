package config

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jasongiroux/hookway/internal/auth"
	"github.com/jasongiroux/hookway/internal/vars"
)

const (
	DeliveryFailurePolicyStopOnFailure = "stop_on_failure"
	DeliveryFailurePolicySkipFailed    = "skip_failed"
)

// authScheme defaults the per-endpoint signature scheme to the timestamped
// HMAC-SHA256 colon scheme when omitted.
func authScheme(scheme string) string {
	if scheme == "" {
		return auth.SchemeHMACSHA256TSColon
	}
	return scheme
}

func deliveryFailurePolicy(policy string) string {
	if policy == "" {
		return DeliveryFailurePolicyStopOnFailure
	}
	return policy
}

type SQSEndpointOptions struct {
	VisibilityTimeout time.Duration
	MessageRetention  time.Duration
}

type SNSEndpointOptions struct {
	Subject string
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
	Destination     string                  `yaml:"destination"`
	Target          string                  `yaml:"target"`
	Create          bool                    `yaml:"create"`
	QueueOptions    RabbitMQQueueOptions    `yaml:"queue_options"`
	SQSOptions      SQSEndpointOptions      `yaml:"sqs_options"`
	SNSOptions      SNSEndpointOptions      `yaml:"sns_options"`
	RedisOptions    RedisEndpointOptions    `yaml:"redis_options"`
	KafkaOptions    KafkaEndpointOptions    `yaml:"kafka_options"`
	TemporalOptions TemporalEndpointOptions `yaml:"temporal_options"`
	FIFO            *FIFOConfig             `yaml:"fifo"`
	Filters         []FilterConditionConfig `yaml:"filters"`
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

type rawFIFOConfig struct {
	GroupID struct {
		Source   string `yaml:"source"`
		Value    string `yaml:"value"`
		Path     string `yaml:"path"`
		Fallback string `yaml:"fallback"`
	} `yaml:"group_id"`
	Dedup struct {
		UseIdempotencyKey    bool `yaml:"use_idempotency_key"`
		ContentBasedFallback bool `yaml:"content_based_fallback"`
	} `yaml:"dedup"`
}

type rawEndpointDestinationConfig struct {
	Destination  string                  `yaml:"destination"`
	Target       string                  `yaml:"target"`
	Create       bool                    `yaml:"create"`
	QueueOptions rawRabbitMQQueueOptions `yaml:"queue_options"`
	SQSOptions   struct {
		VisibilityTimeout string `yaml:"visibility_timeout"`
		MessageRetention  string `yaml:"message_retention"`
	} `yaml:"sqs_options"`
	SNSOptions struct {
		Subject string `yaml:"subject"`
	} `yaml:"sns_options"`
	RedisOptions struct {
		MaxLen        int64  `yaml:"max_len"`
		ConsumerGroup string `yaml:"consumer_group"`
		TTL           string `yaml:"ttl"`
		KeyTemplate   string `yaml:"key_template"`
	} `yaml:"redis_options"`
	KafkaOptions struct {
		PartitionKey struct {
			Source   string `yaml:"source"`
			Value    string `yaml:"value"`
			Path     string `yaml:"path"`
			Fallback string `yaml:"fallback"`
		} `yaml:"partition_key"`
		Partitions        int `yaml:"partitions"`
		ReplicationFactor int `yaml:"replication_factor"`
	} `yaml:"kafka_options"`
	TemporalOptions struct {
		WorkflowType string `yaml:"workflow_type"`
		Input        string `yaml:"input"`
		WorkflowID   struct {
			Source string `yaml:"source"`
			Value  string `yaml:"value"`
			Path   string `yaml:"path"`
			Prefix string `yaml:"prefix"`
		} `yaml:"workflow_id"`
		IDReusePolicy    string `yaml:"id_reuse_policy"`
		ExecutionTimeout string `yaml:"execution_timeout"`
		RunTimeout       string `yaml:"run_timeout"`
		TaskTimeout      string `yaml:"task_timeout"`
	} `yaml:"temporal_options"`
	FIFO    *rawFIFOConfig             `yaml:"fifo"`
	Filters []rawFilterConditionConfig `yaml:"filters"`
}

type rawFilterConditionConfig struct {
	Source   string `yaml:"source"`
	Path     string `yaml:"path"`
	Operator string `yaml:"operator"`
	Value    any    `yaml:"value"`
}

func convertFilters(raw []rawFilterConditionConfig) []FilterConditionConfig {
	if len(raw) == 0 {
		return nil
	}
	filters := make([]FilterConditionConfig, 0, len(raw))
	for _, f := range raw {
		filters = append(filters, FilterConditionConfig(f))
	}
	return filters
}

func convertEndpointDestinations(fieldPrefix string, raw []rawEndpointDestinationConfig) ([]EndpointDestinationConfig, []ValidationIssue) {
	var issues []ValidationIssue
	destinations := make([]EndpointDestinationConfig, 0, len(raw))
	for i, destination := range raw {
		queueOptions, queueOptionIssues := convertQueueOptions(fmt.Sprintf("%s[%d].queue_options", fieldPrefix, i), destination.QueueOptions)
		issues = append(issues, queueOptionIssues...)

		visibilityTimeout, issue := parsePositiveDuration(fmt.Sprintf("%s[%d].sqs_options.visibility_timeout", fieldPrefix, i), destination.SQSOptions.VisibilityTimeout)
		if issue.Field != "" {
			issues = append(issues, issue)
		}
		messageRetention, issue := parsePositiveDuration(fmt.Sprintf("%s[%d].sqs_options.message_retention", fieldPrefix, i), destination.SQSOptions.MessageRetention)
		if issue.Field != "" {
			issues = append(issues, issue)
		}

		redisTTL, issue := parsePositiveDuration(fmt.Sprintf("%s[%d].redis_options.ttl", fieldPrefix, i), destination.RedisOptions.TTL)
		if issue.Field != "" {
			issues = append(issues, issue)
		}

		temporalExecutionTimeout, issue := parsePositiveDuration(fmt.Sprintf("%s[%d].temporal_options.execution_timeout", fieldPrefix, i), destination.TemporalOptions.ExecutionTimeout)
		if issue.Field != "" {
			issues = append(issues, issue)
		}
		temporalRunTimeout, issue := parsePositiveDuration(fmt.Sprintf("%s[%d].temporal_options.run_timeout", fieldPrefix, i), destination.TemporalOptions.RunTimeout)
		if issue.Field != "" {
			issues = append(issues, issue)
		}
		temporalTaskTimeout, issue := parsePositiveDuration(fmt.Sprintf("%s[%d].temporal_options.task_timeout", fieldPrefix, i), destination.TemporalOptions.TaskTimeout)
		if issue.Field != "" {
			issues = append(issues, issue)
		}

		destinations = append(destinations, EndpointDestinationConfig{
			Destination:  destination.Destination,
			Target:       destination.Target,
			Create:       destination.Create,
			QueueOptions: queueOptions,
			SQSOptions: SQSEndpointOptions{
				VisibilityTimeout: visibilityTimeout,
				MessageRetention:  messageRetention,
			},
			SNSOptions: SNSEndpointOptions{
				Subject: destination.SNSOptions.Subject,
			},
			RedisOptions: RedisEndpointOptions{
				MaxLen:        destination.RedisOptions.MaxLen,
				ConsumerGroup: destination.RedisOptions.ConsumerGroup,
				TTL:           redisTTL,
				KeyTemplate:   destination.RedisOptions.KeyTemplate,
			},
			KafkaOptions: KafkaEndpointOptions{
				PartitionKey: KafkaPartitionKeyConfig{
					Source:   destination.KafkaOptions.PartitionKey.Source,
					Value:    destination.KafkaOptions.PartitionKey.Value,
					Path:     destination.KafkaOptions.PartitionKey.Path,
					Fallback: destination.KafkaOptions.PartitionKey.Fallback,
				},
				Partitions:        destination.KafkaOptions.Partitions,
				ReplicationFactor: destination.KafkaOptions.ReplicationFactor,
			},
			TemporalOptions: TemporalEndpointOptions{
				WorkflowType: destination.TemporalOptions.WorkflowType,
				Input:        destination.TemporalOptions.Input,
				WorkflowID: TemporalWorkflowIDConfig{
					Source: destination.TemporalOptions.WorkflowID.Source,
					Value:  destination.TemporalOptions.WorkflowID.Value,
					Path:   destination.TemporalOptions.WorkflowID.Path,
					Prefix: destination.TemporalOptions.WorkflowID.Prefix,
				},
				IDReusePolicy:    destination.TemporalOptions.IDReusePolicy,
				ExecutionTimeout: temporalExecutionTimeout,
				RunTimeout:       temporalRunTimeout,
				TaskTimeout:      temporalTaskTimeout,
			},
			FIFO:    convertFIFO(destination.FIFO),
			Filters: convertFilters(destination.Filters),
		})
	}
	return destinations, issues
}

func convertFIFO(raw *rawFIFOConfig) *FIFOConfig {
	if raw == nil {
		return nil
	}
	return &FIFOConfig{
		GroupID: FIFOGroupIDConfig{
			Source: raw.GroupID.Source, Value: raw.GroupID.Value,
			Path: raw.GroupID.Path, Fallback: raw.GroupID.Fallback,
		},
		Dedup: FIFODedupConfig{
			UseIdempotencyKey:    raw.Dedup.UseIdempotencyKey,
			ContentBasedFallback: raw.Dedup.ContentBasedFallback,
		},
	}
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

func convertVariables(fieldPrefix string, raw map[string]rawVariableConfig) (map[string]VariableConfig, []ValidationIssue) {
	if len(raw) == 0 {
		return nil, nil
	}
	var issues []ValidationIssue
	out := make(map[string]VariableConfig, len(raw))
	for name, v := range raw {
		path := v.Path
		if path == "" {
			path = name // header name / body path / query key / path segment defaults to the variable name
		}
		vc := VariableConfig{Source: v.Source, Path: path, Values: v.Values, Pattern: v.Pattern, Default: v.Default}
		if v.Lookup != nil {
			lc, lookupIssues := convertLookup(fieldPrefix+"."+name+".lookup", name, *v.Lookup)
			issues = append(issues, lookupIssues...)
			vc.Lookup = &lc
		}
		out[name] = vc
	}
	return out, issues
}

func convertLookup(fieldPrefix, name string, raw rawLookupConfig) (LookupConfig, []ValidationIssue) {
	var issues []ValidationIssue
	timeout, issue := parsePositiveDefaultDuration(fieldPrefix+".timeout", raw.Timeout, 2*time.Second)
	if issue.Field != "" {
		issues = append(issues, issue)
	}
	ttl, issue := parsePositiveDefaultDuration(fieldPrefix+".cache.ttl", raw.Cache.TTL, 5*time.Minute)
	if issue.Field != "" {
		issues = append(issues, issue)
	}
	negTTL, issue := parseDuration(fieldPrefix+".cache.negative_ttl", raw.Cache.NegativeTTL, 0)
	if issue.Field != "" {
		issues = append(issues, issue)
	}
	keyPath := raw.Key.Path
	if keyPath == "" {
		keyPath = name
	}
	lc := LookupConfig{
		Key:      VariableKeyConfig{Source: raw.Key.Source, Path: keyPath},
		Provider: raw.Provider,
		Timeout:  timeout,
		Cache:    LookupCacheConfig{TTL: ttl, NegativeTTL: negTTL, MaxEntries: raw.Cache.MaxEntries},
	}
	if raw.DynamoDB != nil {
		d := raw.DynamoDB
		lc.DynamoDB = &LookupDynamoDBConfig{
			Table: d.Table, Index: d.Index, KeyAttribute: d.KeyAttribute,
			FilterAttribute: d.Filter.Attribute, FilterValue: d.Filter.Value,
			ValueAttribute: d.ValueAttribute, ConsistentRead: d.ConsistentRead,
			Region: d.Region, Endpoint: d.Endpoint, AccessKeyID: d.AccessKeyID, SecretAccessKey: d.SecretAccessKey,
		}
	}
	if raw.HTTP != nil {
		lc.HTTP = &LookupHTTPConfig{URL: raw.HTTP.URL, Method: raw.HTTP.Method, Headers: raw.HTTP.Headers, ValuePath: raw.HTTP.ValuePath}
	}
	if raw.Postgres != nil {
		lc.Postgres = &LookupPostgresConfig{URL: raw.Postgres.URL, Query: raw.Postgres.Query}
	}
	return lc, issues
}

func convertFallback(raw *rawFallbackRouteConfig) *FallbackRouteConfig {
	if raw == nil {
		return nil
	}
	return &FallbackRouteConfig{Destination: raw.Destination, Target: raw.Target}
}

type rawVariableConfig struct {
	Source  string           `yaml:"source"`
	Path    string           `yaml:"path"`
	Values  []string         `yaml:"values"`
	Pattern string           `yaml:"pattern"`
	Default string           `yaml:"default"`
	Lookup  *rawLookupConfig `yaml:"lookup"`
}

type rawLookupConfig struct {
	Key struct {
		Source string `yaml:"source"`
		Path   string `yaml:"path"`
	} `yaml:"key"`
	Provider string `yaml:"provider"`
	Timeout  string `yaml:"timeout"`
	Cache    struct {
		TTL         string `yaml:"ttl"`
		NegativeTTL string `yaml:"negative_ttl"`
		MaxEntries  int    `yaml:"max_entries"`
	} `yaml:"cache"`
	DynamoDB *struct {
		Table        string `yaml:"table"`
		Index        string `yaml:"index"`
		KeyAttribute string `yaml:"key_attribute"`
		Filter       struct {
			Attribute string `yaml:"attribute"`
			Value     string `yaml:"value"`
		} `yaml:"filter"`
		ValueAttribute  string `yaml:"value_attribute"`
		ConsistentRead  bool   `yaml:"consistent_read"`
		Region          string `yaml:"region"`
		Endpoint        string `yaml:"endpoint"`
		AccessKeyID     string `yaml:"access_key_id"`
		SecretAccessKey string `yaml:"secret_access_key"`
	} `yaml:"dynamodb"`
	HTTP *struct {
		URL       string            `yaml:"url"`
		Method    string            `yaml:"method"`
		Headers   map[string]string `yaml:"headers"`
		ValuePath string            `yaml:"value_path"`
	} `yaml:"http"`
	Postgres *struct {
		URL   string `yaml:"url"`
		Query string `yaml:"query"`
	} `yaml:"postgres"`
}

type rawFallbackRouteConfig struct {
	Destination string `yaml:"destination"`
	Target      string `yaml:"target"`
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
