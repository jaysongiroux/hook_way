package graph

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestReviewBranchSelectionIsConsumed(t *testing.T) {
	rec := &recorder{}
	reg := labelledRegistry(t, rec, "matched", []string{"matched", "default"})
	if err := reg.Register(NodeSpec{Type: "identity", In: PortEnvelope, Out: PortEnvelope, New: func(nc NodeConfig) (Node, error) { return rec.node(nc.ID), nil }}); err != nil {
		t.Fatal(err)
	}
	fc := labelledSwitchFlow()
	fc.Nodes = append(fc.Nodes, NodeConfig{ID: "middle", Type: "identity"})
	fc.Edges = []string{"in>body", "body>auth", "auth>env", "env>sw", "sw:matched>middle", "middle>a", "sw:default>b"}
	f := mustCompile(t, fc, reg)
	if _, err := f.Run(context.Background(), RequestHead{}, RunOptions{PublishTimeout: time.Second}); err != nil {
		t.Fatal(err)
	}
	if !containsID(rec.entries(), "a") {
		t.Fatal("selected branch never delivered")
	}
}

func TestReviewCaptureCompletesBeforeEveryPublish(t *testing.T) {
	rec := &recorder{}
	fc := flowConfig([][2]string{{"in", "trigger"}, {"body", "read_body"}, {"auth", "auth"}, {"env", "envelope"}, {"a", "dest"}, {"b", "dest"}}, "in>body", "body>auth", "auth>env", "env>a", "env>b")
	fc.DeliveryFailurePolicy = "skip_failed"
	var done atomic.Bool
	var early atomic.Bool
	f := mustCompile(t, fc, registryWithRecorder(t, rec))
	_, err := f.Run(context.Background(), RequestHead{}, RunOptions{PublishTimeout: time.Second, BeforeDetached: func(Payload) { time.Sleep(30 * time.Millisecond); done.Store(true) }, OnNode: func(ev NodeEvent) {
		if ev.NodeType == "dest" && !done.Load() {
			early.Store(true)
		}
	}})
	if err != nil {
		t.Fatal(err)
	}
	if early.Load() {
		t.Fatal("destination completed before replay INSERT")
	}
}

func TestReviewUnselectedBranchIsNotFailedDelivery(t *testing.T) {
	rec := &recorder{}
	reg := labelledRegistry(t, rec, "matched", []string{"matched", "default"})
	spec := reg.specs["dest"]
	spec.New = func(nc NodeConfig) (Node, error) { n := rec.node(nc.ID); n.err = errors.New("down"); return n, nil }
	reg.specs["dest"] = spec
	f := mustCompile(t, labelledSwitchFlow(), reg)
	term, err := f.Run(context.Background(), RequestHead{}, RunOptions{PublishTimeout: time.Second})
	if err == nil {
		t.Fatal("missing failure")
	}
	for _, d := range term.Deliveries {
		if d.NodeID == "b" {
			t.Fatal("unselected branch included in replay failures")
		}
	}
}
