package flowconfig

import (
	"fmt"
	"sort"
	"strings"

	"github.com/jaysongiroux/hook_way/internal/config"
	"github.com/jaysongiroux/hook_way/internal/graph"
)

// Lint reports presence rules that typed ports cannot express. Warnings stay
// warnings unless the caller promotes them with --strict.
func Lint(cfg Config) []config.ValidationIssue {
	issues := configurationPolicyIssues(cfg)
	for _, name := range sortedKeys(cfg.Flows) {
		issues = append(issues, lintFlow(name, cfg)...)
	}
	issues = append(issues, deliveryGuaranteeIssues(cfg)...)
	if cfg.Replay.Configured && cfg.Replay.TLS.InsecureSkipVerify {
		issues = append(issues, cfgWarning("replay.tls.insecure_skip_verify",
			"certificate verification is disabled for uploads to the replay service",
			"set replay.tls.ca_cert to the replay service's CA instead; do not use this in production"))
	}
	return issues
}

func lintFlow(name string, cfg Config) []config.ValidationIssue {
	fc := cfg.Flows[name]
	prefix := "flows." + name
	var issues []config.ValidationIssue
	var hasRateLimit, hasNoAuth, hasPublish, hasRespond, hasInspect bool
	for i, nc := range fc.Nodes {
		switch nc.Type {
		case "auth":
			opts, err := decodeOptions[AuthOptions](nc)
			if err == nil && isSampleCredential(opts.Secret) {
				issues = append(issues, sampleCredentialWarning(fmt.Sprintf("%s.nodes[%d].secret", prefix, i)))
			}
		case "inspect":
			hasInspect = true
		case "mcp_trigger":
			hasRateLimit = true
			o, _ := decodeOptions[MCPTriggerOptions](nc)
			hasNoAuth = o.Auth.NoAuth
		case "rate_limit":
			hasRateLimit = true
		case "no_auth":
			hasNoAuth = true
		case "publish":
			hasPublish = true
			opts, err := decodeOptions[PublishOptions](nc)
			if err == nil && opts.Destination != "" {
				if _, ok := cfg.Destinations[opts.Destination]; !ok {
					issues = append(issues, cfgIssue(
						fmt.Sprintf("%s.nodes[%d].destination", prefix, i),
						fmt.Sprintf("publish destination %q is not in destinations (known: %s)",
							opts.Destination, strings.Join(sortedKeys(cfg.Destinations), ", ")),
						"reference an existing destinations.* id",
					))
				}
			}
		case "batch", "respond", "mcp_result":
			hasRespond = true
		case "code":
			opts, err := decodeOptions[CodeOptions](nc)
			if err == nil && strings.TrimSpace(opts.Timeout) == "" {
				issues = append(issues, cfgWarning(
					fmt.Sprintf("%s.nodes[%d].timeout", prefix, i),
					"code node has no explicit timeout; the 1s default applies and goja has no memory cap (docs/reference/security.md#code-node)",
					"set timeout explicitly; see docs/reference/security.md#code-node",
				))
			}
		case "read_body":
			opts, err := decodeOptions[ReadBodyOptions](nc)
			if err == nil && cfg.Server.MaxBodyBytes > 0 && opts.MaxBytes > cfg.Server.MaxBodyBytes {
				issues = append(issues, cfgWarning(
					fmt.Sprintf("%s.nodes[%d].max_bytes", prefix, i),
					fmt.Sprintf("read_body.max_bytes (%d) is above server.max_body_bytes (%d)", opts.MaxBytes, cfg.Server.MaxBodyBytes),
					"lower max_bytes or raise server.max_body_bytes",
				))
			}
		case "lookup":
			opts, err := decodeOptions[LookupOptions](nc)
			if err == nil && opts.Cache == nil {
				issues = append(issues, cfgWarning(
					fmt.Sprintf("%s.nodes[%d].cache", prefix, i),
					"lookup has no cache block; reads will hit the provider per event",
					"add a cache block to bound provider load",
				))
			}
		}
	}
	if !hasRateLimit {
		issues = append(issues, cfgWarning(prefix,
			fmt.Sprintf("flow %q has no rate_limit node; it is unthrottled", name),
			"add a rate_limit node before read_body"))
	}
	if hasNoAuth {
		issues = append(issues, cfgWarning(prefix,
			fmt.Sprintf("flow %q has a no_auth node; every request is accepted unverified", name),
			"replace no_auth with an auth node unless this is intentional"))
	}
	if !hasPublish && !hasRespond && !hasInspect {
		issues = append(issues, cfgIssue(prefix,
			fmt.Sprintf("flow %q has no publish and no respond node; it can never deliver or reply", name),
			"add a publish or respond node"))
	}
	issues = append(issues, parallelRespondIssues(prefix, fc)...)
	issues = append(issues, lookupBeforeFilterIssues(prefix, fc)...)
	return append(issues, bodyFormatIssues(prefix, fc)...)
}

func lookupBeforeFilterIssues(prefix string, fc graph.FlowConfig) []config.ValidationIssue {
	types := make(map[string]string, len(fc.Nodes))
	hasFilter, hasLookup := false, false
	root := ""
	for _, node := range fc.Nodes {
		types[node.ID] = node.Type
		switch node.Type {
		case "filter":
			hasFilter = true
		case "lookup":
			hasLookup = true
		case "http_trigger":
			if root == "" {
				root = node.ID
			}
		}
	}
	if !hasFilter || !hasLookup || root == "" {
		return nil
	}
	edges, _ := graph.ParseEdges(fc.Edges)
	out := map[string][]string{}
	for _, edge := range edges {
		out[edge.From] = append(out[edge.From], edge.To)
	}
	type frame struct {
		id        string
		sawFilter bool
	}
	seen := map[string]bool{}
	var issues []config.ValidationIssue
	reported := map[string]bool{}
	stack := []frame{{id: root}}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		key := cur.id + ":" + fmt.Sprint(cur.sawFilter)
		if seen[key] {
			continue
		}
		seen[key] = true
		saw := cur.sawFilter || types[cur.id] == "filter"
		if types[cur.id] == "lookup" && !saw && !reported[cur.id] {
			reported[cur.id] = true
			issues = append(issues, cfgIssue(prefix+".edges",
				fmt.Sprintf("lookup %q is reachable without passing a filter; filters must run before variable resolution (I8)", cur.id),
				"place a filter node before lookup on every path"))
		}
		for _, next := range out[cur.id] {
			stack = append(stack, frame{id: next, sawFilter: saw})
		}
	}
	return issues
}

func parallelRespondIssues(prefix string, fc graph.FlowConfig) []config.ValidationIssue {
	types := make(map[string]string, len(fc.Nodes))
	for _, node := range fc.Nodes {
		types[node.ID] = node.Type
		if node.Type == "batch" {
			types[node.ID] = "respond"
		}
	}
	edges, _ := graph.ParseEdges(fc.Edges)
	out := map[string][]string{}
	for _, edge := range edges {
		if edge.FromPort != "" {
			continue
		}
		out[edge.From] = append(out[edge.From], edge.To)
	}
	fromIDs := make([]string, 0, len(out))
	for id := range out {
		fromIDs = append(fromIDs, id)
	}
	sort.Strings(fromIDs)

	var issues []config.ValidationIssue
	for _, from := range fromIDs {
		succs := out[from]
		if len(succs) < 2 {
			continue
		}
		var found []string
		seen := map[string]bool{}
		for _, succ := range succs {
			for _, id := range reachableOfType(succ, out, types, "respond") {
				if seen[id] {
					continue
				}
				seen[id] = true
				found = append(found, id)
			}
		}
		if len(found) >= 2 {
			issues = append(issues, cfgIssue(prefix+".edges",
				fmt.Sprintf("respond nodes %s are reachable through parallel fan-out from %q; only one response may be written",
					strings.Join(found, " and "), from),
				"keep respond nodes on exclusive branches of a switch or if_else"))
		}
	}
	return issues
}

func reachableOfType(start string, out map[string][]string, types map[string]string, want string) []string {
	var found []string
	seen := map[string]bool{}
	var walk func(string)
	walk = func(id string) {
		if seen[id] {
			return
		}
		seen[id] = true
		if types[id] == want {
			found = append(found, id)
		}
		for _, next := range out[id] {
			walk(next)
		}
	}
	walk(start)
	return found
}

// bodyFormatIssues rejects the combinations where a node inspects a body the
// flow's envelope never produced in a readable form. All of these fail today
// without a config-time error: a body filter after body_format: raw matches
// nothing at all (the envelope leaves Body nil), and a transform whose kind
// does not match the body's shape returns a runtime 400.
//
// It walks forward from the trigger, carrying the format established by the
// last envelope on each path, because a flow may have several envelope nodes
// on different branches of a switch — "does this flow contain an xml
// envelope" is the wrong question. This mirrors lookupBeforeFilterIssues.
func bodyFormatIssues(prefix string, fc graph.FlowConfig) []config.ValidationIssue {
	type nodeInfo struct {
		typ   string
		index int
		cfg   graph.NodeConfig
	}
	info := make(map[string]nodeInfo, len(fc.Nodes))
	root := ""
	for i, node := range fc.Nodes {
		info[node.ID] = nodeInfo{typ: node.Type, index: i, cfg: node}
		if node.Type == "http_trigger" && root == "" {
			root = node.ID
		}
	}
	if root == "" {
		return nil
	}
	edges, _ := graph.ParseEdges(fc.Edges)
	out := map[string][]string{}
	for _, edge := range edges {
		out[edge.From] = append(out[edge.From], edge.To)
	}

	type frame struct {
		id     string
		format string // "", "json", "xml", or "raw"
	}
	var issues []config.ValidationIssue
	seen := map[string]bool{}
	reported := map[string]bool{}
	stack := []frame{{id: root}}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		key := cur.id + ":" + cur.format
		if seen[key] {
			continue
		}
		seen[key] = true

		node, known := info[cur.id]
		if !known {
			continue
		}
		format := cur.format
		def := validationCatalog[node.typ]
		if def.bodyFormat != nil {
			format = def.bodyFormat(node.cfg)
		}
		if !reported[cur.id] && def.checkBodyFormat != nil {
			if issue, bad := def.checkBodyFormat(prefix, node.index, cur.id, format, node.cfg); bad {
				reported[cur.id] = true
				issues = append(issues, issue)
			}
		}
		for _, next := range out[cur.id] {
			stack = append(stack, frame{id: next, format: format})
		}
	}
	return issues
}

type bodyFormatCheck func(prefix string, index int, id, format string, nc graph.NodeConfig) (config.ValidationIssue, bool)

func envelopeBodyFormat(nc graph.NodeConfig) string {
	if opts, err := decodeOptions[EnvelopeOptions](nc); err == nil && opts.BodyFormat != "" {
		return opts.BodyFormat
	}
	return "json"
}

func jsonBodyFormatIssue(prefix string, _ int, id, format string, nc graph.NodeConfig) (config.ValidationIssue, bool) {
	if format == "json" {
		return config.ValidationIssue{}, false
	}
	return cfgIssue(prefix+".nodes."+id, nc.Type+" requires a JSON envelope", "set body_format: json"), true
}

// conditionBodyFormatIssue rejects a body condition that can never resolve.
// Only body_format: raw is unreadable now — json walks dotted keys and xml
// walks dotted element names. It covers every node type that routes on a
// condition, because the failure is the same one each time: the body is empty,
// so the condition is uniformly false and the node always takes its
// no-match path.
func conditionBodyFormatIssue(prefix string, index int, id, format string, nc graph.NodeConfig) (config.ValidationIssue, bool) {
	typ := nc.Type
	if format != "raw" {
		return config.ValidationIssue{}, false
	}
	conds, ok := nodeConditions(typ, nc)
	if !ok {
		return config.ValidationIssue{}, false
	}
	outcome := map[string]string{
		"filter":  "every event would take on_no_match",
		"switch":  "every event would take the default port",
		"if_else": "every event would take the false port",
	}[typ]
	for _, cond := range conds {
		if cond.Source == "body" {
			return cfgIssue(
				fmt.Sprintf("%s.nodes[%d].conditions", prefix, index),
				fmt.Sprintf("%s %q reads source: body but reaches an envelope with body_format: raw, which leaves the body empty; %s", typ, id, outcome),
				"use body_format: json or xml, or match on source: header",
			), true
		}
	}
	return config.ValidationIssue{}, false
}

// nodeConditions flattens every condition a routing node evaluates, whichever
// spelling it uses.
func nodeConditions(typ string, nc graph.NodeConfig) ([]FilterConditionOptions, bool) {
	switch typ {
	case "filter":
		opts, err := decodeOptions[FilterOptions](nc)
		if err != nil {
			return nil, false
		}
		return opts.Conditions, true
	case "switch":
		opts, err := decodeOptions[SwitchOptions](nc)
		if err != nil {
			return nil, false
		}
		var conds []FilterConditionOptions
		for _, c := range opts.Cases {
			conds = append(conds, c.set().Conditions...)
		}
		return conds, true
	case "if_else":
		opts, err := decodeOptions[IfElseOptions](nc)
		if err != nil {
			return nil, false
		}
		return opts.Conditions, true
	}
	return nil, false
}

// transformBodyFormatIssue rejects a transform kind that does not match the
// body's shape. Both mismatches are a runtime 400 in node_transform.go today.
func transformBodyFormatIssue(prefix string, index int, id, format string, nc graph.NodeConfig) (config.ValidationIssue, bool) {
	opts, err := decodeOptions[TransformOptions](nc)
	if err != nil {
		return config.ValidationIssue{}, false
	}
	field := fmt.Sprintf("%s.nodes[%d].kind", prefix, index)
	switch opts.Kind {
	case "xml_prune", "xml_ordered_prune":
		if format != "xml" {
			return cfgIssue(field,
				fmt.Sprintf("transform %q uses kind: xml_prune but reaches an envelope with body_format: %s; the body is not an XML document and the node would fail at runtime", id, formatName(format)),
				"set the envelope's body_format to xml, or use a json transform kind",
			), true
		}
	case "field_map", "changed_fields", "changed_fields_js", "json_schema", "strip_fields":
		if format == "xml" {
			return cfgIssue(field,
				fmt.Sprintf("transform %q uses kind: %s but reaches an envelope with body_format: xml; these kinds walk JSON and the node would fail at runtime", id, opts.Kind),
				"use kind: xml_prune for XML bodies",
			), true
		}
		if format == "multipart" {
			return cfgIssue(field,
				fmt.Sprintf("transform %q uses kind: %s but reaches an envelope with body_format: multipart; the parsed body is {fields, files}, not the event's JSON body, and the node would fail at runtime", id, opts.Kind),
				"read the parts in a code node instead",
			), true
		}
		if format == "raw" {
			return cfgIssue(field,
				fmt.Sprintf("transform %q uses kind: %s but reaches an envelope with body_format: raw, which leaves the body empty", id, opts.Kind),
				"use body_format: json",
			), true
		}
	}
	return config.ValidationIssue{}, false
}

// formatName renders the walked format for a message; an empty format means no
// envelope was reached on this path, which the port types already reject, so it
// only appears in a message when a flow is malformed in some other way too.
func formatName(format string) string {
	if format == "" {
		return "none"
	}
	return format
}
