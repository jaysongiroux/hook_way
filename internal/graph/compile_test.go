package graph

import (
	"strings"
	"testing"
)

func flowConfig(nodes [][2]string, edges ...string) FlowConfig {
	fc := FlowConfig{Edges: edges}
	for _, n := range nodes {
		fc.Nodes = append(fc.Nodes, NodeConfig{ID: n[0], Type: n[1]})
	}
	return fc
}

func TestCompileAcceptsAValidFlow(t *testing.T) {
	fc := flowConfig([][2]string{
		{"in", "trigger"}, {"rl", "gate"}, {"body", "read_body"},
		{"auth", "auth"}, {"env", "envelope"}, {"out", "dest"},
	}, "in>rl", "rl>body", "body>auth", "auth>env", "env>out")

	f, err := Compile("github", fc, testRegistry(t))
	if err != nil {
		t.Fatalf("Compile() = %v, want nil", err)
	}
	if f.Name() != "github" {
		t.Errorf("Name() = %q, want github", f.Name())
	}
	if f.Root() != "in" {
		t.Errorf("Root() = %q, want in", f.Root())
	}
	if len(f.NodeIDs()) != 6 {
		t.Errorf("NodeIDs() has %d entries, want 6", len(f.NodeIDs()))
	}
}

// TestCompileRejectsGateAfterReadBody is the invariant regression test: this
// is the exact wiring that would break I1-I4, and it must not compile.
func TestCompileRejectsGateAfterReadBody(t *testing.T) {
	fc := flowConfig([][2]string{
		{"in", "trigger"}, {"body", "read_body"}, {"rl", "gate"},
		{"auth", "auth"}, {"env", "envelope"}, {"out", "dest"},
	}, "in>body", "body>rl", "rl>auth", "auth>env", "env>out")

	_, err := Compile("bad", fc, testRegistry(t))
	if err == nil {
		t.Fatal("Compile() = nil; a gate wired after read_body must not compile (I1-I4)")
	}
	if !strings.Contains(err.Error(), "RequestHead") || !strings.Contains(err.Error(), "RawRequest") {
		t.Errorf("error %q should name both port types so the operator sees the mismatch", err)
	}
}

// TestCompileRejectsAuthBypass covers the property the whole design rests on:
// there is no path to Envelope that skips auth.
func TestCompileRejectsAuthBypass(t *testing.T) {
	fc := flowConfig([][2]string{
		{"in", "trigger"}, {"body", "read_body"}, {"env", "envelope"}, {"out", "dest"},
	}, "in>body", "body>env", "env>out")

	if _, err := Compile("bypass", fc, testRegistry(t)); err == nil {
		t.Fatal("Compile() = nil; envelope wired straight to read_body bypasses auth")
	}
}

func TestCompileStructuralChecks(t *testing.T) {
	cases := []struct {
		name    string
		fc      FlowConfig
		wantMsg string
	}{
		{
			name: "unknown node type",
			fc: flowConfig([][2]string{{"in", "trigger"}, {"x", "nonexistent"}},
				"in>x"),
			wantMsg: "unknown node type",
		},
		{
			name: "duplicate node id",
			fc: flowConfig([][2]string{{"in", "trigger"}, {"in", "gate"}},
				"in>in"),
			wantMsg: "duplicate node id",
		},
		{
			name: "no root",
			fc: flowConfig([][2]string{{"rl", "gate"}, {"body", "read_body"}},
				"rl>body"),
			wantMsg: "no trigger",
		},
		{
			name: "two roots",
			fc: flowConfig([][2]string{{"a", "trigger"}, {"b", "trigger"}, {"body", "read_body"}},
				"a>body", "b>body"),
			wantMsg: "more than one trigger",
		},
		{
			name: "edge to missing node",
			fc: flowConfig([][2]string{{"in", "trigger"}},
				"in>ghost"),
			wantMsg: "unknown node",
		},
		{
			name: "unreachable node",
			fc: flowConfig([][2]string{
				{"in", "trigger"}, {"body", "read_body"}, {"auth", "auth"},
				{"env", "envelope"}, {"out", "dest"}, {"orphan", "process"},
			}, "in>body", "body>auth", "auth>env", "env>out"),
			wantMsg: "unreachable",
		},
		{
			name: "non-terminal leaf",
			fc: flowConfig([][2]string{
				{"in", "trigger"}, {"body", "read_body"}, {"auth", "auth"}, {"env", "envelope"},
			}, "in>body", "body>auth", "auth>env"),
			wantMsg: "not Terminal",
		},
		{
			name: "cycle",
			fc: flowConfig([][2]string{
				{"in", "trigger"}, {"body", "read_body"}, {"auth", "auth"},
				{"env", "envelope"}, {"p1", "process"}, {"p2", "process"}, {"out", "dest"},
			}, "in>body", "body>auth", "auth>env", "env>p1", "p1>p2", "p2>p1", "p1>out"),
			wantMsg: "cycle",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Compile("f", tc.fc, testRegistry(t))
			if err == nil {
				t.Fatalf("Compile() = nil, want error containing %q", tc.wantMsg)
			}
			if !strings.Contains(err.Error(), tc.wantMsg) {
				t.Errorf("Compile() error = %q, want it to contain %q", err, tc.wantMsg)
			}
		})
	}
}

// TestCompileAggregatesProblems keeps `hookway validate` useful: one run
// should report every problem in a flow, not just the first.
func TestCompileAggregatesProblems(t *testing.T) {
	fc := flowConfig([][2]string{{"in", "trigger"}, {"x", "nonexistent"}, {"y", "alsomissing"}},
		"in>x", "x>y")
	_, err := Compile("f", fc, testRegistry(t))
	if err == nil {
		t.Fatal("Compile() = nil, want error")
	}
	var ce *CompileError
	if !asCompileError(err, &ce) {
		t.Fatalf("error is %T, want *CompileError", err)
	}
	if len(ce.Problems) < 2 {
		t.Errorf("Problems has %d entries, want >= 2: %v", len(ce.Problems), ce.Problems)
	}
}

func asCompileError(err error, target **CompileError) bool {
	ce, ok := err.(*CompileError)
	if ok {
		*target = ce
	}
	return ok
}
