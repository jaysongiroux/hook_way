package flowconfig

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/jasongiroux/hookway/internal/config"
	"github.com/jasongiroux/hookway/internal/graph"
)

func TestSwitchFirstMatchWinsAndDefault(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	spec, _ := reg.Lookup("switch")
	node, err := spec.New(nodeConfigFromYAML(t, "switch",
		"cases:\n"+
			"  - {port: created, when: {source: body, path: event, operator: eq, value: created}}\n"+
			"  - {port: other, when: {source: body, path: event, operator: eq, value: created}}\n"+
			"default: unmatched\n"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := node.Run(context.Background(), graph.Envelope{Body: map[string]any{"event": "created"}})
	if err != nil {
		t.Fatal(err)
	}
	if out.(graph.Envelope).Branch != "created" {
		t.Fatalf("first match = %q", out.(graph.Envelope).Branch)
	}
	out, err = node.Run(context.Background(), graph.Envelope{Body: map[string]any{"event": "deleted"}})
	if err != nil {
		t.Fatal(err)
	}
	if out.(graph.Envelope).Branch != "unmatched" {
		t.Fatalf("default = %q", out.(graph.Envelope).Branch)
	}
}

func TestSwitchNoDefaultIsError(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	spec, _ := reg.Lookup("switch")
	node, err := spec.New(nodeConfigFromYAML(t, "switch",
		"cases: [{port: created, when: {source: body, path: event, operator: eq, value: created}}]\n"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = node.Run(context.Background(), graph.Envelope{Body: map[string]any{"event": "x"}})
	var ne graph.NodeError
	if err == nil || !errors.As(err, &ne) || ne.Status != http.StatusBadRequest {
		t.Fatalf("expected 400 when no case matches, got %v", err)
	}
}

func TestSwitchConcurrentRequestsSelectIndependentBranches(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	spec, _ := reg.Lookup("switch")
	node, err := spec.New(nodeConfigFromYAML(t, "switch",
		"cases:\n"+
			"  - {port: created, when: {source: body, path: event, operator: eq, value: created}}\n"+
			"default: unmatched\n"))
	if err != nil {
		t.Fatal(err)
	}
	const n = 64
	got := make([]string, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			event := "created"
			want := "created"
			if i%2 == 1 {
				event = "other"
				want = "unmatched"
			}
			out, err := node.Run(context.Background(), graph.Envelope{Body: map[string]any{"event": event}})
			if err != nil {
				t.Errorf("run %d: %v", i, err)
				return
			}
			got[i] = out.(graph.Envelope).Branch
			if got[i] != want {
				t.Errorf("run %d branch = %q, want %q", i, got[i], want)
			}
		}(i)
	}
	wg.Wait()
}

func TestSwitchCompileRejectsUndeclaredPort(t *testing.T) {
	src := `
schema_version: 2
server: {addr: ":8080", max_body_bytes: 1024}
destinations:
  primary: {type: webhook, webhook: {url: "https://example.test"}}
flows:
  demo:
    nodes:
      - {id: in, type: http_trigger, path: /w, methods: [POST]}
      - {id: body, type: read_body, max_bytes: 1024}
      - {id: auth, type: no_auth}
      - {id: env, type: envelope}
      - {id: sw, type: switch, cases: [{port: created, when: {source: body, path: event, operator: eq, value: created}}]}
      - {id: out, type: publish, destination: primary, target: t}
    edges: [in>body, body>auth, auth>env, env>sw, sw:ghost>out]
`
	_, issues := LoadBytes([]byte(src), "switch", false)
	if !containsIssue(issues, "not declared") {
		t.Fatalf("issues = %v, want undeclared port", issues)
	}
}

func containsIssue(issues []config.ValidationIssue, want string) bool {
	for _, issue := range issues {
		if strings.Contains(issue.Message, want) {
			return true
		}
	}
	return false
}
