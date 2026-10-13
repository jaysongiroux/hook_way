package graph

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type inputFuncNode func(context.Context, Payload) (Payload, error)

func (n inputFuncNode) Run(ctx context.Context, in Payload) (Payload, error) { return n(ctx, in) }

func multiInputRegistry(t *testing.T, send func(context.Context, string, Envelope) error) *Registry {
	t.Helper()
	r := continuationRegistry(t, &recorder{})
	process := r.specs["process"]
	process.New = func(nc NodeConfig) (Node, error) {
		return inputFuncNode(func(_ context.Context, in Payload) (Payload, error) {
			env := in.(Envelope)
			if nc.ID == "a" || nc.ID == "b" {
				env.Body = map[string]any{"origin": nc.ID}
				env.Vars = map[string]string{"origin": nc.ID}
			}
			return env, nil
		}), nil
	}
	r.specs["process"] = process
	dest := r.specs["dest"]
	dest.AllowMultipleInputs = true
	dest.New = func(nc NodeConfig) (Node, error) {
		return inputFuncNode(func(ctx context.Context, in Payload) (Payload, error) {
			return Terminal{}, send(ctx, nc.ID, in.(Envelope))
		}), nil
	}
	r.specs["dest"] = dest
	return r
}

func sharedInputFlow(tailNodes [][2]string, tailEdges ...string) FlowConfig {
	nodes := [][2]string{{"in", "trigger"}, {"body", "read_body"}, {"auth", "auth"}, {"env", "envelope"}, {"a", "process"}, {"b", "process"}, {"shared", "dest"}}
	edges := []string{"in>body", "body>auth", "auth>env", "env>a", "env>b", "a>shared", "b>shared"}
	return flowConfig(append(nodes, tailNodes...), append(edges, tailEdges...)...)
}

func TestSharedDestinationPublishesAndContinuesEachInput(t *testing.T) {
	var inputs []string
	reg := multiInputRegistry(t, func(_ context.Context, id string, env Envelope) error {
		if env.Body.(map[string]any)["origin"] != env.Vars["origin"] {
			t.Fatal("body and variables disagree")
		}
		inputs = append(inputs, id+":"+env.Vars["origin"])
		return nil
	})
	fc := sharedInputFlow([][2]string{{"mid", "process"}, {"after", "dest"}}, "shared>mid", "mid>after")
	var events []string
	term, err := mustCompile(t, fc, reg).Run(t.Context(), RequestHead{}, RunOptions{PublishTimeout: time.Second, OnNode: func(ev NodeEvent) { events = append(events, ev.NodeID) }})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"shared:a", "after:a", "shared:b", "after:b"}
	if !reflect.DeepEqual(inputs, want) || len(term.Deliveries) != 4 {
		t.Fatalf("inputs=%v deliveries=%+v", inputs, term.Deliveries)
	}
	if strings.Count(strings.Join(events, ","), "shared") != 2 {
		t.Fatalf("events=%v", events)
	}
}

func TestSharedDestinationKeepsCompletedAndBlockedArrivals(t *testing.T) {
	fail := errors.New("blocked branch")
	reg := multiInputRegistry(t, func(context.Context, string, Envelope) error { return nil })
	process := reg.specs["process"]
	original := process.New
	process.New = func(nc NodeConfig) (Node, error) {
		if nc.ID == "b" {
			return inputFuncNode(func(context.Context, Payload) (Payload, error) { return nil, fail }), nil
		}
		return original(nc)
	}
	reg.specs["process"] = process
	fc := sharedInputFlow([][2]string{{"after", "dest"}}, "shared>after")
	term, err := mustCompile(t, fc, reg).Run(t.Context(), RequestHead{}, RunOptions{PublishTimeout: time.Second})
	if !errors.Is(err, fail) {
		t.Fatal(err)
	}
	if len(term.Deliveries) != 4 {
		t.Fatalf("deliveries=%+v", term.Deliveries)
	}
	for i, d := range term.Deliveries {
		if d.Unattempted != (i >= 2) {
			t.Fatalf("delivery %d=%+v", i, d)
		}
	}
}

func TestSharedDestinationExclusiveAndDroppedPaths(t *testing.T) {
	for _, drop := range []bool{false, true} {
		t.Run(map[bool]string{false: "exclusive", true: "dropped"}[drop], func(t *testing.T) {
			fail := errors.New("shared publish failed")
			reg := multiInputRegistry(t, func(context.Context, string, Envelope) error { return fail })
			selector := reg.specs["process"]
			selector.New = func(nc NodeConfig) (Node, error) {
				if nc.ID == "sw" {
					return portNode{Node: inputFuncNode(func(_ context.Context, in Payload) (Payload, error) {
						env := in.(Envelope)
						env.Branch = "yes"
						return env, nil
					}), ports: []string{"yes", "no"}}, nil
				}
				return inputFuncNode(func(_ context.Context, in Payload) (Payload, error) {
					if drop {
						return Terminal{}, nil
					}
					return in, nil
				}), nil
			}
			reg.specs["process"] = selector
			fc := flowConfig([][2]string{{"in", "trigger"}, {"body", "read_body"}, {"auth", "auth"}, {"env", "envelope"}, {"sw", "process"}, {"a", "process"}, {"b", "process"}, {"shared", "dest"}}, "in>body", "body>auth", "auth>env", "env>sw", "sw:yes>a", "sw:no>b", "a>shared", "b>shared")
			term, err := mustCompile(t, fc, reg).Run(t.Context(), RequestHead{}, RunOptions{PublishTimeout: time.Second})
			if drop {
				if err != nil || len(term.Deliveries) != 0 {
					t.Fatal(term, err)
				}
				return
			}
			if !errors.Is(err, fail) || len(term.Deliveries) != 1 || term.Deliveries[0].Unattempted {
				t.Fatal(term, err)
			}
		})
	}
}

func TestSharedDestinationHandlesErrorsForEachInput(t *testing.T) {
	var inputs []string
	fail := errors.New("publish failed")
	reg := multiInputRegistry(t, func(_ context.Context, id string, env Envelope) error {
		inputs = append(inputs, id+":"+env.Vars["origin"])
		if id == "shared" {
			return fail
		}
		return nil
	})
	fc := sharedInputFlow([][2]string{{"ok", "dest"}, {"recover", "dest"}}, "shared>ok", "shared:on_error>recover")
	term, err := mustCompile(t, fc, reg).Run(t.Context(), RequestHead{}, RunOptions{PublishTimeout: time.Second})
	if err != nil || len(term.Deliveries) != 4 || !reflect.DeepEqual(inputs, []string{"shared:a", "recover:a", "shared:b", "recover:b"}) {
		t.Fatal(inputs, term, err)
	}
}

func TestSharedDestinationConcurrentContinuations(t *testing.T) {
	var arrivals atomic.Int32
	ready := make(chan struct{})
	var mu sync.Mutex
	counts := map[string]int{}
	reg := multiInputRegistry(t, func(ctx context.Context, id string, _ Envelope) error {
		mu.Lock()
		counts[id]++
		mu.Unlock()
		if id == "shared" {
			if arrivals.Add(1) == 2 {
				close(ready)
			}
			select {
			case <-ready:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	})
	fc := flowConfig([][2]string{{"in", "trigger"}, {"body", "read_body"}, {"auth", "auth"}, {"env", "envelope"}, {"left", "dest"}, {"right", "dest"}, {"shared", "dest"}, {"after", "dest"}}, "in>body", "body>auth", "auth>env", "env>left", "env>right", "left>shared", "right>shared", "shared>after")
	fc.DeliveryFailurePolicy = "skip_failed"
	f := mustCompile(t, fc, reg)
	term, err := f.Run(t.Context(), RequestHead{}, RunOptions{PublishTimeout: time.Second, FanoutConcurrency: 2})
	if err != nil || len(term.Deliveries) != 6 || counts["shared"] != 2 || counts["after"] != 2 {
		t.Fatal(counts, term, err)
	}
	// Starting a replay at the shared node must execute just the saved arrival.
	term, err = f.RunFrom(t.Context(), "shared", Envelope{}, RunOptions{PublishTimeout: time.Second})
	if err != nil || len(term.Deliveries) != 2 || counts["shared"] != 3 || counts["left"] != 1 || counts["right"] != 1 {
		t.Fatal(counts, term, err)
	}
}

func TestCompileMultipleInputsCapability(t *testing.T) {
	reg := multiInputRegistry(t, func(context.Context, string, Envelope) error { return nil })
	fc := sharedInputFlow(nil)
	if _, err := Compile("shared", fc, reg); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		change func(*FlowConfig)
		want   string
	}{
		{"processing", func(fc *FlowConfig) { fc.Nodes[len(fc.Nodes)-1].Type = "process" }, "multiple incoming"},
		{"duplicate", func(fc *FlowConfig) { fc.Edges = append(fc.Edges, "a>shared") }, "duplicate edge"},
		{"cycle", func(fc *FlowConfig) { fc.Edges = append(fc.Edges, "shared>a") }, "cycle"},
		{"incompatible", func(fc *FlowConfig) { fc.Edges = append(fc.Edges, "body>shared") }, "type mismatch"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			copy := sharedInputFlow(nil)
			tc.change(&copy)
			_, err := Compile(tc.name, copy, reg)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatal(err)
			}
		})
	}
}

func TestSharedDestinationSuppressionDoesNotHideBlockedArrival(t *testing.T) {
	for _, exclusive := range []bool{false, true} {
		t.Run(map[bool]string{false: "drop", true: "unselected"}[exclusive], func(t *testing.T) {
			fail := errors.New("selected branch failed before publish")
			reg := multiInputRegistry(t, func(context.Context, string, Envelope) error { t.Error("blocked destination ran"); return nil })
			process := reg.specs["process"]
			process.New = func(nc NodeConfig) (Node, error) {
				if nc.ID == "choose" {
					return portNode{Node: inputFuncNode(func(_ context.Context, in Payload) (Payload, error) {
						env := in.(Envelope)
						env.Branch = "right"
						return env, nil
					}), ports: []string{"left", "right"}}, nil
				}
				return inputFuncNode(func(_ context.Context, in Payload) (Payload, error) {
					if nc.ID == "a" {
						return Terminal{}, nil
					}
					return nil, fail
				}), nil
			}
			reg.specs["process"] = process
			fc := sharedInputFlow(nil)
			if exclusive {
				fc.Nodes = append(fc.Nodes, NodeConfig{ID: "choose", Type: "process"})
				fc.Edges = []string{"in>body", "body>auth", "auth>env", "env>choose", "choose:left>a", "choose:right>b", "a>shared", "b>shared"}
			}
			term, err := mustCompile(t, fc, reg).Run(t.Context(), RequestHead{}, RunOptions{PublishTimeout: time.Second})
			if !errors.Is(err, fail) || len(term.Deliveries) != 1 || !term.Deliveries[0].Unattempted {
				t.Fatal(term, err)
			}
		})
	}
}

func TestSharedDestinationPartialSuccessKeepsBlockedContinuation(t *testing.T) {
	var arrivals atomic.Int32
	reg := multiInputRegistry(t, func(_ context.Context, id string, _ Envelope) error {
		if id == "shared" && arrivals.Add(1) == 1 {
			return errors.New("one arrival failed")
		}
		return nil
	})
	fc := flowConfig([][2]string{{"in", "trigger"}, {"body", "read_body"}, {"auth", "auth"}, {"env", "envelope"}, {"left", "dest"}, {"right", "dest"}, {"shared", "dest"}, {"after", "dest"}}, "in>body", "body>auth", "auth>env", "env>left", "env>right", "left>shared", "right>shared", "shared>after")
	fc.DeliveryFailurePolicy = "skip_failed"
	term, err := mustCompile(t, fc, reg).Run(t.Context(), RequestHead{}, RunOptions{PublishTimeout: time.Second, FanoutConcurrency: 2})
	if err != nil {
		t.Fatal(err)
	}
	attempted, blocked := 0, 0
	for _, d := range term.Deliveries {
		if d.NodeID == "after" {
			if d.Unattempted {
				blocked++
			} else {
				attempted++
			}
		}
	}
	if attempted != 1 || blocked != 1 || len(term.Deliveries) != 6 {
		t.Fatal(term.Deliveries)
	}
}
