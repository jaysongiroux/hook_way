package flowconfig

import (
	"bytes"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/jaysongiroux/hook_way/internal/config"
	"github.com/jaysongiroux/hook_way/internal/graph"
	"github.com/jaysongiroux/hook_way/internal/protection"
	"github.com/jaysongiroux/hook_way/internal/routing"
	"github.com/jaysongiroux/hook_way/internal/vars"
)

// Config is a Hookway config document: shared process settings plus named flows.
type Config struct {
	StateStores  map[string]protection.Config
	Routing      routing.Config
	Credentials  map[string]CredentialProfile
	Lookups      map[string]LookupProviderConfig
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
		expanded, missing := ExpandConfigEnv(string(data), config.ExpandEnvStrict)
		if len(missing) > 0 {
			return Config{}, explicitErrorSeverity(config.MissingEnvIssues(missing))
		}
		data = []byte(expanded)
	}

	var issues []config.ValidationIssue
	// `flows` and `routing` are decoded separately below. Every other
	// non-shared top-level key -- including
	// `endpoints` -- is reported as unknown by the strict decoder.
	shared, sharedIssues := config.DecodeSharedYAML(data, source, "flows", "routing", "credentials", "lookups", "state_stores")
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
	var routingConfig routing.Config
	// Decode this extra block separately, strictly, without accepting unknown
	// keys inside it. Shared sections and flows have their own strict decoders.
	var root map[string]yaml.Node
	if yaml.Unmarshal(data, &root) == nil {
		if node, ok := root["state_stores"]; ok {
			block, _ := yaml.Marshal(node)
			decoder := yaml.NewDecoder(bytes.NewReader(block))
			decoder.KnownFields(true)
			if err := decoder.Decode(&cfg.StateStores); err != nil {
				issues = append(issues, cfgIssue("state_stores", "unknown or invalid state store configuration", "check backend fields and duration values"))
			}
		}
		if node, ok := root["routing"]; ok {
			block, _ := yaml.Marshal(node)
			decoder := yaml.NewDecoder(bytes.NewReader(block))
			decoder.KnownFields(true)
			if err := decoder.Decode(&routingConfig); err != nil {
				issues = append(issues, cfgIssue("routing", err.Error(), ""))
			}
		}
	}
	var credentials map[string]CredentialProfile
	if node, ok := root["credentials"]; ok {
		block, _ := yaml.Marshal(node)
		decoder := yaml.NewDecoder(bytes.NewReader(block))
		decoder.KnownFields(true)
		if err := decoder.Decode(&credentials); err != nil {
			issues = append(issues, cfgIssue("credentials", err.Error(), ""))
		}
	}
	if node, ok := root["lookups"]; ok {
		block, _ := yaml.Marshal(node)
		decoder := yaml.NewDecoder(bytes.NewReader(block))
		decoder.KnownFields(true)
		if err := decoder.Decode(&cfg.Lookups); err != nil {
			issues = append(issues, cfgIssue("lookups", err.Error(), ""))
		}
	}
	issues = append(issues, LookupConfigurationIssues(cfg)...)
	cfg.Credentials = credentials
	cfg.Routing = routingConfig
	issues = append(issues, CredentialConfigurationIssues(cfg)...)
	issues = append(issues, RoutingConfigurationIssues(cfg)...)
	issues = append(issues, ProtectionConfigurationIssues(cfg)...)
	issues = append(issues, compileFlows(cfg)...)
	issues = append(issues, crossFlowIssues(cfg)...)
	return cfg, issues
}

func RoutingConfigurationIssues(cfg Config) []config.ValidationIssue {
	registry, err := routing.NewRegistry(cfg.Routing)
	if err != nil {
		return []config.ValidationIssue{cfgIssue("routing", err.Error(), "")}
	}
	var issues []config.ValidationIssue
	for _, name := range sortedKeys(cfg.Flows) {
		seen := map[string]bool{}
		for _, node := range ExpandedNodes(cfg.Flows[name]) {
			if seen[node.ID] {
				issues = append(issues, cfgIssue("flows."+name+".nodes."+node.ID, "duplicate node ID across parent and child definitions", ""))
			}
			seen[node.ID] = true
			if node.Type == "batch" {
				o, err := decodeOptions[BatchOptions](node)
				if err == nil {
					field := "flows." + name + ".nodes." + node.ID
					if !registry.HasProfile(o.Profile) {
						issues = append(issues, cfgIssue(field, "batch routing profile is not configured", ""))
					}
					if o.Mode == "dedicated" {
						if _, err := registry.Admit(o.ReceivingEnvironment, o.Profile); err != nil {
							issues = append(issues, cfgIssue(field, "batch receiving environment is not admitted", ""))
						}
					}
					if cfg.Replay.Configured && cfg.Flows[name].CapturesFailures() {
						issues = append(issues, cfgIssue(field, "batch failure capture is unsupported; explicitly set capture_failures: false", ""))
					}
				}
			}
			if node.Type == "http_request" {
				o, err := decodeOptions[HTTPRequestOptions](node)
				if err == nil && o.Upstream != nil && !registry.HasProfile(o.Upstream.Profile) {
					issues = append(issues, cfgIssue("flows."+name+".nodes."+node.ID, "upstream profile is not configured", ""))
				}
			}
			if node.Type == "register_route" {
				o, err := decodeOptions[RegisterRouteOptions](node)
				if err == nil {
					field := "flows." + name + ".nodes." + node.ID
					if !registry.HasProfile(o.Profile) {
						issues = append(issues, cfgIssue(field, "registration profile is not configured", ""))
					}
					if _, ok := cfg.Routing.Stores[o.Store]; !ok {
						issues = append(issues, cfgIssue(field, "registration store is not configured", ""))
					}
				}
			}
			if node.Type != "route" {
				continue
			}
			o, err := decodeOptions[RouteOptions](node)
			if err != nil {
				continue
			}
			field := "flows." + name + ".nodes." + node.ID
			if !registry.HasProfile(o.Profile) {
				issues = append(issues, cfgIssue(field, "route profile is not configured", ""))
			}
			if o.Store != "" {
				if _, ok := cfg.Routing.Stores[o.Store]; !ok {
					issues = append(issues, cfgIssue(field, "route store is not configured", ""))
				}
			}
			if o.Fixed != "" {
				if _, err := registry.Admit(o.Fixed, o.Profile); err != nil {
					issues = append(issues, cfgIssue(field, "fixed environment is not admitted", ""))
				}
			}
		}
	}
	return issues
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
		for _, nc := range ExpandedNodes(fc) {
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
					var fifo *config.FIFOConfig
					if opts.FIFO != nil && opts.FIFO.ContentBased {
						fifo = &config.FIFOConfig{}
						fifo.Dedup.ContentBasedFallback = true
					}
					var sqsOptions config.SQSEndpointOptions
					var snsOptions config.SNSEndpointOptions
					if opts.SQS != nil {
						sqsOptions.QueueOwnerAccountID = opts.SQS.QueueOwnerAccountID
					}
					if opts.SNS != nil {
						snsOptions.TopicARN = opts.SNS.TopicARN
					}
					dests = append(dests, config.EndpointDestinationConfig{
						Destination: opts.Destination, Target: opts.Target, Create: opts.Create, FIFO: fifo, BrokerAttributes: opts.ResourceAttributes, SQSOptions: sqsOptions, SNSOptions: snsOptions, TemporalOptions: temporalEndpointOptions(opts.Temporal),
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
		if !graph.ValidID(name) {
			issues = append(issues, cfgIssue(field, "flow name must be printable UTF-8 of at most 1024 bytes, without whitespace, ':' or '>'", "rename the flow"))
			continue
		}
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
	reg, err := NewRegistryWithOptions(RegistryOptions{ValidateOnly: true})
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
		nodeIndexes := make(map[string]int, len(cfg.Flows[name].Nodes))
		for nodeIndex, node := range cfg.Flows[name].Nodes {
			// A duplicate ID identifies both entries. The first declaration is
			// a stable repair target until one of the duplicate names is changed.
			if _, present := nodeIndexes[node.ID]; !present {
				nodeIndexes[node.ID] = nodeIndex
			}
		}
		for problemIndex, problem := range ce.Problems {
			field := "flows." + name
			if nodeID, tagged := ce.ProblemFields[problemIndex]; tagged {
				if nodeIndex, present := nodeIndexes[nodeID]; present {
					field = fmt.Sprintf("flows.%s.nodes[%d]", name, nodeIndex)
				}
			}
			issues = append(issues, cfgIssue(field, problem, ""))
		}
	}
	return issues
}

func crossFlowIssues(cfg Config) []config.ValidationIssue {
	issues := RoutingIssues(cfg)
	issues = append(issues, temporalPublishIssues(cfg)...)
	brokerDedup := map[string]bool{}
	brokerResources := map[string]map[string]string{}
	brokerBindings := map[string]string{}
	for _, name := range sortedKeys(cfg.Flows) {
		flow := cfg.Flows[name]
		for _, nc := range ExpandedNodes(flow) {
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
				awsBroker := dest.Type == "sqs" || dest.Type == "sns"
				if (opts.GroupID != nil || opts.SQS != nil || opts.SNS != nil || len(opts.ResourceAttributes) > 0 || opts.FIFO != nil || opts.Retry != nil || (opts.WireBody != "" && opts.WireBody != "envelope")) && !awsBroker {
					issues = append(issues, cfgIssue("flows."+name+".nodes."+nc.ID, "fifo, retry and broker wire_body require an sqs or sns destination", ""))
				}
				if awsBroker {
					field := "flows." + name + ".nodes." + nc.ID
					if opts.SQS != nil && dest.Type != "sqs" || opts.SNS != nil && dest.Type != "sns" {
						issues = append(issues, cfgIssue(field, "sqs/sns options must match destination type", ""))
					}
					if opts.GroupID != nil && strings.HasSuffix(opts.Target, ".fifo") {
						issues = append(issues, cfgIssue(field, "group_id is for standard targets", ""))
					}
					if err := config.ValidateBrokerAttributes(dest.Type, strings.HasSuffix(opts.Target, ".fifo"), opts.ResourceAttributes); err != nil {
						issues = append(issues, cfgIssue(field+".resource_attributes", err.Error(), ""))
					}
					key := opts.Destination + "/" + opts.Target
					if previous, seen := brokerResources[key]; seen && !maps.Equal(previous, opts.ResourceAttributes) {
						issues = append(issues, cfgIssue(field+".resource_attributes", "publish nodes sharing a target must agree on resource attributes", ""))
					}
					brokerResources[key] = opts.ResourceAttributes
					binding := ""
					if opts.SQS != nil {
						binding = opts.SQS.QueueOwnerAccountID
					}
					if opts.SNS != nil {
						binding = opts.SNS.TopicARN
					}
					if previous, seen := brokerBindings[key]; seen && previous != binding {
						issues = append(issues, cfgIssue(field, "publish nodes sharing a target must agree on broker resource binding", ""))
					}
					brokerBindings[key] = binding
					if opts.FIFO != nil {
						key := opts.Destination + "/" + opts.Target
						if previous, seen := brokerDedup[key]; seen && previous != opts.FIFO.ContentBased {
							issues = append(issues, cfgIssue("flows."+name+".nodes."+nc.ID+".fifo", "publish nodes sharing a broker target must agree on content_based_deduplication", ""))
						}
						brokerDedup[key] = opts.FIFO.ContentBased
					}
					fifoTarget := strings.HasSuffix(opts.Target, ".fifo")
					if fifoTarget != (opts.FIFO != nil) {
						issues = append(issues, cfgIssue("flows."+name+".nodes."+nc.ID+".fifo", "FIFO targets require fifo settings; standard targets must omit them", ""))
					}
				}
				if opts.Payload == "object" && !objectPayloadDestType(dest.Type) {
					issues = append(issues, cfgIssue("flows."+name+".nodes."+nc.ID+".payload",
						fmt.Sprintf("payload: object is not supported for destination type %q", dest.Type),
						"use an s3 or redis_kv destination, or omit payload (envelope)"))
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
		for _, nc := range ExpandedNodes(cfg.Flows[name]) {
			if nc.Type == "lookup" {
				o, err := decodeOptions[LookupOptions](nc)
				if err != nil {
					continue
				}
				if _, ok := cfg.Lookups[o.Provider]; !ok {
					issues = append(issues, cfgIssue("flows."+name+".nodes."+nc.ID, "lookup requires a configured provider or an injected provider", "add the named provider under top-level lookups"))
				}
				if o.Table != "" || o.Cache != nil || o.NegativeTTL != "" {
					issues = append(issues, cfgIssue("flows."+name+".nodes."+nc.ID, "standalone lookup table and cache options belong to the named provider", "move table into lookups.<provider>.dynamodb and cache settings into lookups.<provider>.cache"))
				}
			}
		}
	}
	return issues
}

// AuthenticationRedactions includes configured credential sources even when
// their names do not match the built-in sensitive-header heuristic.
func AuthenticationRedactions(cfg Config) (headers, queries []string) {
	for _, p := range cfg.Credentials {
		for h := range p.Headers {
			headers = append(headers, h)
		}
		if p.Kind != "static" {
			headers = append(headers, "Authorization")
		}
	}
	for _, fc := range cfg.Flows {
		for _, nc := range ExpandedNodes(fc) {
			if nc.Type == "http_request" {
				if o, e := decodeOptions[HTTPRequestOptions](nc); e == nil {
					headers = append(headers, o.RequireHeaders...)
				}
			}
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
