package graph

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"
)

var errFail = errors.New("boom")

// failureRegistry is continuationRegistry plus a "stamp" node that sets
// Vars["stage"] to its own id, and it makes every process or dest node whose
// id is listed in fail return errFail.
func failureRegistry(t *testing.T, rec *recorder, fail ...string) *Registry {
	t.Helper()
	r := continuationRegistry(t, rec)
	for _, typ := range []string{"process", "dest"} {
		s := r.specs[typ]
		orig := s.New
		s.New = func(nc NodeConfig) (Node, error) {
			n, err := orig(nc)
			if err != nil {
				return nil, err
			}
			if slices.Contains(fail, nc.ID) {
				n.(*recordingNode).err = errFail
			}
			return n, nil
		}
		r.specs[typ] = s
	}
	if err := r.Register(NodeSpec{
		Type: "stamp", In: PortEnvelope, Out: PortEnvelope,
		New: func(nc NodeConfig) (Node, error) {
			n := rec.node(nc.ID)
			n.out = Envelope{Vars: map[string]string{"stage": nc.ID}}
			return n, nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	return r
}

// failureCollector records FailureEvents; OnFailure may run concurrently.
type failureCollector struct {
	mu     sync.Mutex
	events []FailureEvent
}

func (c *failureCollector) record(ev FailureEvent) {
	c.mu.Lock()
	c.events = append(c.events, ev)
	c.mu.Unlock()
}

func (c *failureCollector) nodeIDs() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	ids := make([]string, 0, len(c.events))
	for _, ev := range c.events {
		ids = append(ids, ev.NodeID)
	}
	slices.Sort(ids)
	return ids
}

var headNodes = [][2]string{{"in", "trigger"}, {"body", "read_body"}, {"auth", "auth"}, {"env", "envelope"}}
var headEdges = []string{"in>body", "body>auth", "auth>env"}

func nodesWith(extra ...[2]string) [][2]string { return append(slices.Clone(headNodes), extra...) }
func edgesWith(extra ...string) []string       { return append(slices.Clone(headEdges), extra...) }

func TestOnFailureFiresOnceAtTheOriginatingNode(t *testing.T) {
	rec := &recorder{}
	fc := flowConfig(nodesWith([2]string{"st", "stamp"}, [2]string{"a", "dest"}), edgesWith("env>st", "st>a")...)
	f := mustCompile(t, fc, failureRegistry(t, rec, "a"))
	var c failureCollector
	_, err := f.Run(context.Background(), RequestHead{}, RunOptions{PublishTimeout: time.Second, OnFailure: c.record})
	if !errors.Is(err, errFail) {
		t.Fatalf("Run() = %v, want errFail", err)
	}
	if got := c.nodeIDs(); !slices.Equal(got, []string{"a"}) {
		t.Fatalf("OnFailure nodes = %v, want [a] (never ancestors)", got)
	}
	ev := c.events[0]
	if ev.NodeType != "dest" {
		t.Fatalf("NodeType = %q, want dest", ev.NodeType)
	}
	env, ok := ev.Input.(Envelope)
	if !ok || env.Vars["stage"] != "st" {
		t.Fatalf("Input = %#v, want the Envelope stamp produced", ev.Input)
	}
	var ne NodeError
	if !errors.As(ev.Err, &ne) || ne.Status != 503 || !errors.Is(ev.Err, errFail) {
		t.Fatalf("Err = %v, want a 503 NodeError wrapping errFail", ev.Err)
	}
}

func TestOnFailureDoesNotFireOnSuccess(t *testing.T) {
	rec := &recorder{}
	fc := flowConfig(nodesWith([2]string{"a", "dest"}), edgesWith("env>a")...)
	f := mustCompile(t, fc, failureRegistry(t, rec))
	var c failureCollector
	if _, err := f.Run(context.Background(), RequestHead{}, RunOptions{PublishTimeout: time.Second, OnFailure: c.record}); err != nil {
		t.Fatalf("Run() = %v", err)
	}
	if got := c.nodeIDs(); len(got) != 0 {
		t.Fatalf("OnFailure nodes = %v, want none", got)
	}
}

func TestOnFailureSkipsFailuresHandledByOnError(t *testing.T) {
	rec := &recorder{}
	fc := flowConfig(nodesWith([2]string{"a", "dest"}, [2]string{"fb", "dest"}), edgesWith("env>a", "a:on_error>fb")...)
	f := mustCompile(t, fc, failureRegistry(t, rec, "a"))
	var c failureCollector
	if _, err := f.Run(context.Background(), RequestHead{}, RunOptions{PublishTimeout: time.Second, OnFailure: c.record}); err != nil {
		t.Fatalf("Run() = %v, want nil (on_error handled the failure)", err)
	}
	if got := c.nodeIDs(); len(got) != 0 {
		t.Fatalf("OnFailure nodes = %v, want none for a handled failure", got)
	}
}

func TestOnFailureFiresForAFailingErrorPath(t *testing.T) {
	rec := &recorder{}
	fc := flowConfig(nodesWith([2]string{"a", "dest"}, [2]string{"fb", "dest"}), edgesWith("env>a", "a:on_error>fb")...)
	f := mustCompile(t, fc, failureRegistry(t, rec, "a", "fb"))
	var c failureCollector
	if _, err := f.Run(context.Background(), RequestHead{}, RunOptions{PublishTimeout: time.Second, OnFailure: c.record}); err == nil {
		t.Fatal("Run() = nil, want the error path's failure")
	}
	if got := c.nodeIDs(); !slices.Equal(got, []string{"fb"}) {
		t.Fatalf("OnFailure nodes = %v, want [fb] only", got)
	}
}

func TestOnFailureFiresPerFailingDestinationUnderSkipFailed(t *testing.T) {
	rec := &recorder{}
	fc := flowConfig(
		nodesWith([2]string{"d0", "dest"}, [2]string{"d1", "dest"}, [2]string{"d2", "dest"}, [2]string{"d3", "dest"}),
		edgesWith("env>d0", "env>d1", "env>d2", "env>d3")...)
	fc.DeliveryFailurePolicy = "skip_failed"
	f := mustCompile(t, fc, failureRegistry(t, rec, "d1", "d3"))
	var c failureCollector
	_, err := f.Run(context.Background(), RequestHead{}, RunOptions{
		PublishTimeout: time.Second, FanoutConcurrency: 4, OnFailure: c.record,
	})
	if err != nil {
		t.Fatalf("Run() = %v, want nil (partial success)", err)
	}
	if got := c.nodeIDs(); !slices.Equal(got, []string{"d1", "d3"}) {
		t.Fatalf("OnFailure nodes = %v, want [d1 d3]", got)
	}
}

func TestOnFailureInsideSuccessContinuation(t *testing.T) {
	rec := &recorder{}
	// b is the leaf the compiler requires after p (a leaf must be terminal or a
	// SuccessContinuation); p fails first, so b never runs.
	fc := flowConfig(nodesWith([2]string{"st", "stamp"}, [2]string{"a", "dest"}, [2]string{"p", "process"}, [2]string{"b", "dest"}),
		edgesWith("env>st", "st>a", "a>p", "p>b")...)
	f := mustCompile(t, fc, failureRegistry(t, rec, "p"))
	var c failureCollector
	if _, err := f.Run(context.Background(), RequestHead{}, RunOptions{PublishTimeout: time.Second, OnFailure: c.record}); err == nil {
		t.Fatal("Run() = nil, want the continuation failure")
	}
	if got := c.nodeIDs(); !slices.Equal(got, []string{"p"}) {
		t.Fatalf("OnFailure nodes = %v, want [p]", got)
	}
	if env, ok := c.events[0].Input.(Envelope); !ok || env.Vars["stage"] != "st" {
		t.Fatalf("Input = %#v, want the envelope a published (continuations carry the publish input)", c.events[0].Input)
	}
}

func TestOnFailureObserverPanicDoesNotChangeTheResult(t *testing.T) {
	rec := &recorder{}
	fc := flowConfig(nodesWith([2]string{"a", "dest"}), edgesWith("env>a")...)
	f := mustCompile(t, fc, failureRegistry(t, rec, "a"))
	term, err := f.Run(context.Background(), RequestHead{}, RunOptions{
		PublishTimeout: time.Second,
		OnFailure:      func(FailureEvent) { panic("observer") },
	})
	if !errors.Is(err, errFail) {
		t.Fatalf("Run() = %v, want errFail despite the observer panic", err)
	}
	if len(term.Deliveries) != 1 || term.Deliveries[0].NodeID != "a" || term.Deliveries[0].Err == nil {
		t.Fatalf("Deliveries = %+v, want one failed delivery for a", term.Deliveries)
	}
}
