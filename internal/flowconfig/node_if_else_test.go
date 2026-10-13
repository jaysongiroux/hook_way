package flowconfig

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/jaysongiroux/hook_way/internal/graph"
)

func TestIfElseRoutesOnCombinator(t *testing.T) {
	reg, err := NewRegistryWithOptions(RegistryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	spec, _ := reg.Lookup("if_else")
	for _, tc := range []struct {
		name string
		yaml string
		body map[string]any
		want string
	}{
		{
			"all holds", "match: all\nconditions:\n" +
				"  - {source: body, path: env, operator: eq, value: production}\n" +
				"  - {source: body, path: tier, operator: eq, value: pro}\n",
			map[string]any{"env": "production", "tier": "pro"}, "true",
		},
		{
			"all: one fails", "match: all\nconditions:\n" +
				"  - {source: body, path: env, operator: eq, value: production}\n" +
				"  - {source: body, path: tier, operator: eq, value: pro}\n",
			map[string]any{"env": "production", "tier": "free"}, "false",
		},
		{
			"any: second holds", "match: any\nconditions:\n" +
				"  - {source: body, path: env, operator: eq, value: production}\n" +
				"  - {source: body, path: tier, operator: eq, value: pro}\n",
			map[string]any{"env": "staging", "tier": "pro"}, "true",
		},
		{
			"any: neither holds", "match: any\nconditions:\n" +
				"  - {source: body, path: env, operator: eq, value: production}\n" +
				"  - {source: body, path: tier, operator: eq, value: pro}\n",
			map[string]any{"env": "staging", "tier": "free"}, "false",
		},
		{
			"match defaults to all", "conditions:\n" +
				"  - {source: body, path: env, operator: eq, value: production}\n" +
				"  - {source: body, path: tier, operator: eq, value: pro}\n",
			map[string]any{"env": "production", "tier": "free"}, "false",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			node, err := spec.New(nodeConfigFromYAML(t, "if_else", tc.yaml))
			if err != nil {
				t.Fatal(err)
			}
			out, err := node.Run(context.Background(), graph.Envelope{Body: tc.body})
			if err != nil {
				t.Fatal(err)
			}
			if got := out.(graph.Envelope).Branch; got != tc.want {
				t.Fatalf("branch = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestIfElseDeclaresBothPorts(t *testing.T) {
	reg, err := NewRegistryWithOptions(RegistryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	spec, _ := reg.Lookup("if_else")
	node, err := spec.New(nodeConfigFromYAML(t, "if_else",
		"conditions: [{source: body, path: env, operator: eq, value: production}]\n"))
	if err != nil {
		t.Fatal(err)
	}
	declarer, ok := node.(graph.PortDeclarer)
	if !ok {
		t.Fatal("if_else must declare labelled ports")
	}
	if got := declarer.OutPorts(); len(got) != 2 || got[0] != "true" || got[1] != "false" {
		t.Fatalf("OutPorts() = %v, want [true false]", got)
	}
}

func TestIfElseRejectsNonEnvelope(t *testing.T) {
	reg, err := NewRegistryWithOptions(RegistryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	spec, _ := reg.Lookup("if_else")
	node, err := spec.New(nodeConfigFromYAML(t, "if_else",
		"conditions: [{source: body, path: env, operator: eq, value: production}]\n"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = node.Run(context.Background(), graph.RequestHead{})
	var ne graph.NodeError
	if err == nil || !errors.As(err, &ne) || ne.Status != http.StatusInternalServerError {
		t.Fatalf("expected 500 for a non-Envelope payload, got %v", err)
	}
}

func TestIfElseOptionValidation(t *testing.T) {
	reg, err := NewRegistryWithOptions(RegistryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	spec, _ := reg.Lookup("if_else")
	for _, tc := range []struct {
		name, yaml, want string
	}{
		{"no conditions", "match: all\n", "at least one condition"},
		{
			"unknown match",
			"match: some\nconditions: [{source: body, path: env, operator: eq, value: production}]\n",
			"unknown value \"some\"",
		},
		{"incomplete condition", "conditions: [{source: body}]\n", "requires source, path, and operator"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := spec.New(nodeConfigFromYAML(t, "if_else", tc.yaml))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func ifElseFlow(t *testing.T, edges string) []string {
	t.Helper()
	src := `
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
      - {id: cond, type: if_else, conditions: [{source: body, path: env, operator: eq, value: production}]}
      - {id: yes, type: publish, destination: primary, target: t}
      - {id: no, type: publish, destination: primary, target: u}
    edges: [in>body, body>auth, auth>env, env>cond, ` + edges + `]
`
	_, issues := LoadBytes([]byte(src), "if_else", false)
	msgs := make([]string, len(issues))
	for i, issue := range issues {
		msgs[i] = issue.Message
	}
	return msgs
}

func TestIfElseRequiresBothPortsWired(t *testing.T) {
	if msgs := ifElseFlow(t, "cond:true>yes, cond:false>no"); len(msgs) != 0 {
		t.Fatalf("both ports wired should compile, got %v", msgs)
	}
	for _, tc := range []struct {
		name, edges, want string
	}{
		{"only true", "cond:true>yes", `port "false" requires exactly one successor`},
		{"unlabelled", "cond>yes", "requires labelled successors"},
		{"undeclared port", "cond:maybe>yes, cond:true>no", "not declared"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			msgs := ifElseFlow(t, tc.edges)
			if !strings.Contains(strings.Join(msgs, "\n"), tc.want) {
				t.Fatalf("issues = %v, want %q", msgs, tc.want)
			}
		})
	}
}
