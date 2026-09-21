package graph

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

type runFunc func(context.Context, Payload) (Payload, error)

func (f runFunc) Run(ctx context.Context, p Payload) (Payload, error) { return f(ctx, p) }
func replaceRun(r *Registry, kind string, fn runFunc) {
	s := r.specs[kind]
	s.New = func(NodeConfig) (Node, error) { return fn, nil }
	r.specs[kind] = s
}
func compileWithCompensator(t *testing.T, n Node, failDest bool) *Flow {
	r := testRegistry(t)
	if err := r.Register(NodeSpec{Type: "guard", In: PortVerified, Out: PortVerified, New: func(NodeConfig) (Node, error) { return n, nil }}); err != nil {
		t.Fatal(err)
	}
	if failDest {
		replaceRun(r, "dest", func(context.Context, Payload) (Payload, error) { return nil, errors.New("publish") })
	}
	return mustCompile(t, flowConfig([][2]string{{"in", "trigger"}, {"body", "read_body"}, {"auth", "auth"}, {"guard", "guard"}, {"env", "envelope"}, {"out", "dest"}}, "in>body", "body>auth", "auth>guard", "guard>env", "env>out"), r)
}
func compileWithFailingDestination(t *testing.T, rec *recorder, err error) *Flow {
	r := registryWithRecorder(t, rec)
	replaceRun(r, "dest", func(context.Context, Payload) (Payload, error) { return nil, err })
	return mustCompile(t, linearFlow(), r)
}
func TestCompileAdditionalFailures(t *testing.T) {
	for _, tc := range []struct {
		name  string
		nodes [][2]string
		edges []string
		want  string
	}{
		{"empty id", [][2]string{{"", "trigger"}}, nil, "empty"},
		{"duplicate unknown", [][2]string{{"x", "unknown"}, {"x", "unknown"}}, nil, "duplicate node id"},
		{"unsupported label", [][2]string{{"in", "trigger"}, {"out", "gate"}}, []string{"in:success>out"}, "labelled"},
		{"duplicate edge", [][2]string{{"in", "trigger"}, {"out", "gate"}}, []string{"in>out", "in>out"}, "duplicate edge"},
		{"terminal successor", [][2]string{{"in", "trigger"}, {"out", "dest"}}, []string{"out>in"}, "Terminal"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Compile("bad", flowConfig(tc.nodes, tc.edges...), testRegistry(t))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want %s", err, tc.want)
			}
		})
	}
	r := testRegistry(t)
	for _, kind := range []string{"auth", "dest"} {
		s := r.specs[kind]
		s.New = func(NodeConfig) (Node, error) { return nil, errors.New("bad options") }
		r.specs[kind] = s
	}
	fc := linearFlow()
	fc.Edges = append(fc.Edges, "broken", "also broken", "missing>othermissing")
	_, err := Compile("aggregate", fc, r)
	for _, want := range []string{"bad options", "edges[5]", "edges[6]", "missing", "othermissing"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("missing %s: %v", want, err)
		}
	}
}

func TestCompileRejectsNilRegistry(t *testing.T) {
	_, err := Compile("bad", linearFlow(), nil)
	if err == nil || !strings.Contains(err.Error(), "registry is nil") {
		t.Fatalf("Compile() = %v, want a nil-registry error", err)
	}
}

func TestParseRejectsAmbiguousIdentifiers(t *testing.T) {
	for _, s := range []string{"a>b:port", "a a>b", "a>b b", ":x>b", "a:x y>b"} {
		if _, err := ParseEdge(s); err == nil {
			t.Errorf("accepted %q", s)
		}
	}
}
func TestParseAggregates(t *testing.T) {
	edges, err := ParseEdges([]string{"a>b", "bad", "nope"})
	if len(edges) != 1 || err == nil || !strings.Contains(err.Error(), "edges[1]") || !strings.Contains(err.Error(), "edges[2]") {
		t.Fatalf("%v %v", edges, err)
	}
}
func TestNodeOptionsExcludeIdentity(t *testing.T) {
	var n NodeConfig
	if err := yaml.Unmarshal([]byte("id: x\ntype: thing\ntimeout: 1s\n"), &n); err != nil {
		t.Fatal(err)
	}
	var opts map[string]any
	if err := n.Options.Decode(&opts); err != nil {
		t.Fatal(err)
	}
	if len(opts) != 1 || opts["timeout"] != "1s" {
		t.Fatalf("options: %v", opts)
	}
}
func TestMissingNodeIdentityAggregatesAtCompile(t *testing.T) {
	var fc FlowConfig
	if err := yaml.Unmarshal([]byte("nodes: [{type: gate}, {id: x}]\nedges: [bad, broken]"), &fc); err != nil {
		t.Fatal(err)
	}
	_, err := Compile("bad", fc, testRegistry(t))
	for _, want := range []string{"empty", "no type", "edges[0]", "edges[1]"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("missing %s: %v", want, err)
		}
	}
}
func TestReturnedResponseSurvivesError(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(fmt.Sprint(failed), func(t *testing.T) {
			r := testRegistry(t)
			sentinel := errors.New("upstream")
			replaceRun(r, "respond", func(context.Context, Payload) (Payload, error) {
				term := Terminal{Response: &Response{Status: 418, Header: http.Header{"X-Test": {"yes"}}, Body: []byte("tea")}}
				if failed {
					return term, sentinel
				}
				return term, nil
			})
			fc := linearFlow()
			fc.Nodes[len(fc.Nodes)-1].Type = "respond"
			term, err := mustCompile(t, fc, r).Run(context.Background(), RequestHead{}, RunOptions{PublishTimeout: time.Second})
			if failed && !errors.Is(err, sentinel) {
				t.Fatal(err)
			}
			if !failed && err != nil {
				t.Fatal(err)
			}
			if term.Response == nil || term.Response.Status != 418 || string(term.Response.Body) != "tea" || len(term.Deliveries) != 0 {
				t.Fatalf("lost result: %+v", term)
			}
		})
	}
}
func TestRespondIsNotDelivery(t *testing.T) {
	r := testRegistry(t)
	replaceRun(r, "respond", func(context.Context, Payload) (Payload, error) {
		return Terminal{Response: &Response{Status: 201}}, nil
	})
	fc := linearFlow()
	fc.Nodes[len(fc.Nodes)-1].Type = "respond"
	term, err := mustCompile(t, fc, r).Run(context.Background(), RequestHead{}, RunOptions{PublishTimeout: time.Second})
	if err != nil || term.Response == nil || len(term.Deliveries) != 0 {
		t.Fatalf("%+v %v", term, err)
	}
}
func TestEarlyTerminalPreservesCompensation(t *testing.T) {
	n := &recordingCompensator{out: Terminal{Response: &Response{Status: 202}}}
	f := compileWithCompensator(t, n, false)
	term, err := f.Run(context.Background(), RequestHead{}, RunOptions{PublishTimeout: time.Second})
	if err != nil || term.Response == nil || len(term.Deliveries) != 0 || len(term.Compensations) != 1 || NeedsCompensation(term, err) {
		t.Fatalf("%+v %v", term, err)
	}
}
func TestRuntimeRejectsBadOutputsAndPanic(t *testing.T) {
	for _, tc := range []struct {
		name string
		fn   runFunc
		want string
	}{
		{"nil", func(context.Context, Payload) (Payload, error) { return nil, nil }, "nil"},
		{"wrong phase", func(context.Context, Payload) (Payload, error) { return Envelope{}, nil }, "RawRequest"},
		{"panic", func(context.Context, Payload) (Payload, error) { panic("boom") }, "panicked"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := testRegistry(t)
			replaceRun(r, "read_body", tc.fn)
			var events []NodeEvent
			term, err := mustCompile(t, linearFlow(), r).Run(context.Background(), RequestHead{}, RunOptions{PublishTimeout: time.Second, OnNode: func(e NodeEvent) { events = append(events, e) }})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatal(err)
			}
			if tc.name == "panic" {
				var pe PanicError
				if !errors.As(err, &pe) {
					t.Fatalf("panic = %v (%T), want PanicError", err, err)
				}
			}
			if len(events) != 3 || events[2].Err == nil {
				t.Fatalf("events: %+v", events)
			}
			if len(term.Deliveries) != 1 || !term.Deliveries[0].Unattempted {
				t.Fatalf("missing unattempted: %+v", term)
			}
		})
	}
}
func TestStopOnFailureIsSequential(t *testing.T) {
	r := testRegistry(t)
	var calls []string
	sentinel := errors.New("publish")
	s := r.specs["dest"]
	s.New = func(n NodeConfig) (Node, error) {
		return runFunc(func(context.Context, Payload) (Payload, error) {
			calls = append(calls, n.ID)
			d := Delivery{NodeID: n.ID, Target: "{route}-queue"}
			if n.ID == "b" {
				d.Err = sentinel
				return Terminal{Deliveries: []Delivery{d}}, sentinel
			}
			return Terminal{Deliveries: []Delivery{d}}, nil
		}), nil
	}
	r.specs["dest"] = s
	fc := linearFlow()
	fc.Nodes[len(fc.Nodes)-1].ID = "a"
	fc.Nodes = append(fc.Nodes, NodeConfig{ID: "b", Type: "dest"}, NodeConfig{ID: "c", Type: "dest"})
	fc.Edges[len(fc.Edges)-1] = "env>a"
	fc.Edges = append(fc.Edges, "env>b", "env>c")
	term, err := mustCompile(t, fc, r).Run(context.Background(), RequestHead{}, RunOptions{PublishTimeout: time.Second, FanoutConcurrency: 99})
	if !errors.Is(err, sentinel) || strings.Join(calls, ",") != "a,b" {
		t.Fatalf("calls %v err %v", calls, err)
	}
	if len(term.Deliveries) != 3 || term.Deliveries[0].Unattempted || !errors.Is(term.Deliveries[1].Err, sentinel) || !term.Deliveries[2].Unattempted {
		t.Fatalf("%+v", term.Deliveries)
	}
}
func TestFanoutOwnsNestedPayloads(t *testing.T) {
	r := testRegistry(t)
	replaceRun(r, "envelope", func(context.Context, Payload) (Payload, error) {
		return Envelope{Body: map[string]any{"nested": []any{map[string]any{"x": "before"}}}, Vars: map[string]string{"v": "before"}, V: Verified{Raw: RawRequest{Body: []byte("original"), Head: RequestHead{Header: http.Header{"X": {"before"}}}}}, Upstream: &UpstreamResponse{Body: []byte("upstream")}}, nil
	})
	replaceRun(r, "process", func(_ context.Context, p Payload) (Payload, error) {
		e := p.(Envelope)
		e.Body.(map[string]any)["nested"].([]any)[0].(map[string]any)["x"] = "after"
		e.Vars["v"] = "after"
		e.V.Raw.Body[0] = 'X'
		e.V.Raw.Head.Header.Set("X", "after")
		e.Upstream.Body[0] = 'X'
		return e, nil
	})
	s := r.specs["dest"]
	s.New = func(n NodeConfig) (Node, error) {
		return runFunc(func(_ context.Context, p Payload) (Payload, error) {
			if n.ID == "clean" {
				e := p.(Envelope)
				if e.Vars["v"] != "before" || e.Body.(map[string]any)["nested"].([]any)[0].(map[string]any)["x"] != "before" || string(e.V.Raw.Body) != "original" || e.V.Raw.Head.Header.Get("X") != "before" || string(e.Upstream.Body) != "upstream" {
					return nil, errors.New("sibling mutation leaked")
				}
			}
			return Terminal{}, nil
		}), nil
	}
	r.specs["dest"] = s
	fc := linearFlow()
	fc.Nodes[len(fc.Nodes)-1].ID = "dirty"
	fc.Nodes = append(fc.Nodes, NodeConfig{ID: "mutate", Type: "process"}, NodeConfig{ID: "clean", Type: "dest"})
	fc.Edges[len(fc.Edges)-1] = "env>mutate"
	fc.Edges = append(fc.Edges, "mutate>dirty", "env>clean")
	f := mustCompile(t, fc, r)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := f.Run(context.Background(), RequestHead{}, RunOptions{PublishTimeout: time.Second}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
}

func TestCompileRejectsReconvergence(t *testing.T) {
	fc := linearFlow()
	fc.Nodes = append(fc.Nodes, NodeConfig{ID: "left", Type: "process"}, NodeConfig{ID: "right", Type: "process"})
	fc.Edges = fc.Edges[:len(fc.Edges)-1]
	fc.Edges = append(fc.Edges, "env>left", "env>right", "left>out", "right>out")
	_, err := Compile("join", fc, testRegistry(t))
	if err == nil || !strings.Contains(err.Error(), "reconvergence") {
		t.Fatal(err)
	}
}
func TestRejectsPointerPayloadWithoutPanic(t *testing.T) {
	r := testRegistry(t)
	var nilHead *RequestHead
	f := mustCompile(t, linearFlow(), r)
	if _, err := f.Run(context.Background(), nilHead, RunOptions{PublishTimeout: time.Second}); err == nil {
		t.Fatal("accepted nil pointer seed")
	}
	replaceRun(r, "read_body", func(context.Context, Payload) (Payload, error) { var raw *RawRequest; return raw, nil })
	if _, err := mustCompile(t, linearFlow(), r).Run(context.Background(), RequestHead{}, RunOptions{PublishTimeout: time.Second}); err == nil {
		t.Fatal("accepted nil pointer output")
	}
}
func TestNodeCannotInjectCompensation(t *testing.T) {
	r := testRegistry(t)
	replaceRun(r, "dest", func(context.Context, Payload) (Payload, error) {
		return Terminal{Compensations: []Compensation{{NodeID: "last"}, {NodeID: "first"}}}, nil
	})
	term, err := mustCompile(t, linearFlow(), r).Run(context.Background(), RequestHead{}, RunOptions{PublishTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if len(term.Compensations) != 0 {
		t.Fatalf("node injected compensation: %+v", term.Compensations)
	}
}

type mapNode map[string]string

func (mapNode) Run(context.Context, Payload) (Payload, error) { return Terminal{}, nil }

type sliceNode []int

func (sliceNode) Run(context.Context, Payload) (Payload, error) { return Terminal{}, nil }

type chanNode chan int

func (chanNode) Run(context.Context, Payload) (Payload, error) { return Terminal{}, nil }
func TestCompileRejectsTypedNilNodes(t *testing.T) {
	var ptr *staticNode
	var fn runFunc
	var m mapNode
	var s sliceNode
	var ch chanNode
	for _, node := range []Node{ptr, fn, m, s, ch} {
		t.Run(fmt.Sprintf("%T", node), func(t *testing.T) {
			r := testRegistry(t)
			spec := r.specs["dest"]
			spec.New = func(NodeConfig) (Node, error) { return node, nil }
			r.specs["dest"] = spec
			_, err := Compile("nil", linearFlow(), r)
			if err == nil || !strings.Contains(err.Error(), "nil node") {
				t.Fatalf("%T: %v", node, err)
			}
		})
	}
}
func TestDetachedDeliveryErrorIsRecorded(t *testing.T) {
	r := testRegistry(t)
	sentinel := errors.New("publish failed")
	replaceRun(r, "dest", func(context.Context, Payload) (Payload, error) {
		return Terminal{Deliveries: []Delivery{{NodeID: "out", Target: "configured"}}}, sentinel
	})
	term, err := mustCompile(t, linearFlow(), r).Run(context.Background(), RequestHead{}, RunOptions{PublishTimeout: time.Second})
	if !errors.Is(err, sentinel) || len(term.Deliveries) != 1 || !errors.Is(term.Deliveries[0].Err, sentinel) {
		t.Fatalf("%+v %v", term, err)
	}
}
func TestCollectedCompensationsAreReversed(t *testing.T) {
	var order []string
	r := testRegistry(t)
	spec := NodeSpec{Type: "reserve", In: PortVerified, Out: PortVerified, New: func(n NodeConfig) (Node, error) { return &orderCompensator{id: n.ID, log: &order}, nil }}
	if err := r.Register(spec); err != nil {
		t.Fatal(err)
	}
	fc := linearFlow()
	fc.Nodes = append(fc.Nodes, NodeConfig{ID: "first", Type: "reserve"}, NodeConfig{ID: "second", Type: "reserve"})
	fc.Edges[3] = "auth>first"
	fc.Edges = append(fc.Edges, "first>second", "second>env")
	term, err := mustCompile(t, fc, r).Run(context.Background(), RequestHead{}, RunOptions{PublishTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if len(order) != 0 {
		t.Fatal("automatically compensated")
	}
	if len(term.Compensations) != 2 || term.Compensations[0].NodeID != "second" {
		t.Fatalf("%+v", term.Compensations)
	}
	if err := RunCompensations(context.Background(), term, nil, time.Second); err != nil {
		t.Fatal(err)
	}
	if strings.Join(order, ",") != "second,first" {
		t.Fatal(order)
	}
}

type metadataNode struct{ panicTarget bool }

func (metadataNode) Run(context.Context, Payload) (Payload, error) { return Terminal{}, nil }
func (n metadataNode) DeliveryTarget() string {
	if n.panicTarget {
		panic("bad target")
	}
	return "{route}-queue"
}
func TestMetadataPanicFailsCompile(t *testing.T) {
	r := testRegistry(t)
	s := r.specs["dest"]
	s.New = func(NodeConfig) (Node, error) { return metadataNode{panicTarget: true}, nil }
	r.specs["dest"] = s
	if _, err := Compile("bad", linearFlow(), r); err == nil {
		t.Fatal("accepted panic in destination metadata")
	}
}
func TestConfiguredTargetForUnattempted(t *testing.T) {
	r := testRegistry(t)
	s := r.specs["dest"]
	s.New = func(NodeConfig) (Node, error) { return metadataNode{}, nil }
	r.specs["dest"] = s
	replaceRun(r, "auth", func(context.Context, Payload) (Payload, error) { return nil, errors.New("auth") })
	term, _ := mustCompile(t, linearFlow(), r).Run(context.Background(), RequestHead{}, RunOptions{PublishTimeout: time.Second})
	if len(term.Deliveries) != 1 || term.Deliveries[0].Target != "{route}-queue" || !term.Deliveries[0].Unattempted {
		t.Fatalf("%+v", term)
	}
}

type panicBody struct{}

func (panicBody) CloneBody() (any, error) { panic("clone failed") }
func TestBodyClonePanicIsError(t *testing.T) {
	if _, err := clonePayload(Envelope{Body: panicBody{}}); err == nil {
		t.Fatal("clone panic accepted")
	}
}

type targetRunNode struct {
	runFunc
	target string
}

func (n targetRunNode) DeliveryTarget() string { return n.target }
func TestDeliveryIdentityComesFromConfig(t *testing.T) {
	r := testRegistry(t)
	s := r.specs["dest"]
	s.New = func(NodeConfig) (Node, error) {
		return targetRunNode{target: "{route}-queue", runFunc: func(context.Context, Payload) (Payload, error) {
			return Terminal{Deliveries: []Delivery{{NodeID: "foreign", Target: "customer-123-queue", Unattempted: true}}}, nil
		}}, nil
	}
	r.specs["dest"] = s
	term, err := mustCompile(t, linearFlow(), r).Run(context.Background(), RequestHead{}, RunOptions{PublishTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if len(term.Deliveries) != 1 || term.Deliveries[0].NodeID != "out" || term.Deliveries[0].Target != "{route}-queue" || term.Deliveries[0].Unattempted {
		t.Fatalf("spoofed metadata: %+v", term.Deliveries)
	}
}

func TestDestinationCannotReportResponse(t *testing.T) {
	r := testRegistry(t)
	replaceRun(r, "dest", func(context.Context, Payload) (Payload, error) {
		return Terminal{Response: &Response{Status: 200}}, nil
	})
	term, err := mustCompile(t, linearFlow(), r).Run(context.Background(), RequestHead{}, RunOptions{PublishTimeout: time.Second})
	if err != nil || term.Response != nil || len(term.Deliveries) != 1 {
		t.Fatalf("destination reported response: %+v, %v", term, err)
	}
}

func TestRespondCannotReportDeliveries(t *testing.T) {
	r := testRegistry(t)
	replaceRun(r, "respond", func(context.Context, Payload) (Payload, error) {
		return Terminal{Response: &Response{Status: 201}, Deliveries: []Delivery{{NodeID: "spoof", Err: errors.New("invented failure")}}}, nil
	})
	fc := linearFlow()
	fc.Nodes[len(fc.Nodes)-1].Type = "respond"
	term, err := mustCompile(t, fc, r).Run(context.Background(), RequestHead{}, RunOptions{PublishTimeout: time.Second})
	if err != nil || term.Response == nil || term.Response.Status != 201 || len(term.Deliveries) != 0 || NeedsCompensation(term, err) {
		t.Fatalf("respond reported deliveries: %+v, %v", term, err)
	}
}
func TestDeliveryOutcomeFailureStopsSuccessors(t *testing.T) {
	r := testRegistry(t)
	sentinel := errors.New("outcome failed")
	var calls []string
	s := r.specs["dest"]
	s.New = func(n NodeConfig) (Node, error) {
		return runFunc(func(context.Context, Payload) (Payload, error) {
			calls = append(calls, n.ID)
			d := Delivery{}
			if n.ID == "b" {
				d.Err = sentinel
			}
			return Terminal{Deliveries: []Delivery{d}}, nil
		}), nil
	}
	r.specs["dest"] = s
	fc := linearFlow()
	fc.Nodes[len(fc.Nodes)-1].ID = "a"
	fc.Nodes = append(fc.Nodes, NodeConfig{ID: "b", Type: "dest"}, NodeConfig{ID: "c", Type: "dest"})
	fc.Edges[len(fc.Edges)-1] = "env>a"
	fc.Edges = append(fc.Edges, "env>b", "env>c")
	var events []NodeEvent
	term, err := mustCompile(t, fc, r).Run(context.Background(), RequestHead{}, RunOptions{PublishTimeout: time.Second, OnNode: func(e NodeEvent) { events = append(events, e) }})
	if !errors.Is(err, sentinel) || strings.Join(calls, ",") != "a,b" {
		t.Fatalf("calls %v, error %v", calls, err)
	}
	if len(term.Deliveries) != 3 || term.Deliveries[0].Err != nil || !errors.Is(term.Deliveries[1].Err, sentinel) || !term.Deliveries[2].Unattempted {
		t.Fatalf("outcomes: %+v", term.Deliveries)
	}
	if events[len(events)-1].NodeID != "b" || !errors.Is(events[len(events)-1].Err, sentinel) {
		t.Fatalf("event lost outcome error: %+v", events)
	}
}
func TestCompileParseProblemsHaveSeparateBullets(t *testing.T) {
	fc := linearFlow()
	fc.Edges = append(fc.Edges, "bad", "broken", "a>b>c")
	_, err := Compile("bad", fc, testRegistry(t))
	var ce *CompileError
	if !errors.As(err, &ce) {
		t.Fatalf("got %v", err)
	}
	if len(ce.Problems) != 3 {
		t.Fatalf("got %d problems: %v", len(ce.Problems), ce.Problems)
	}
	if strings.Count(ce.Error(), "\n  - ") != 3 || !strings.Contains(ce.Error(), "3 problem(s)") {
		t.Fatalf("wrong summary: %s", ce)
	}
	for i, p := range ce.Problems {
		if !strings.Contains(p, fmt.Sprintf("edges[%d]", i+5)) || strings.Contains(p, "\n") {
			t.Fatalf("problem[%d]: %q", i, p)
		}
	}
}
