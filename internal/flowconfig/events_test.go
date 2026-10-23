package flowconfig

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jaysongiroux/hook_way/internal/graph"
)

const namedEventConfig = `
server: {addr: ":8080", max_body_bytes: 1048576}
events:
  issue.opened:
    description: A new issue was opened.
    payload_schema:
      type: object
      required: [issue_id, title]
      properties:
        issue_id: {type: string}
        title: {type: string}
        amount: {type: integer, minimum: 9007199254740993}
        defaulted: {type: string, default: not-inserted}
flows:
  issues:
    nodes:
      - {id: in, type: http_trigger, path: /issues}
      - {id: body, type: read_body, max_bytes: 1048576}
      - {id: auth, type: no_auth}
      - {id: env, type: envelope}
      - {id: event, type: event, event: issue.opened, event_id_path: issue_id}
      - {id: done, type: respond, status: 200}
    edges: [in>body, body>auth, auth>env, env>event, event>done]
`

func testEventCatalog(t *testing.T) *EventCatalog {
	t.Helper()
	cfg, issues := LoadBytes([]byte(namedEventConfig), "test", false)
	if len(issues) != 0 {
		t.Fatal(issues)
	}
	catalog, issues := CompileEventCatalog(cfg.Events)
	if len(issues) != 0 {
		t.Fatal(issues)
	}
	return catalog
}

func TestEventCatalogValidatesDefinitionsAndReferences(t *testing.T) {
	for name, test := range map[string]struct{ source, field string }{
		"not a map":           {"events: []", "events"},
		"not a definition":    {"events: {issue: null}", "events.issue"},
		"unknown field":       {"events: {issue: {description: Issue, payload_schema: {type: object}, unexpected: true}}", "events.issue"},
		"missing description": {"events: {issue: {payload_schema: {type: object}}}", "events.issue.description"},
		"invalid name":        {"events: {'bad name': {description: Issue, payload_schema: {type: object}}}", "events.bad name"},
		"not object schema":   {"events: {issue: {description: Issue, payload_schema: {type: array}}}", "events.issue.payload_schema"},
		"invalid schema":      {"events: {issue: {description: Issue, payload_schema: {type: object, required: broken}}}", "events.issue.payload_schema"},
		"external reference":  {"events: {issue: {description: Issue, payload_schema: {type: object, properties: {id: {$ref: 'https://example.invalid/schema'}}}}}", "events.issue.payload_schema"},
		"missing reference":   {strings.Replace(namedEventConfig, "event: issue.opened", "event: missing", 1), "flows.issues.nodes.event.event"},
		"invalid ID path":     {strings.Replace(namedEventConfig, "event_id_path: issue_id", "event_id_path: issue..id", 1), "flows.issues.nodes[4]"},
	} {
		t.Run(name, func(t *testing.T) {
			_, issues := LoadBytes([]byte(test.source), "test", false)
			for _, issue := range issues {
				if issue.Field == test.field {
					return
				}
			}
			t.Fatalf("missing issue at %s: %+v", test.field, issues)
		})
	}
	if _, err := newEventNode("event", EventOptions{Event: "missing"}, testEventCatalog(t), false); err == nil {
		t.Fatal("runtime accepted an unresolved event")
	}
}

func TestEventCreatesImmutableExactOccurrence(t *testing.T) {
	n, err := newEventNode("event", EventOptions{Event: "issue.opened", EventIDPath: "issue_id"}, testEventCatalog(t), false)
	if err != nil {
		t.Fatal(err)
	}
	input := graph.Envelope{BodyFormat: "json", Body: map[string]any{
		"issue_id": "sender-id", "title": "Open", "amount": json.Number("9007199254740993"), "nested": map[string]any{"keep": true},
	}, Vars: map[string]string{"keep": "yes"}}
	before, _ := json.Marshal(input.Body)
	result, err := n.Run(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	out := result.(graph.Envelope)
	body := out.Body.(map[string]any)
	if len(body) != 4 || body["eventId"] != "sender-id" || body["name"] != "issue.opened" || out.Vars["keep"] != "yes" {
		t.Fatalf("wrong occurrence: %#v", out)
	}
	stamp, err := time.Parse(time.RFC3339Nano, body["timestamp"].(string))
	if err != nil || !strings.HasSuffix(body["timestamp"].(string), "Z") || time.Since(stamp) > time.Second {
		t.Fatalf("invalid construction timestamp: %#v", body["timestamp"])
	}
	data := body["data"].(map[string]any)
	if data["amount"] != json.Number("9007199254740993") || data["defaulted"] != nil {
		t.Fatalf("precision or defaults changed: %#v", data)
	}
	data["nested"].(map[string]any)["keep"] = false
	after, _ := json.Marshal(input.Body)
	if !bytes.Equal(before, after) {
		t.Fatal("output aliases the input body")
	}
}

func TestEventGeneratesUniqueUUIDs(t *testing.T) {
	n, _ := newEventNode("event", EventOptions{Event: "issue.opened"}, testEventCatalog(t), false)
	seen := map[string]bool{}
	for range 2 {
		out, err := n.Run(context.Background(), graph.Envelope{Body: map[string]any{"issue_id": "same", "title": "Open"}})
		if err != nil {
			t.Fatal(err)
		}
		id := out.(graph.Envelope).Body.(map[string]any)["eventId"].(string)
		if _, err := uuid.Parse(id); err != nil || seen[id] {
			t.Fatalf("invalid or repeated UUID: %q", id)
		}
		seen[id] = true
	}
}

func TestEventRejectsInvalidPayloadsWithoutLeakingValues(t *testing.T) {
	n, _ := newEventNode("event", EventOptions{Event: "issue.opened", EventIDPath: "issue_id"}, testEventCatalog(t), false)
	for _, body := range []any{
		nil, []any{}, "not-object", map[string]any{},
		map[string]any{"issue_id": 12, "title": "secret-value"},
		map[string]any{"issue_id": "", "title": "secret-value"},
		map[string]any{"issue_id": "  ", "title": "secret-value"},
		map[string]any{"issue_id": "id", "title": "secret-value", "amount": json.Number("9007199254740992")},
		map[string]any{"issue_id": "id", "title": strings.Repeat("s", MCPMaxBytes)},
	} {
		_, err := n.Run(context.Background(), graph.Envelope{BodyFormat: "json", Body: body})
		var ne graph.NodeError
		if !errors.As(err, &ne) || ne.Status != 400 || strings.Contains(err.Error(), "secret-value") {
			t.Fatalf("invalid body accepted or leaked: %v", err)
		}
	}
	for _, format := range []string{"raw", "xml", "multipart"} {
		if _, err := n.Run(context.Background(), graph.Envelope{BodyFormat: format, Body: map[string]any{"issue_id": "id", "title": "Open"}}); err == nil {
			t.Fatalf("accepted %s", format)
		}
	}
	deep := any("value")
	for range 140 {
		deep = map[string]any{"child": deep}
	}
	if _, err := n.Run(context.Background(), graph.Envelope{Body: map[string]any{"issue_id": "id", "title": "Open", "deep": deep}}); err == nil {
		t.Fatal("structure limit bypassed")
	}
	missing, _ := newEventNode("event", EventOptions{Event: "issue.opened", EventIDPath: "absent"}, testEventCatalog(t), false)
	if _, err := missing.Run(context.Background(), graph.Envelope{Body: map[string]any{"issue_id": "id", "title": "Open"}}); err == nil {
		t.Fatal("missing ID silently generated a UUID")
	}
}

func TestEventRequiresContinuationAndSupportsErrorEdge(t *testing.T) {
	leaf := strings.Replace(namedEventConfig, "event>done", "env>done", 1)
	if _, issues := LoadBytes([]byte(leaf), "test", false); len(issues) == 0 {
		t.Fatal("event leaf accepted")
	}
	source := strings.Replace(namedEventConfig, "event>done", "event>done, event:on_error>recover", 1)
	source = strings.Replace(source, "      - {id: done,", "      - {id: recover, type: respond, status: 422}\n      - {id: done,", 1)
	cfg, issues := LoadBytes([]byte(source), "test", false)
	if len(issues) != 0 {
		t.Fatal(issues)
	}
	reg, _ := NewRegistryWithOptions(RegistryOptions{Events: testEventCatalog(t)})
	flow, err := graph.Compile("issues", cfg.Flows["issues"], reg)
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithBodyReader(context.Background(), strings.NewReader(`{"issue_id":12,"title":"Invalid"}`))
	term, err := flow.Run(ctx, graph.RequestHead{}, graph.RunOptions{PublishTimeout: time.Second})
	if err != nil || term.Response == nil || term.Response.Status != 422 || term.SideEffects {
		t.Fatalf("event failure did not follow its error edge without side effects: %+v %v", term, err)
	}

}

func TestEventCatalogLocalReferencesAndEnvironmentLiterals(t *testing.T) {
	source := `server: {addr: ":8080", max_body_bytes: 1048576}
events:
  issue.opened:
    description: Issue
    payload_schema:
      type: object
      properties:
        issue_id: {$ref: '#/$defs/identifier'}
        $literal: {type: string}
      $defs:
        identifier: {type: string}
`
	expanded, missing := ExpandConfigEnv(source, func(value string) (string, []string) {
		if strings.Contains(value, "$defs") || strings.Contains(value, "$ref") || strings.Contains(value, "$literal") {
			return value, []string{"schema escaped into expansion"}
		}
		return value, nil
	})
	if len(missing) > 0 || expanded != source {
		t.Fatalf("schema changed: %s %v", expanded, missing)
	}
	if _, issues := LoadBytes([]byte(source), "test", false); len(issues) != 0 {
		t.Fatal(issues)
	}
	large := fmt.Sprintf("events: {issue: {description: Issue, payload_schema: {type: object, description: '%s'}}}", strings.Repeat("s", 65<<10))
	if _, issues := LoadBytes([]byte(large), "test", false); len(issues) == 0 {
		t.Fatal("unbounded schema accepted")
	}
}

func TestWebhookJSONPublishAdmission(t *testing.T) {
	for _, tc := range []struct {
		name, auth, options string
		valid               bool
	}{
		{"json", "", ", wire_body: json", true},
		{"default envelope", "", "", true},
		{"broker payload mode", "", ", wire_body: json_js", false},
		{"broker retry", "", ", wire_body: json, retry: {max_attempts: 2}", false},
		{"proxy", ", auth: {type: proxy}", ", wire_body: json", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := strings.Replace(namedEventConfig, "type: respond, status: 200", "type: publish, destination: receiver, target: events"+tc.options, 1)
			source += "\ndestinations:\n  receiver: {type: webhook, webhook: {url: 'https://example.test'" + tc.auth + "}}\n"
			_, issues := LoadBytes([]byte(source), "test", false)
			if tc.valid {
				if len(issues) != 0 {
					t.Fatal(issues)
				}
			} else if !strings.Contains(issuesText(issues), "broker wire_body require an sqs or sns destination") {
				t.Fatalf("broker/proxy restriction lost: %+v", issues)
			}
		})
	}
}
