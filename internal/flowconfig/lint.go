package flowconfig

import (
	"fmt"
	"sort"
	"strings"

	"github.com/jasongiroux/hookway/internal/config"
	"github.com/jasongiroux/hookway/internal/graph"
)

// Lint reports presence rules that typed ports cannot express. Warnings stay
// warnings unless the caller promotes them with --strict.
func Lint(cfg Config) []config.ValidationIssue {
	var issues []config.ValidationIssue
	for _, name := range sortedKeys(cfg.Flows) {
		issues = append(issues, lintFlow(name, cfg)...)
	}
	return issues
}

func lintFlow(name string, cfg Config) []config.ValidationIssue {
	fc := cfg.Flows[name]
	prefix := "flows." + name
	var issues []config.ValidationIssue
	var hasRateLimit, hasNoAuth, hasPublish, hasRespond bool
	for i, nc := range fc.Nodes {
		switch nc.Type {
		case "rate_limit":
			hasRateLimit = true
		case "no_auth":
			hasNoAuth = true
		case "publish":
			hasPublish = true
			opts, err := decodeOptions[PublishOptions](nc)
			if err == nil && opts.Destination != "" {
				if _, ok := cfg.Destinations[opts.Destination]; !ok {
					issues = append(issues, v2Issue(
						fmt.Sprintf("%s.nodes[%d].destination", prefix, i),
						fmt.Sprintf("publish destination %q is not in destinations (known: %s)",
							opts.Destination, strings.Join(sortedKeys(cfg.Destinations), ", ")),
						"reference an existing destinations.* id",
					))
				}
			}
		case "respond":
			hasRespond = true
		case "code":
			opts, err := decodeOptions[CodeOptions](nc)
			if err == nil && strings.TrimSpace(opts.Timeout) == "" {
				issues = append(issues, v2Warning(
					fmt.Sprintf("%s.nodes[%d].timeout", prefix, i),
					"code node has no explicit timeout; the 1s default applies and goja has no memory cap",
					"set timeout explicitly",
				))
			}
		case "read_body":
			opts, err := decodeOptions[ReadBodyOptions](nc)
			if err == nil && cfg.Server.MaxBodyBytes > 0 && opts.MaxBytes > cfg.Server.MaxBodyBytes {
				issues = append(issues, v2Warning(
					fmt.Sprintf("%s.nodes[%d].max_bytes", prefix, i),
					fmt.Sprintf("read_body.max_bytes (%d) is above server.max_body_bytes (%d)", opts.MaxBytes, cfg.Server.MaxBodyBytes),
					"lower max_bytes or raise server.max_body_bytes",
				))
			}
		case "lookup":
			opts, err := decodeOptions[LookupOptions](nc)
			if err == nil && opts.Cache == nil {
				issues = append(issues, v2Warning(
					fmt.Sprintf("%s.nodes[%d].cache", prefix, i),
					"lookup has no cache block; reads will hit the provider per event",
					"add a cache block to bound provider load",
				))
			}
		}
	}
	if !hasRateLimit {
		issues = append(issues, v2Warning(prefix,
			fmt.Sprintf("flow %q has no rate_limit node; it is unthrottled", name),
			"add a rate_limit node before read_body"))
	}
	if hasNoAuth {
		issues = append(issues, v2Warning(prefix,
			fmt.Sprintf("flow %q has a no_auth node; every request is accepted unverified", name),
			"replace no_auth with an auth node unless this is intentional"))
	}
	if !hasPublish && !hasRespond {
		issues = append(issues, v2Issue(prefix,
			fmt.Sprintf("flow %q has no publish and no respond node; it can never deliver or reply", name),
			"add a publish or respond node"))
	}
	return append(issues, parallelRespondIssues(prefix, fc)...)
}

func parallelRespondIssues(prefix string, fc graph.FlowConfig) []config.ValidationIssue {
	types := make(map[string]string, len(fc.Nodes))
	for _, node := range fc.Nodes {
		types[node.ID] = node.Type
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
			issues = append(issues, v2Issue(prefix+".edges",
				fmt.Sprintf("respond nodes %s are reachable through parallel fan-out from %q; only one response may be written",
					strings.Join(found, " and "), from),
				"keep respond nodes on exclusive switch branches"))
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
