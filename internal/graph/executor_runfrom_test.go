package graph

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// reserveNode is a Compensator placed mid-flow, to prove RunFrom never
// returns compensations.
type reserveNode struct{ rec *recorder }

func (n reserveNode) Run(_ context.Context, in Payload) (Payload, error) {
	n.rec.mu.Lock()
	n.rec.log = append(n.rec.log, "reserve")
	n.rec.mu.Unlock()
	return in, nil
}

func (reserveNode) Compensate(context.Context, Payload, error) error { return nil }

func runFromOpts(c *failureCollector) RunOptions {
	opts := RunOptions{PublishTimeout: time.Second}
	if c != nil {
		opts.OnFailure = c.record
	}
	return opts
}

func TestRunFromSkipsUpstreamNodes(t *testing.T) {
	rec := &recorder{}
	fc := flowConfig(nodesWith([2]string{"p", "process"}, [2]string{"a", "dest"}), edgesWith("env>p", "p>a")...)
	f := mustCompile(t, fc, failureRegistry(t, rec))
	term, err := f.RunFrom(context.Background(), "p", Envelope{Vars: map[string]string{"k": "v"}}, runFromOpts(nil))
	if err != nil {
		t.Fatalf("RunFrom() = %v", err)
	}
	if got := strings.Join(rec.entries(), ","); got != "p,a" {
		t.Fatalf("ran %q, want p,a (upstream nodes never run)", got)
	}
	if len(term.Deliveries) != 1 || term.Deliveries[0].NodeID != "a" || term.Deliveries[0].Err != nil {
		t.Fatalf("Deliveries = %+v, want one successful delivery for a", term.Deliveries)
	}
}

func TestRunFromRejectsBadArguments(t *testing.T) {
	rec := &recorder{}
	fc := flowConfig(nodesWith([2]string{"p", "process"}, [2]string{"a", "dest"}), edgesWith("env>p", "p>a")...)
	f := mustCompile(t, fc, failureRegistry(t, rec))
	cases := map[string]struct {
		node string
		in   Payload
		opts RunOptions
		want string
	}{
		"unknown node":      {"nope", Envelope{}, runFromOpts(nil), `no node "nope"`},
		"wrong port":        {"p", Verified{}, runFromOpts(nil), "expects Envelope"},
		"nil payload":       {"p", nil, runFromOpts(nil), "nil payload"},
		"no publish budget": {"p", Envelope{}, RunOptions{}, "PublishTimeout must be positive"},
		"trigger":           {"in", RequestHead{}, runFromOpts(nil), "consumes an Envelope"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := f.RunFrom(context.Background(), c.node, c.in, c.opts)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("RunFrom() = %v, want an error containing %q", err, c.want)
			}
		})
	}
	if got := rec.entries(); len(got) != 0 {
		t.Fatalf("rejected calls ran nodes: %v", got)
	}
}

func TestRunFromRunsTheSuccessContinuation(t *testing.T) {
	rec := &recorder{}
	fc := flowConfig(nodesWith([2]string{"a", "dest"}, [2]string{"b", "dest"}), edgesWith("env>a", "a>b")...)
	f := mustCompile(t, fc, failureRegistry(t, rec))
	if _, err := f.RunFrom(context.Background(), "a", Envelope{}, runFromOpts(nil)); err != nil {
		t.Fatalf("RunFrom() = %v", err)
	}
	if got := strings.Join(rec.entries(), ","); got != "a,b" {
		t.Fatalf("ran %q, want a,b", got)
	}
}

func TestRunFromReportsUnattemptedOnlyInsideTheResumedSubgraph(t *testing.T) {
	rec := &recorder{}
	fc := flowConfig(
		nodesWith([2]string{"x", "dest"}, [2]string{"p", "process"}, [2]string{"a", "dest"}, [2]string{"b", "dest"}),
		edgesWith("env>x", "env>p", "p>a", "p>b")...)
	f := mustCompile(t, fc, failureRegistry(t, rec, "a"))
	var c failureCollector
	term, err := f.RunFrom(context.Background(), "p", Envelope{}, runFromOpts(&c))
	if !errors.Is(err, errFail) {
		t.Fatalf("RunFrom() = %v, want errFail", err)
	}
	byID := map[string]Delivery{}
	for _, d := range term.Deliveries {
		byID[d.NodeID] = d
	}
	if _, ok := byID["x"]; ok {
		t.Fatalf("Deliveries = %+v: x is outside the resumed subgraph and must not be reported", term.Deliveries)
	}
	if d, ok := byID["a"]; !ok || d.Err == nil {
		t.Fatalf("Deliveries = %+v, want a failed", term.Deliveries)
	}
	if d, ok := byID["b"]; !ok || !d.Unattempted {
		t.Fatalf("Deliveries = %+v, want b unattempted", term.Deliveries)
	}
	if got := c.nodeIDs(); !slices.Equal(got, []string{"a"}) {
		t.Fatalf("OnFailure nodes = %v, want [a]", got)
	}
}

func TestRunFromNeverReturnsCompensations(t *testing.T) {
	rec := &recorder{}
	r := failureRegistry(t, rec)
	if err := r.Register(NodeSpec{
		Type: "reserve", In: PortEnvelope, Out: PortEnvelope,
		New: func(NodeConfig) (Node, error) { return reserveNode{rec: rec}, nil },
	}); err != nil {
		t.Fatal(err)
	}
	fc := flowConfig(nodesWith([2]string{"r", "reserve"}, [2]string{"a", "dest"}), edgesWith("env>r", "r>a")...)
	f := mustCompile(t, fc, r)
	full, err := f.Run(context.Background(), RequestHead{}, runFromOpts(nil))
	if err != nil || len(full.Compensations) != 1 {
		t.Fatalf("Run() = %v, compensations %d; want the reservation collected (sanity check)", err, len(full.Compensations))
	}
	term, err := f.RunFrom(context.Background(), "r", Envelope{}, runFromOpts(nil))
	if err != nil {
		t.Fatalf("RunFrom() = %v", err)
	}
	if len(term.Compensations) != 0 {
		t.Fatalf("RunFrom compensations = %+v, want none", term.Compensations)
	}
}

func TestRunFromDoesNotShareTheCallersPayload(t *testing.T) {
	rec := &recorder{}
	fc := flowConfig(nodesWith([2]string{"a", "dest"}), edgesWith("env>a")...)
	f := mustCompile(t, fc, failureRegistry(t, rec, "a"))
	in := Envelope{Vars: map[string]string{"k": "v"}}
	var c failureCollector
	_, _ = f.RunFrom(context.Background(), "a", in, runFromOpts(&c))
	got, ok := c.events[0].Input.(Envelope)
	if !ok {
		t.Fatalf("Input = %#v", c.events[0].Input)
	}
	got.Vars["k"] = "changed"
	if in.Vars["k"] != "v" {
		t.Fatal("RunFrom handed the caller's payload to nodes; it must seed a clone, like Run")
	}
}

// deadlineSpy swaps the process and dest constructors for nodes that record
// the deadline their context carries.
func deadlineSpy(t *testing.T) (*Registry, func(string) (time.Time, bool)) {
	t.Helper()
	r := continuationRegistry(t, &recorder{})
	var mu sync.Mutex
	seen := map[string]time.Time{}
	has := map[string]bool{}
	for typ, out := range map[string]Payload{"process": Envelope{}, "dest": Terminal{}} {
		s := r.specs[typ]
		s.New = func(nc NodeConfig) (Node, error) {
			id := nc.ID
			return &fnNode{out: out, fn: func(ctx context.Context) {
				dl, ok := ctx.Deadline()
				mu.Lock()
				seen[id], has[id] = dl, ok
				mu.Unlock()
			}}, nil
		}
		r.specs[typ] = s
	}
	return r, func(id string) (time.Time, bool) {
		mu.Lock()
		defer mu.Unlock()
		return seen[id], has[id]
	}
}

// A resumed chain runs on one budget, as a success chain does inside Run.
func TestRunFromSharesOneBudgetAcrossTheResumedChain(t *testing.T) {
	r, deadline := deadlineSpy(t)
	fc := flowConfig(nodesWith([2]string{"a", "dest"}, [2]string{"p", "process"}, [2]string{"b", "dest"}), edgesWith("env>a", "a>p", "p>b")...)
	f := mustCompile(t, fc, r)
	if _, err := f.RunFrom(context.Background(), "p", Envelope{}, RunOptions{PublishTimeout: time.Minute}); err != nil {
		t.Fatalf("RunFrom() = %v", err)
	}
	pd, pok := deadline("p")
	bd, bok := deadline("b")
	if !pok || !bok || !pd.Equal(bd) {
		t.Fatalf("p deadline %v (set=%v), b deadline %v (set=%v): the resumed chain must share one budget", pd, pok, bd, bok)
	}
}

// The caller's deadline bounds the whole resumed run, so a redrive can never
// outlive the time the caller is prepared to wait.
func TestRunFromCapsTheBudgetAtTheCallersDeadline(t *testing.T) {
	r, deadline := deadlineSpy(t)
	f := mustCompile(t, flowConfig(nodesWith([2]string{"a", "dest"}), edgesWith("env>a")...), r)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	callerDeadline, _ := ctx.Deadline()
	if _, err := f.RunFrom(ctx, "a", Envelope{}, RunOptions{PublishTimeout: time.Minute}); err != nil {
		t.Fatalf("RunFrom() = %v", err)
	}
	ad, ok := deadline("a")
	if !ok || ad.After(callerDeadline) {
		t.Fatalf("a deadline %v (set=%v), caller deadline %v: the caller's deadline must bound the run", ad, ok, callerDeadline)
	}
}

func TestRunFromRejectsAnEndedContext(t *testing.T) {
	rec := &recorder{}
	f := mustCompile(t, flowConfig(nodesWith([2]string{"a", "dest"}), edgesWith("env>a")...), failureRegistry(t, rec))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := f.RunFrom(ctx, "a", Envelope{}, runFromOpts(nil)); !errors.Is(err, context.Canceled) {
		t.Fatalf("RunFrom() = %v, want context.Canceled", err)
	}
	if got := rec.entries(); len(got) != 0 {
		t.Fatalf("ran %v on an ended context", got)
	}
}

// A redrive resumes only at nodes after the envelope exists: resuming at a
// gate would rerun replay_guard, rate limits, or auth.
func TestRunFromRefusesPreEnvelopeNodes(t *testing.T) {
	rec := &recorder{}
	fc := flowConfig(nodesWith([2]string{"p", "process"}, [2]string{"a", "dest"}), edgesWith("env>p", "p>a")...)
	f := mustCompile(t, fc, failureRegistry(t, rec))
	// Each start node gets the payload its port expects, so only the
	// Envelope rule can refuse it.
	for start, in := range map[string]Payload{"body": RequestHead{}, "auth": RawRequest{}, "env": Verified{}} {
		_, err := f.RunFrom(context.Background(), start, in, runFromOpts(nil))
		if err == nil || !strings.Contains(err.Error(), "consumes an Envelope") {
			t.Fatalf("RunFrom(%s) = %v, want a refusal", start, err)
		}
	}
	if got := rec.entries(); len(got) != 0 {
		t.Fatalf("refused calls ran nodes: %v", got)
	}
}

// Branch belongs to the switch that set it during the original run; a
// pass-through node would otherwise route the resumed payload by it.
func TestRunFromIgnoresTheSeedsBranch(t *testing.T) {
	rec := &recorder{}
	reg := failureRegistry(t, rec)
	// pass hands its input on unchanged, so a stale Branch would route by it.
	if err := reg.Register(NodeSpec{Type: "pass", In: PortEnvelope, Out: PortEnvelope,
		New: func(nc NodeConfig) (Node, error) { return rec.node(nc.ID), nil }}); err != nil {
		t.Fatal(err)
	}
	fc := flowConfig(nodesWith([2]string{"p", "pass"}, [2]string{"a", "dest"}), edgesWith("env>p", "p>a")...)
	f := mustCompile(t, fc, reg)
	if _, err := f.RunFrom(context.Background(), "p", Envelope{Branch: "left"}, runFromOpts(nil)); err != nil {
		t.Fatalf("RunFrom with a stale Branch = %v", err)
	}
	if got := strings.Join(rec.entries(), ","); got != "p,a" {
		t.Fatalf("ran %q, want p,a", got)
	}
}
