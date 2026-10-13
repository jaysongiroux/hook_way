package graph

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestCompilePrimaryNodeFailuresDoNotCascade(t *testing.T) {
	reg := testRegistry(t)
	first := errors.New("missing required option")
	second := errors.New("invalid selected mode")
	for name, cause := range map[string]error{"incomplete": first, "invalid": second} {
		err := reg.Register(NodeSpec{Type: name, In: PortRawRequest, Out: PortVerified, New: func(NodeConfig) (Node, error) { return nil, cause }})
		if err != nil {
			t.Fatal(err)
		}
	}
	fc := flowConfig([][2]string{{"in", "trigger"}, {"body", "read_body"}, {"auth.primary", "incomplete"}, {"auth-secondary", "invalid"}, {"env", "envelope"}}, "in>body", "body>auth.primary", "auth.primary>env", "env>auth-secondary")
	_, err := Compile("sample", fc, reg)
	var ce *CompileError
	if !errors.As(err, &ce) {
		t.Fatalf("error=%v, want CompileError", err)
	}
	if len(ce.Problems) != 2 {
		t.Fatalf("primary issues=%v, want exactly both node failures", ce.Problems)
	}
	if ce.ProblemFields[0] != "auth.primary" || ce.ProblemFields[1] != "auth-secondary" {
		t.Fatalf("node identities=%v", ce.ProblemFields)
	}
	if !errors.Is(err, first) || !errors.Is(err, second) {
		t.Fatal("original construction causes must remain available through errors.Is")
	}
	assertNoFormCascades(t, ce)
}

func TestCompilePrimaryIdentityProblemsCarryNodeIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, id, typ string
		duplicate     bool
		want          string
	}{
		{name: "invalid id", id: "bad id", typ: "gate", want: "empty or invalid"},
		{name: "missing type", id: "missing", want: "has no type"},
		{name: "unknown type", id: "unknown", typ: "not-registered", want: "unknown node type"},
		{name: "duplicate id", id: "in", typ: "gate", duplicate: true, want: "duplicate node id"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fc := flowConfig([][2]string{{"in", "trigger"}, {tc.id, tc.typ}})
			_, err := Compile("sample", fc, testRegistry(t))
			var ce *CompileError
			if !errors.As(err, &ce) || len(ce.Problems) != 1 {
				t.Fatalf("error=%v", err)
			}
			if !strings.Contains(ce.Problems[0], tc.want) || ce.ProblemFields[0] != tc.id {
				t.Fatalf("problem=%v, identities=%v", ce.Problems, ce.ProblemFields)
			}
			assertNoFormCascades(t, ce)
		})
	}
}

type formTargetPanicNode struct{ staticNode }

func (*formTargetPanicNode) DeliveryTarget() string { panic("unavailable destination metadata") }

func TestCompileConfiguredTargetFailureHasNodeIdentity(t *testing.T) {
	reg := testRegistry(t)
	if err := reg.Register(NodeSpec{Type: "bad-target", In: PortEnvelope, Out: PortTerminal, New: func(NodeConfig) (Node, error) { return &formTargetPanicNode{}, nil }}); err != nil {
		t.Fatal(err)
	}
	_, err := Compile("sample", flowConfig([][2]string{{"in", "trigger"}, {"target.primary", "bad-target"}}, "in>target.primary"), reg)
	var ce *CompileError
	if !errors.As(err, &ce) || len(ce.Problems) != 1 || ce.ProblemFields[0] != "target.primary" {
		t.Fatalf("error=%v metadata=%+v", err, ce)
	}
	assertNoFormCascades(t, ce)
}

func TestCompileKeepsIndependentDiagnosticsAlongsideNodeFailure(t *testing.T) {
	fc := flowConfig([][2]string{{"in", "trigger"}, {"bad", "not-registered"}}, "malformed-edge")
	fc.Responses = map[string]ResponsePolicy{"published": {Status: 99}}
	fc.DeliveryFailurePolicy = "unsupported"
	_, err := Compile("sample", fc, testRegistry(t))
	var ce *CompileError
	if !errors.As(err, &ce) {
		t.Fatalf("error=%v", err)
	}
	if len(ce.Problems) != 4 {
		t.Fatalf("want response, delivery policy, node and raw syntax problems: %v", ce.Problems)
	}
	if ce.ProblemFields[2] != "bad" || len(ce.ProblemFields) != 1 {
		t.Fatalf("node identity associations=%v", ce.ProblemFields)
	}
	for _, fragment := range []string{"responses", "delivery_failure_policy", "unknown node type", "edge"} {
		if !strings.Contains(strings.Join(ce.Problems, "\n"), fragment) {
			t.Errorf("missing independent issue %q", fragment)
		}
	}
	assertNoFormCascades(t, ce)
}

func assertNoFormCascades(t *testing.T, ce *CompileError) {
	t.Helper()
	for _, problem := range ce.Problems {
		for _, cascade := range []string{"unknown node \"", "unreachable", "no outgoing edge", "no trigger node", "type mismatch"} {
			if strings.Contains(problem, cascade) {
				t.Errorf("construction failure produced cascade: %s", problem)
			}
		}
	}
}

func TestCompileValidBindingStillRunsStructuralChecks(t *testing.T) {
	_, err := Compile("sample", flowConfig([][2]string{{"in", "trigger"}, {"orphan", "gate"}}), testRegistry(t))
	var ce *CompileError
	if !errors.As(err, &ce) || !strings.Contains(fmt.Sprint(ce.Problems), "unreachable") {
		t.Fatalf("structural checks skipped for bound nodes: %v", err)
	}
	if len(ce.ProblemFields) != 0 {
		t.Fatalf("flow structural diagnostics unexpectedly tagged: %v", ce.ProblemFields)
	}
}

func TestCompileRetainsMissingDeclaredEdgeEndpointsOnNodeFailure(t *testing.T) {
	fc := flowConfig([][2]string{{"in", "trigger"}, {"bad", "not-registered"}}, "in>bad", "missing>also-missing")
	_, err := Compile("sample", fc, testRegistry(t))
	var ce *CompileError
	if !errors.As(err, &ce) || len(ce.Problems) != 3 {
		t.Fatalf("want node failure plus two truly absent declarations: %v", err)
	}
	for _, problem := range ce.Problems {
		if strings.Contains(problem, "unknown node \"bad\"") {
			t.Fatalf("failed declaration misreported as absent: %s", problem)
		}
	}
}
