package graph

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestStopOnFailureDoesNotCallLaterDestinations(t *testing.T) {
	rec := &recorder{}
	r := registryWithRecorder(t, rec)
	s := r.specs["dest"]
	s.New = func(nc NodeConfig) (Node, error) {
		n := rec.node(nc.ID)
		n.out = Terminal{}
		if nc.ID == "a" {
			n.err = errors.New("first failed")
		}
		return n, nil
	}
	r.specs["dest"] = s
	fc := flowConfig([][2]string{
		{"in", "trigger"}, {"body", "read_body"}, {"auth", "auth"}, {"env", "envelope"},
		{"a", "dest"}, {"b", "dest"},
	}, "in>body", "body>auth", "auth>env", "env>a", "env>b")
	f := mustCompile(t, fc, r)
	term, err := f.Run(context.Background(), RequestHead{}, RunOptions{PublishTimeout: time.Second})
	if err == nil {
		t.Fatal("expected first destination failure")
	}
	if containsID(rec.entries(), "b") {
		t.Fatalf("stop_on_failure ran dest b: %v", rec.entries())
	}
	var unattempted bool
	for _, d := range term.Deliveries {
		if d.NodeID == "b" && d.Unattempted {
			unattempted = true
		}
	}
	if !unattempted {
		t.Fatalf("unattempted dest b missing from deliveries: %+v", term.Deliveries)
	}
}

func TestSkipFailedAttemptsEveryDestination(t *testing.T) {
	rec := &recorder{}
	r := registryWithRecorder(t, rec)
	s := r.specs["dest"]
	s.New = func(nc NodeConfig) (Node, error) {
		n := rec.node(nc.ID)
		n.out = Terminal{}
		if nc.ID == "a" {
			n.err = errors.New("first failed")
		}
		return n, nil
	}
	r.specs["dest"] = s
	fc := flowConfig([][2]string{
		{"in", "trigger"}, {"body", "read_body"}, {"auth", "auth"}, {"env", "envelope"},
		{"a", "dest"}, {"b", "dest"},
	}, "in>body", "body>auth", "auth>env", "env>a", "env>b")
	fc.DeliveryFailurePolicy = "skip_failed"
	f := mustCompile(t, fc, r)
	term, err := f.Run(context.Background(), RequestHead{}, RunOptions{PublishTimeout: time.Second})
	if err != nil {
		t.Fatalf("partial skip_failed should succeed: %v", err)
	}
	if !containsID(rec.entries(), "a") || !containsID(rec.entries(), "b") {
		t.Fatalf("skip_failed must attempt both dests: %v", rec.entries())
	}
	if !NeedsCompensation(term, err) {
		t.Fatal("partial skip_failed must need compensation (I17)")
	}
}

func TestSkipFailedAllFailuresReturnError(t *testing.T) {
	rec := &recorder{}
	r := registryWithRecorder(t, rec)
	s := r.specs["dest"]
	s.New = func(nc NodeConfig) (Node, error) {
		n := rec.node(nc.ID)
		n.out = Terminal{}
		n.err = errors.New("down")
		return n, nil
	}
	r.specs["dest"] = s
	fc := flowConfig([][2]string{
		{"in", "trigger"}, {"body", "read_body"}, {"auth", "auth"}, {"env", "envelope"},
		{"a", "dest"}, {"b", "dest"},
	}, "in>body", "body>auth", "auth>env", "env>a", "env>b")
	fc.DeliveryFailurePolicy = "skip_failed"
	f := mustCompile(t, fc, r)
	_, err := f.Run(context.Background(), RequestHead{}, RunOptions{PublishTimeout: time.Second})
	if err == nil {
		t.Fatal("zero successes must error (HTTP 503)")
	}
}

func TestOnErrorRoutesFailureAndSucceeds(t *testing.T) {
	rec := &recorder{}
	r := registryWithRecorder(t, rec)
	s := r.specs["process"]
	s.New = func(nc NodeConfig) (Node, error) {
		n := rec.node(nc.ID)
		n.out = Envelope{}
		if nc.ID == "look" {
			n.err = errors.New("lookup miss")
		}
		return n, nil
	}
	r.specs["process"] = s
	fc := flowConfig([][2]string{
		{"in", "trigger"}, {"body", "read_body"}, {"auth", "auth"}, {"env", "envelope"},
		{"look", "process"}, {"out", "dest"}, {"fb", "dest"},
	}, "in>body", "body>auth", "auth>env", "env>look", "look>out", "look:on_error>fb")
	f := mustCompile(t, fc, r)
	_, err := f.Run(context.Background(), RequestHead{}, RunOptions{PublishTimeout: time.Second})
	if err != nil {
		t.Fatalf("on_error should handle lookup failure: %v", err)
	}
	if containsID(rec.entries(), "out") {
		t.Fatalf("success dest ran after lookup failure: %v", rec.entries())
	}
	if !containsID(rec.entries(), "fb") {
		t.Fatalf("on_error dest did not run: %v", rec.entries())
	}
}

func TestOnErrorAbsentPropagates(t *testing.T) {
	rec := &recorder{}
	r := registryWithRecorder(t, rec)
	s := r.specs["process"]
	s.New = func(nc NodeConfig) (Node, error) {
		n := rec.node(nc.ID)
		n.out = Envelope{}
		n.err = errors.New("lookup miss")
		return n, nil
	}
	r.specs["process"] = s
	fc := flowConfig([][2]string{
		{"in", "trigger"}, {"body", "read_body"}, {"auth", "auth"}, {"env", "envelope"},
		{"look", "process"}, {"out", "dest"},
	}, "in>body", "body>auth", "auth>env", "env>look", "look>out")
	f := mustCompile(t, fc, r)
	_, err := f.Run(context.Background(), RequestHead{}, RunOptions{PublishTimeout: time.Second})
	if err == nil {
		t.Fatal("expected propagated lookup error")
	}
}

func TestCompileRejectsOnErrorTypeMismatch(t *testing.T) {
	fc := flowConfig([][2]string{
		{"in", "trigger"}, {"body", "read_body"}, {"auth", "auth"}, {"env", "envelope"},
		{"look", "process"}, {"out", "dest"}, {"gate", "gate"},
	}, "in>body", "body>auth", "auth>env", "env>look", "look>out", "look:on_error>gate")
	_, err := Compile("bad", fc, testRegistry(t))
	if err == nil || !strings.Contains(err.Error(), "on_error type mismatch") {
		t.Fatalf("Compile() = %v, want on_error type mismatch (error path carries Envelope, gate consumes RequestHead)", err)
	}
}

func TestCompileRejectsUnknownDeliveryFailurePolicy(t *testing.T) {
	fc := linearFlow()
	fc.DeliveryFailurePolicy = "explode"
	_, err := Compile("bad", fc, testRegistry(t))
	if err == nil || !strings.Contains(err.Error(), "delivery_failure_policy") {
		t.Fatalf("Compile() = %v, want policy error", err)
	}
}
