package flowconfig

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/jaysongiroux/hook_way/internal/graph"
)

func formIssueOptions(t *testing.T, value any) yaml.Node {
	t.Helper()
	var node yaml.Node
	if err := node.Encode(value); err != nil {
		t.Fatal(err)
	}
	return node
}

func TestFormIssuesUseOriginalNodeArrayIndexes(t *testing.T) {
	fc := graph.FlowConfig{Nodes: []graph.NodeConfig{
		{ID: "in", Type: "http_trigger", Options: formIssueOptions(t, HTTPTriggerOptions{Path: "/sample"})},
		{ID: "body", Type: "read_body", Options: formIssueOptions(t, ReadBodyOptions{MaxBytes: 1024})},
		{ID: "auth.primary", Type: "auth", Options: formIssueOptions(t, AuthOptions{Scheme: "jwt_hs256", Secret: "sample-shared-key", SignatureHeader: "Authorization"})},
		{ID: "auth-secondary", Type: "auth", Options: formIssueOptions(t, AuthOptions{Scheme: "unknown", Secret: "sample-shared-key"})},
	}, Edges: []string{"in>body", "body>auth.primary", "auth.primary>auth-secondary"}}
	issues := compileFlows(Config{Flows: map[string]graph.FlowConfig{"sample": fc}})
	if len(issues) != 2 {
		t.Fatalf("want both primary issues, got %+v", issues)
	}
	if issues[0].Field != "flows.sample.nodes[2]" || issues[1].Field != "flows.sample.nodes[3]" {
		t.Fatalf("structured locations=%+v", issues)
	}
	if !strings.Contains(issues[0].Message, "body_hash_claim") || !strings.Contains(issues[1].Message, "unknown signature scheme") {
		t.Fatalf("primary problems=%+v", issues)
	}
	for _, issue := range issues {
		if strings.Contains(issue.Message, "unreachable") || strings.Contains(issue.Message, "unknown node \"") {
			t.Fatalf("cascade returned: %+v", issue)
		}
	}
}

func TestFormChildCompileFailurePointsToParentNode(t *testing.T) {
	batch := BatchOptions{Mode: "shared", Profile: "primary", EnvironmentPath: "environment", EventIDPath: "event_id", Child: BatchChildOptions{Entry: "batch.decode", Nodes: []map[string]any{
		{"id": "batch.decode", "type": "transform", "options": map[string]any{"kind": "not-supported"}},
		{"id": "batch.send", "type": "publish", "options": map[string]any{"destination": "queue", "target": "sample"}},
	}, Edges: []string{"batch.decode>batch.send"}}}
	fc := graph.FlowConfig{Nodes: []graph.NodeConfig{{ID: "in", Type: "http_trigger", Options: formIssueOptions(t, HTTPTriggerOptions{Path: "/sample"})}, {ID: "batch", Type: "batch", Options: formIssueOptions(t, batch)}}, Edges: []string{"in>batch"}}
	issues := compileFlows(Config{Flows: map[string]graph.FlowConfig{"sample": fc}})
	if len(issues) != 1 || issues[0].Field != "flows.sample.nodes[1]" {
		t.Fatalf("parent repair location=%+v", issues)
	}
	if !strings.Contains(issues[0].Message, "batch.decode") || strings.Contains(issues[0].Message, "unreachable") {
		t.Fatalf("child problem=%+v", issues[0])
	}
}

func TestFormFlowWideIssuesRemainFlowWide(t *testing.T) {
	fc := graph.FlowConfig{Nodes: []graph.NodeConfig{{ID: "body", Type: "read_body", Options: formIssueOptions(t, ReadBodyOptions{MaxBytes: 1024})}}}
	issues := compileFlows(Config{Flows: map[string]graph.FlowConfig{"sample": fc}})
	if len(issues) == 0 {
		t.Fatal("missing trigger must remain invalid")
	}
	for _, issue := range issues {
		if issue.Field != "flows.sample" {
			t.Fatalf("unattributed structural issue should stay flow-wide: %+v", issue)
		}
	}
}
