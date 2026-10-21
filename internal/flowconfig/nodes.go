// Package flowconfig defines Hookway's schema-version 2 node catalog.
package flowconfig

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/jaysongiroux/hook_way/internal/auth"
	"github.com/jaysongiroux/hook_way/internal/featureflags"
	"github.com/jaysongiroux/hook_way/internal/graph"
	"github.com/jaysongiroux/hook_way/internal/protection"
	"github.com/jaysongiroux/hook_way/internal/routing"
	"github.com/jaysongiroux/hook_way/internal/transform"
	"github.com/jaysongiroux/hook_way/internal/vars"
)

var ErrAuthConfiguration = errors.New("invalid authentication configuration")

var errNotImplemented = errors.New("flowconfig: node behavior not implemented")

type HTTPTriggerOptions struct {
	Path    string   `yaml:"path" json:"path" jsonschema:"required"`
	Methods []string `yaml:"methods,omitempty" json:"methods,omitempty"`
}
type LoopGuardOptions struct {
	MaxHops int `yaml:"max_hops,omitempty" json:"max_hops,omitempty" jsonschema:"minimum=0"`
}
type IPAllowlistOptions struct {
	CIDRs []string `yaml:"cidrs" json:"cidrs" jsonschema:"required,minItems=1"`
}
type RateLimitOptions struct {
	Store    string `yaml:"store" json:"store" jsonschema:"required"`
	PolicyID string `yaml:"policy_id,omitempty" json:"policy_id,omitempty"`
	Stage    string `yaml:"stage,omitempty" json:"stage,omitempty" jsonschema:"enum=request_head,enum=verified"`
	Strategy string `yaml:"strategy,omitempty" json:"strategy,omitempty" jsonschema:"enum=token_bucket,enum=sliding_window_counter"`
	Limit    int    `yaml:"limit,omitempty" json:"limit,omitempty" jsonschema:"minimum=1"`
	Window   string `yaml:"window,omitempty" json:"window,omitempty"`
	RPM      int    `yaml:"rpm,omitempty" json:"rpm,omitempty" jsonschema:"minimum=1"`
	Burst    int    `yaml:"burst,omitempty" json:"burst,omitempty" jsonschema:"minimum=0"`
	KeyBy    string `yaml:"key_by,omitempty" json:"key_by,omitempty"`
	MaxKeys  *int   `yaml:"max_keys,omitempty" json:"max_keys,omitempty" jsonschema:"minimum=1"`
}
type ReadBodyOptions struct {
	AllowEmpty bool  `yaml:"allow_empty,omitempty" json:"allow_empty,omitempty"`
	MaxBytes   int64 `yaml:"max_bytes" json:"max_bytes" jsonschema:"required,minimum=1"`
}
type SecretRefOptions struct {
	Selection string `yaml:"selection,omitempty" json:"selection,omitempty" jsonschema:"enum=exact,enum=newest_matching"`
	JSONField string `yaml:"json_field,omitempty" json:"json_field,omitempty"`
	Provider  string `yaml:"provider" json:"provider" jsonschema:"required,enum=env,enum=file,enum=aws_secrets_manager"`
	Name      string `yaml:"name" json:"name" jsonschema:"required"`
	Refresh   string `yaml:"refresh,omitempty" json:"refresh,omitempty"`
	Overlap   string `yaml:"overlap,omitempty" json:"overlap,omitempty"`
}
type AuthSourceOptions struct {
	Kind string `yaml:"kind" json:"kind" jsonschema:"required,enum=header,enum=query"`
	Key  string `yaml:"key" json:"key" jsonschema:"required"`
}
type AuthOptions struct {
	CredentialID       string             `yaml:"credential_id,omitempty" json:"credential_id,omitempty"`
	TenantClaim        string             `yaml:"tenant_claim,omitempty" json:"tenant_claim,omitempty"`
	TimestampHeader    string             `yaml:"timestamp_header,omitempty" json:"timestamp_header,omitempty"`
	TimestampUnit      string             `yaml:"timestamp_unit,omitempty" json:"timestamp_unit,omitempty" jsonschema:"enum=seconds,enum=milliseconds"`
	TimestampPolicy    string             `yaml:"timestamp_policy,omitempty" json:"timestamp_policy,omitempty" jsonschema:"enum=symmetric,enum=age,enum=unchecked"`
	BodyRepresentation string             `yaml:"body_representation,omitempty" json:"body_representation,omitempty" jsonschema:"enum=raw,enum=json_js"`
	SignatureLabel     string             `yaml:"signature_label,omitempty" json:"signature_label,omitempty"`
	Scheme             string             `yaml:"scheme" json:"scheme" jsonschema:"required"`
	Secret             string             `yaml:"secret,omitempty" json:"secret,omitempty"`
	SecretRef          *SecretRefOptions  `yaml:"secret_ref,omitempty" json:"secret_ref,omitempty"`
	SignatureHeader    string             `yaml:"signature_header,omitempty" json:"signature_header,omitempty"`
	Source             *AuthSourceOptions `yaml:"source,omitempty" json:"source,omitempty"`
	TimestampTolerance string             `yaml:"timestamp_tolerance,omitempty" json:"timestamp_tolerance,omitempty"`
	RemovePrefix       string             `yaml:"remove_prefix,omitempty" json:"remove_prefix,omitempty"`
	RemoveBearer       bool               `yaml:"remove_bearer,omitempty" json:"remove_bearer,omitempty"`
	ExpectedIssuer     string             `yaml:"expected_issuer,omitempty" json:"expected_issuer,omitempty"`
	ExpectedAudience   string             `yaml:"expected_audience,omitempty" json:"expected_audience,omitempty"`
	BodyHashClaim      string             `yaml:"body_hash_claim,omitempty" json:"body_hash_claim,omitempty"`
}
type NoAuthOptions struct{}
type ReplayGuardOptions struct {
	Store      string `yaml:"store" json:"store" jsonschema:"required"`
	PolicyID   string `yaml:"policy_id,omitempty" json:"policy_id,omitempty"`
	TTL        string `yaml:"ttl,omitempty" json:"ttl,omitempty"`
	MaxEntries int    `yaml:"max_entries,omitempty" json:"max_entries,omitempty" jsonschema:"minimum=1"`
	Key        string `yaml:"key,omitempty" json:"key,omitempty"`
}
type EnvelopeOptions struct {
	JSON       JSONParseOptions `yaml:"json,omitempty" json:"json,omitempty"`
	XML        XMLParseOptions  `yaml:"xml,omitempty" json:"xml,omitempty"`
	BodyFormat string           `yaml:"body_format,omitempty" json:"body_format,omitempty" jsonschema:"enum=json,enum=xml,enum=raw,enum=multipart"`
	Multipart  MultipartOptions `yaml:"multipart,omitempty" json:"multipart,omitempty"`
}

type JSONParseOptions struct {
	MaxDepth int `yaml:"max_depth,omitempty" json:"max_depth,omitempty" jsonschema:"minimum=1,maximum=1024"`
	MaxNodes int `yaml:"max_nodes,omitempty" json:"max_nodes,omitempty" jsonschema:"minimum=1,maximum=1000000"`
}

type XMLParseOptions struct {
	MaxBytes int64 `yaml:"max_bytes,omitempty" json:"max_bytes,omitempty" jsonschema:"minimum=1,maximum=67108864"`
	MaxDepth int   `yaml:"max_depth,omitempty" json:"max_depth,omitempty" jsonschema:"minimum=1,maximum=1024"`
	MaxNodes int   `yaml:"max_nodes,omitempty" json:"max_nodes,omitempty" jsonschema:"minimum=1,maximum=1000000"`
}

// MultipartOptions bounds a body_format: multipart parse. The defaults are
// deliberately modest rather than generous: the worst case an operator is
// exposed to is max_parts x max_part_bytes held in memory, plus a 4/3 base64
// copy of whatever content is carried, per in-flight request.
type MultipartOptions struct {
	MaxTotalBytes int64 `yaml:"max_total_bytes,omitempty" json:"max_total_bytes,omitempty" jsonschema:"minimum=1,maximum=67108864"`
	MaxParts      int   `yaml:"max_parts,omitempty" json:"max_parts,omitempty" jsonschema:"minimum=1,maximum=256"`
	MaxPartBytes  int64 `yaml:"max_part_bytes,omitempty" json:"max_part_bytes,omitempty" jsonschema:"minimum=1"`
	// IncludeContent is a pointer so an explicit false is distinguishable
	// from an unset field; the default is true.
	IncludeContent *bool `yaml:"include_content,omitempty" json:"include_content,omitempty"`
}

const (
	defaultMultipartMaxParts     = 16
	defaultMultipartMaxPartBytes = 1 << 20
)

// limits applies the documented defaults.
func (o MultipartOptions) limits() multipartLimits {
	lim := multipartLimits{
		MaxTotalBytes:  o.MaxTotalBytes,
		MaxParts:       o.MaxParts,
		MaxPartBytes:   o.MaxPartBytes,
		IncludeContent: true,
	}
	if lim.MaxParts <= 0 {
		lim.MaxParts = defaultMultipartMaxParts
	}
	if lim.MaxPartBytes <= 0 {
		lim.MaxPartBytes = defaultMultipartMaxPartBytes
	}
	if o.IncludeContent != nil {
		lim.IncludeContent = *o.IncludeContent
	}
	return lim
}

type FilterConditionOptions struct {
	Source   string `yaml:"source" json:"source" jsonschema:"required"`
	Path     string `yaml:"path" json:"path" jsonschema:"required"`
	Operator string `yaml:"operator" json:"operator" jsonschema:"required"`
	Value    any    `yaml:"value,omitempty" json:"value,omitempty"`
}
type FilterOptions struct {
	DropReason string                   `yaml:"drop_reason,omitempty" json:"drop_reason,omitempty" jsonschema:"enum=dropped_unused_event_type,enum=dropped_unsupported_event_type"`
	Conditions []FilterConditionOptions `yaml:"conditions" json:"conditions" jsonschema:"required,minItems=1"`
	OnNoMatch  string                   `yaml:"on_no_match,omitempty" json:"on_no_match,omitempty" jsonschema:"enum=drop,enum=reject"`
}
type InspectOptions struct{}

type SampleOptions struct {
	// A pointer distinguishes an omitted rate from an explicit zero.
	Rate *float64 `yaml:"rate" json:"rate" jsonschema:"required,minimum=0,maximum=1"`
}
type LookupCacheOptions struct {
	TTL        string `yaml:"ttl,omitempty" json:"ttl,omitempty"`
	MaxEntries int    `yaml:"max_entries,omitempty" json:"max_entries,omitempty" jsonschema:"minimum=1"`
}
type LookupOptions struct {
	Provider    string              `yaml:"provider" json:"provider" jsonschema:"required"`
	Key         string              `yaml:"key" json:"key" jsonschema:"required"`
	Name        string              `yaml:"name,omitempty" json:"name,omitempty"`
	Table       string              `yaml:"table,omitempty" json:"table,omitempty"`
	Cache       *LookupCacheOptions `yaml:"cache,omitempty" json:"cache,omitempty"`
	Fallback    string              `yaml:"fallback,omitempty" json:"fallback,omitempty"`
	NegativeTTL string              `yaml:"negative_ttl,omitempty" json:"negative_ttl,omitempty"`
}
type FieldMappingOptions struct {
	From string `yaml:"from" json:"from" jsonschema:"required"`
	To   string `yaml:"to" json:"to" jsonschema:"required"`
}
type JSONValidateOptions struct {
	Schema    transform.JSONSchema       `yaml:"schema" json:"schema" jsonschema:"required"`
	Coercions []transform.ScalarCoercion `yaml:"coercions,omitempty" json:"coercions,omitempty"`
}
type TransformOptions struct {
	JSONSchema transform.JSONSchema         `yaml:"json_schema,omitempty" json:"json_schema,omitempty"`
	Coercions  []transform.ScalarCoercion   `yaml:"coercions,omitempty" json:"coercions,omitempty"`
	Fields     []string                     `yaml:"fields,omitempty" json:"fields,omitempty"`
	XMLAdapter *transform.XMLAdapterOptions `yaml:"xml_adapter,omitempty" json:"xml_adapter,omitempty"`
	Kind       string                       `yaml:"kind" json:"kind" jsonschema:"required,enum=changed_fields,enum=field_map,enum=xml_prune,enum=xml_ordered_prune,enum=json_schema,enum=strip_fields,enum=changed_fields_js"`
	From       string                       `yaml:"from,omitempty" json:"from,omitempty"`
	To         string                       `yaml:"to,omitempty" json:"to,omitempty"`
	Name       string                       `yaml:"name,omitempty" json:"name,omitempty"`
	Mappings   []FieldMappingOptions        `yaml:"mappings,omitempty" json:"mappings,omitempty"`
	Rules      []string                     `yaml:"rules,omitempty" json:"rules,omitempty"`
}
type CodeOptions struct {
	Script         string `yaml:"script" json:"script" jsonschema:"required"`
	Timeout        string `yaml:"timeout,omitempty" json:"timeout,omitempty"`
	MaxInputBytes  int64  `yaml:"max_input_bytes,omitempty" json:"max_input_bytes,omitempty" jsonschema:"minimum=1"`
	MaxOutputBytes int64  `yaml:"max_output_bytes,omitempty" json:"max_output_bytes,omitempty" jsonschema:"minimum=1"`
	MaxCallStack   int    `yaml:"max_call_stack,omitempty" json:"max_call_stack,omitempty" jsonschema:"minimum=1"`
}

// SwitchCaseOptions takes either the original single `when` condition or a
// `conditions` list with a `match` combinator, never both. When is a pointer so
// an absent block is distinguishable from an empty one.
type SwitchCaseOptions struct {
	Port                string                  `yaml:"port" json:"port" jsonschema:"required"`
	When                *FilterConditionOptions `yaml:"when,omitempty" json:"when,omitempty"`
	ConditionSetOptions `yaml:",inline"`
}

// set normalizes either spelling into the shared shape.
func (c SwitchCaseOptions) set() ConditionSetOptions {
	if c.When != nil {
		return ConditionSetOptions{Conditions: []FilterConditionOptions{*c.When}}
	}
	return c.ConditionSetOptions
}

type SwitchOptions struct {
	Cases   []SwitchCaseOptions `yaml:"cases" json:"cases" jsonschema:"required,minItems=1"`
	Default string              `yaml:"default,omitempty" json:"default,omitempty"`
}

// IfElseOptions is a switch with two fixed ports. conditions is required here,
// which is why it does not embed ConditionSetOptions.
type IfElseOptions struct {
	Match      string                   `yaml:"match,omitempty" json:"match,omitempty" jsonschema:"enum=all,enum=any"`
	Conditions []FilterConditionOptions `yaml:"conditions" json:"conditions" jsonschema:"required,minItems=1"`
}

func (o IfElseOptions) set() ConditionSetOptions {
	return ConditionSetOptions(o)
}

type HTTPRequestOptions struct {
	Credential        *HTTPCredentialOptions `yaml:"credential,omitempty" json:"credential,omitempty"`
	RequireHeaders    []string               `yaml:"require_headers,omitempty" json:"require_headers,omitempty"`
	RequireBodyFields []string               `yaml:"require_body_fields,omitempty" json:"require_body_fields,omitempty"`
	URL               string                 `yaml:"url,omitempty" json:"url,omitempty"`
	Upstream          *HTTPUpstreamOptions   `yaml:"upstream,omitempty" json:"upstream,omitempty"`
	Method            string                 `yaml:"method,omitempty" json:"method,omitempty"`
	BodyMethods       []string               `yaml:"body_methods,omitempty" json:"body_methods,omitempty"`
	QueryMode         string                 `yaml:"query_mode,omitempty" json:"query_mode,omitempty" jsonschema:"enum=all,enum=first,enum=last"`
	RemoveQuery       []string               `yaml:"remove_query,omitempty" json:"remove_query,omitempty"`
	HeaderMode        string                 `yaml:"header_mode,omitempty" json:"header_mode,omitempty" jsonschema:"enum=forward,enum=replace"`
	RemoveHeaders     []string               `yaml:"remove_headers,omitempty" json:"remove_headers,omitempty"`
	RemoveBodyFields  []string               `yaml:"remove_body_fields,omitempty" json:"remove_body_fields,omitempty"`
	MaxResponseBytes  int64                  `yaml:"max_response_bytes,omitempty" json:"max_response_bytes,omitempty" jsonschema:"minimum=1,maximum=67108864"`
	ResponseBindings  *HTTPResponseBindings  `yaml:"response_bindings,omitempty" json:"response_bindings,omitempty"`
	Redirects         *HTTPRedirectOptions   `yaml:"redirects,omitempty" json:"redirects,omitempty"`
	ForwardMethod     bool                   `yaml:"forward_method,omitempty" json:"forward_method,omitempty"`
	ForwardQuery      bool                   `yaml:"forward_query,omitempty" json:"forward_query,omitempty"`
	ForwardHeaders    []string               `yaml:"forward_headers,omitempty" json:"forward_headers,omitempty"`
	Timeout           string                 `yaml:"timeout,omitempty" json:"timeout,omitempty"`
	Headers           map[string]string      `yaml:"headers,omitempty" json:"headers,omitempty"`
	ErrorOnStatus     []string               `yaml:"error_on_status,omitempty" json:"error_on_status,omitempty"`
	Body              string                 `yaml:"body,omitempty" json:"body,omitempty" jsonschema:"enum=raw,enum=transformed,enum=none"`
	Retry             HTTPRetryOptions       `yaml:"retry,omitempty" json:"retry,omitempty"`
	// AllowPrivateTargets opts this node out of the SSRF egress policy that
	// blocks loopback, link-local, private, and cloud-metadata destinations.
	// It exists for operators who genuinely proxy to an internal service. It
	// defaults to false: the policy fails closed, and reaching an internal
	// address must be a deliberate, reviewable line in the config.
	AllowPrivateTargets bool `yaml:"allow_private_targets,omitempty" json:"allow_private_targets,omitempty"`
}

// HTTPRetryOptions is the opt-in retry budget for an http_request node. A zero
// value — an absent or empty `retry:` block — means exactly one attempt, which
// is the historical behavior and the safe default for the non-idempotent POST
// this node usually issues. Retries cover transport errors, timeouts, 429, and
// 5xx; every other status is terminal.
type HTTPRetryOptions struct {
	On             string   `yaml:"on,omitempty" json:"on,omitempty" jsonschema:"enum=network,enum=network_and_status"`
	Methods        []string `yaml:"methods,omitempty" json:"methods,omitempty"`
	TotalTimeout   string   `yaml:"total_timeout,omitempty" json:"total_timeout,omitempty"`
	MaxAttempts    int      `yaml:"max_attempts,omitempty" json:"max_attempts,omitempty" jsonschema:"minimum=1,maximum=10"`
	InitialBackoff string   `yaml:"initial_backoff,omitempty" json:"initial_backoff,omitempty"`
	MaxBackoff     string   `yaml:"max_backoff,omitempty" json:"max_backoff,omitempty"`
}

type HTTPUpstreamOptions struct {
	Profile             string `yaml:"profile" json:"profile" jsonschema:"required"`
	EnvironmentVariable string `yaml:"environment_variable" json:"environment_variable" jsonschema:"required"`
	Scheme              string `yaml:"scheme,omitempty" json:"scheme,omitempty" jsonschema:"enum=http,enum=https"`
	Port                int    `yaml:"port,omitempty" json:"port,omitempty" jsonschema:"minimum=1,maximum=65535"`
	Path                string `yaml:"path,omitempty" json:"path,omitempty"`
}
type HTTPResponseBindings struct {
	Status       string            `yaml:"status,omitempty" json:"status,omitempty"`
	Fields       map[string]string `yaml:"fields,omitempty" json:"fields,omitempty"`
	MaxJSONBytes int64             `yaml:"max_json_bytes,omitempty" json:"max_json_bytes,omitempty" jsonschema:"minimum=1,maximum=1048576"`
}
type HTTPRedirectOptions struct {
	MaxHops int `yaml:"max_hops" json:"max_hops" jsonschema:"required,minimum=1,maximum=10"`
}

// PublishIdentityOptions selects a bounded FIFO identity, independently of the
// transport request ID. Hash sources always use the original request bytes.
type PublishIdentityOptions struct {
	Source       string `yaml:"source" json:"source" jsonschema:"required,enum=static,enum=body,enum=header,enum=variable,enum=raw_sha256,enum=json_js_sha256"`
	Value        string `yaml:"value,omitempty" json:"value,omitempty"`
	Path         string `yaml:"path,omitempty" json:"path,omitempty"`
	Fallback     string `yaml:"fallback,omitempty" json:"fallback,omitempty"`
	HashLength   int    `yaml:"hash_length,omitempty" json:"hash_length,omitempty" jsonschema:"minimum=1,maximum=64"`
	FallbackHash string `yaml:"fallback_hash,omitempty" json:"fallback_hash,omitempty" jsonschema:"enum=raw_sha256,enum=json_js_sha256"`
}
type PublishFIFOOptions struct {
	GroupID         PublishIdentityOptions  `yaml:"group_id" json:"group_id" jsonschema:"required"`
	DeduplicationID *PublishIdentityOptions `yaml:"deduplication_id,omitempty" json:"deduplication_id,omitempty"`
	ContentBased    bool                    `yaml:"content_based_deduplication,omitempty" json:"content_based_deduplication,omitempty"`
}
type PublishRetryOptions struct {
	MaxAttempts    int    `yaml:"max_attempts" json:"max_attempts" jsonschema:"required,minimum=1,maximum=10"`
	InitialBackoff string `yaml:"initial_backoff,omitempty" json:"initial_backoff,omitempty"`
	MaxBackoff     string `yaml:"max_backoff,omitempty" json:"max_backoff,omitempty"`
}
type PublishSQSOptions struct {
	QueueOwnerAccountID string `yaml:"queue_owner_account_id,omitempty" json:"queue_owner_account_id,omitempty"`
	DelaySeconds        *int32 `yaml:"delay_seconds,omitempty" json:"delay_seconds,omitempty" jsonschema:"minimum=0,maximum=900"`
}
type PublishSNSOptions struct {
	TopicARN         string `yaml:"topic_arn,omitempty" json:"topic_arn,omitempty"`
	Subject          string `yaml:"subject,omitempty" json:"subject,omitempty"`
	MessageStructure string `yaml:"message_structure,omitempty" json:"message_structure,omitempty" jsonschema:"enum=json"`
}
type PublishOptions struct {
	Temporal           *PublishTemporalOptions `yaml:"temporal,omitempty" json:"temporal,omitempty"`
	GroupID            *PublishIdentityOptions `yaml:"group_id,omitempty" json:"group_id,omitempty"`
	SQS                *PublishSQSOptions      `yaml:"sqs,omitempty" json:"sqs,omitempty"`
	SNS                *PublishSNSOptions      `yaml:"sns,omitempty" json:"sns,omitempty"`
	ResourceAttributes map[string]string       `yaml:"resource_attributes,omitempty" json:"resource_attributes,omitempty"`
	WireBody           string                  `yaml:"wire_body,omitempty" json:"wire_body,omitempty" jsonschema:"enum=envelope,enum=raw,enum=json,enum=json_js"`
	FIFO               *PublishFIFOOptions     `yaml:"fifo,omitempty" json:"fifo,omitempty"`
	Retry              *PublishRetryOptions    `yaml:"retry,omitempty" json:"retry,omitempty"`
	Destination        string                  `yaml:"destination" json:"destination" jsonschema:"required"`
	Target             string                  `yaml:"target" json:"target" jsonschema:"required"`
	Create             bool                    `yaml:"create,omitempty" json:"create,omitempty"`
	MessageAttributes  map[string]any          `yaml:"message_attributes,omitempty" json:"message_attributes,omitempty"`
	// Payload selects how object-capable destinations store the event.
	// Empty or "envelope" (default) writes the JSON Message. "object" writes
	// decoded file bytes (body must be a {content, filename?, content_type?}
	// map, or an empty parsed body backed by raw request bytes). Only s3 and
	// redis_kv accept "object".
	Payload string `yaml:"payload,omitempty" json:"payload,omitempty" jsonschema:"enum=envelope,enum=object"`
}
type RespondOptions struct {
	ForwardSetCookie bool              `yaml:"forward_set_cookie,omitempty" json:"forward_set_cookie,omitempty"`
	ForwardHeaders   []string          `yaml:"forward_headers,omitempty" json:"forward_headers,omitempty"`
	Status           int               `yaml:"status,omitempty" json:"status,omitempty" jsonschema:"minimum=200,maximum=599"`
	Headers          map[string]string `yaml:"headers,omitempty" json:"headers,omitempty"`
	Body             string            `yaml:"body,omitempty" json:"body,omitempty"`
	Passthrough      bool              `yaml:"passthrough,omitempty" json:"passthrough,omitempty"`
}

type nodeDefinition struct {
	name       string
	in, out    graph.PortType
	detached   bool
	newOptions func() any
	newNode    func(graph.NodeConfig) (graph.Node, error)
	nodeMetadata
}

// Node-specific authoring and placement rules live beside the constructor.
// Zero metadata uses reflected options and ordinary error edges; exceptional
// rules opt in here without adding another switch over node names.
type nodeMetadata struct {
	allowMultipleInputs bool
	allowLeaf           bool
	noErrorEdge         bool
	successContinuation bool
	// Positive values permit batch children and preserve their editor order.
	batchChildOrder int
	formDefaults    map[string]any
	configureSchema func(*NodeSchema)
	stagePorts      map[string]nodePorts
	stage           func(graph.NodeConfig) (string, error)
	bodyFormat      func(graph.NodeConfig) string
	checkBodyFormat bodyFormatCheck
}

type nodePorts struct{ in, out graph.PortType }

func (d nodeDefinition) withMetadata(metadata nodeMetadata) nodeDefinition {
	d.nodeMetadata = metadata
	return d
}

// The validation lookup is read-only after package initialization. Building it
// does not construct runtime nodes or open native providers.
var validationCatalog map[string]nodeDefinition

func init() {
	definitions := catalog(RegistryOptions{})
	validationCatalog = make(map[string]nodeDefinition, len(definitions))
	for _, def := range definitions {
		validationCatalog[def.name] = def
	}
}

func define[T any](name string, in, out graph.PortType, detached bool, validate func(T) error, build func(string, T) graph.Node) nodeDefinition {
	return defineErr(name, in, out, detached, validate, func(id string, opts T) (graph.Node, error) {
		if build != nil {
			return build(id, opts), nil
		}
		return stubNode{name: name}, nil
	})
}

func defineErr[T any](name string, in, out graph.PortType, detached bool, validate func(T) error, build func(string, T) (graph.Node, error)) nodeDefinition {
	return nodeDefinition{
		name: name, in: in, out: out, detached: detached,
		newOptions: func() any { return new(T) },
		newNode: func(nc graph.NodeConfig) (graph.Node, error) {
			opts, err := decodeOptions[T](nc)
			if err != nil {
				return nil, nodeBuildError(name, err)
			}
			if validate != nil {
				if err := validate(opts); err != nil {
					return nil, fmt.Errorf("node %q (%s): %w", nc.ID, name, nodeBuildError(name, err))
				}
			}
			node, err := build(nc.ID, opts)
			return node, nodeBuildError(name, err)
		},
	}
}

// RegistryOptions customizes runtime node constructors used when compiling
// flows for serving. Validation uses the zero value.
type RegistryOptions struct {
	Protection         func(string) protection.Store
	StateStores        map[string]protection.Config
	ProtectionPolicies map[string][]protection.Policy
	FlowID             string
	BatchSlots         chan struct{}
	BatchObservation   func(string, string)
	Credentials        map[string]CredentialProvider
	RoutingRegistry    *routing.Registry
	RoutingStores      map[string]routing.Reader
	RoutingObservation func(string, string)
	ValidateOnly       bool
	AuthSecrets        func(AuthOptions) (SecretsFunc, error)
	Publishers         PublisherResolver
	Lookups            func(LookupOptions) LookupFunc
	LaunchDarkly       func(string) featureflags.Evaluator
	FlagObservation    func(string, string)
	Secrets            func(SecretRefOptions) (SecretsFunc, error)
	Logger             *slog.Logger
	HeaderDenylist     []string
	HeaderAllowlist    []string
	QueryDenylist      []string
}

// SecretsFunc returns the currently-valid secret values for an auth node.
// Serving injects a cached secretref resolver; tests inject a fake. Defined
// here so flowconfig does not import internal/secretref (WASM).
type SecretsFunc func(ctx context.Context) ([][]byte, error)

func catalog(opts RegistryOptions) []nodeDefinition {
	return []nodeDefinition{
		define("mcp_trigger", graph.PortNone, graph.PortEnvelope, false, validateMCPTrigger, newMCPTrigger),
		defineErr("mcp_result", graph.PortEnvelope, graph.PortTerminal, false, nil, newMCPResult).withMetadata(nodeMetadata{noErrorEdge: true}),
		define("http_trigger", graph.PortNone, graph.PortRequestHead, false, func(o HTTPTriggerOptions) error { return require("path", o.Path) }, newHTTPTrigger),
		define("loop_guard", graph.PortRequestHead, graph.PortRequestHead, false, func(o LoopGuardOptions) error {
			if o.MaxHops < 0 {
				return errors.New("max_hops must be >= 0")
			}
			return nil
		}, newLoopGuard).withMetadata(nodeMetadata{
			formDefaults: map[string]any{"max_hops": newLoopGuard("schema", LoopGuardOptions{}).(loopGuardNode).maxHops},
		}),
		define("ip_allowlist", graph.PortRequestHead, graph.PortRequestHead, false, validateIPAllowlist, newIPAllowlist),
		defineErr("rate_limit", graph.PortRequestHead, graph.PortRequestHead, false, validateRateLimit, func(id string, o RateLimitOptions) (graph.Node, error) {
			return bindRateLimit(id, o, opts)
		}).withMetadata(nodeMetadata{
			formDefaults: map[string]any{"stage": "request_head", "strategy": "token_bucket", "key_by": "source_ip", "max_keys": protection.DefaultRateLimitMaxKeys},
			stagePorts: map[string]nodePorts{
				"request_head": {graph.PortRequestHead, graph.PortRequestHead},
				"verified":     {graph.PortVerified, graph.PortVerified},
			},
			stage: func(nc graph.NodeConfig) (string, error) {
				o, err := decodeOptions[RateLimitOptions](nc)
				return o.Stage, err
			},
		}),
		define("read_body", graph.PortRequestHead, graph.PortRawRequest, false, func(o ReadBodyOptions) error {
			if o.MaxBytes <= 0 {
				return errors.New("max_bytes must be positive")
			}
			return nil
		}, newReadBody).withMetadata(nodeMetadata{noErrorEdge: true}), // A failed read consumes the body.
		defineErr("auth", graph.PortRawRequest, graph.PortVerified, false, validateAuth, func(id string, o AuthOptions) (graph.Node, error) {
			if opts.ValidateOnly {
				return stubNode{name: "auth validation only"}, nil
			}
			var secrets SecretsFunc
			if o.SecretRef != nil && opts.AuthSecrets != nil {
				fn, err := opts.AuthSecrets(o)
				if err != nil {
					return nil, err
				}
				secrets = fn
			} else if o.SecretRef != nil && opts.Secrets != nil {
				fn, err := opts.Secrets(*o.SecretRef)
				if err != nil {
					return nil, err
				}
				secrets = fn
			}
			return newAuthNode(id, o, secrets)
		}).withMetadata(nodeMetadata{
			configureSchema: func(meta *NodeSchema) {
				meta.AuthSchemes = authSchemeSchemas(meta.Schema)
				meta.Schema.OneOf = exclusivePropertyContract("secret", "secret_ref")
			},
		}),
		define[NoAuthOptions]("no_auth", graph.PortRawRequest, graph.PortVerified, false, nil, func(id string, _ NoAuthOptions) graph.Node {
			return newNoAuthNode(id, opts.Logger)
		}).withMetadata(nodeMetadata{noErrorEdge: true}), // no_auth never fails a well-typed request.
		defineErr("replay_guard", graph.PortVerified, graph.PortVerified, false, validateReplayGuard, func(id string, o ReplayGuardOptions) (graph.Node, error) { return newReplayGuard(id, o, opts) }).withMetadata(nodeMetadata{
			formDefaults: map[string]any{"ttl": "5m", "max_entries": 1024},
		}),
		define("envelope", graph.PortVerified, graph.PortEnvelope, false, validateEnvelope, func(id string, o EnvelopeOptions) graph.Node {
			n := newEnvelopeNode(id, o, opts.HeaderAllowlist, opts.HeaderDenylist).(envelopeNode)
			n.queryDenylist = opts.QueryDenylist
			return n
		}).withMetadata(nodeMetadata{formDefaults: envelopeFormDefaults(), bodyFormat: envelopeBodyFormat}),
		defineErr("batch", graph.PortEnvelope, graph.PortTerminal, false, validateBatch, func(id string, o BatchOptions) (graph.Node, error) { return newBatchNode(id, o, opts) }).withMetadata(nodeMetadata{
			formDefaults: batchFormDefaults(), checkBodyFormat: jsonBodyFormatIssue,
		}),
		define("filter", graph.PortEnvelope, graph.PortEnvelope, false, validateFilter, newFilterNode).withMetadata(nodeMetadata{
			batchChildOrder: 3, formDefaults: map[string]any{"on_no_match": "drop"}, checkBodyFormat: conditionBodyFormatIssue,
		}),
		define("inspect", graph.PortEnvelope, graph.PortEnvelope, false, nil, newInspectNode).withMetadata(nodeMetadata{allowLeaf: true, noErrorEdge: true}),
		define("sample", graph.PortEnvelope, graph.PortEnvelope, false, validateSample, newSampleNode).withMetadata(nodeMetadata{
			noErrorEdge: true, batchChildOrder: 7,
		}),
		defineErr("register_route", graph.PortEnvelope, graph.PortEnvelope, false, validateRegisterRoute, func(id string, o RegisterRouteOptions) (graph.Node, error) { return newRegisterRouteNode(id, o, opts) }),
		defineErr("route", graph.PortEnvelope, graph.PortEnvelope, false, validateRoute, func(id string, o RouteOptions) (graph.Node, error) {
			return newRouteNode(id, o, opts)
		}).withMetadata(nodeMetadata{
			configureSchema: func(meta *NodeSchema) { meta.Schema.OneOf = exclusivePropertyContract("fixed", "candidates") },
		}),
		defineErr("lookup", graph.PortEnvelope, graph.PortEnvelope, false, validateLookup, func(id string, o LookupOptions) (graph.Node, error) {
			var fn LookupFunc
			if opts.Lookups != nil {
				fn = opts.Lookups(o)
			}
			return newLookupNode(id, o, fn)
		}),
		define("launchdarkly", graph.PortEnvelope, graph.PortEnvelope, false, validateLaunchDarkly, func(id string, o LaunchDarklyOptions) graph.Node {
			var fn featureflags.Evaluator
			if opts.LaunchDarkly != nil {
				fn = opts.LaunchDarkly(o.Connection)
			}
			return launchDarklyNode{id: id, opts: o, evaluate: fn, observe: opts.FlagObservation}
		}).withMetadata(nodeMetadata{noErrorEdge: true, configureSchema: configureLaunchDarklySchema}),
		defineErr("json_validate", graph.PortEnvelope, graph.PortEnvelope, false, nil, newJSONValidateNode).withMetadata(nodeMetadata{checkBodyFormat: jsonBodyFormatIssue}),
		defineErr("transform", graph.PortEnvelope, graph.PortEnvelope, false, validateTransform, func(id string, o TransformOptions) (graph.Node, error) {
			return newTransformNode(id, o, opts.Logger)
		}).withMetadata(nodeMetadata{batchChildOrder: 1, checkBodyFormat: transformBodyFormatIssue}),
		defineErr("code", graph.PortEnvelope, graph.PortEnvelope, false, validateCode, func(id string, o CodeOptions) (graph.Node, error) {
			n, err := newCodeNode(id, o)
			if err != nil {
				return nil, err
			}
			code := n.(codeNode)
			code.allowlist, code.denylist = opts.HeaderAllowlist, opts.HeaderDenylist
			code.queryDenylist = opts.QueryDenylist
			return code, nil
		}).withMetadata(nodeMetadata{
			batchChildOrder: 2,
			formDefaults:    map[string]any{"timeout": defaultCodeTimeout.String(), "max_call_stack": defaultCodeStack, "max_input_bytes": defaultCodeInputSize, "max_output_bytes": defaultCodeOutputSize},
		}),
		define("switch", graph.PortEnvelope, graph.PortEnvelope, false, validateSwitch, newSwitchNode).withMetadata(nodeMetadata{
			batchChildOrder: 5, checkBodyFormat: conditionBodyFormatIssue,
			configureSchema: func(meta *NodeSchema) {
				// Exclusivity belongs to each case: when and conditions are two
				// spellings of one rule, rather than two fields to fill in.
				if cases, ok := meta.Schema.Properties.Get("cases"); ok && cases.Items != nil {
					cases.Items.OneOf = exclusivePropertyContract("when", "conditions")
				}
			},
		}),
		define("if_else", graph.PortEnvelope, graph.PortEnvelope, false, validateIfElse, newIfElseNode).withMetadata(nodeMetadata{
			// A condition that does not hold takes the false branch, without error.
			noErrorEdge: true, batchChildOrder: 4, checkBodyFormat: conditionBodyFormatIssue,
		}),
		defineErr("http_request", graph.PortEnvelope, graph.PortEnvelope, false, validateHTTPRequest, func(id string, o HTTPRequestOptions) (graph.Node, error) {
			value, err := newHTTPRequestNodeWithRegistry(id, o, opts.HeaderAllowlist, opts.HeaderDenylist, opts.RoutingRegistry, opts.ValidateOnly)
			if err != nil {
				return nil, err
			}
			n := value.(httpRequestNode)
			n.credentials = opts.Credentials
			if !opts.ValidateOnly && o.Credential != nil {
				profiles := []string{o.Credential.Profile}
				if profiles[0] == "" {
					profiles = nil
					for _, p := range o.Credential.EnvironmentProfiles {
						profiles = append(profiles, p)
					}
				}
				for _, p := range profiles {
					if opts.Credentials[p] == nil {
						return nil, ErrCredentialConfiguration
					}
				}
			}
			return n, nil
		}).withMetadata(nodeMetadata{
			formDefaults:    map[string]any{"method": "POST", "timeout": defaultHTTPRequestTimeout.String(), "retry.initial_backoff": "500ms", "retry.max_backoff": "30s"},
			configureSchema: func(meta *NodeSchema) { meta.Schema.OneOf = exclusivePropertyContract("url", "upstream") },
		}),
		defineErr("publish", graph.PortEnvelope, graph.PortEnvelope, true, validatePublish, func(id string, o PublishOptions) (graph.Node, error) {
			return newPublishNode(id, o, opts.Publishers)
		}).withMetadata(nodeMetadata{allowMultipleInputs: true, successContinuation: true, batchChildOrder: 6, formDefaults: publishFormDefaults()}),
		defineErr("respond", graph.PortEnvelope, graph.PortTerminal, false, validateRespond, newRespondNode).withMetadata(nodeMetadata{
			configureSchema: func(meta *NodeSchema) { meta.Schema.OneOf = exclusivePropertyContract("status", "passthrough") },
		}),
	}
}

func (d nodeDefinition) spec() graph.NodeSpec {
	spec := graph.NodeSpec{
		Type:                d.name,
		In:                  d.in,
		Out:                 d.out,
		Detached:            d.detached,
		SuccessContinuation: d.successContinuation,
		AllowLeaf:           d.allowLeaf,
		AllowMultipleInputs: d.allowMultipleInputs,
		New:                 d.newNode,
	}
	if d.stage != nil {
		spec.ResolvePorts = func(nc graph.NodeConfig) (graph.PortType, graph.PortType, error) {
			stage, err := d.stage(nc)
			if err != nil {
				return 0, 0, err
			}
			if ports, ok := d.stagePorts[stage]; ok {
				return ports.in, ports.out, nil
			}
			return d.in, d.out, nil
		}
	}
	return spec
}

type stubNode struct{ name string }

func (n stubNode) Run(context.Context, graph.Payload) (graph.Payload, error) {
	return nil, fmt.Errorf("%w: %s", errNotImplemented, n.name)
}

func RegisterWithOptions(reg *graph.Registry, opts RegistryOptions) error {
	if reg == nil {
		return errors.New("flowconfig: nil graph registry")
	}
	for _, def := range catalog(opts) {
		if err := reg.Register(def.spec()); err != nil {
			return err
		}
	}
	return nil
}

func NewRegistryWithOptions(opts RegistryOptions) (*graph.Registry, error) {
	if opts.BatchSlots == nil {
		opts.BatchSlots = make(chan struct{}, 1024)
	}
	reg := graph.NewRegistry()
	if err := RegisterWithOptions(reg, opts); err != nil {
		return nil, err
	}
	return reg, nil
}

func decodeOptions[T any](nc graph.NodeConfig) (T, error) {
	var out T
	if err := decodeOptionsInto(nc, &out); err != nil {
		return out, err
	}
	return out, nil
}

func TriggerOptions(nc graph.NodeConfig) (HTTPTriggerOptions, error) {
	return decodeOptions[HTTPTriggerOptions](nc)
}

func PublishNodeOptions(nc graph.NodeConfig) (PublishOptions, error) {
	return decodeOptions[PublishOptions](nc)
}

func decodeOptionsInto(nc graph.NodeConfig, out any) error {
	raw, err := yaml.Marshal(&nc.Options)
	if err != nil {
		return fmt.Errorf("node %q options: %w", nc.ID, err)
	}
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(out); err != nil {
		return fmt.Errorf("node %q options: %w", nc.ID, err)
	}
	return nil
}

func require(name, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s is required", name)
	}
	return nil
}

func validateAuth(o AuthOptions) error {
	if o.CredentialID != "" && (!graph.ValidID(o.CredentialID) || len(o.CredentialID) > 255) {
		return errors.New("credential_id must be a stable printable ID of at most 255 bytes")
	}
	if o.TenantClaim != "" && (len(o.TenantClaim) > 128 || strings.ContainsAny(o.TenantClaim, "\x00\r\n") || !auth.SchemeAllowedForAuthType(auth.AuthTypeJWT, defaultAuthScheme(o.Scheme))) {
		return errors.New("tenant_claim must be a bounded JWT claim name and requires a JWT scheme")
	}
	for _, header := range []string{o.SignatureHeader, o.TimestampHeader} {
		if header != "" && !validAuthHeader(header) {
			return errors.New("authentication header must be a valid HTTP field name")
		}
	}
	if o.Source != nil && o.Source.Kind == "header" && !validAuthHeader(o.Source.Key) {
		return errors.New("authentication header must be a valid HTTP field name")
	}
	if o.SecretRef != nil && len(o.SecretRef.JSONField) > 256 {
		return errors.New("secret_ref.json_field is limited to 256 bytes")
	}
	if err := auth.ValidateSpec(AuthSpec(o)); err != nil {
		return err
	}
	if auth.SeparateTimestamp(o.Scheme) {
		if o.TimestampHeader == "" {
			return errors.New("timestamp_header is required for separate-header signatures")
		}
		signature := o.SignatureHeader
		if o.Source != nil {
			if o.Source.Kind != "header" {
				return errors.New("separate signatures require header source")
			}
			signature = o.Source.Key
		}
		if signature == "" || strings.EqualFold(signature, o.TimestampHeader) {
			return errors.New("separate signature and timestamp headers must be distinct and nonempty")
		}
	} else if o.TimestampHeader != "" {
		return errors.New("timestamp_header requires a separate-header signature scheme")
	}
	if o.Scheme == auth.SchemeHMACSHA256TSDotMulti && o.SignatureHeader == "" && o.Source == nil {
		return errors.New("multi-signature scheme requires a credential source")
	}
	if o.Secret != "" && !strings.Contains(o.Secret, "${") {
		if err := ValidateAuthKey(o, []byte(o.Secret)); err != nil {
			return errors.New("invalid authentication key material")
		}
	}

	if o.RemovePrefix != "" && (o.Scheme != auth.SchemeExact || o.RemoveBearer || strings.ContainsAny(o.RemovePrefix, "\r\n\x00")) {
		return errors.New("remove_prefix requires exact auth without remove_bearer and a valid header prefix")
	}
	if o.Scheme == auth.SchemeNoAuth {
		return errors.New("use the explicit no_auth node instead of auth.scheme: no_auth")
	}
	if strings.HasPrefix(o.Scheme, "jwt_") && strings.TrimSpace(o.BodyHashClaim) == "" {
		return errors.New("body_hash_claim is required for JWT raw-body binding")
	}

	if err := require("scheme", o.Scheme); err != nil {
		return err
	}
	if (o.Secret == "") == (o.SecretRef == nil) {
		return errors.New("exactly one of secret or secret_ref is required")
	}
	if o.SecretRef != nil {
		if o.SecretRef.Selection != "" {
			if err := validateCredentialSecret(*o.SecretRef); err != nil {
				return err
			}
		}
		if err := require("secret_ref.provider", o.SecretRef.Provider); err != nil {
			return err
		}
		switch o.SecretRef.Provider {
		case "env", "file", "aws_secrets_manager":
		default:
			return fmt.Errorf("secret_ref.provider %q is not supported", o.SecretRef.Provider)
		}
		if err := require("secret_ref.name", o.SecretRef.Name); err != nil {
			return err
		}
		if err := validateDuration("secret_ref.refresh", o.SecretRef.Refresh, false); err != nil {
			return err
		}
		if err := validateDuration("secret_ref.overlap", o.SecretRef.Overlap, true); err != nil {
			return err
		}
	}
	if o.Source != nil {
		switch o.Source.Kind {
		case "header", "query":
		default:
			return errors.New("source.kind must be header or query")
		}
		if err := require("source.key", o.Source.Key); err != nil {
			return err
		}
	}
	return validateDuration("timestamp_tolerance", o.TimestampTolerance, false)
}
func validateIPAllowlist(o IPAllowlistOptions) error {
	if len(o.CIDRs) == 0 {
		return errors.New("cidrs is required")
	}
	for _, entry := range o.CIDRs {
		if net.ParseIP(entry) != nil {
			continue
		}
		if _, _, err := net.ParseCIDR(entry); err != nil {
			return fmt.Errorf("invalid cidr %q", entry)
		}
	}
	return nil
}
func validateReplayGuard(o ReplayGuardOptions) error {
	if len(o.Key) > 4096 {
		return errors.New("key template must be at most 4096 bytes")
	}
	if o.Store == "" {
		return errors.New("store is required; select a named state_stores backend")
	}
	if o.MaxEntries < 0 {
		return errors.New("max_entries must be >= 1")
	}
	if err := validateDuration("ttl", o.TTL, false); err != nil {
		return err
	}
	if _, err := parseGuardKey(o.Key); err != nil {
		return fmt.Errorf("key: %w", err)
	}
	if guardPolicy("validation", "node", o).Validate() != nil {
		return errors.New("replay guard policy ID, TTL or capacity outside supported bounds")
	}
	return nil
}
func validateEnvelope(o EnvelopeOptions) error {
	j := o.JSON
	if (j.MaxDepth != 0 || j.MaxNodes != 0) && o.BodyFormat != "" && o.BodyFormat != "json" {
		return errors.New("json options require body_format: json")
	}
	if j.MaxDepth < 0 || j.MaxDepth > 1024 || j.MaxNodes < 0 || j.MaxNodes > 1000000 {
		return errors.New("invalid JSON parse limits")
	}
	x := o.XML
	if (x.MaxBytes != 0 || x.MaxDepth != 0 || x.MaxNodes != 0) && o.BodyFormat != "xml" {
		return errors.New("xml options require body_format: xml")
	}
	if x.MaxBytes < 0 || x.MaxBytes > 64<<20 || x.MaxDepth < 0 || x.MaxDepth > 1024 || x.MaxNodes < 0 || x.MaxNodes > 1000000 {
		return errors.New("invalid XML parse limits")
	}

	switch o.BodyFormat {
	case "", "json", "xml", "raw", "multipart":
	default:
		return fmt.Errorf("unknown body_format %q", o.BodyFormat)
	}
	return validateMultipart(o)
}

// validateMultipart rejects a multipart block that cannot do what it looks
// like it does. Options set without body_format: multipart are rejected rather
// than silently ignored, the same reasoning as http_request's retry block: a
// lone limit is always a half-written config.
func validateMultipart(o EnvelopeOptions) error {
	set := o.Multipart.MaxParts != 0 || o.Multipart.MaxPartBytes != 0 || o.Multipart.MaxTotalBytes != 0 || o.Multipart.IncludeContent != nil
	if set && o.BodyFormat != "multipart" {
		return errors.New("multipart options have no effect without body_format: multipart")
	}
	if o.Multipart.MaxParts < 0 {
		return errors.New("multipart.max_parts must not be negative")
	}
	if o.Multipart.MaxParts > 256 {
		return errors.New("multipart.max_parts must be 256 or fewer")
	}
	if o.Multipart.MaxTotalBytes < 0 || o.Multipart.MaxTotalBytes > 64<<20 {
		return errors.New("multipart.max_total_bytes must be 1..67108864")
	}
	if o.Multipart.MaxPartBytes < 0 || o.Multipart.MaxPartBytes > 64<<20 {
		return errors.New("multipart.max_part_bytes must not be negative")
	}
	return nil
}
func validateFilter(o FilterOptions) error {
	if o.DropReason != "" && o.DropReason != "dropped_unused_event_type" && o.DropReason != "dropped_unsupported_event_type" {
		return errors.New("invalid finite filter drop_reason")
	}
	if o.DropReason != "" && o.OnNoMatch == "reject" {
		return errors.New("drop_reason requires a drop filter")
	}
	if len(o.Conditions) == 0 {
		return errors.New("conditions is required")
	}
	for i, c := range o.Conditions {
		if c.Source == "" || c.Path == "" || c.Operator == "" {
			return fmt.Errorf("conditions[%d] requires source, path, and operator", i)
		}
	}
	if o.OnNoMatch != "" && o.OnNoMatch != "drop" && o.OnNoMatch != "reject" {
		return fmt.Errorf("unknown on_no_match %q", o.OnNoMatch)
	}
	return nil
}
func validateLookup(o LookupOptions) error {
	if err := require("provider", o.Provider); err != nil {
		return err
	}
	if err := require("key", o.Key); err != nil {
		return err
	}
	if err := validateTemplate("key", o.Key); err != nil && !isBodyPathKey(o.Key) {
		return err
	}
	if o.Cache != nil {
		if o.Cache.MaxEntries < 0 {
			return errors.New("cache.max_entries must be >= 1")
		}
		if err := validateDuration("cache.ttl", o.Cache.TTL, false); err != nil {
			return err
		}
	}
	return validateDuration("negative_ttl", o.NegativeTTL, true)
}
func validateTransform(o TransformOptions) error {
	if (o.JSONSchema != nil || len(o.Coercions) > 0) && o.Kind != "json_schema" {
		return errors.New("json_schema and coercions require kind: json_schema")
	}
	if len(o.Fields) > 0 && o.Kind != "strip_fields" {
		return errors.New("fields requires strip_fields")
	}
	if o.XMLAdapter != nil && o.Kind != "xml_ordered_prune" {
		return errors.New("xml_adapter requires xml_ordered_prune")
	}
	if err := require("kind", o.Kind); err != nil {
		return err
	}
	switch o.Kind {
	case "json_schema":
		_, err := transform.NewValidator(o.JSONSchema, o.Coercions)
		return err
	case "strip_fields":
		if len(o.Fields) == 0 || len(o.Fields) > 64 {
			return errors.New("strip_fields requires 1..64 paths")
		}
		for _, f := range o.Fields {
			if !transform.JSONFieldPath(f) {
				return errors.New("invalid strip_fields path")
			}
		}
	case "changed_fields", "changed_fields_js":
		if o.From == "" || o.To == "" {
			return errors.New("changed_fields requires from and to")
		}
		if o.Kind == "changed_fields_js" && (!strings.HasPrefix(o.From, "body.") || !strings.HasPrefix(o.To, "body.")) {
			return errors.New("changed_fields_js requires body paths into the original JSON request")
		}
	case "field_map":
		if len(o.Mappings) == 0 {
			return errors.New("field_map requires mappings")
		}
	case "xml_ordered_prune":
		if o.XMLAdapter == nil || len(o.Rules) > 0 {
			return errors.New("xml_ordered_prune requires xml_adapter and forbids rules overrides")
		}
		return o.XMLAdapter.Validate()
	case "xml_prune":
		for i, rule := range o.Rules {
			if !transform.KnownXMLPruneRule(rule) {
				return fmt.Errorf("xml_prune rules[%d] unknown %q", i, rule)
			}
		}
	default:
		return fmt.Errorf("unknown transform kind %q", o.Kind)
	}
	return nil
}
func validateCode(o CodeOptions) error {
	if err := require("script", o.Script); err != nil {
		return err
	}
	if o.MaxInputBytes < 0 || o.MaxInputBytes > 64<<20 {
		return errors.New("max_input_bytes must be positive")
	}
	if o.MaxOutputBytes < 0 || o.MaxOutputBytes > 64<<20 {
		return errors.New("max_output_bytes must be positive")
	}
	if o.MaxCallStack < 0 {
		return errors.New("max_call_stack must be positive")
	}
	return validateDuration("timeout", o.Timeout, false)
}
func validateSwitch(o SwitchOptions) error {
	if o.Default == "on_error" {
		return errors.New("switch port on_error is reserved")
	}
	if len(o.Cases) == 0 {
		return errors.New("cases is required")
	}
	seen := make(map[string]struct{}, len(o.Cases))
	for i, c := range o.Cases {
		if c.Port == "on_error" {
			return errors.New("switch port on_error is reserved")
		}
		if c.Port == "" {
			return fmt.Errorf("cases[%d] requires port", i)
		}
		if c.When != nil && len(c.Conditions) > 0 {
			return fmt.Errorf("cases[%d]: set either when or conditions, not both", i)
		}
		if c.When == nil && len(c.Conditions) == 0 {
			return fmt.Errorf("cases[%d] requires when or conditions", i)
		}
		if c.When != nil {
			if c.Match != "" {
				return fmt.Errorf("cases[%d]: match has no effect on a single when condition", i)
			}
			if c.When.Source == "" || c.When.Path == "" || c.When.Operator == "" {
				return fmt.Errorf("cases[%d].when requires source, path, and operator", i)
			}
		} else if err := validateConditions(fmt.Sprintf("cases[%d].", i), c.Match, c.Conditions); err != nil {
			return err
		}
		if _, ok := seen[c.Port]; ok {
			return fmt.Errorf("duplicate switch port %q", c.Port)
		}
		seen[c.Port] = struct{}{}
	}
	return nil
}

func validateIfElse(o IfElseOptions) error {
	return validateConditions("", o.Match, o.Conditions)
}
func validateHTTPRequest(o HTTPRequestOptions) error {
	if (o.URL == "") == (o.Upstream == nil) {
		return errors.New("exactly one of url or upstream is required")
	}
	if o.Upstream != nil {
		u := o.Upstream
		if u.Profile == "" || !vars.ValidName(u.EnvironmentVariable) {
			return errors.New("upstream requires profile and a valid environment_variable")
		}
		if u.Scheme != "" && u.Scheme != "http" && u.Scheme != "https" {
			return errors.New("upstream scheme must be http or https")
		}
		if u.Port < 0 || u.Port > 65535 {
			return errors.New("invalid upstream port")
		}
		if u.Path != "" && (!strings.HasPrefix(u.Path, "/") || strings.HasPrefix(u.Path, "//") || strings.ContainsAny(u.Path, "?#")) {
			return errors.New("upstream.path must be an absolute path without query or fragment")
		}
		if err := validateTemplate("upstream.path", u.Path); err != nil {
			return err
		}
	} else {
		if err := validateTemplate("url", o.URL); err != nil {
			return err
		}
		if tpl, err := vars.Parse(o.URL); err == nil && !tpl.HasPlaceholders() {
			if err := validateHTTPURL(o.URL); err != nil {
				return fmt.Errorf("url: %w", err)
			}
		}
		if err := validateURLHostTemplate(o.URL); err != nil {
			return fmt.Errorf("url: %w", err)
		}
		// Literal MCP operations also require explicit private-target permission.
		// Ordinary webhook execution keeps its existing literal-origin policy.
	}
	if err := validateHTTPCredential(o.Credential); err != nil {
		return err
	}
	if o.Credential != nil && (o.Retry.MaxAttempts > 1 || o.Redirects != nil) {
		return errors.New("managed credentials own session retry and forbid additional retries or redirects")
	}
	for _, header := range o.RequireHeaders {
		if !validAuthHeader(header) {
			return errors.New("invalid required header")
		}
	}
	for _, path := range o.RequireBodyFields {
		if !validSelectionPath(path) {
			return errors.New("invalid required body field")
		}
	}
	if len(o.RequireHeaders) > 32 || len(o.RequireBodyFields) > 32 {
		return errors.New("request preflight fields exceed 32")
	}
	if err := validateProxyContract(o); err != nil {
		return err
	}
	switch strings.ToLower(strings.TrimSpace(o.Body)) {
	case "", "raw", "transformed", "none":
	default:
		return fmt.Errorf("http_request.body %q must be raw, transformed or none", o.Body)
	}
	if err := validateHTTPRetry(o.Retry); err != nil {
		return err
	}
	return validateDuration("timeout", o.Timeout, false)
}

// validateHTTPRetry rejects a retry block that cannot do what it looks like it
// does. In particular a backoff set without max_attempts >= 2 is rejected
// rather than silently ignored: retry is opt-in through max_attempts, so a
// lone backoff value is always a half-written config.
func validateHTTPRetry(o HTTPRetryOptions) error {
	if o.On != "" && o.On != "network" && o.On != "network_and_status" {
		return errors.New("retry.on must be network or network_and_status")
	}
	if err := validateDuration("retry.total_timeout", o.TotalTimeout, false); err != nil {
		return err
	}
	if o.MaxAttempts < 2 && (o.On != "" || len(o.Methods) > 0) {
		return errors.New("retry.on and retry.methods require at least two attempts")
	}

	if o.MaxAttempts < 0 {
		return errors.New("retry.max_attempts must not be negative")
	}
	if o.MaxAttempts > 10 {
		return errors.New("retry.max_attempts must be 10 or fewer")
	}
	if o.MaxAttempts < 2 && (o.InitialBackoff != "" || o.MaxBackoff != "") {
		return errors.New("retry.initial_backoff and retry.max_backoff have no effect without retry.max_attempts of 2 or more")
	}
	if err := validateDuration("retry.initial_backoff", o.InitialBackoff, false); err != nil {
		return err
	}
	if err := validateDuration("retry.max_backoff", o.MaxBackoff, false); err != nil {
		return err
	}
	if o.InitialBackoff != "" && o.MaxBackoff != "" {
		initial, err := time.ParseDuration(o.InitialBackoff)
		if err != nil {
			return fmt.Errorf("retry.initial_backoff: %w", err)
		}
		maxBackoff, err := time.ParseDuration(o.MaxBackoff)
		if err != nil {
			return fmt.Errorf("retry.max_backoff: %w", err)
		}
		if initial > maxBackoff {
			return errors.New("retry.initial_backoff must not exceed retry.max_backoff")
		}
	}
	return nil
}
func validatePublish(o PublishOptions) error {
	if err := require("destination", o.Destination); err != nil {
		return err
	}
	if err := require("target", o.Target); err != nil {
		return err
	}
	if err := validateTemplate("target", o.Target); err != nil {
		return err
	}
	if o.Temporal != nil {
		if err := validatePublishTemporal(*o.Temporal, o.Target); err != nil {
			return err
		}
	}
	switch o.Payload {
	case "", "envelope", "object":
		return validatePublishDelivery(o)
	default:
		return fmt.Errorf("payload %q is not supported (use envelope or object)", o.Payload)
	}
}
func validateRespond(o RespondOptions) error {
	if !o.Passthrough && (o.ForwardSetCookie || len(o.ForwardHeaders) > 0) {
		return errors.New("forward_set_cookie and forward_headers require passthrough")
	}
	for _, name := range o.ForwardHeaders {
		if !validAuthHeader(name) {
			return errors.New("invalid response header name")
		}
	}

	if o.Passthrough == (o.Status != 0) {
		return errors.New("exactly one of status or passthrough is required")
	}
	if o.Status != 0 && (o.Status < 200 || o.Status > 599) {
		return errors.New("status must be between 200 and 599")
	}
	if o.Body != "" && (o.Status == 204 || o.Status == 304) {
		return errors.New("status 204 and 304 must not include a body")
	}
	if err := validateTemplate("body", o.Body); err != nil {
		return err
	}
	return nil
}
func validateDuration(name, value string, allowZero bool) error {
	if value == "" {
		return nil
	}
	d, err := time.ParseDuration(value)
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	if d < 0 || (!allowZero && d == 0) {
		return fmt.Errorf("%s must be %s", name, map[bool]string{true: "non-negative", false: "positive"}[allowZero])
	}
	return nil
}

func validAuthHeader(s string) bool {
	if s == "" || len(s) > 256 {
		return false
	}
	for _, c := range s {
		valid := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", c)
		if !valid {
			return false
		}
	}
	return true
}

func nodeBuildError(name string, err error) error {
	if err != nil && (name == "rate_limit" || name == "replay_guard") {
		return fmt.Errorf("%w: %w", ErrProtectionConfiguration, err)
	}
	if err != nil && name == "auth" {
		return fmt.Errorf("%w: %w", ErrAuthConfiguration, err)
	}
	return err
}
