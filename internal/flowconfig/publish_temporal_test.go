package flowconfig

import (
	"strings"
	"testing"
	"time"
)

const temporalFlowYAML = `
server: {addr: ":8080", max_body_bytes: 1048576}
destinations:
  workflows:
    type: temporal
    temporal: {host_port: localhost:7233, namespace: default}
flows:
  orders:
    nodes:
      - {id: in, type: http_trigger, path: /orders}
      - {id: body, type: read_body, max_bytes: 1048576}
      - {id: auth, type: no_auth}
      - {id: env, type: envelope}
      - id: out
        type: publish
        destination: workflows
        target: order-events
        temporal:
          workflow_type: OrderWorkflow
          input: body
          workflow_id: {source: header, path: X-Event-ID, prefix: "order-"}
          id_reuse_policy: reject_duplicate
          execution_timeout: 2h
          run_timeout: 1h
          task_timeout: 2m
    edges: [in>body, body>auth, auth>env, env>out]
`

func TestTemporalPublishProjection(t *testing.T) {
	cfg, issues := LoadBytes([]byte(temporalFlowYAML), "test", false)
	if len(issues) != 0 {
		t.Fatalf("valid standalone Temporal config: %+v", issues)
	}
	got := cfg.ServingConfig().Endpoints["orders"].Destinations[0].TemporalOptions
	if got.WorkflowType != "OrderWorkflow" || got.Input != "body" || got.WorkflowID.Source != "header" || got.WorkflowID.Path != "X-Event-ID" || got.WorkflowID.Prefix != "order-" || got.IDReusePolicy != "reject_duplicate" || got.ExecutionTimeout != 2*time.Hour || got.RunTimeout != time.Hour || got.TaskTimeout != 2*time.Minute {
		t.Fatalf("lost Temporal settings: %+v", got)
	}
}

func TestTemporalPublishRejectsInvalidBeforeStartup(t *testing.T) {
	for _, tc := range []struct{ name, old, replacement, problem string }{
		{"missing workflow type", "workflow_type: OrderWorkflow", "workflow_type: ''", "workflow_type"},
		{"bad input", "input: body", "input: headers", "input"},
		{"bad reuse", "id_reuse_policy: reject_duplicate", "id_reuse_policy: arbitrary", "id_reuse_policy"},
		{"duration", "run_timeout: 1h", "run_timeout: tomorrow", "run_timeout"},
		{"negative", "run_timeout: 1h", "run_timeout: -1s", "run_timeout"},
		{"zero", "run_timeout: 1h", "run_timeout: 0s", "run_timeout"},
		{"run exceeds execution", "run_timeout: 1h", "run_timeout: 3h", "run_timeout"},
		{"task too long", "task_timeout: 2m", "task_timeout: 121s", "task_timeout"},
		{"missing header", "path: X-Event-ID", "path: ''", "workflow_id.path"},
		{"bad prefix", `prefix: "order-"`, `prefix: "{order"`, "prefix"},
		{"create", "target: order-events", "target: order-events\n        create: true", "create"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, issues := LoadBytes([]byte(strings.Replace(temporalFlowYAML, tc.old, tc.replacement, 1)), "test", false)
			for _, issue := range issues {
				if strings.Contains(issue.Field+issue.Message, tc.problem) {
					return
				}
			}
			t.Fatalf("expected %s validation issue: %+v", tc.problem, issues)
		})
	}
	cfg, _ := LoadBytes([]byte(temporalFlowYAML), "test", false)
	node := &cfg.Flows["orders"].Nodes[4]
	for i := 0; i < len(node.Options.Content); i += 2 {
		if node.Options.Content[i].Value == "temporal" {
			node.Options.Content = append(node.Options.Content[:i], node.Options.Content[i+2:]...)
			break
		}
	}
	if issues := temporalPublishIssues(cfg); len(issues) != 1 || !strings.Contains(issues[0].Message, "required") {
		t.Fatalf("missing options: %+v", issues)
	}
}

func TestTemporalSharedTargetCannotDiscardSettings(t *testing.T) {
	cfg, issues := LoadBytes([]byte(temporalFlowYAML), "test", false)
	if len(issues) > 0 {
		t.Fatal(issues)
	}
	other, _ := LoadBytes([]byte(strings.Replace(temporalFlowYAML, "OrderWorkflow", "OtherWorkflow", 1)), "test", false)
	cfg.Flows["other"] = other.Flows["orders"]
	if got := temporalPublishIssues(cfg); len(got) != 1 || !strings.Contains(got[0].Message, "must agree") {
		t.Fatalf("shared target conflict: %+v", got)
	}
	dest := cfg.Destinations["workflows"]
	dest.Type = "webhook"
	cfg.Destinations["workflows"] = dest
	if got := temporalPublishIssues(cfg); len(got) != 2 || !strings.Contains(got[0].Message, "require a temporal") {
		t.Fatalf("wrong destination: %+v", got)
	}
}
