package graph

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// continuationRegistry is like registryWithRecorder but dest is a
// SuccessContinuation publish (Out=Envelope at compile time, Terminal at run).
func continuationRegistry(t *testing.T, rec *recorder) *Registry {
	t.Helper()
	r := NewRegistry()
	add := func(name string, in, out PortType, detached, cont bool) {
		spec := NodeSpec{
			Type: name, In: in, Out: out, Detached: detached, SuccessContinuation: cont,
			New: func(nc NodeConfig) (Node, error) {
				n := rec.node(nc.ID)
				if cont {
					n.out = Terminal{}
				} else {
					n.out = zeroPayload(out)
				}
				return n, nil
			},
		}
		if err := r.Register(spec); err != nil {
			t.Fatalf("Register(%s) = %v", name, err)
		}
	}
	add("trigger", PortNone, PortRequestHead, false, false)
	add("read_body", PortRequestHead, PortRawRequest, false, false)
	add("auth", PortRawRequest, PortVerified, false, false)
	add("envelope", PortVerified, PortEnvelope, false, false)
	add("process", PortEnvelope, PortEnvelope, false, false)
	add("dest", PortEnvelope, PortEnvelope, true, true)
	return r
}

func TestSuccessContinuationRunsSecondAfterFirst(t *testing.T) {
	rec := &recorder{}
	r := continuationRegistry(t, rec)
	var firstDone atomic.Bool
	var secondSawFirst atomic.Bool
	s := r.specs["dest"]
	s.New = func(nc NodeConfig) (Node, error) {
		n := rec.node(nc.ID)
		n.out = Terminal{}
		id := nc.ID
		orig := n
		return &fnNode{
			out: Terminal{},
			fn: func(context.Context) {
				orig.mu.Lock()
				*orig.log = append(*orig.log, id)
				orig.mu.Unlock()
				if id == "a" {
					time.Sleep(40 * time.Millisecond)
					firstDone.Store(true)
				}
				if id == "b" {
					secondSawFirst.Store(firstDone.Load())
				}
			},
		}, nil
	}
	r.specs["dest"] = s

	fc := flowConfig([][2]string{
		{"in", "trigger"}, {"body", "read_body"}, {"auth", "auth"}, {"env", "envelope"},
		{"a", "dest"}, {"b", "dest"},
	}, "in>body", "body>auth", "auth>env", "env>a", "a>b")
	f := mustCompile(t, fc, r)
	term, err := f.Run(context.Background(), RequestHead{}, RunOptions{PublishTimeout: time.Second})
	if err != nil {
		t.Fatalf("Run() = %v", err)
	}
	got := strings.Join(rec.entries(), ",")
	if want := "in,body,auth,env,a,b"; got != want {
		t.Fatalf("execution order = %q, want %q", got, want)
	}
	if !secondSawFirst.Load() {
		t.Fatal("second publish ran before the first returned")
	}
	if len(term.Deliveries) != 2 {
		t.Fatalf("Deliveries = %+v, want 2", term.Deliveries)
	}
	if term.Deliveries[0].NodeID != "a" || term.Deliveries[1].NodeID != "b" {
		t.Fatalf("delivery order = %+v, want a then b", term.Deliveries)
	}
}

func TestSuccessContinuationSkippedOnFirstFailure(t *testing.T) {
	rec := &recorder{}
	r := continuationRegistry(t, rec)
	fail := errors.New("first publish failed")
	s := r.specs["dest"]
	s.New = func(nc NodeConfig) (Node, error) {
		n := rec.node(nc.ID)
		n.out = Terminal{}
		if nc.ID == "a" {
			n.err = fail
		}
		return n, nil
	}
	r.specs["dest"] = s
	fc := flowConfig([][2]string{
		{"in", "trigger"}, {"body", "read_body"}, {"auth", "auth"}, {"env", "envelope"},
		{"a", "dest"}, {"b", "dest"}, {"fb", "dest"},
	}, "in>body", "body>auth", "auth>env", "env>a", "a>b", "a:on_error>fb")
	f := mustCompile(t, fc, r)
	_, err := f.Run(context.Background(), RequestHead{}, RunOptions{PublishTimeout: time.Second})
	if err != nil {
		t.Fatalf("Run() = %v, want nil (on_error handled)", err)
	}
	entries := rec.entries()
	if containsID(entries, "b") {
		t.Fatalf("success edge ran after failure: %v", entries)
	}
	if !containsID(entries, "fb") {
		t.Fatalf("on_error did not run: %v", entries)
	}
}

func TestSuccessContinuationSecondFailureKeepsFirstDelivery(t *testing.T) {
	rec := &recorder{}
	r := continuationRegistry(t, rec)
	fail := errors.New("second failed")
	s := r.specs["dest"]
	s.New = func(nc NodeConfig) (Node, error) {
		n := rec.node(nc.ID)
		n.out = Terminal{}
		if nc.ID == "b" {
			n.err = fail
		}
		return n, nil
	}
	r.specs["dest"] = s

	fc := flowConfig([][2]string{
		{"in", "trigger"}, {"body", "read_body"}, {"auth", "auth"}, {"env", "envelope"},
		{"a", "dest"}, {"b", "dest"},
	}, "in>body", "body>auth", "auth>env", "env>a", "a>b")
	f := mustCompile(t, fc, r)
	term, err := f.Run(context.Background(), RequestHead{}, RunOptions{PublishTimeout: time.Second})
	if err == nil {
		t.Fatal("Run() = nil, want second publish error")
	}
	if len(term.Deliveries) < 1 || term.Deliveries[0].NodeID != "a" || term.Deliveries[0].Err != nil {
		t.Fatalf("first delivery = %+v, want successful a", term.Deliveries)
	}
	var sawB bool
	for _, d := range term.Deliveries {
		if d.NodeID == "b" {
			sawB = true
			if d.Err == nil {
				t.Fatal("b delivery missing error")
			}
		}
	}
	if !sawB {
		t.Fatalf("Deliveries = %+v, want b recorded", term.Deliveries)
	}
}

func TestSuccessContinuationSharesPublishBudget(t *testing.T) {
	var deadlines []time.Time
	var sawCancel []bool
	var mu sync.Mutex
	reg := NewRegistry()
	add := func(name string, in, out PortType, detached, cont bool) {
		spec := NodeSpec{
			Type: name, In: in, Out: out, Detached: detached, SuccessContinuation: cont,
			New: func(nc NodeConfig) (Node, error) {
				id := nc.ID
				return &fnNode{
					out: func() Payload {
						if cont {
							return Terminal{}
						}
						return zeroPayload(out)
					}(),
					fn: func(ctx context.Context) {
						if !detached {
							return
						}
						d, _ := ctx.Deadline()
						mu.Lock()
						deadlines = append(deadlines, d)
						sawCancel = append(sawCancel, ctx.Err() != nil)
						_ = id
						mu.Unlock()
					},
				}, nil
			},
		}
		if err := reg.Register(spec); err != nil {
			t.Fatalf("Register(%s) = %v", name, err)
		}
	}
	add("trigger", PortNone, PortRequestHead, false, false)
	add("read_body", PortRequestHead, PortRawRequest, false, false)
	add("auth", PortRawRequest, PortVerified, false, false)
	add("envelope", PortVerified, PortEnvelope, false, false)
	add("dest", PortEnvelope, PortEnvelope, true, true)

	fc := flowConfig([][2]string{
		{"in", "trigger"}, {"body", "read_body"}, {"auth", "auth"}, {"env", "envelope"},
		{"a", "dest"}, {"b", "dest"},
	}, "in>body", "body>auth", "auth>env", "env>a", "a>b")
	f := mustCompile(t, fc, reg)

	req, cancel := context.WithCancel(context.Background())
	cancel() // request already cancelled; chain must still run detached
	_, err := f.Run(req, RequestHead{}, RunOptions{PublishTimeout: 500 * time.Millisecond})
	if err != nil {
		t.Fatalf("Run() = %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(deadlines) != 2 {
		t.Fatalf("dest observations = %d, want 2", len(deadlines))
	}
	if !deadlines[0].Equal(deadlines[1]) {
		t.Fatalf("deadlines differ: %v vs %v; chain must share one publish_timeout", deadlines[0], deadlines[1])
	}
	for i, c := range sawCancel {
		if c {
			t.Errorf("publish %d saw cancelled context; I10a requires detached budget", i)
		}
	}
}

func TestSuccessContinuationRootTimeoutStillRunsOnError(t *testing.T) {
	rec := &recorder{}
	r := continuationRegistry(t, rec)
	s := r.specs["dest"]
	s.New = func(nc NodeConfig) (Node, error) {
		if nc.ID == "a" {
			return timeoutDest{id: "a", mu: &rec.mu, log: &rec.log}, nil
		}
		n := rec.node(nc.ID)
		n.out = Terminal{}
		return n, nil
	}
	r.specs["dest"] = s

	fc := flowConfig([][2]string{
		{"in", "trigger"}, {"body", "read_body"}, {"auth", "auth"}, {"env", "envelope"},
		{"a", "dest"}, {"fb", "dest"},
	}, "in>body", "body>auth", "auth>env", "env>a", "a:on_error>fb")
	f := mustCompile(t, fc, r)
	_, err := f.Run(context.Background(), RequestHead{}, RunOptions{PublishTimeout: 30 * time.Millisecond})
	if err != nil {
		t.Fatalf("Run() = %v, want nil after on_error", err)
	}
	if !containsID(rec.entries(), "fb") {
		t.Fatalf("root timeout did not reach on_error on request context: %v", rec.entries())
	}
}

type timeoutDest struct {
	id  string
	mu  *sync.Mutex
	log *[]string
}

func (n timeoutDest) Run(ctx context.Context, _ Payload) (Payload, error) {
	n.mu.Lock()
	*n.log = append(*n.log, n.id)
	n.mu.Unlock()
	<-ctx.Done()
	return Terminal{}, ctx.Err()
}

func TestSkipFailedSiblingChainStaysOrdered(t *testing.T) {
	rec := &recorder{}
	r := continuationRegistry(t, rec)
	var aDone atomic.Bool
	var a2Waited atomic.Bool
	s := r.specs["dest"]
	s.New = func(nc NodeConfig) (Node, error) {
		id := nc.ID
		n := rec.node(id)
		n.out = Terminal{}
		return &fnNode{
			out: Terminal{},
			fn: func(context.Context) {
				n.mu.Lock()
				*n.log = append(*n.log, id)
				n.mu.Unlock()
				switch id {
				case "a":
					time.Sleep(50 * time.Millisecond)
					aDone.Store(true)
				case "a2":
					a2Waited.Store(aDone.Load())
				case "b":
					// sibling should be able to run without waiting for a2
				}
			},
		}, nil
	}
	r.specs["dest"] = s

	fc := flowConfig([][2]string{
		{"in", "trigger"}, {"body", "read_body"}, {"auth", "auth"}, {"env", "envelope"},
		{"a", "dest"}, {"a2", "dest"}, {"b", "dest"},
	}, "in>body", "body>auth", "auth>env", "env>a", "env>b", "a>a2")
	fc.DeliveryFailurePolicy = "skip_failed"
	f := mustCompile(t, fc, r)
	start := time.Now()
	_, err := f.Run(context.Background(), RequestHead{}, RunOptions{
		PublishTimeout:    time.Second,
		FanoutConcurrency: 4,
	})
	if err != nil {
		t.Fatalf("Run() = %v", err)
	}
	elapsed := time.Since(start)
	if !a2Waited.Load() {
		t.Fatal("a2 ran before a returned; continuation must stay ordered")
	}
	// a and b are siblings under skip_failed: b should overlap a's sleep, so
	// wall time stays near a+a2 (~100ms) rather than a+b+a2 serial.
	if elapsed > 250*time.Millisecond {
		t.Fatalf("elapsed %s; sibling publishes under skip_failed should overlap", elapsed)
	}
	entries := rec.entries()
	if !containsID(entries, "b") || !containsID(entries, "a2") {
		t.Fatalf("missing nodes in %v", entries)
	}
}
