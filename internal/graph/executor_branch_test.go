package graph

import (
	"context"
	"strings"
	"testing"
	"time"
)

type portNode struct {
	Node
	ports []string
}

func (n portNode) OutPorts() []string { return n.ports }

func labelledRegistry(t *testing.T, rec *recorder, branch string, ports []string) *Registry {
	t.Helper()
	r := registryWithRecorder(t, rec)
	s := r.specs["process"]
	s.New = func(nc NodeConfig) (Node, error) {
		n := rec.node(nc.ID)
		n.out = Envelope{Branch: branch}
		return portNode{Node: n, ports: ports}, nil
	}
	r.specs["process"] = s
	return r
}

func labelledSwitchFlow() FlowConfig {
	return flowConfig([][2]string{
		{"in", "trigger"}, {"body", "read_body"}, {"auth", "auth"}, {"env", "envelope"},
		{"sw", "process"}, {"a", "dest"}, {"b", "dest"},
	}, "in>body", "body>auth", "auth>env", "env>sw", "sw:matched>a", "sw:default>b")
}

func TestRunFollowsOnlySelectedLabelledPort(t *testing.T) {
	rec := &recorder{}
	f := mustCompile(t, labelledSwitchFlow(), labelledRegistry(t, rec, "matched", []string{"matched", "default"}))
	if _, err := f.Run(context.Background(), RequestHead{}, RunOptions{PublishTimeout: time.Second}); err != nil {
		t.Fatal(err)
	}
	got := rec.entries()
	if !containsID(got, "a") || containsID(got, "b") {
		t.Fatalf("execution = %q, want dest a only", strings.Join(got, ","))
	}
}

func TestRunFollowsDefaultLabelledPort(t *testing.T) {
	rec := &recorder{}
	f := mustCompile(t, labelledSwitchFlow(), labelledRegistry(t, rec, "default", []string{"matched", "default"}))
	if _, err := f.Run(context.Background(), RequestHead{}, RunOptions{PublishTimeout: time.Second}); err != nil {
		t.Fatal(err)
	}
	got := rec.entries()
	if !containsID(got, "b") || containsID(got, "a") {
		t.Fatalf("execution = %q, want dest b only", strings.Join(got, ","))
	}
}

func TestRunErrorsWhenSelectedPortHasNoSuccessor(t *testing.T) {
	rec := &recorder{}
	f := mustCompile(t, labelledSwitchFlow(), labelledRegistry(t, rec, "missing", []string{"matched", "default", "missing"}))
	_, err := f.Run(context.Background(), RequestHead{}, RunOptions{PublishTimeout: time.Second})
	if err == nil || !strings.Contains(err.Error(), "no successor for port") {
		t.Fatalf("Run() = %v, want no-successor error (not a silent drop)", err)
	}
}

func TestRunFansOutAllSuccessorsOfSelectedPort(t *testing.T) {
	rec := &recorder{}
	fc := flowConfig([][2]string{
		{"in", "trigger"}, {"body", "read_body"}, {"auth", "auth"}, {"env", "envelope"},
		{"sw", "process"}, {"a", "dest"}, {"b", "dest"},
	}, "in>body", "body>auth", "auth>env", "env>sw", "sw:matched>a", "sw:matched>b")
	f := mustCompile(t, fc, labelledRegistry(t, rec, "matched", []string{"matched"}))
	if _, err := f.Run(context.Background(), RequestHead{}, RunOptions{PublishTimeout: time.Second}); err != nil {
		t.Fatal(err)
	}
	got := rec.entries()
	if !containsID(got, "a") || !containsID(got, "b") {
		t.Fatalf("same-port fan-out = %q, want both dests", strings.Join(got, ","))
	}
}

func TestCompileRejectsUndeclaredLabelledPort(t *testing.T) {
	rec := &recorder{}
	_, err := Compile("bad", labelledSwitchFlow(), labelledRegistry(t, rec, "matched", []string{"matched"}))
	if err == nil || !strings.Contains(err.Error(), "not declared") {
		t.Fatalf("Compile() = %v, want undeclared port error", err)
	}
}

func TestCompileAcceptsDeclaredLabelledPorts(t *testing.T) {
	rec := &recorder{}
	if _, err := Compile("ok", labelledSwitchFlow(), labelledRegistry(t, rec, "matched", []string{"matched", "default"})); err != nil {
		t.Fatal(err)
	}
}

func containsID(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}
