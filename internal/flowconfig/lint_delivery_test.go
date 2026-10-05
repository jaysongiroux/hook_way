package flowconfig

import (
	"strings"
	"testing"

	"github.com/jaysongiroux/hook_way/internal/config"
	"github.com/jaysongiroux/hook_way/internal/graph"
)

func TestDeliveryGuaranteeWarnings(t *testing.T) {
	t.Run("used implicit and explicit async destinations", func(t *testing.T) {
		for _, mode := range []string{"", ", delivery_mode: async"} {
			src := strings.ReplaceAll(cleanLintYAML, ", delivery_mode: sync", mode)
			assertLint(t, mustLoad(t, src), config.SeverityWarning, "in-memory queue")
		}
	})
	t.Run("unused async destination is silent", func(t *testing.T) {
		cfg := mustLoad(t, cleanLintYAML)
		cfg.Destinations["unused"] = config.DestinationConfig{Type: "webhook"}
		if issues := Lint(cfg); len(issues) > 0 {
			t.Fatalf("issues = %+v", issues)
		}
	})
	t.Run("durable fallback does not protect async queue from crash", func(t *testing.T) {
		cfg := mustLoad(t, cleanLintYAML)
		dest := cfg.Destinations["primary"]
		dest.Webhook.DeliveryMode = "async"
		dest.Webhook.DeadLetter = config.WebhookDeadLetterConfig{Destination: "dlq", Target: "failed"}
		cfg.Destinations["primary"] = dest
		assertLint(t, cfg, config.SeverityWarning, "even with dead_letter or replay")
	})
	t.Run("explicit failure responses only", func(t *testing.T) {
		for _, outcome := range graph.ResponseOutcomes {
			cfg := mustLoad(t, cleanLintYAML)
			flow := cfg.Flows["demo"]
			flow.Responses = map[string]graph.ResponsePolicy{outcome: {Status: 202}}
			cfg.Flows["demo"] = flow
			want := outcome != "responded" && outcome != "published" && outcome != "dropped"
			if got := containsMessage(Lint(cfg), "despite a failure"); got != want {
				t.Fatalf("outcome %s warning = %v, want %v", outcome, got, want)
			}
			flow.Responses[outcome] = graph.ResponsePolicy{Status: 503}
			if containsMessage(Lint(cfg), "despite a failure") {
				t.Fatalf("non-2xx outcome %s warned", outcome)
			}
		}
	})
	t.Run("replay opt out only warns with configured replay", func(t *testing.T) {
		cfg := mustLoad(t, cleanLintYAML)
		flow := cfg.Flows["demo"]
		capture := false
		flow.CaptureFailures = &capture
		cfg.Flows["demo"] = flow
		if issues := Lint(cfg); len(issues) != 0 {
			t.Fatalf("without replay: %+v", issues)
		}
		cfg.Replay.Configured = true
		assertLint(t, cfg, config.SeverityWarning, "failure capture is disabled")
	})
	t.Run("skip_failed only warns for actual sibling publishes", func(t *testing.T) {
		cfg := mustLoad(t, cleanLintYAML)
		flow := cfg.Flows["demo"]
		flow.DeliveryFailurePolicy = "skip_failed"
		cfg.Flows["demo"] = flow
		if issues := Lint(cfg); len(issues) != 0 {
			t.Fatalf("single publish: %+v", issues)
		}
		second := flow.Nodes[len(flow.Nodes)-1]
		second.ID = "out2"
		flow.Nodes = append(flow.Nodes, second)
		flow.Edges = append(flow.Edges, "env>out2")
		cfg.Flows["demo"] = flow
		assertLint(t, cfg, config.SeverityWarning, "acknowledge partial delivery")
		flow.Responses = map[string]graph.ResponsePolicy{"partial_failure": {Status: 503}}
		cfg.Flows["demo"] = flow
		if containsMessage(Lint(cfg), "acknowledge partial delivery") {
			t.Fatal("explicit non-2xx partial failure response warned")
		}
		flow.Responses = nil
		cfg.Flows["demo"] = flow
		// Exclusive ports cannot partially deliver to each other.
		flow.Edges[len(flow.Edges)-2] = "env:one>out"
		flow.Edges[len(flow.Edges)-1] = "env:two>out2"
		if containsMessage(Lint(cfg), "acknowledge partial delivery") {
			t.Fatal("exclusive ports treated as fan-out")
		}
	})
	t.Run("on_error acknowledging without fallback", func(t *testing.T) {
		for _, status := range []string{"202", "503"} {
			src := strings.ReplaceAll(cleanLintYAML, "    edges:", "      - {id: reply, type: respond, status: "+status+"}\n    edges:")
			src = strings.ReplaceAll(src, "env>out]", "env>out, out:on_error>reply]")
			cfg := mustLoad(t, src)
			if got := containsMessage(Lint(cfg), "without a fallback publish"); got != (status == "202") {
				t.Fatalf("status %s warning = %v", status, got)
			}
		}
	})
	t.Run("confirmed error fallback is silent", func(t *testing.T) {
		src := strings.ReplaceAll(cleanLintYAML, "    edges:", "      - {id: fallback, type: publish, destination: primary, target: failed}\n    edges:")
		src = strings.ReplaceAll(src, "env>out]", "env>out, out:on_error>fallback]")
		if issues := Lint(mustLoad(t, src)); len(issues) > 0 {
			t.Fatalf("issues = %+v", issues)
		}
	})
}
