package graph

import (
	"context"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// TestCompileAndRunFromYAML is the acceptance test for Chunk 1: real YAML in,
// type-checked graph out, executed in order. It uses test nodes because real
// node implementations land in Chunk 3.
func TestCompileAndRunFromYAML(t *testing.T) {
	const src = `
nodes:
  - {id: in,   type: trigger}
  - {id: hops, type: gate}
  - {id: rl,   type: gate}
  - {id: body, type: read_body}
  - {id: auth, type: auth}
  - {id: env,  type: envelope}
  - {id: a,    type: dest}
  - {id: b,    type: dest}
edges: [in>hops, hops>rl, rl>body, body>auth, auth>env, env>a, env>b]
`
	var fc FlowConfig
	if err := yaml.Unmarshal([]byte(src), &fc); err != nil {
		t.Fatalf("Unmarshal() = %v", err)
	}

	rec := &recorder{}
	f, err := Compile("github", fc, registryWithRecorder(t, rec))
	if err != nil {
		t.Fatalf("Compile() = %v", err)
	}

	term, err := f.Run(context.Background(), RequestHead{}, RunOptions{PublishTimeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("Run() = %v", err)
	}
	if len(term.Deliveries) != 2 {
		t.Errorf("Deliveries has %d entries, want 2", len(term.Deliveries))
	}
	// The gate chain must precede read_body in the actual execution trace,
	// not merely in the config, which is the runtime half of I1-I4.
	order := strings.Join(rec.entries(), ",")
	bodyAt, rlAt := strings.Index(order, "body"), strings.Index(order, "rl")
	if rlAt == -1 || bodyAt == -1 || rlAt > bodyAt {
		t.Errorf("execution order %q: gate must run before read_body", order)
	}
}

// TestInvariantGateBeforeBodyCannotBeConfiguredAway restates the design's
// central claim as an executable assertion: the wiring that would break
// I1-I4 is not merely discouraged, it does not compile.
func TestInvariantGateBeforeBodyCannotBeConfiguredAway(t *testing.T) {
	const src = `
nodes:
  - {id: in,   type: trigger}
  - {id: body, type: read_body}
  - {id: rl,   type: gate}
  - {id: auth, type: auth}
  - {id: env,  type: envelope}
  - {id: out,  type: dest}
edges: [in>body, body>rl, rl>auth, auth>env, env>out]
`
	var fc FlowConfig
	if err := yaml.Unmarshal([]byte(src), &fc); err != nil {
		t.Fatalf("Unmarshal() = %v", err)
	}
	if _, err := Compile("bad", fc, testRegistry(t)); err == nil {
		t.Fatal("Compile() = nil: a gate after read_body must be a type error (I1-I4)")
	}
}
