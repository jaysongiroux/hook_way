package flowconfig

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/jaysongiroux/hook_way/internal/config"
	"github.com/jaysongiroux/hook_way/internal/featureflags"
	"github.com/jaysongiroux/hook_way/internal/graph"
	"github.com/jaysongiroux/hook_way/internal/vars"
	"gopkg.in/yaml.v3"
)

type LaunchDarklyConnection struct {
	SDKKeyEnv             string `yaml:"sdk_key_env" json:"sdk_key_env" jsonschema:"required"`
	InitializationTimeout string `yaml:"initialization_timeout,omitempty" json:"initialization_timeout,omitempty" jsonschema:"default=5s"`
	SendEvents            *bool  `yaml:"send_events,omitempty" json:"send_events,omitempty" jsonschema:"default=true"`
}
type FlagSelector struct {
	Source   string `yaml:"source,omitempty" json:"source,omitempty" jsonschema:"enum=body,enum=header,enum=vars"`
	Path     string `yaml:"path,omitempty" json:"path,omitempty"`
	Value    any    `yaml:"value,omitempty" json:"value,omitempty"`
	hasValue bool
}
type FlagContextOptions struct {
	Kind       string                  `yaml:"kind,omitempty" json:"kind,omitempty" jsonschema:"default=user"`
	Key        FlagSelector            `yaml:"key" json:"key" jsonschema:"required"`
	Attributes map[string]FlagSelector `yaml:"attributes,omitempty" json:"attributes,omitempty"`
}
type FlagOptions struct {
	Key         string `yaml:"key" json:"key" jsonschema:"required"`
	Type        string `yaml:"type" json:"type" jsonschema:"required,enum=boolean,enum=string,enum=number,enum=json"`
	Fallback    any    `yaml:"fallback" json:"fallback" jsonschema:"required"`
	Variable    string `yaml:"variable,omitempty" json:"variable,omitempty"`
	hasFallback bool
}
type LaunchDarklyOptions struct {
	Connection string                 `yaml:"connection" json:"connection" jsonschema:"required"`
	Name       string                 `yaml:"name" json:"name" jsonschema:"required"`
	Context    FlagContextOptions     `yaml:"context" json:"context" jsonschema:"required"`
	Flags      map[string]FlagOptions `yaml:"flags" json:"flags" jsonschema:"required"`
}

func present(node *yaml.Node, key string) bool {
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return true
		}
	}
	return false
}
func decodeFlagFields(node *yaml.Node, out any) error {
	data, err := yaml.Marshal(node)
	if err != nil {
		return err
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	return dec.Decode(out)
}
func (s *FlagSelector) UnmarshalYAML(node *yaml.Node) error {
	type plain FlagSelector
	var out plain
	if err := decodeFlagFields(node, &out); err != nil {
		return err
	}
	*s = FlagSelector(out)
	s.hasValue = present(node, "value")
	return nil
}
func (s *FlagOptions) UnmarshalYAML(node *yaml.Node) error {
	type plain FlagOptions
	var out plain
	if err := decodeFlagFields(node, &out); err != nil {
		return err
	}
	*s = FlagOptions(out)
	s.hasFallback = present(node, "fallback")
	return nil
}

var flagEnvName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var flagKind = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
var flagHeaderName = regexp.MustCompile("^[!#$%&'*+.^_\x60|~0-9A-Za-z-]+$")

func (c LaunchDarklyConnection) Validate() error {
	if !flagEnvName.MatchString(c.SDKKeyEnv) {
		return errors.New("sdk_key_env must name an environment variable, not contain a key or ${reference}")
	}
	if c.InitializationTimeout != "" {
		d, e := time.ParseDuration(c.InitializationTimeout)
		if e != nil || d <= 0 || d > time.Minute {
			return errors.New("initialization_timeout must be positive and at most 1m")
		}
	}
	return nil
}
func validateFlagSelector(s FlagSelector) error {
	if s.hasValue {
		if s.Source != "" || s.Path != "" {
			return errors.New("set either value or source and path")
		}
		_, err := featureflags.Normalize(s.Value)
		return err
	}
	if s.Source != "body" && s.Source != "header" && s.Source != "vars" {
		return errors.New("source must be body, header or vars")
	}
	if strings.TrimSpace(s.Path) == "" {
		return errors.New("path is required")
	}
	switch s.Source {
	case "vars":
		if !vars.ValidName(s.Path) {
			return errors.New("vars path must be a variable identifier")
		}
	case "header":
		if !flagHeaderName.MatchString(s.Path) {
			return errors.New("header path must be a valid header name")
		}
	case "body":
		for _, segment := range strings.Split(s.Path, ".") {
			if segment == "" || strings.ContainsAny(segment, "[]") {
				return errors.New("body path must contain nonempty object fields; array indexing is unsupported")
			}
		}
	}
	return nil
}
func validateLaunchDarkly(o LaunchDarklyOptions) error {
	if strings.TrimSpace(o.Connection) == "" || !vars.ValidName(o.Name) {
		return errors.New("connection and a valid variable identifier name are required")
	}
	if o.Context.Kind != "" && (!flagKind.MatchString(o.Context.Kind) || o.Context.Kind == "kind" || o.Context.Kind == "multi") {
		return errors.New("context.kind must be a single context kind")
	}
	if err := validateFlagSelector(o.Context.Key); err != nil {
		return fmt.Errorf("context.key: %w", err)
	}
	if o.Context.Key.hasValue {
		if key, ok := o.Context.Key.Value.(string); !ok || key == "" {
			return errors.New("context.key.value must be a nonempty string")
		}
	}
	for name, s := range o.Context.Attributes {
		if name == "" || name == "key" || name == "kind" || name == "_meta" {
			return errors.New("context attributes must not replace reserved context fields")
		}
		if err := validateFlagSelector(s); err != nil {
			return fmt.Errorf("context.attributes.%s: %w", name, err)
		}
	}
	if len(o.Flags) == 0 {
		return errors.New("flags must contain at least one named flag")
	}
	bindings := map[string]bool{}
	for _, alias := range sortedKeys(o.Flags) {
		f := o.Flags[alias]
		if !vars.ValidName(alias) || strings.TrimSpace(f.Key) == "" {
			return errors.New("flags require valid output aliases and nonempty flag keys")
		}
		if !f.hasFallback {
			return fmt.Errorf("flags.%s.fallback is required (null is allowed for json)", alias)
		}
		v, err := featureflags.Normalize(f.Fallback)
		if err != nil || !featureflags.Matches(f.Type, v) {
			return fmt.Errorf("flags.%s.fallback must match type %s and fit JSON budgets", alias, f.Type)
		}
		if f.Variable != "" {
			if !vars.ValidName(f.Variable) || bindings[f.Variable] {
				return errors.New("variable bindings must be valid, distinct variable identifiers")
			}
			bindings[f.Variable] = true
		}
	}
	return nil
}
func LaunchDarklyConfigurationIssues(cfg Config) []config.ValidationIssue {
	var issues []config.ValidationIssue
	for _, name := range sortedKeys(cfg.LaunchDarkly) {
		if !graph.ValidID(name) {
			issues = append(issues, cfgIssue("launchdarkly."+name, "invalid connection name", ""))
		}
		if err := cfg.LaunchDarkly[name].Validate(); err != nil {
			issues = append(issues, cfgIssue("launchdarkly."+name, err.Error(), ""))
		}
	}
	for _, flow := range sortedKeys(cfg.Flows) {
		names := map[string]bool{}
		for _, node := range ExpandedNodes(cfg.Flows[flow]) {
			if node.Type != "launchdarkly" {
				continue
			}
			o, err := decodeOptions[LaunchDarklyOptions](node)
			if err != nil {
				continue
			}
			field := "flows." + flow + ".nodes." + node.ID
			if _, ok := cfg.LaunchDarkly[o.Connection]; !ok {
				issues = append(issues, cfgIssue(field+".connection", "LaunchDarkly connection is not configured", ""))
			}
			if names[o.Name] {
				issues = append(issues, cfgIssue(field+".name", "flag result namespace must be unique per flow", ""))
			}
			names[o.Name] = true
		}
	}
	return issues
}

type launchDarklyNode struct {
	id       string
	opts     LaunchDarklyOptions
	evaluate featureflags.Evaluator
	observe  func(string, string)
}

func resolveFlagSelector(s FlagSelector, env graph.Envelope) (any, bool) {
	if s.hasValue {
		return s.Value, true
	}
	switch s.Source {
	case "body":
		return dotted(env.Body, s.Path)
	case "header":
		h := eventHeaders(env)
		if len(h.Values(s.Path)) == 0 {
			return nil, false
		}
		return h.Get(s.Path), true
	case "vars":
		v, ok := env.Vars[s.Path]
		return v, ok
	}
	return nil, false
}
func (n launchDarklyNode) evaluationContext(env graph.Envelope) (featureflags.Context, bool) {
	value, ok := resolveFlagSelector(n.opts.Context.Key, env)
	key, isString := value.(string)
	if !ok || !isString || key == "" {
		return featureflags.Context{}, false
	}
	kind := n.opts.Context.Kind
	if kind == "" {
		kind = "user"
	}
	out := featureflags.Context{Kind: kind, Key: key, Attributes: map[string]any{}}
	for name, s := range n.opts.Context.Attributes {
		v, ok := resolveFlagSelector(s, env)
		if !ok {
			return featureflags.Context{}, false
		}
		normalized, err := featureflags.Normalize(v)
		if err != nil || normalized == nil {
			return featureflags.Context{}, false
		}
		out.Attributes[name] = normalized
	}
	return out, true
}
func (n launchDarklyNode) Run(ctx context.Context, in graph.Payload) (graph.Payload, error) {
	env, ok := in.(graph.Envelope)
	if !ok {
		return nil, graph.NodeError{NodeID: n.id, Status: http.StatusInternalServerError, Err: errors.New("launchdarkly requires an envelope")}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	evaluationContext, valid := n.evaluationContext(env)
	results := map[string]any{}
	values := maps.Clone(env.Vars)
	if values == nil {
		values = map[string]string{}
	}
	for _, alias := range sortedKeys(n.opts.Flags) {
		f := n.opts.Flags[alias]
		fallback, _ := featureflags.Normalize(f.Fallback)
		r := featureflags.Result{Value: fallback, UsedFallback: true, Reason: "ERROR", ErrorKind: "CLIENT_NOT_READY"}
		if !valid {
			r.ErrorKind = "CONTEXT_INVALID"
		} else if n.evaluate != nil {
			r = n.evaluate(evaluationContext, f.Key, f.Type, fallback)
		}
		normalized, err := featureflags.Normalize(r.Value)
		if err != nil || !featureflags.Matches(f.Type, normalized) {
			r = featureflags.Result{Value: fallback, UsedFallback: true, Reason: "ERROR", ErrorKind: "WRONG_TYPE"}
			normalized = fallback
		}
		result := map[string]any{"value": normalized, "used_fallback": r.UsedFallback, "reason": r.Reason}
		if r.ErrorKind != "" {
			result["error_kind"] = r.ErrorKind
		}
		results[alias] = result
		if f.Variable != "" {
			if s, ok := normalized.(string); ok && f.Type == "string" {
				values[f.Variable] = s
			} else {
				data, _ := json.Marshal(normalized)
				values[f.Variable] = string(data)
			}
		}
		if n.observe != nil {
			outcome := "evaluated"
			if r.UsedFallback {
				outcome = "fallback"
			}
			n.observe(n.id, outcome)
		}
	}
	flags := maps.Clone(env.Flags)
	if flags == nil {
		flags = map[string]any{}
	}
	flags[n.opts.Name] = results
	env.Flags = flags
	env.Vars = values
	return env, nil
}
