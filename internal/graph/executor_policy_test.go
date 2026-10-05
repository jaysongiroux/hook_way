package graph

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
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
	if NeedsCompensation(term, err) {
		t.Fatal("partial delivery must retain the guard after accepted or uncertain effects")
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

// TestSkipFailedRunsDestinationsConcurrently: with skip_failed no destination
// gates another, so four 100ms publishes must take ~100ms, not ~400ms.
// Serialising them makes request latency the SUM of destination latencies,
// which is what docs/reference/configuration.md promises it is not.
func TestSkipFailedRunsDestinationsConcurrently(t *testing.T) {
	const dests = 4
	started := make(chan struct{}, dests)
	release := make(chan struct{})
	var mu sync.Mutex
	inFlight, peak := 0, 0
	r := registryWithRecorder(t, &recorder{})
	s := r.specs["dest"]
	s.New = func(NodeConfig) (Node, error) {
		return &fnNode{out: Terminal{}, fn: func(context.Context) {
			mu.Lock()
			inFlight++
			if inFlight > peak {
				peak = inFlight
			}
			mu.Unlock()
			started <- struct{}{}
			<-release
			mu.Lock()
			inFlight--
			mu.Unlock()
		}}, nil
	}
	r.specs["dest"] = s

	nodes := [][2]string{{"in", "trigger"}, {"body", "read_body"}, {"auth", "auth"}, {"env", "envelope"}}
	edges := []string{"in>body", "body>auth", "auth>env"}
	for i := range dests {
		id := fmt.Sprintf("d%d", i)
		nodes = append(nodes, [2]string{id, "dest"})
		edges = append(edges, "env>"+id)
	}
	fc := flowConfig(nodes, edges...)
	fc.DeliveryFailurePolicy = "skip_failed"
	f := mustCompile(t, fc, r)

	var term Terminal
	var runErr error
	done := make(chan struct{})
	go func() {
		defer close(done)
		term, runErr = f.Run(context.Background(), RequestHead{}, RunOptions{
			PublishTimeout: 5 * time.Second, FanoutConcurrency: dests,
		})
	}()
	for i := 0; i < dests; i++ {
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatalf("destination %d did not start; serial execution suspected", i+1)
		}
	}
	mu.Lock()
	gotPeak := peak
	mu.Unlock()
	close(release)
	<-done
	if runErr != nil {
		t.Fatalf("Run() = %v", runErr)
	}
	if len(term.Deliveries) != dests {
		t.Fatalf("Deliveries = %d, want %d", len(term.Deliveries), dests)
	}
	if gotPeak != dests {
		t.Fatalf("peak concurrency = %d, want %d concurrent destinations", gotPeak, dests)
	}
}

// TestSkipFailedHonorsConcurrencyLimit asserts the bound is real: with a limit
// of 1, four 50ms publishes must serialise. An unbounded pool would let a
// fan-out to many destinations open an unbounded number of broker connections.
func TestSkipFailedHonorsConcurrencyLimit(t *testing.T) {
	const dests = 4
	first := make(chan struct{})
	extra := make(chan struct{}, dests)
	release := make(chan struct{})
	var entered atomic.Int32
	r := registryWithRecorder(t, &recorder{})
	s := r.specs["dest"]
	s.New = func(NodeConfig) (Node, error) {
		return &fnNode{out: Terminal{}, fn: func(context.Context) {
			if entered.Add(1) == 1 {
				close(first)
			} else {
				extra <- struct{}{}
			}
			<-release
		}}, nil
	}
	r.specs["dest"] = s

	nodes := [][2]string{{"in", "trigger"}, {"body", "read_body"}, {"auth", "auth"}, {"env", "envelope"}}
	edges := []string{"in>body", "body>auth", "auth>env"}
	for i := range dests {
		id := fmt.Sprintf("d%d", i)
		nodes = append(nodes, [2]string{id, "dest"})
		edges = append(edges, "env>"+id)
	}
	fc := flowConfig(nodes, edges...)
	fc.DeliveryFailurePolicy = "skip_failed"
	f := mustCompile(t, fc, r)

	done := make(chan error, 1)
	go func() {
		_, err := f.Run(context.Background(), RequestHead{}, RunOptions{
			PublishTimeout: 5 * time.Second, FanoutConcurrency: 1,
		})
		done <- err
	}()
	select {
	case <-first:
	case <-time.After(2 * time.Second):
		t.Fatal("first destination did not start")
	}
	for i := 0; i < 1000; i++ {
		select {
		case <-extra:
			t.Fatal("peak concurrency exceeded 1 with FanoutConcurrency: 1")
		default:
			runtime.Gosched()
		}
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("Run() = %v", err)
	}
}

// TestSkipFailedPrefersRealErrorOverContextError: when one
// destination is cancelled and another fails for a real reason, the reported
// error must be the real one or the root cause is lost from logs and replay.
func TestSkipFailedPrefersRealErrorOverContextError(t *testing.T) {
	realErr := errors.New("broker down")
	r := registryWithRecorder(t, &recorder{})
	s := r.specs["dest"]
	s.New = func(nc NodeConfig) (Node, error) {
		err := realErr
		if nc.ID == "a" {
			err = context.Canceled
		}
		return &errNode{err: err}, nil
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
		t.Fatal("all destinations failed; want an error")
	}
	if !errors.Is(err, realErr) {
		t.Fatalf("reported %v; want the real cause %v, not a context error", err, realErr)
	}
}

type errNode struct{ err error }

func (n *errNode) Run(context.Context, Payload) (Payload, error) { return nil, n.err }
