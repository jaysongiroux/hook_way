package replaywire

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func childMetadata() FailureMetadata {
	m := validResume()
	m.ID, m.NodeID = "01926d3c-aaaa-7b3a-9c1e-2f4a5b6c7d8e", "out_lambda"
	return m
}

func validResult() DeliverResult {
	return DeliverResult{
		Nodes: []NodeOutcome{
			{NodeID: "out_s3", Outcome: NodeDelivered, Attempts: 1},
			{NodeID: "out_lambda", Outcome: NodeFailed, ErrorClass: ErrorClass5xx, ErrorMessage: "503", Attempts: 1},
		},
		Children:    []FailureMetadata{childMetadata()},
		CoreVersion: "v1", CoreInstance: "core-0",
	}
}

func TestDeliverResultValidate(t *testing.T) {
	if err := validResult().Validate("out_s3"); err != nil {
		t.Fatalf("valid result: %v", err)
	}
	many := validResult()
	many.Children = nil
	for i := range MaxChildren + 1 {
		c := childMetadata()
		c.ID = fmt.Sprintf("01926d3c-aaaa-7b3a-9c1e-%012x", i)
		many.Children = append(many.Children, c)
	}
	cases := map[string]struct {
		mutate func(*DeliverResult)
		resume string
		want   string
	}{
		"missing resume node":  {func(*DeliverResult) {}, "other", "resume node"},
		"resume not run":       {func(r *DeliverResult) { r.Nodes[0].Outcome = NodeNotRun }, "out_s3", "resume node"},
		"unknown outcome":      {func(r *DeliverResult) { r.Nodes[1].Outcome = "exploded" }, "out_s3", "outcome"},
		"duplicate node":       {func(r *DeliverResult) { r.Nodes = append(r.Nodes, r.Nodes[0]) }, "out_s3", "twice"},
		"empty node id":        {func(r *DeliverResult) { r.Nodes[1].NodeID = "" }, "out_s3", "node_id"},
		"long message":         {func(r *DeliverResult) { r.Nodes[1].ErrorMessage = strings.Repeat("e", MaxErrorMessageLen+1) }, "out_s3", "error_message"},
		"children on failure":  {func(r *DeliverResult) { r.Nodes[0].Outcome = NodeFailed }, "out_s3", "children"},
		"too many children":    {func(r *DeliverResult) { *r = many }, "out_s3", "exceed the limit"},
		"duplicate child":      {func(r *DeliverResult) { r.Children = append(r.Children, r.Children[0]) }, "out_s3", "twice"},
		"invalid child":        {func(r *DeliverResult) { r.Children[0].InboundStatus = 500 }, "out_s3", "inbound_status"},
		"child not resume":     {func(r *DeliverResult) { r.Children[0].Kind = KindPublishOnly }, "out_s3", "resume"},
		"child at resume node": {func(r *DeliverResult) { r.Children[0].NodeID = "out_s3" }, "out_s3", "downstream"},
		"nul in message":       {func(r *DeliverResult) { r.Nodes[1].ErrorMessage = "bad\x00byte" }, "out_s3", "error_message"},
		"latin-1 in message":   {func(r *DeliverResult) { r.Nodes[1].ErrorMessage = "caf\xe9" }, "out_s3", "error_message"},
		"unknown class":        {func(r *DeliverResult) { r.Nodes[1].ErrorClass = "weird" }, "out_s3", "error_class"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			r := validResult()
			c.mutate(&r)
			if err := r.Validate(c.resume); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("Validate = %v, want an error mentioning %q", err, c.want)
			}
		})
	}
}

func TestResumeOutcome(t *testing.T) {
	r := validResult()
	if o, ok := r.ResumeOutcome("out_lambda"); !ok || o.Outcome != NodeFailed {
		t.Fatalf("ResumeOutcome = %+v, %v", o, ok)
	}
	if _, ok := r.ResumeOutcome("missing"); ok {
		t.Fatal("found a node that is not in the result")
	}
}

func TestChildPartNames(t *testing.T) {
	if ChildSnapshotPart(3) != "child-3-snapshot" || ChildPayloadPart(0) != "child-0-payload" {
		t.Fatal("child part names changed; core and replay must agree on them")
	}
}

func TestDeliverRequestJSONNames(t *testing.T) {
	b, err := json.Marshal(DeliverRequest{RunID: "r", FailureID: "f", Kind: KindResume, Flow: "orders", NodeID: "out",
		NodeType: "publish", SnapshotSHA256: digest, SnapshotVersion: 1, DeadlineMS: 25000})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"run_id"`, `"failure_id"`, `"kind"`, `"flow"`, `"node_id"`, `"node_type"`, `"snapshot_sha256"`, `"snapshot_version"`, `"deadline_ms"`} {
		if !strings.Contains(string(b), key) {
			t.Errorf("request JSON %s lacks %s", b, key)
		}
	}
}

func TestDeliverRequestCarriesTheCaptureContext(t *testing.T) {
	b, err := json.Marshal(DeliverRequest{InboundStatus: 202, MaxRecordBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"inbound_status":202`, `"max_record_bytes":1024`} {
		if !strings.Contains(string(b), key) {
			t.Errorf("request JSON %s lacks %s", b, key)
		}
	}
}

// The executor must drop an idle connection before core's control listener
// closes it, or a reused connection can die mid-request and turn a clean
// redrive into an ambiguous one.
func TestControlIdleTimeoutsLeaveTheClientFirst(t *testing.T) {
	if ControlClientIdleTimeout >= ControlServerIdleTimeout {
		t.Fatalf("client idle timeout %v must be below the server's %v", ControlClientIdleTimeout, ControlServerIdleTimeout)
	}
}
