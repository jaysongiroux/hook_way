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

	"github.com/jaysongiroux/hook_way/internal/auth"
	"github.com/jaysongiroux/hook_way/internal/graph"
	"github.com/jaysongiroux/hook_way/internal/transform"
	"github.com/jaysongiroux/hook_way/internal/vars"
	"gopkg.in/yaml.v3"
)

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
	RPM   int    `yaml:"rpm" json:"rpm" jsonschema:"required,minimum=1"`
	Burst int    `yaml:"burst,omitempty" json:"burst,omitempty" jsonschema:"minimum=0"`
	KeyBy string `yaml:"key_by,omitempty" json:"key_by,omitempty"`
}
type ReadBodyOptions struct {
	MaxBytes int64 `yaml:"max_bytes" json:"max_bytes" jsonschema:"required,minimum=1"`
}
type SecretRefOptions struct {
	Provider string `yaml:"provider" json:"provider" jsonschema:"required,enum=env,enum=file,enum=aws_secrets_manager"`
	Name     string `yaml:"name" json:"name" jsonschema:"required"`
	Refresh  string `yaml:"refresh,omitempty" json:"refresh,omitempty"`
	Overlap  string `yaml:"overlap,omitempty" json:"overlap,omitempty"`
}
type AuthSourceOptions struct {
	Kind string `yaml:"kind" json:"kind" jsonschema:"required,enum=header,enum=query"`
	Key  string `yaml:"key" json:"key" jsonschema:"required"`
}
type AuthOptions struct {
	Scheme             string             `yaml:"scheme" json:"scheme" jsonschema:"required"`
	Secret             string             `yaml:"secret,omitempty" json:"secret,omitempty"`
	SecretRef          *SecretRefOptions  `yaml:"secret_ref,omitempty" json:"secret_ref,omitempty"`
	SignatureHeader    string             `yaml:"signature_header,omitempty" json:"signature_header,omitempty"`
	Source             *AuthSourceOptions `yaml:"source,omitempty" json:"source,omitempty"`
	TimestampTolerance string             `yaml:"timestamp_tolerance,omitempty" json:"timestamp_tolerance,omitempty"`
	RemoveBearer       bool               `yaml:"remove_bearer,omitempty" json:"remove_bearer,omitempty"`
	ExpectedIssuer     string             `yaml:"expected_issuer,omitempty" json:"expected_issuer,omitempty"`
	ExpectedAudience   string             `yaml:"expected_audience,omitempty" json:"expected_audience,omitempty"`
	BodyHashClaim      string             `yaml:"body_hash_claim,omitempty" json:"body_hash_claim,omitempty"`
}
type NoAuthOptions struct{}
type ReplayGuardOptions struct {
	TTL        string `yaml:"ttl,omitempty" json:"ttl,omitempty"`
	MaxEntries int    `yaml:"max_entries,omitempty" json:"max_entries,omitempty" jsonschema:"minimum=1"`
	Key        string `yaml:"key,omitempty" json:"key,omitempty"`
}
type EnvelopeOptions struct {
	BodyFormat string           `yaml:"body_format,omitempty" json:"body_format,omitempty" jsonschema:"enum=json,enum=xml,enum=raw,enum=multipart"`
	Multipart  MultipartOptions `yaml:"multipart,omitempty" json:"multipart,omitempty"`
}

// MultipartOptions bounds a body_format: multipart parse. The defaults are
// deliberately modest rather than generous: the worst case an operator is
// exposed to is max_parts x max_part_bytes held in memory, plus a 4/3 base64
// copy of whatever content is carried, per in-flight request.
type MultipartOptions struct {
	MaxParts     int   `yaml:"max_parts,omitempty" json:"max_parts,omitempty" jsonschema:"minimum=1,maximum=256"`
	MaxPartBytes int64 `yaml:"max_part_bytes,omitempty" json:"max_part_bytes,omitempty" jsonschema:"minimum=1"`
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
	Conditions []FilterConditionOptions `yaml:"conditions" json:"conditions" jsonschema:"required,minItems=1"`
	OnNoMatch  string                   `yaml:"on_no_match,omitempty" json:"on_no_match,omitempty" jsonschema:"enum=drop,enum=reject"`
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
type TransformOptions struct {
	Kind     string                `yaml:"kind" json:"kind" jsonschema:"required,enum=changed_fields,enum=field_map,enum=xml_prune"`
	From     string                `yaml:"from,omitempty" json:"from,omitempty"`
	To       string                `yaml:"to,omitempty" json:"to,omitempty"`
	Name     string                `yaml:"name,omitempty" json:"name,omitempty"`
	Mappings []FieldMappingOptions `yaml:"mappings,omitempty" json:"mappings,omitempty"`
	Rules    []string              `yaml:"rules,omitempty" json:"rules,omitempty"`
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
	URL            string            `yaml:"url" json:"url" jsonschema:"required"`
	ForwardMethod  bool              `yaml:"forward_method,omitempty" json:"forward_method,omitempty"`
	ForwardQuery   bool              `yaml:"forward_query,omitempty" json:"forward_query,omitempty"`
	ForwardHeaders []string          `yaml:"forward_headers,omitempty" json:"forward_headers,omitempty"`
	Timeout        string            `yaml:"timeout,omitempty" json:"timeout,omitempty"`
	Headers        map[string]string `yaml:"headers,omitempty" json:"headers,omitempty"`
	ErrorOnStatus  []string          `yaml:"error_on_status,omitempty" json:"error_on_status,omitempty"`
	Body           string            `yaml:"body,omitempty" json:"body,omitempty" jsonschema:"enum=raw,enum=transformed"`
	Retry          HTTPRetryOptions  `yaml:"retry,omitempty" json:"retry,omitempty"`
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
	MaxAttempts    int    `yaml:"max_attempts,omitempty" json:"max_attempts,omitempty" jsonschema:"minimum=1,maximum=10"`
	InitialBackoff string `yaml:"initial_backoff,omitempty" json:"initial_backoff,omitempty"`
	MaxBackoff     string `yaml:"max_backoff,omitempty" json:"max_backoff,omitempty"`
}
type PublishOptions struct {
	Destination       string         `yaml:"destination" json:"destination" jsonschema:"required"`
	Target            string         `yaml:"target" json:"target" jsonschema:"required"`
	Create            bool           `yaml:"create,omitempty" json:"create,omitempty"`
	MessageAttributes map[string]any `yaml:"message_attributes,omitempty" json:"message_attributes,omitempty"`
	// Payload selects how object-capable destinations store the event.
	// Empty or "envelope" (default) writes the JSON Message. "object" writes
	// decoded file bytes (body must be a {content, filename?, content_type?}
	// map, or an empty parsed body backed by raw request bytes). Only s3 and
	// redis_kv accept "object".
	Payload string `yaml:"payload,omitempty" json:"payload,omitempty" jsonschema:"enum=envelope,enum=object"`
}
type RespondOptions struct {
	Status      int               `yaml:"status,omitempty" json:"status,omitempty" jsonschema:"minimum=200,maximum=599"`
	Headers     map[string]string `yaml:"headers,omitempty" json:"headers,omitempty"`
	Body        string            `yaml:"body,omitempty" json:"body,omitempty"`
	Passthrough bool              `yaml:"passthrough,omitempty" json:"passthrough,omitempty"`
}

type nodeDefinition struct {
	name       string
	in, out    graph.PortType
	detached   bool
	newOptions func() any
	newNode    func(graph.NodeConfig) (graph.Node, error)
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
				return nil, err
			}
			if validate != nil {
				if err := validate(opts); err != nil {
					return nil, fmt.Errorf("node %q (%s): %w", nc.ID, name, err)
				}
			}
			return build(nc.ID, opts)
		},
	}
}

// RegistryOptions customizes runtime node constructors used when compiling
// flows for serving. Validation uses the zero value.
type RegistryOptions struct {
	Publishers      PublisherResolver
	Lookups         func(LookupOptions) LookupFunc
	Secrets         func(SecretRefOptions) (SecretsFunc, error)
	Logger          *slog.Logger
	HeaderDenylist  []string
	HeaderAllowlist []string
	QueryDenylist   []string
}

// SecretsFunc returns the currently-valid secret values for an auth node.
// Serving injects a cached secretref resolver; tests inject a fake. Defined
// here so flowconfig does not import internal/secretref (WASM).
type SecretsFunc func(ctx context.Context) ([][]byte, error)

func catalog(opts RegistryOptions) []nodeDefinition {
	return []nodeDefinition{
		define("http_trigger", graph.PortNone, graph.PortRequestHead, false, func(o HTTPTriggerOptions) error { return require("path", o.Path) }, newHTTPTrigger),
		define("loop_guard", graph.PortRequestHead, graph.PortRequestHead, false, func(o LoopGuardOptions) error {
			if o.MaxHops < 0 {
				return errors.New("max_hops must be >= 0")
			}
			return nil
		}, newLoopGuard),
		define("ip_allowlist", graph.PortRequestHead, graph.PortRequestHead, false, validateIPAllowlist, newIPAllowlist),
		define("rate_limit", graph.PortRequestHead, graph.PortRequestHead, false, func(o RateLimitOptions) error {
			if o.RPM <= 0 {
				return errors.New("rpm must be positive")
			}
			if o.Burst < 0 {
				return errors.New("burst must be >= 0")
			}
			return nil
		}, newRateLimit),
		define("read_body", graph.PortRequestHead, graph.PortRawRequest, false, func(o ReadBodyOptions) error {
			if o.MaxBytes <= 0 {
				return errors.New("max_bytes must be positive")
			}
			return nil
		}, newReadBody),
		defineErr("auth", graph.PortRawRequest, graph.PortVerified, false, validateAuth, func(id string, o AuthOptions) (graph.Node, error) {
			var secrets SecretsFunc
			if o.SecretRef != nil && opts.Secrets != nil {
				fn, err := opts.Secrets(*o.SecretRef)
				if err != nil {
					return nil, err
				}
				secrets = fn
			}
			return newAuthNode(id, o, opts.Logger, secrets), nil
		}),
		define[NoAuthOptions]("no_auth", graph.PortRawRequest, graph.PortVerified, false, nil, func(id string, _ NoAuthOptions) graph.Node {
			return newNoAuthNode(id, opts.Logger)
		}),
		defineErr("replay_guard", graph.PortVerified, graph.PortVerified, false, validateReplayGuard, newReplayGuard),
		define("envelope", graph.PortVerified, graph.PortEnvelope, false, validateEnvelope, func(id string, o EnvelopeOptions) graph.Node {
			n := newEnvelopeNode(id, o, opts.HeaderAllowlist, opts.HeaderDenylist).(envelopeNode)
			n.queryDenylist = opts.QueryDenylist
			return n
		}),
		define("filter", graph.PortEnvelope, graph.PortEnvelope, false, validateFilter, newFilterNode),
		defineErr("lookup", graph.PortEnvelope, graph.PortEnvelope, false, validateLookup, func(id string, o LookupOptions) (graph.Node, error) {
			var fn LookupFunc
			if opts.Lookups != nil {
				fn = opts.Lookups(o)
			}
			return newLookupNode(id, o, fn)
		}),
		define("transform", graph.PortEnvelope, graph.PortEnvelope, false, validateTransform, func(id string, o TransformOptions) graph.Node {
			return newTransformNode(id, o, opts.Logger)
		}),
		defineErr("code", graph.PortEnvelope, graph.PortEnvelope, false, validateCode, func(id string, o CodeOptions) (graph.Node, error) {
			n, err := newCodeNode(id, o)
			if err != nil {
				return nil, err
			}
			code := n.(codeNode)
			code.allowlist, code.denylist = opts.HeaderAllowlist, opts.HeaderDenylist
			return code, nil
		}),
		define("switch", graph.PortEnvelope, graph.PortEnvelope, false, validateSwitch, newSwitchNode),
		define("if_else", graph.PortEnvelope, graph.PortEnvelope, false, validateIfElse, newIfElseNode),
		defineErr("http_request", graph.PortEnvelope, graph.PortEnvelope, false, validateHTTPRequest, func(id string, o HTTPRequestOptions) (graph.Node, error) {
			return newHTTPRequestNode(id, o, opts.HeaderAllowlist, opts.HeaderDenylist)
		}),
		defineErr("publish", graph.PortEnvelope, graph.PortTerminal, true, validatePublish, func(id string, o PublishOptions) (graph.Node, error) {
			return newPublishNode(id, o, opts.Publishers)
		}),
		defineErr("respond", graph.PortEnvelope, graph.PortTerminal, false, validateRespond, newRespondNode),
	}
}

func (d nodeDefinition) spec() graph.NodeSpec {
	return graph.NodeSpec{Type: d.name, In: d.in, Out: d.out, Detached: d.detached, New: d.newNode}
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

func NewRegistry() (*graph.Registry, error) {
	return NewRegistryWithOptions(RegistryOptions{})
}

func NewRegistryWithOptions(opts RegistryOptions) (*graph.Registry, error) {
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
	if o.MaxEntries < 0 {
		return errors.New("max_entries must be >= 1")
	}
	if err := validateDuration("ttl", o.TTL, false); err != nil {
		return err
	}
	return validateTemplate("key", o.Key)
}
func validateEnvelope(o EnvelopeOptions) error {
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
	set := o.Multipart.MaxParts != 0 || o.Multipart.MaxPartBytes != 0 || o.Multipart.IncludeContent != nil
	if set && o.BodyFormat != "multipart" {
		return errors.New("multipart options have no effect without body_format: multipart")
	}
	if o.Multipart.MaxParts < 0 {
		return errors.New("multipart.max_parts must not be negative")
	}
	if o.Multipart.MaxParts > 256 {
		return errors.New("multipart.max_parts must be 256 or fewer")
	}
	if o.Multipart.MaxPartBytes < 0 {
		return errors.New("multipart.max_part_bytes must not be negative")
	}
	return nil
}
func validateFilter(o FilterOptions) error {
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
	if err := require("kind", o.Kind); err != nil {
		return err
	}
	switch o.Kind {
	case "changed_fields":
		if o.From == "" || o.To == "" {
			return errors.New("changed_fields requires from and to")
		}
	case "field_map":
		if len(o.Mappings) == 0 {
			return errors.New("field_map requires mappings")
		}
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
	if o.MaxInputBytes < 0 {
		return errors.New("max_input_bytes must be positive")
	}
	if o.MaxOutputBytes < 0 {
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
	if err := require("url", o.URL); err != nil {
		return err
	}
	if err := validateTemplate("url", o.URL); err != nil {
		return err
	}
	if tpl, err := vars.Parse(o.URL); err == nil && !tpl.HasPlaceholders() {
		if err := validateHTTPURL(o.URL); err != nil {
			return fmt.Errorf("url: %w", err)
		}
	}
	// A templated URL gets no host validation above -- there is no host to
	// validate until a request renders one. That is exactly the SSRF case, so
	// the shape of the template is constrained instead: a {variable} in the
	// authority must be followed by a literal dotted domain, which pins the
	// destination to a fixed domain no matter what a request supplies.
	if err := validateURLHostTemplate(o.URL); err != nil {
		return fmt.Errorf("url: %w", err)
	}
	// The egress policy only applies when a request can choose the host, so
	// opting out of it on a node with a literal host does nothing. Reject it
	// rather than accept a line that reads like a security decision and is
	// not one -- the same reasoning as the retry block below.
	if o.AllowPrivateTargets && !urlAuthorityIsTemplated(o.URL) {
		return errors.New("allow_private_targets has no effect unless the url host contains a {variable}; a literal host is already the operator's own choice")
	}
	switch strings.ToLower(strings.TrimSpace(o.Body)) {
	case "", "raw", "transformed":
	default:
		return fmt.Errorf("http_request.body %q must be raw or transformed", o.Body)
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
	switch o.Payload {
	case "", "envelope", "object":
		return nil
	default:
		return fmt.Errorf("payload %q is not supported (use envelope or object)", o.Payload)
	}
}
func validateRespond(o RespondOptions) error {
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
