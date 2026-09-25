package graph

import (
	"context"
	"errors"
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
