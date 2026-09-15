package graph

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func mustCompile(t *testing.T, fc FlowConfig, reg *Registry) *Flow {
	t.Helper()
	f, err := Compile("test", fc, reg)
	if err != nil {
		t.Fatalf("Compile() = %v", err)
	}
	return f
}

// registryWithRecorder replaces the passthrough constructors with recording
// nodes so tests can assert execution order.
func registryWithRecorder(t *testing.T, rec *recorder) *Registry {
	t.Helper()
	r := NewRegistry()
	add := func(name string, in, out PortType, detached bool) {
		spec := NodeSpec{
			Type: name, In: in, Out: out, Detached: detached,
			New: func(nc NodeConfig) (Node, error) {
				n := rec.node(nc.ID)
				n.out = zeroPayload(out)
				return n, nil
			},
		}
		if err := r.Register(spec); err != nil {
			t.Fatalf("Register(%s) = %v", name, err)
		}
	}
	add("trigger", PortNone, PortRequestHead, false)
	add("gate", PortRequestHead, PortRequestHead, false)
	add("read_body", PortRequestHead, PortRawRequest, false)
	add("auth", PortRawRequest, PortVerified, false)
	add("envelope", PortVerified, PortEnvelope, false)
	add("process", PortEnvelope, PortEnvelope, false)
	add("dest", PortEnvelope, PortTerminal, true)
	return r
}

func linearFlow() FlowConfig {
	return flowConfig([][2]string{
		{"in", "trigger"}, {"rl", "gate"}, {"body", "read_body"},
		{"auth", "auth"}, {"env", "envelope"}, {"out", "dest"},
	}, "in>rl", "rl>body", "body>auth", "auth>env", "env>out")
}

func TestRunExecutesInTopologicalOrder(t *testing.T) {
	rec := &recorder{}
	f := mustCompile(t, linearFlow(), registryWithRecorder(t, rec))

	term, err := f.Run(context.Background(), RequestHead{}, RunOptions{PublishTimeout: time.Second})
	if err != nil {
		t.Fatalf("Run() = %v, want nil", err)
	}
	got := strings.Join(rec.entries(), ",")
	if want := "in,rl,body,auth,env,out"; got != want {
		t.Errorf("execution order = %q, want %q", got, want)
	}
	if len(term.Deliveries) != 1 || term.Deliveries[0].NodeID != "out" {
		t.Errorf("Deliveries = %+v, want one entry for node out", term.Deliveries)
	}
}

func TestRunFansOutToEveryDestination(t *testing.T) {
	rec := &recorder{}
	fc := flowConfig([][2]string{
		{"in", "trigger"}, {"body", "read_body"}, {"auth", "auth"}, {"env", "envelope"},
		{"a", "dest"}, {"b", "dest"},
	}, "in>body", "body>auth", "auth>env", "env>a", "env>b")
	f := mustCompile(t, fc, registryWithRecorder(t, rec))

	term, err := f.Run(context.Background(), RequestHead{}, RunOptions{PublishTimeout: time.Second})
	if err != nil {
		t.Fatalf("Run() = %v, want nil", err)
	}
	if len(term.Deliveries) != 2 {
		t.Errorf("Deliveries has %d entries, want 2: %+v", len(term.Deliveries), term.Deliveries)
	}
}

func TestRunReportsNodeErrors(t *testing.T) {
	rec := &recorder{}
	sentinel := errors.New("auth failed")
	reg := NewRegistry()
	specs := []struct {
		name     string
		in, out  PortType
		detached bool
		err      error
	}{
		{"trigger", PortNone, PortRequestHead, false, nil},
		{"read_body", PortRequestHead, PortRawRequest, false, nil},
		{"auth", PortRawRequest, PortVerified, false, sentinel},
		{"envelope", PortVerified, PortEnvelope, false, nil},
		{"dest", PortEnvelope, PortTerminal, true, nil},
	}
	for _, s := range specs {
		s := s
		if err := reg.Register(NodeSpec{
			Type: s.name, In: s.in, Out: s.out, Detached: s.detached,
			New: func(nc NodeConfig) (Node, error) {
				n := rec.node(nc.ID)
				n.err, n.out = s.err, zeroPayload(s.out)
				return n, nil
			},
		}); err != nil {
			t.Fatalf("Register(%s) = %v", s.name, err)
		}
	}
	fc := flowConfig([][2]string{
		{"in", "trigger"}, {"body", "read_body"}, {"auth", "auth"},
		{"env", "envelope"}, {"out", "dest"},
	}, "in>body", "body>auth", "auth>env", "env>out")
	f := mustCompile(t, fc, reg)

	_, err := f.Run(context.Background(), RequestHead{}, RunOptions{PublishTimeout: time.Second})
	if !errors.Is(err, sentinel) {
		t.Fatalf("Run() = %v, want it to wrap %v", err, sentinel)
	}
	// Nothing downstream of the failure may run.
	for _, id := range rec.entries() {
		if id == "env" || id == "out" {
			t.Errorf("node %q ran after auth failed", id)
		}
	}
}

// TestRunDetachesDestinationContext pins I10: a cancelled inbound request must
// not cancel a destination node, because that work is already committed.
func TestRunDetachesDestinationContext(t *testing.T) {
	var destSawCancelled bool
	reg := NewRegistry()
	add := func(name string, in, out PortType, detached bool, run func(context.Context)) {
		if err := reg.Register(NodeSpec{
			Type: name, In: in, Out: out, Detached: detached,
			New: func(NodeConfig) (Node, error) {
				return &fnNode{out: zeroPayload(out), fn: run}, nil
			},
		}); err != nil {
			t.Fatalf("Register(%s) = %v", name, err)
		}
	}
	add("trigger", PortNone, PortRequestHead, false, nil)
	add("read_body", PortRequestHead, PortRawRequest, false, nil)
	add("auth", PortRawRequest, PortVerified, false, nil)
	add("envelope", PortVerified, PortEnvelope, false, nil)
	add("dest", PortEnvelope, PortTerminal, true, func(ctx context.Context) {
		destSawCancelled = ctx.Err() != nil
	})

	fc := flowConfig([][2]string{
		{"in", "trigger"}, {"body", "read_body"}, {"auth", "auth"},
		{"env", "envelope"}, {"out", "dest"},
	}, "in>body", "body>auth", "auth>env", "env>out")
	f := mustCompile(t, fc, reg)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // caller has already disconnected

	if _, err := f.Run(ctx, RequestHead{}, RunOptions{PublishTimeout: time.Second}); err != nil {
		t.Fatalf("Run() = %v, want nil: a detached destination must still run", err)
	}
	if destSawCancelled {
		t.Error("destination node saw a cancelled context; I10 requires it be detached")
	}
}

// --- compensation (invariant I17) ---

// recordingCompensator reserves nothing real; it records that Compensate was
// called and with which reason, so tests can assert the executor collected it
// and the caller invoked it.
type recordingCompensator struct {
	out         Payload
	fail        bool
	mu          sync.Mutex
	compensated int
	lastInput   Payload
}

func (n *recordingCompensator) Run(_ context.Context, in Payload) (Payload, error) {
	if n.fail {
		return nil, errors.New("node failed")
	}
	return n.out, nil
}

func (n *recordingCompensator) Compensate(_ context.Context, in Payload, _ error) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.compensated++
	n.lastInput = in
	return nil
}

func TestRunCollectsCompensationsForNodesThatRan(t *testing.T) {
	comp := &recordingCompensator{out: Verified{}}
	f := compileWithCompensator(t, comp, false)

	term, err := f.Run(context.Background(), RequestHead{}, RunOptions{PublishTimeout: time.Second})
	if err != nil {
		t.Fatalf("Run() = %v", err)
	}
	if len(term.Compensations) != 1 {
		t.Fatalf("Compensations has %d entries, want 1", len(term.Compensations))
	}
	if term.Compensations[0].NodeID != "guard" {
		t.Errorf("NodeID = %q, want guard", term.Compensations[0].NodeID)
	}
	// Run must NOT have invoked it: a successful flow keeps the reservation.
	if comp.compensated != 0 {
		t.Errorf("Compensate called %d times during Run; Run must only collect, never invoke", comp.compensated)
	}
}

// TestRunPopulatesTerminalOnError pins the contract the caller depends on:
// V1 returns the succeeded and unattempted destination lists even on a 503
// (server.go:322), and compensation needs Deliveries to make its decision.
func TestRunPopulatesTerminalOnError(t *testing.T) {
	rec := &recorder{}
	sentinel := errors.New("publish failed")
	f := compileWithFailingDestination(t, rec, sentinel)

	term, err := f.Run(context.Background(), RequestHead{}, RunOptions{PublishTimeout: time.Second})
	if !errors.Is(err, sentinel) {
		t.Fatalf("Run() err = %v, want it to wrap %v", err, sentinel)
	}
	if len(term.Deliveries) == 0 {
		t.Fatal("Terminal.Deliveries is empty on the error path; the caller needs it for the " +
			"503 response body and for replay capture")
	}
}

// TestRunDoesNotCollectCompensationForNodesThatFailed: a node that errored
// reserved nothing, so compensating it would release a key it never took.
func TestRunDoesNotCollectCompensationForNodesThatFailed(t *testing.T) {
	comp := &recordingCompensator{out: Verified{}, fail: true}
	f := compileWithCompensator(t, comp, false)

	term, err := f.Run(context.Background(), RequestHead{}, RunOptions{PublishTimeout: time.Second})
	if err == nil {
		t.Fatal("Run() = nil, want the node error")
	}
	if len(term.Compensations) != 0 {
		t.Errorf("Compensations has %d entries for a node that failed, want 0", len(term.Compensations))
	}
}

func TestNeedsCompensation(t *testing.T) {
	failed := Terminal{Deliveries: []Delivery{{NodeID: "a"}, {NodeID: "b", Err: errors.New("x")}}}
	allOK := Terminal{Deliveries: []Delivery{{NodeID: "a"}, {NodeID: "b"}}}
	none := Terminal{}

	cases := []struct {
		name string
		term Terminal
		err  error
		want bool
	}{
		{"flow errored", none, errors.New("boom"), true},
		{"full success", allOK, nil, false},
		// The skip_failed partial-failure case: 202 to the sender, but the
		// failed destinations still need the retry (server.go:344).
		{"partial delivery failure, no error", failed, nil, true},
		// A filter silent drop: no error, no deliveries attempted. The
		// delivery was consumed, so the reservation is kept (server.go:181).
		{"filtered, nothing attempted", none, nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := NeedsCompensation(tc.term, tc.err); got != tc.want {
				t.Errorf("NeedsCompensation() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestRunCompensationsUsesDetachedContext is the subtle one. A client
// disconnect is exactly when a publish fails, and the reservation MUST still
// be released -- otherwise it leaks until TTL and the sender's retry gets a
// 409 instead of being redelivered.
func TestRunCompensationsUsesDetachedContext(t *testing.T) {
	comp := &recordingCompensator{out: Verified{}}
	term := Terminal{Compensations: []Compensation{{NodeID: "guard", Node: comp, Input: RawRequest{}}}}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // caller already gone

	if err := RunCompensations(ctx, term, errors.New("publish failed"), time.Second); err != nil {
		t.Fatalf("RunCompensations() = %v, want nil", err)
	}
	if comp.compensated != 1 {
		t.Fatal("compensation was skipped because the caller's context was cancelled; " +
			"it must run on a detached context or the reservation leaks (I17)")
	}
}

func TestRunCompensationsRunsInReverseOrder(t *testing.T) {
	var order []string
	mk := func(id string) Compensator { return &orderCompensator{id: id, log: &order} }
	term := Terminal{Compensations: []Compensation{
		{NodeID: "second", Node: mk("second")},
		{NodeID: "first", Node: mk("first")},
	}}
	if err := RunCompensations(context.Background(), term, nil, time.Second); err != nil {
		t.Fatalf("RunCompensations() = %v", err)
	}
	// Terminal.Compensations is already reverse execution order, so it runs
	// front to back. LIFO, like defer.
	if strings.Join(order, ",") != "second,first" {
		t.Errorf("order = %v, want second,first", order)
	}
}

type orderCompensator struct {
	id  string
	log *[]string
}

func (o *orderCompensator) Run(_ context.Context, in Payload) (Payload, error) { return in, nil }
func (o *orderCompensator) Compensate(_ context.Context, _ Payload, _ error) error {
	*o.log = append(*o.log, o.id)
	return nil
}

// TestRunCompensationsSurvivesPanic: a failing release must be visible but
// must never escalate. The response has already failed; a panic here would
// turn a handled 503 into a crash.
func TestRunCompensationsSurvivesPanic(t *testing.T) {
	term := Terminal{Compensations: []Compensation{
		{NodeID: "boom", Node: panicCompensator{}},
		{NodeID: "ok", Node: &orderCompensator{id: "ok", log: new([]string)}},
	}}
	err := RunCompensations(context.Background(), term, nil, time.Second)
	if err == nil {
		t.Error("RunCompensations() = nil; a panicking compensation must be reported")
	}
	// Reaching here without the process dying is the rest of the assertion.
}

type panicCompensator struct{}

func (panicCompensator) Run(_ context.Context, in Payload) (Payload, error) { return in, nil }
func (panicCompensator) Compensate(_ context.Context, _ Payload, _ error) error {
	panic("release exploded")
}

type fnNode struct {
	out Payload
	fn  func(context.Context)
}

func (n *fnNode) Run(ctx context.Context, _ Payload) (Payload, error) {
	if n.fn != nil {
		n.fn(ctx)
	}
	return n.out, nil
}

// TestRunEmitsOneEventPerNode covers scalability rule R2: per-node
// observability comes from this callback, never from a persisted row.
func TestRunEmitsOneEventPerNode(t *testing.T) {
	rec := &recorder{}
	f := mustCompile(t, linearFlow(), registryWithRecorder(t, rec))

	var events []NodeEvent
	_, err := f.Run(context.Background(), RequestHead{}, RunOptions{
		PublishTimeout: time.Second,
		OnNode:         func(e NodeEvent) { events = append(events, e) },
	})
	if err != nil {
		t.Fatalf("Run() = %v", err)
	}
	if len(events) != 6 {
		t.Fatalf("got %d node events, want 6: %+v", len(events), events)
	}
	for _, e := range events {
		if e.NodeID == "" || e.NodeType == "" {
			t.Errorf("event %+v is missing NodeID or NodeType (needed as metric labels)", e)
		}
	}
}

func TestRunRejectsWrongRootPayload(t *testing.T) {
	rec := &recorder{}
	f := mustCompile(t, linearFlow(), registryWithRecorder(t, rec))
	_, err := f.Run(context.Background(), Envelope{}, RunOptions{PublishTimeout: time.Second})
	if err == nil {
		t.Fatal("Run() = nil; an Envelope cannot seed a flow whose trigger emits RequestHead")
	}
}

func TestBeforeDetachedRunsOnceBeforePublishTimeoutContext(t *testing.T) {
	var beforeCount int
	var remaining time.Duration
	reg := NewRegistry()
	add := func(name string, in, out PortType, detached bool, run func(context.Context)) {
		if err := reg.Register(NodeSpec{
			Type: name, In: in, Out: out, Detached: detached,
			New: func(NodeConfig) (Node, error) {
				return &fnNode{out: zeroPayload(out), fn: run}, nil
			},
		}); err != nil {
			t.Fatalf("Register(%s) = %v", name, err)
		}
	}
	add("trigger", PortNone, PortRequestHead, false, nil)
	add("read_body", PortRequestHead, PortRawRequest, false, nil)
	add("auth", PortRawRequest, PortVerified, false, nil)
	add("envelope", PortVerified, PortEnvelope, false, nil)
	add("dest", PortEnvelope, PortTerminal, true, func(ctx context.Context) {
		deadline, ok := ctx.Deadline()
		if !ok {
			t.Error("detached node missing deadline")
			return
		}
		remaining = time.Until(deadline)
	})
	fc := flowConfig([][2]string{
		{"in", "trigger"}, {"body", "read_body"}, {"auth", "auth"},
		{"env", "envelope"}, {"a", "dest"}, {"b", "dest"},
	}, "in>body", "body>auth", "auth>env", "env>a", "env>b")
	f := mustCompile(t, fc, reg)
	_, err := f.Run(context.Background(), RequestHead{}, RunOptions{
		PublishTimeout: 200 * time.Millisecond,
		BeforeDetached: func(Payload) {
			beforeCount++
			time.Sleep(80 * time.Millisecond)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if beforeCount != 1 {
		t.Fatalf("BeforeDetached calls = %d, want 1 (once per request, not per destination)", beforeCount)
	}
	if remaining < 150*time.Millisecond {
		t.Fatalf("publish context remaining %s; BeforeDetached must not consume PublishTimeout", remaining)
	}
}

