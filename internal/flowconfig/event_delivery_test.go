package flowconfig

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestEventDeliveryConfiguration(t *testing.T) {
	b, e := os.ReadFile("../../examples/mcp-events.yaml")
	if e != nil {
		t.Fatal(e)
	}
	cfg, issues := LoadBytes(b, "example", false)
	if len(issues) > 0 {
		t.Fatal(issues)
	}
	if cfg.EventDelivery.Defaults().Workers != 4 {
		t.Fatal("defaults missing")
	}
	tests := map[string][2]string{
		"invalid namespace":      {"namespace: example-agent-events", "namespace: 'bad name'"},
		"missing endpoint":       {"endpoint: agent_tools", "endpoint: missing"},
		"missing event":          {"event: issue.opened", "event: missing"},
		"unknown grant":          {"access: {api_keys: [reader]}", "access: {api_keys: [missing]}"},
		"unknown setting":        {"event_delivery:", "event_delivery:\n  typo: true"},
		"unbound filter":         {"filters: {repository: repository}", "filters: {}"},
		"object argument":        {"properties: {repository: {type: string, minLength: 1}}", "properties: {repository: {type: object}}"},
		"undeclared arguments":   {"additionalProperties: false\n              required:", "additionalProperties: true\n              required:"},
		"dynamic publish target": {"target: issue.opened", "target: '{event}'"},
		"broker options":         {"destination: agents, target: issue.opened", "destination: agents, target: issue.opened, create: true"},
	}
	for n, pair := range tests {
		t.Run(n, func(t *testing.T) {
			changed := strings.Replace(string(b), pair[0], pair[1], 1)
			if changed == string(b) {
				t.Fatal("fixture replacement missed")
			}
			_, issues := LoadBytes([]byte(changed), "test", false)
			if len(issues) == 0 {
				t.Fatal("invalid config accepted")
			}
		})
	}
	for _, p := range []EventPrincipal{{}, {ID: "key:reader", Key: "reader", ExpiresAt: time.Now().Add(-time.Second)}, {ID: "key:other", Key: "other"}} {
		if EventAllowed(p, MCPAccessOptions{APIKeys: []string{"reader"}}) {
			t.Fatal("invalid principal allowed")
		}
	}
	if !EventAllowed(EventPrincipal{ID: "oauth:x", Scopes: []string{"events:read"}, Roles: []string{"reader"}}, MCPAccessOptions{Scopes: []string{"events:read"}, Roles: []string{"reader"}}) {
		t.Fatal("OAuth grants missing")
	}
}
