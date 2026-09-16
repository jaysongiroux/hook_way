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

	"github.com/jasongiroux/hookway/internal/auth"
	"github.com/jasongiroux/hookway/internal/graph"
	"github.com/jasongiroux/hookway/internal/transform"
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
	BodyFormat string `yaml:"body_format,omitempty" json:"body_format,omitempty" jsonschema:"enum=json,enum=xml,enum=raw"`
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
	MaxOutputBytes int64  `yaml:"max_output_bytes,omitempty" json:"max_output_bytes,omitempty" jsonschema:"minimum=1"`
	MaxCallStack   int    `yaml:"max_call_stack,omitempty" json:"max_call_stack,omitempty" jsonschema:"minimum=1"`
}
type SwitchCaseOptions struct {
	Port string                 `yaml:"port" json:"port" jsonschema:"required"`
	When FilterConditionOptions `yaml:"when" json:"when" jsonschema:"required"`
}
type SwitchOptions struct {
	Cases   []SwitchCaseOptions `yaml:"cases" json:"cases" jsonschema:"required,minItems=1"`
	Default string              `yaml:"default,omitempty" json:"default,omitempty"`
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
}
type PublishOptions struct {
	Destination       string         `yaml:"destination" json:"destination" jsonschema:"required"`
	Target            string         `yaml:"target" json:"target" jsonschema:"required"`
	Create            bool           `yaml:"create,omitempty" json:"create,omitempty"`
	MessageAttributes map[string]any `yaml:"message_attributes,omitempty" json:"message_attributes,omitempty"`
}
type RespondOptions struct {
	Status      int               `yaml:"status,omitempty" json:"status,omitempty" jsonschema:"minimum=100,maximum=599"`
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
		define("replay_guard", graph.PortVerified, graph.PortVerified, false, validateReplayGuard, newReplayGuard),
		define("envelope", graph.PortVerified, graph.PortEnvelope, false, validateEnvelope, func(id string, o EnvelopeOptions) graph.Node {
			n := newEnvelopeNode(id, o, opts.HeaderAllowlist, opts.HeaderDenylist).(envelopeNode)
			n.queryDenylist = opts.QueryDenylist
			return n
		}),
		define("filter", graph.PortEnvelope, graph.PortEnvelope, false, validateFilter, newFilterNode),
		define("lookup", graph.PortEnvelope, graph.PortEnvelope, false, validateLookup, func(id string, o LookupOptions) graph.Node {
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
		define("http_request", graph.PortEnvelope, graph.PortEnvelope, false, validateHTTPRequest, func(id string, o HTTPRequestOptions) graph.Node {
			return newHTTPRequestNode(id, o, opts.HeaderAllowlist, opts.HeaderDenylist)
		}),
		define("publish", graph.PortEnvelope, graph.PortTerminal, true, validatePublish, func(id string, o PublishOptions) graph.Node {
			return newPublishNode(id, o, opts.Publishers)
		}),
		define("respond", graph.PortEnvelope, graph.PortTerminal, false, validateRespond, newRespondNode),
	}
}

func (d nodeDefinition) spec() graph.NodeSpec {
	return graph.NodeSpec{Type: d.name, In: d.in, Out: d.out, Detached: d.detached, New: d.newNode}
}

type stubNode struct{ name string }

func (n stubNode) Run(context.Context, graph.Payload) (graph.Payload, error) {
	return nil, fmt.Errorf("%w: %s", errNotImplemented, n.name)
}

type passthroughNode struct{ name string }

func (n passthroughNode) Run(_ context.Context, in graph.Payload) (graph.Payload, error) {
	if in == nil {
		return nil, fmt.Errorf("%s: nil payload", n.name)
	}
	return in, nil
}

func Register(reg *graph.Registry) error {
	return RegisterWithOptions(reg, RegistryOptions{})
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
	return validateDuration("ttl", o.TTL, false)
}
func validateEnvelope(o EnvelopeOptions) error {
	if o.BodyFormat != "" && o.BodyFormat != "json" && o.BodyFormat != "xml" && o.BodyFormat != "raw" {
		return fmt.Errorf("unknown body_format %q", o.BodyFormat)
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
		if c.Port == "" || c.When.Source == "" || c.When.Path == "" || c.When.Operator == "" {
			return fmt.Errorf("cases[%d] requires port and a complete condition", i)
		}
		if _, ok := seen[c.Port]; ok {
			return fmt.Errorf("duplicate switch port %q", c.Port)
		}
		seen[c.Port] = struct{}{}
	}
	return nil
}
func validateHTTPRequest(o HTTPRequestOptions) error {
	if err := require("url", o.URL); err != nil {
		return err
	}
	switch strings.ToLower(strings.TrimSpace(o.Body)) {
	case "", "raw", "transformed":
	default:
		return fmt.Errorf("http_request.body %q must be raw or transformed", o.Body)
	}
	return validateDuration("timeout", o.Timeout, false)
}
func validatePublish(o PublishOptions) error {
	if err := require("destination", o.Destination); err != nil {
		return err
	}
	return require("target", o.Target)
}
func validateRespond(o RespondOptions) error {
	if o.Passthrough == (o.Status != 0) {
		return errors.New("exactly one of status or passthrough is required")
	}
	if o.Status != 0 && (o.Status < 100 || o.Status > 599) {
		return errors.New("status must be between 100 and 599")
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
