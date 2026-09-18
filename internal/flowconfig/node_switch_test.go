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
	"github.com/jasongiroux/hookway/internal/webhook"
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

func TestSwitchConditionListCombinators(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	spec, _ := reg.Lookup("switch")
	node, err := spec.New(nodeConfigFromYAML(t, "switch",
		"cases:\n"+
			"  - port: both\n"+
			"    match: all\n"+
			"    conditions:\n"+
			"      - {source: body, path: event, operator: eq, value: created}\n"+
			"      - {source: body, path: tier, operator: eq, value: pro}\n"+
			"  - port: either\n"+
			"    match: any\n"+
			"    conditions:\n"+
			"      - {source: body, path: event, operator: eq, value: deleted}\n"+
			"      - {source: header, path: X-Force, operator: eq, value: \"1\"}\n"+
			"default: unmatched\n"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		body    map[string]any
		headers http.Header
		want    string
	}{
		{"all conditions hold", map[string]any{"event": "created", "tier": "pro"}, nil, "both"},
		{"all: one condition fails", map[string]any{"event": "created", "tier": "free"}, nil, "unmatched"},
		{"any: first holds", map[string]any{"event": "deleted"}, nil, "either"},
		{"any: only the header holds", map[string]any{"event": "x"}, http.Header{"X-Force": {"1"}}, "either"},
		{"any: neither holds", map[string]any{"event": "x"}, nil, "unmatched"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := graph.Envelope{Body: tc.body}
			env.V.Raw.Head.Header = tc.headers
			out, err := node.Run(context.Background(), env)
			if err != nil {
				t.Fatal(err)
			}
			if got := out.(graph.Envelope).Branch; got != tc.want {
				t.Fatalf("branch = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSwitchCaseOptionValidation(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	spec, _ := reg.Lookup("switch")
	cond := "{source: body, path: event, operator: eq, value: created}"
	for _, tc := range []struct {
		name string
		yaml string
		want string
	}{
		{
			"both spellings", "cases: [{port: a, when: " + cond + ", conditions: [" + cond + "]}]\n",
			"either when or conditions",
		},
		{"neither spelling", "cases: [{port: a}]\n", "requires when or conditions"},
		{
			"match alongside when", "cases: [{port: a, match: any, when: " + cond + "}]\n",
			"match has no effect",
		},
		{
			"unknown match", "cases: [{port: a, match: some, conditions: [" + cond + "]}]\n",
			"unknown value \"some\"",
		},
		// An empty list is indistinguishable from an absent one by length, so
		// it lands on the same "pick a spelling" error rather than its own.
		{"empty conditions", "cases: [{port: a, conditions: []}]\n", "requires when or conditions"},
		{
			"incomplete condition", "cases: [{port: a, conditions: [{source: body}]}]\n",
			"requires source, path, and operator",
		},
		{"missing port", "cases: [{conditions: [" + cond + "]}]\n", "requires port"},
		{"unknown key", "cases: [{port: a, conditions: [" + cond + "], matches: any}]\n", "field matches not found"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := spec.New(nodeConfigFromYAML(t, "switch", tc.yaml))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

// A replayed event carries the headers it originally arrived with. Routing it
// on the replay request's headers would send it down a different branch than
// it took the first time, which is the one thing a replay must not do.
func TestSwitchRoutesReplayedEventOnOriginalHeaders(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	spec, _ := reg.Lookup("switch")
	node, err := spec.New(nodeConfigFromYAML(t, "switch",
		"cases: [{port: gh, when: {source: header, path: X-GitHub-Event, operator: eq, value: push}}]\n"+
			"default: other\n"))
	if err != nil {
		t.Fatal(err)
	}
	env := graph.Envelope{Body: map[string]any{}}
	env.V.Raw.Head.Header = http.Header{"X-Replay": {"1"}}
	env.Extra = CapturedEvent{Event: webhook.Envelope{
		OrigHeaders: http.Header{"X-Github-Event": {"push"}},
	}}
	out, err := node.Run(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	if got := out.(graph.Envelope).Branch; got != "gh" {
		t.Fatalf("branch = %q, want gh from the original headers", got)
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
