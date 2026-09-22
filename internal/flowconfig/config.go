package flowconfig

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/jaysongiroux/hook_way/internal/config"
	"github.com/jaysongiroux/hook_way/internal/graph"
	"github.com/jaysongiroux/hook_way/internal/vars"
	"gopkg.in/yaml.v3"
)

// Config is a Hookway config document: shared process settings plus named flows.
type Config struct {
	Server       config.ServerConfig
	Security     config.SecurityConfig
	Metrics      config.MetricsConfig
	Logging      config.LoggingConfig
	Replay       config.ReplayConfig
	Destinations map[string]config.DestinationConfig
	Flows        map[string]graph.FlowConfig
}

// Load reads path and expands environment variables strictly.
func Load(path string) (Config, []config.ValidationIssue) {
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return Config{}, []config.ValidationIssue{cfgIssue("config", fmt.Sprintf("read %s: %v", path, err), "")}
	}
	return LoadBytes(data, path, true)
}

// LoadBytes decodes a config document, compiles every flow, and reports every
// structural problem it can find. Policy lint is a separate pass.
func LoadBytes(data []byte, source string, expandEnv bool) (Config, []config.ValidationIssue) {
	if expandEnv {
		expanded, missing := config.ExpandEnvStrict(string(data))
		if len(missing) > 0 {
			return Config{}, explicitErrorSeverity(config.MissingEnvIssues(missing))
		}
		data = []byte(expanded)
	}

	var issues []config.ValidationIssue
	// Only `flows` is tolerated alongside the shared process settings; it is
	// decoded separately below. Every other top-level key -- including
	// `endpoints` -- is reported as unknown by the strict decoder.
	shared, sharedIssues := config.DecodeSharedYAML(data, source, "flows")
	issues = append(issues, sharedIssues...)
	issues = append(issues, shared.ValidationIssues()...)

	flows, flowIssues := decodeFlows(data, source)
	issues = append(issues, flowIssues...)

	cfg := Config{
		Server:       shared.Server,
		Security:     shared.Security,
		Metrics:      shared.Metrics,
		Logging:      shared.Logging,
		Replay:       shared.Replay,
		Destinations: shared.Destinations,
		Flows:        flows,
	}
	issues = append(issues, compileFlows(cfg)...)
	issues = append(issues, crossFlowIssues(cfg)...)
	return cfg, issues
}

// ServingConfig projects this document onto config.Config so the
// existing destination factory can collect webhook targets from publish nodes.
func (c Config) ServingConfig() config.Config {
	cfg := config.Config{
		Server: c.Server, Security: c.Security, Metrics: c.Metrics,
		Logging: c.Logging, Replay: c.Replay, Destinations: c.Destinations,
		Endpoints: map[string]config.EndpointConfig{},
	}
	for name, fc := range c.Flows {
		path := "/"
		var dests []config.EndpointDestinationConfig
		variables := map[string]config.VariableConfig{}
		for _, nc := range fc.Nodes {
			switch nc.Type {
			case "http_trigger":
				if opts, err := decodeOptions[HTTPTriggerOptions](nc); err == nil && opts.Path != "" {
					path = opts.Path
				}
			case "publish":
				if opts, err := decodeOptions[PublishOptions](nc); err == nil && opts.Destination != "" {
					// Reuse the destination factory's existing safe-name matcher.
					// Path {vars} are resolved by nodes, not the destination factory's variable resolver.
					if tpl, err := vars.Parse(opts.Target); err == nil {
						for _, name := range tpl.Names() {
							variables[name] = config.VariableConfig{Pattern: "^[a-zA-Z0-9._-]{1,128}$"}
						}
					}
					dests = append(dests, config.EndpointDestinationConfig{
						Destination: opts.Destination, Target: opts.Target, Create: opts.Create,
					})
				}
			}
		}
		cfg.Endpoints[name] = config.EndpointConfig{Enabled: true, Path: path, Destinations: dests, Variables: variables}
	}
	return cfg
}

// ValidateBytes is the non-expanding validation entry used by the CLI JSON
// path and the editor. It includes policy lint.
func ValidateBytes(data []byte) []config.ValidationIssue {
	cfg, issues := LoadBytes(data, "config", false)
	return append(append(issues, Lint(cfg)...), StandaloneIssues(cfg)...)
}

func decodeFlows(data []byte, source string) (map[string]graph.FlowConfig, []config.ValidationIssue) {
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return map[string]graph.FlowConfig{}, []config.ValidationIssue{
			cfgIssue("config", fmt.Sprintf("invalid YAML syntax in %s: %v", source, err), ""),
		}
	}
	if len(document.Content) == 0 {
		return map[string]graph.FlowConfig{}, nil
	}
	root := document.Content[0]
	if root.Kind != yaml.MappingNode {
		return map[string]graph.FlowConfig{}, []config.ValidationIssue{cfgIssue("flows", "top level must be a mapping", "")}
	}
	var flowsNode *yaml.Node
	for i := 0; i < len(root.Content); i += 2 {
		if root.Content[i].Value == "flows" {
			flowsNode = root.Content[i+1]
			break
		}
	}
	if flowsNode == nil || flowsNode.Kind == yaml.ScalarNode && (flowsNode.Value == "" || flowsNode.Value == "null") {
		return map[string]graph.FlowConfig{}, nil
	}
	if flowsNode.Kind != yaml.MappingNode {
		return map[string]graph.FlowConfig{}, []config.ValidationIssue{
			cfgIssue("flows", "must be a mapping of flow name to flow config", ""),
		}
	}

	out := map[string]graph.FlowConfig{}
	var issues []config.ValidationIssue
	for i := 0; i < len(flowsNode.Content); i += 2 {
		name := flowsNode.Content[i].Value
		node := flowsNode.Content[i+1]
		field := "flows." + name
		if node.Kind != yaml.MappingNode {
			issues = append(issues, cfgIssue(field, "flow must be a mapping", ""))
			continue
		}
		encoded, err := yaml.Marshal(node)
		if err != nil {
			issues = append(issues, cfgIssue(field, err.Error(), ""))
			continue
		}
		var fc graph.FlowConfig
		decoder := yaml.NewDecoder(bytes.NewReader(encoded))
		decoder.KnownFields(true)
		if err := decoder.Decode(&fc); err != nil {
			issues = append(issues, cfgIssue(field, fmt.Sprintf("unknown or invalid YAML field: %v", err),
				"remove the field or correct its spelling"))
			continue
		}
		out[name] = fc
	}
	return out, issues
}

func compileFlows(cfg Config) []config.ValidationIssue {
	reg, err := NewRegistry()
	if err != nil {
		return []config.ValidationIssue{cfgIssue("flows", err.Error(), "")}
	}
	var issues []config.ValidationIssue
	for _, name := range sortedKeys(cfg.Flows) {
		_, err := graph.Compile(name, cfg.Flows[name], reg)
		if err == nil {
			continue
		}
		var ce *graph.CompileError
		if !errors.As(err, &ce) {
			issues = append(issues, cfgIssue("flows."+name, err.Error(), ""))
			continue
		}
		for _, problem := range ce.Problems {
			issues = append(issues, cfgIssue("flows."+name, problem, ""))
		}
	}
	return issues
}

func crossFlowIssues(cfg Config) []config.ValidationIssue {
	issues := RoutingIssues(cfg)
	for _, name := range sortedKeys(cfg.Flows) {
		flow := cfg.Flows[name]
		replayCaptured := replayCapturesFlow(cfg.Replay, flow)
		for _, nc := range flow.Nodes {
			switch nc.Type {
			case "publish":
				opts, err := decodeOptions[PublishOptions](nc)
				if err != nil || opts.Destination == "" {
					continue
				}
				dest, ok := cfg.Destinations[opts.Destination]
				if !ok {
					issues = append(issues, cfgIssue("flows."+name,
						fmt.Sprintf("publish destination %q is not in destinations (known: %s)",
							opts.Destination, strings.Join(sortedKeys(cfg.Destinations), ", ")),
						"reference an existing destinations.* id"))
					continue
				}
				if opts.Payload == "object" && !objectPayloadDestType(dest.Type) {
					issues = append(issues, cfgIssue("flows."+name+".nodes."+nc.ID+".payload",
						fmt.Sprintf("payload: object is not supported for destination type %q", dest.Type),
						"use an s3 or redis_kv destination, or omit payload (envelope)"))
				}
				if opts.Payload == "object" && replayCaptured {
					issues = append(issues, cfgIssue("flows."+name+".nodes."+nc.ID+".payload",
						"payload: object cannot be used while replay capture is enabled for this flow",
						"disable replay for the process, skip capture for this no_auth flow, or use payload: envelope"))
				}
				if dest.Type == "lambda" {
					field := "flows." + name + ".nodes." + nc.ID
					if opts.Create {
						issues = append(issues, cfgIssue(field+".create",
							"create is not supported for lambda destinations (Hookway does not create functions)",
							"remove create, or set create: false"))
					}
					issues = append(issues, lambdaTargetIssues(field+".target", opts.Target)...)
				}
			}
		}
	}
	return issues
}

// lambdaFunctionName matches AWS Lambda FunctionName forms: plain name,
// name:qualifier (including :$LATEST), partial ARN, or full ARN (max 256).
var lambdaFunctionName = regexp.MustCompile(
	`^(?:` +
		`[a-zA-Z0-9](?:[a-zA-Z0-9._-]{0,63})(?::(?:\$LATEST|[a-zA-Z0-9._-]+))?` +
		`|` +
		`\d{12}:function:[a-zA-Z0-9](?:[a-zA-Z0-9._-]{0,63})(?::(?:\$LATEST|[a-zA-Z0-9._-]+))?` +
		`|` +
		`arn:aws(?:-[a-z]+)?:lambda:[a-z0-9-]+:\d{12}:function:[a-zA-Z0-9](?:[a-zA-Z0-9._-]{0,63})(?::(?:\$LATEST|[a-zA-Z0-9._-]+))?` +
		`)$`,
)

// lambdaTargetLiteral constrains literal text around {variable} placeholders
// in a templated lambda target (colons allowed for "{fn}:prod").
var lambdaTargetLiteral = regexp.MustCompile(`^[a-zA-Z0-9._$:-]*$`)

func lambdaTargetIssues(field, target string) []config.ValidationIssue {
	tpl, err := vars.Parse(target)
	if err != nil {
		return []config.ValidationIssue{cfgIssue(field, "target template is malformed: "+err.Error(),
			"use {variable} placeholders with balanced braces, e.g. \"{fn}:prod\"")}
	}
	if tpl.HasPlaceholders() {
		if !lambdaTargetLiteral.MatchString(tpl.Literal()) {
			return []config.ValidationIssue{cfgIssue(field,
				"target template literal text contains unsupported characters",
				"use letters, numbers, dot, underscore, hyphen, colon, or $ around {variable} placeholders")}
		}
		if len(target) > 256 {
			return []config.ValidationIssue{cfgIssue(field,
				"target template exceeds 256 characters",
				"shorten the function name template")}
		}
		return nil
	}
	if target == "" || len(target) > 256 {
		return []config.ValidationIssue{cfgIssue(field,
			"lambda target must be 1-256 characters",
			"set target to a function name, name:alias, or function ARN")}
	}
	if !lambdaFunctionName.MatchString(target) {
		return []config.ValidationIssue{cfgIssue(field,
			"lambda target is not a valid function name or ARN",
			"use a function name (optionally :alias / :version / :$LATEST) or a function ARN")}
	}
	return nil
}

// Keep this check in flowconfig rather than importing queue: the same validator
// is compiled to WebAssembly and the queue package pulls in backend SDKs.
func objectPayloadDestType(destType string) bool {
	return destType == "s3" || destType == "redis_kv"
}

func replayCapturesFlow(replay config.ReplayConfig, flow graph.FlowConfig) bool {
	if !replay.Enabled {
		return false
	}
	if replay.SkipUnauthenticated {
		for _, node := range flow.Nodes {
			if node.Type == "no_auth" {
				return false
			}
		}
	}
	return true
}

// RoutingIssues is shared by validation and serving so an ambiguous path
// cannot silently select a different flow's authentication policy.
func RoutingIssues(cfg Config) []config.ValidationIssue {
	type route struct {
		name    string
		pattern vars.PathPattern
	}
	var routes []route
	var issues []config.ValidationIssue
	reserved := []string{"/health", "/healthz", "/ready", "/readyz"}
	if cfg.Metrics.Enabled && cfg.Metrics.Addr == "" {
		reserved = append(reserved, cfg.Metrics.Path)
	}
	for _, name := range sortedKeys(cfg.Flows) {
		for _, nc := range cfg.Flows[name].Nodes {
			if nc.Type != "http_trigger" {
				continue
			}
			opts, err := TriggerOptions(nc)
			if err != nil {
				continue
			}
			pattern, err := vars.ParsePath(opts.Path)
			if err != nil {
				issues = append(issues, cfgIssue("flows."+name, err.Error(), "use a valid http_trigger path"))
				continue
			}
			for _, path := range reserved {
				rp, err := vars.ParsePath(path)
				if err == nil && vars.Overlap(pattern, rp) {
					issues = append(issues, cfgIssue("flows."+name, fmt.Sprintf("trigger path %s overlaps reserved path %s", opts.Path, path), "choose a separate webhook path"))
				}
			}
			for _, prev := range routes {
				if vars.Overlap(pattern, prev.pattern) {
					issues = append(issues, cfgIssue("flows."+name, fmt.Sprintf("trigger path %s is already claimed by or overlaps flow %q (%s)", opts.Path, prev.name, prev.pattern.String()), "give each flow a non-overlapping http_trigger path"))
				}
			}
			routes = append(routes, route{name, pattern})
		}
	}
	return issues
}

func cfgIssue(field, message, suggestion string) config.ValidationIssue {
	return config.ValidationIssue{
		Field: field, Message: message, Suggestion: suggestion, Severity: config.SeverityError,
	}
}

func cfgWarning(field, message, suggestion string) config.ValidationIssue {
	return config.ValidationIssue{
		Field: field, Message: message, Suggestion: suggestion, Severity: config.SeverityWarning,
	}
}

func explicitErrorSeverity(issues []config.ValidationIssue) []config.ValidationIssue {
	out := make([]config.ValidationIssue, len(issues))
	for i, issue := range issues {
		if issue.Severity == "" {
			issue.Severity = config.SeverityError
		}
		out[i] = issue
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// StandaloneIssues rejects features that require an embedding-only binding.
// Keep graph validation usable with explicitly injected providers in tests/hosts.
func StandaloneIssues(cfg Config) []config.ValidationIssue {
	var issues []config.ValidationIssue
	for _, name := range sortedKeys(cfg.Flows) {
		for _, nc := range cfg.Flows[name].Nodes {
			if nc.Type == "lookup" {
				issues = append(issues, cfgIssue("flows."+name, "lookup requires an injected provider; the standalone server has no lookup provider configuration", "use path variables or transform field_map; standalone lookup binding is not implemented"))
			}
		}
	}
	return issues
}

// AuthenticationRedactions includes configured credential sources even when
// their names do not match the built-in sensitive-header heuristic.
func AuthenticationRedactions(cfg Config) (headers, queries []string) {
	for _, fc := range cfg.Flows {
		for _, nc := range fc.Nodes {
			if nc.Type != "auth" {
				continue
			}
			o, err := decodeOptions[AuthOptions](nc)
			if err != nil {
				continue
			}
			if o.Source != nil {
				if o.Source.Kind == "query" {
					queries = append(queries, o.Source.Key)
				} else {
					headers = append(headers, o.Source.Key)
				}
			} else if o.SignatureHeader != "" {
				headers = append(headers, o.SignatureHeader)
			}
		}
	}
	return headers, queries
}
