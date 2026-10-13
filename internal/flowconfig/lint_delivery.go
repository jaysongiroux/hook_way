package flowconfig

import (
	"fmt"
	"strings"

	"github.com/jaysongiroux/hook_way/internal/config"
	"github.com/jaysongiroux/hook_way/internal/graph"
)

// Delivery warnings describe intentional but weaker acknowledgement contracts.
// They do not change execution or make existing configurations invalid.
func deliveryGuaranteeIssues(cfg Config) []config.ValidationIssue {
	var issues []config.ValidationIssue
	usedDestinations := map[string]bool{}
	for _, name := range sortedKeys(cfg.Flows) {
		fc := cfg.Flows[name]
		prefix := "flows." + name
		hasPublish, hasBatch := false, false
		for _, node := range ExpandedNodes(fc) {
			if node.Type == "batch" {
				hasBatch = true
			}
			if node.Type != "publish" {
				continue
			}
			hasPublish = true
			if opts, err := decodeOptions[PublishOptions](node); err == nil {
				usedDestinations[opts.Destination] = true
			}
		}
		if cfg.Replay.Configured && !fc.CapturesFailures() && hasPublish && !hasBatch {
			issues = append(issues, cfgWarning(prefix+".capture_failures",
				"failure capture is disabled for this flow; acknowledged delivery failures will not be available in replay",
				"enable capture_failures for best-effort recovery, or document your external recovery process; replay is not a durable ingress queue"))
		}
		for _, outcome := range sortedKeys(fc.Responses) {
			policy := fc.Responses[outcome]
			if policy.Status < 200 || policy.Status >= 300 || (outcome != "partial_failure" && outcome != "error" && !strings.HasSuffix(outcome, "_error") && outcome != "payload_too_large") {
				continue
			}
			issues = append(issues, cfgWarning(prefix+".responses."+outcome+".status",
				fmt.Sprintf("%s returns HTTP %d despite a failure; the sender sees success and may not retry", outcome, policy.Status),
				"return a non-2xx status when sender recovery is required; use a confirmed fallback destination for accepted failures (docs/operations/delivery-guarantees.md)"))
		}
		issues = append(issues, flowDeliveryIssues(prefix, fc)...)
	}
	for _, name := range sortedKeys(usedDestinations) {
		dest, ok := cfg.Destinations[name]
		if !ok || dest.Type != "webhook" || dest.Webhook.DeliveryMode == "sync" {
			continue
		}
		issues = append(issues, cfgWarning("destinations."+name+".webhook.delivery_mode",
			"async webhook delivery acknowledges an in-memory queue; a process crash can lose acknowledged deliveries even with dead_letter or replay configured",
			"use delivery_mode: sync to wait for the HTTP result, or publish to a durable broker before acknowledging; see docs/operations/delivery-guarantees.md"))
	}
	return issues
}

func flowDeliveryIssues(prefix string, fc graph.FlowConfig) []config.ValidationIssue {
	nodes := map[string]graph.NodeConfig{}
	for _, n := range fc.Nodes {
		nodes[n.ID] = n
	}
	edges, _ := graph.ParseEdges(fc.Edges)
	out := map[string][]graph.Edge{}
	for _, edge := range edges {
		out[edge.From] = append(out[edge.From], edge)
	}
	var issues []config.ValidationIssue
	// Match the executor: skip_failed applies to sibling publish successors,
	// including multiple destinations attached to the same selected output port.
	partialResponse, hasPartialResponse := fc.Responses["partial_failure"]
	acknowledgesPartial := !hasPartialResponse || (partialResponse.Status >= 200 && partialResponse.Status < 300)
	if fc.DeliveryFailurePolicy == config.DeliveryFailurePolicySkipFailed && acknowledgesPartial {
		for _, id := range sortedKeys(out) {
			groups := map[string][]graph.Edge{}
			for _, edge := range out[id] {
				if edge.FromPort != "on_error" {
					groups[edge.FromPort] = append(groups[edge.FromPort], edge)
				}
			}
			partial := false
			for _, siblings := range groups {
				if len(siblings) < 2 {
					continue
				}
				allPublish := true
				for _, edge := range siblings {
					allPublish = allPublish && nodes[edge.To].Type == "publish"
				}
				partial = partial || allPublish
			}
			if partial {
				issues = append(issues, cfgWarning(prefix+".delivery_failure_policy",
					"skip_failed can acknowledge partial delivery when only some destinations succeed; successful destinations may receive duplicates on sender retry",
					"use stop_on_failure when every destination must succeed before acknowledgement; neither policy rolls back successful publishes; use stable sender event IDs and a recovery process"))
				break
			}
		}
	}
	for i, edge := range edges {
		if edge.FromPort != "on_error" {
			continue
		}
		seen := map[string]bool{}
		stack := []string{edge.To}
		for len(stack) > 0 {
			id := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if seen[id] {
				continue
			}
			seen[id] = true
			node := nodes[id]
			// A confirmed fallback publish is a deliberate recovery contract, covered
			// separately if its destination is itself volatile.
			if node.Type == "publish" {
				continue
			}
			if node.Type == "respond" {
				opts, err := decodeOptions[RespondOptions](node)
				if err == nil && !opts.Passthrough && opts.Status >= 200 && opts.Status < 300 {
					issues = append(issues, cfgWarning(fmt.Sprintf("%s.edges[%d]", prefix, i),
						fmt.Sprintf("on_error from %q can return HTTP %d without a fallback publish; the failure is handled and is not captured for replay", edge.From, opts.Status),
						"return a non-2xx status or publish to a confirmed fallback destination before acknowledging the failure; see docs/operations/delivery-guarantees.md"))
					break
				}
			}
			for _, next := range out[id] {
				if next.FromPort != "on_error" {
					stack = append(stack, next.To)
				}
			}
		}
	}
	return issues
}
