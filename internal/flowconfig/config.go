package flowconfig

import (
	"bytes"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/jasongiroux/hookway/internal/config"
	"github.com/jasongiroux/hookway/internal/graph"
	"github.com/jasongiroux/hookway/internal/vars"
	"gopkg.in/yaml.v3"
)

// Config is a schema_version 2 document: shared process settings plus named flows.
type Config struct {
	SchemaVersion int
	Server        config.ServerConfig
	Security      config.SecurityConfig
	Metrics       config.MetricsConfig
	Logging       config.LoggingConfig
	Replay        config.ReplayConfig
	Destinations  map[string]config.DestinationConfig
	Flows         map[string]graph.FlowConfig
}

// DetectSchemaVersion reads only schema_version. Absent or 0 means version 1.
func DetectSchemaVersion(data []byte) (int, error) {
	var head struct {
		SchemaVersion int `yaml:"schema_version"`
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&head); err != nil {
		return 0, err
	}
	if head.SchemaVersion == 0 {
		return 1, nil
	}
	return head.SchemaVersion, nil
}

// Load reads path and expands environment variables strictly.
func Load(path string) (Config, []config.ValidationIssue) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, []config.ValidationIssue{v2Issue("config", fmt.Sprintf("read %s: %v", path, err), "")}
	}
	return LoadBytes(data, path, true)
}

// LoadBytes decodes a V2 document, compiles every flow, and reports every
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
	version, err := DetectSchemaVersion(data)
	if err != nil {
		return Config{}, []config.ValidationIssue{v2Issue("config", fmt.Sprintf("invalid YAML syntax in %s: %v", source, err), "")}
	}
	if version != 2 {
		return Config{}, []config.ValidationIssue{V1UnsupportedIssue()}
	}

	shared, sharedIssues := config.DecodeSharedYAML(data, source, "schema_version", "flows")
	issues = append(issues, sharedIssues...)
	issues = append(issues, shared.ValidationIssues()...)

	flows, flowIssues := decodeFlows(data, source)
	issues = append(issues, flowIssues...)

	cfg := Config{
		SchemaVersion: 2,
		Server:        shared.Server,
		Security:      shared.Security,
		Metrics:       shared.Metrics,
		Logging:       shared.Logging,
		Replay:        shared.Replay,
		Destinations:  shared.Destinations,
		Flows:         flows,
	}
	issues = append(issues, compileFlows(cfg)...)
	issues = append(issues, crossFlowIssues(cfg)...)
	return cfg, issues
}

// ServingConfig projects this V2 document onto V1's config.Config so the
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
					// V2 variables are resolved by nodes, not the V1 variable resolver.
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

// ValidateBytes is the non-expanding V2 validation entry used by the CLI JSON
// path and the editor. It includes policy lint.
func ValidateBytes(data []byte) []config.ValidationIssue {
	cfg, issues := LoadBytes(data, "config", false)
	return append(append(issues, Lint(cfg)...), StandaloneIssues(cfg)...)
}

func decodeFlows(data []byte, source string) (map[string]graph.FlowConfig, []config.ValidationIssue) {
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return map[string]graph.FlowConfig{}, []config.ValidationIssue{
			v2Issue("config", fmt.Sprintf("invalid YAML syntax in %s: %v", source, err), ""),
		}
	}
	if len(document.Content) == 0 {
		return map[string]graph.FlowConfig{}, nil
	}
	root := document.Content[0]
	if root.Kind != yaml.MappingNode {
		return map[string]graph.FlowConfig{}, []config.ValidationIssue{v2Issue("flows", "top level must be a mapping", "")}
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
			v2Issue("flows", "must be a mapping of flow name to flow config", ""),
		}
	}

	out := map[string]graph.FlowConfig{}
	var issues []config.ValidationIssue
	for i := 0; i < len(flowsNode.Content); i += 2 {
		name := flowsNode.Content[i].Value
		node := flowsNode.Content[i+1]
		field := "flows." + name
		if node.Kind != yaml.MappingNode {
			issues = append(issues, v2Issue(field, "flow must be a mapping", ""))
			continue
		}
		encoded, err := yaml.Marshal(node)
		if err != nil {
			issues = append(issues, v2Issue(field, err.Error(), ""))
			continue
		}
		var fc graph.FlowConfig
		decoder := yaml.NewDecoder(bytes.NewReader(encoded))
		decoder.KnownFields(true)
		if err := decoder.Decode(&fc); err != nil {
			issues = append(issues, v2Issue(field, fmt.Sprintf("unknown or invalid YAML field: %v", err),
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
		return []config.ValidationIssue{v2Issue("flows", err.Error(), "")}
	}
	var issues []config.ValidationIssue
	for _, name := range sortedKeys(cfg.Flows) {
		_, err := graph.Compile(name, cfg.Flows[name], reg)
		if err == nil {
			continue
		}
		ce, ok := err.(*graph.CompileError)
		if !ok {
			issues = append(issues, v2Issue("flows."+name, err.Error(), ""))
			continue
		}
		for _, problem := range ce.Problems {
			issues = append(issues, v2Issue("flows."+name, problem, ""))
		}
	}
	return issues
}

func crossFlowIssues(cfg Config) []config.ValidationIssue {
	issues := RoutingIssues(cfg)
	for _, name := range sortedKeys(cfg.Flows) {
		for _, nc := range cfg.Flows[name].Nodes {
			switch nc.Type {
			case "publish":
				opts, err := decodeOptions[PublishOptions](nc)
				if err != nil || opts.Destination == "" {
					continue
				}
				if _, ok := cfg.Destinations[opts.Destination]; ok {
					continue
				}
				issues = append(issues, v2Issue("flows."+name,
					fmt.Sprintf("publish destination %q is not in destinations (known: %s)",
						opts.Destination, strings.Join(sortedKeys(cfg.Destinations), ", ")),
					"reference an existing destinations.* id"))
			}
		}
	}
	return issues
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
				issues = append(issues, v2Issue("flows."+name, err.Error(), "use a valid http_trigger path"))
				continue
			}
			for _, path := range reserved {
				rp, err := vars.ParsePath(path)
				if err == nil && vars.Overlap(pattern, rp) {
					issues = append(issues, v2Issue("flows."+name, fmt.Sprintf("trigger path %s overlaps reserved path %s", opts.Path, path), "choose a separate webhook path"))
				}
			}
			for _, prev := range routes {
				if vars.Overlap(pattern, prev.pattern) {
					issues = append(issues, v2Issue("flows."+name, fmt.Sprintf("trigger path %s is already claimed by or overlaps flow %q (%s)", opts.Path, prev.name, prev.pattern.String()), "give each flow a non-overlapping http_trigger path"))
				}
			}
			routes = append(routes, route{name, pattern})
		}
	}
	return issues
}

// V1UnsupportedIssue is the migration error for schema_version 1, an absent
// version (DetectSchemaVersion reports 1), or any other non-2 document.
func V1UnsupportedIssue() config.ValidationIssue {
	return v2Issue("schema_version",
		"schema_version 1 (endpoints:) is no longer supported",
		"set schema_version: 2 and rewrite endpoints: as flows:; see docs/configuration.md")
}

func v2Issue(field, message, suggestion string) config.ValidationIssue {
	return config.ValidationIssue{
		Field: field, Message: message, Suggestion: suggestion, Severity: config.SeverityError,
	}
}

func v2Warning(field, message, suggestion string) config.ValidationIssue {
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
				issues = append(issues, v2Issue("flows."+name, "lookup requires an injected provider; the standalone server has no lookup provider configuration", "use path variables or transform field_map; standalone lookup binding is not implemented"))
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
