package flowconfig

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/jaysongiroux/hook_way/internal/graph"
)

func TestInspectPassThroughAndObserverIsolation(t *testing.T) {
	env := graph.Envelope{Body: map[string]any{"id": "original"}, Vars: map[string]string{"route": "events"}}
	n := newInspectNode("observe", InspectOptions{})
	for _, observer := range []func(string, graph.Envelope){nil, func(id string, got graph.Envelope) {
		if id != "observe" || !reflect.DeepEqual(env, got) {
			t.Fatal("observer input changed")
		}
	}, func(string, graph.Envelope) { panic("observer unavailable") }} {
		ctx := context.Background()
		if observer != nil {
			ctx = WithInspectionObserver(ctx, observer)
		}
		out, err := n.Run(ctx, env)
		if err != nil || !reflect.DeepEqual(env, out) {
			t.Fatalf("pass-through: %v %v", out, err)
		}
	}
}

func TestInspectConfigLeafInlineAndRequirements(t *testing.T) {
	const replay = `replay:
  url: http://localhost:8081
  capture_token: capture-token-0123456789abcdefghijkl
  control: {addr: ':9092', token: control-token-0123456789abcdefghijkl}
`
	const src = `server: {addr: ":8080", max_body_bytes: 1048576}
flows:
  observe:
    capture_failures: false
    nodes:
      - {id: in, type: http_trigger, path: /events, methods: [POST]}
      - {id: body, type: read_body, max_bytes: 1048576}
      - {id: auth, type: no_auth}
      - {id: env, type: envelope}
      - {id: look, type: inspect}
    edges: [in>body, body>auth, auth>env, env>look]
`
	for _, test := range []struct {
		name, source string
		valid        bool
	}{
		{"leaf", replay + src, true},
		{"inline", replay + strings.Replace(strings.Replace(src, "    edges:", "      - {id: reply, type: respond, status: 202}\n    edges:", 1), "env>look]", "env>look, look>reply]", 1), true},
		{"missing replay", src, false},
		{"wrong port", replay + strings.Replace(src, "auth>env, env>look", "auth>look, look>env", 1), false},
		{"unknown option", replay + strings.Replace(src, "type: inspect}", "type: inspect, rate: 1}", 1), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, issues := LoadBytes([]byte(test.source), "test", false)
			if (len(issues) == 0) != test.valid {
				t.Fatalf("issues: %+v", issues)
			}
		})
	}
	if !NodeSchemas()["inspect"].AllowLeaf || NodeSchemas()["inspect"].BatchChildOrder != 0 {
		t.Fatal("wrong inspect placement metadata")
	}
}

func TestInspectForbiddenInsideBatch(t *testing.T) {
	opts := batchTestOptions("shared")
	opts.Child.Nodes = append(opts.Child.Nodes, map[string]any{"id": "look", "type": "inspect"})
	if err := validateBatch(opts); err == nil {
		t.Fatal("batch accepted inspection")
	}
}
