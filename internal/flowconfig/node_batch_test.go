package flowconfig

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jaysongiroux/hook_way/internal/graph"
	"github.com/jaysongiroux/hook_way/internal/jsoncanon"
	"github.com/jaysongiroux/hook_way/internal/routing"
)

type batchPublisherFunc func(context.Context, string, string, graph.Envelope) error

func (f batchPublisherFunc) Publish(c context.Context, d, s string, e graph.Envelope) error {
	return f(c, d, s, e)
}
func batchTestOptions(mode string) BatchOptions {
	o := BatchOptions{Mode: mode, Profile: "sandbox", EnvironmentPath: "environment", EventIDPath: "event_id", MaxChildren: 10, Concurrency: 2, MaxInflight: 2, Timeout: "1s", Child: BatchChildOptions{Entry: "batch.send", Nodes: []map[string]any{{"id": "batch.send", "type": "publish", "destination": "primary", "target": "{route_prefix}-sample.fifo", "wire_body": "json", "fifo": map[string]any{"group_id": map[string]any{"source": "variable", "path": "batch_group_id"}, "deduplication_id": map[string]any{"source": "variable", "path": "batch_event_id"}}}}, Edges: []string{}}}
	if mode == "shared" {
		o.Concurrency = 1
		o.GroupIDPath = "message_id"
	} else {
		o.ReceivingEnvironment = "preview"
	}
	return o
}
func batchTestRegistry(t *testing.T) *routing.Registry {
	c := routeTestConfig()
	c.Environments["other"] = routing.Environment{Profiles: []string{"sandbox"}, Accounts: []string{"000000000000"}, DestinationPrefix: "other"}
	r, e := routing.NewRegistry(c)
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func batchTestNode(t *testing.T, o BatchOptions, p Publisher) *batchNode {
	t.Helper()
	if err := validateBatch(o); err != nil {
		t.Fatal(err)
	}
	n, err := newBatchNode("batch", o, RegistryOptions{RoutingRegistry: batchTestRegistry(t), Publishers: staticResolver{pub: p}})
	if err != nil {
		t.Fatal(err)
	}
	return n.(*batchNode)
}
func TestBatchSharedIsolationRoutingAndContinue(t *testing.T) {
	var ids []string
	var published []graph.Envelope
	p := batchPublisherFunc(func(_ context.Context, _, target string, e graph.Envelope) error {
		ids = append(ids, e.Vars["batch_event_id"])
		published = append(published, e)
		if e.V.OriginalJSON != nil {
			t.Error("child retained parent canonicalization metadata")
		}
		if strings.HasPrefix(string(e.V.Raw.Body), "[") {
			t.Error("published parent array")
		}
		if target != "preview-sample.fifo" {
			t.Error(target)
		}
		if e.Vars["batch_event_id"] == "two" {
			return errors.New("injected broker error")
		}
		// Executor owns child maps, head and bytes, even if an extension mutates them.
		e.Body.(map[string]any)["mutated"] = true
		e.V.Raw.Head.Header.Set("X-Child", "changed")
		return nil
	})
	n := batchTestNode(t, batchTestOptions("shared"), p)
	var body any
	raw := []byte(`[{"environment":"staging","event_id":"one","message_id":"group"},{"environment":"preview","event_id":"two"},{"environment":"production","event_id":"cross"},{"event_id":"missing"},{"environment":"preview","event_id":{}},{"environment":"preview","event_id":"three"}]`)
	_ = json.Unmarshal(raw, &body)
	parent := graph.Envelope{Body: body, Vars: map[string]string{"existing": "keep"}, V: graph.Verified{Raw: graph.RawRequest{Head: graph.RequestHead{Header: map[string][]string{}, RequestID: "parent"}, Body: raw}}}
	representation, err := jsoncanon.Prepare(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	parent.V.OriginalJSON = representation
	original, _ := json.Marshal(parent)
	out, err := n.Run(context.Background(), parent)
	if err != nil {
		t.Fatal(err)
	}
	term := out.(graph.Terminal)
	if string(term.Response.Body) != `{"dropped":4,"message":"Processed","primary":2}` || term.Response.Status != 200 {
		t.Fatal(string(term.Response.Body), term.Response.Status)
	}
	if !reflect.DeepEqual(ids, []string{"one", "two", "three"}) || len(term.Deliveries) != 3 {
		t.Fatal(ids, term)
	}
	if published[0].Vars["batch_group_id"] != "group" || published[2].Vars["batch_group_id"] != "three" || published[2].Vars["batch_index"] != "5" {
		t.Fatal(published)
	}
	if parent.V.OriginalJSON != representation {
		t.Fatal("batch mutated parent representation")
	}
	after, _ := json.Marshal(parent)
	if string(after) != string(original) {
		t.Fatal("parent/sibling ownership lost")
	}
	if term.Deliveries[0].Err != nil || term.Deliveries[0].Unattempted || term.Deliveries[1].Err == nil || term.Deliveries[2].Err != nil {
		t.Fatal(term.Deliveries)
	}
}
func TestBatchDedicatedBoundsCancellationAndAttemptAll(t *testing.T) {
	var active, maximum, calls atomic.Int32
	var mu sync.Mutex
	ids := []string{}
	started := make(chan struct{}, 8)
	p := batchPublisherFunc(func(ctx context.Context, _, _ string, e graph.Envelope) error {
		n := active.Add(1)
		defer active.Add(-1)
		for old := maximum.Load(); n > old; old = maximum.Load() {
			if maximum.CompareAndSwap(old, n) {
				break
			}
		}
		calls.Add(1)
		started <- struct{}{}
		mu.Lock()
		ids = append(ids, e.Vars["batch_event_id"])
		mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
		if e.Vars["batch_event_id"] == "one" {
			return errors.New("injected broker error")
		}
		return nil
	})
	n := batchTestNode(t, batchTestOptions("dedicated"), p)
	children := []any{}
	for _, id := range []string{"one", "two", "three", "four"} {
		children = append(children, map[string]any{"environment": "staging", "event_id": id})
	}
	children = append(children, map[string]any{"environment": "other", "event_id": "skip"})
	out, err := n.Run(context.Background(), graph.Envelope{Body: children})
	if err != nil {
		t.Fatal(err)
	}
	term := out.(graph.Terminal)
	if term.Response.Status != 502 || calls.Load() != 4 || maximum.Load() > 2 || len(term.Deliveries) != 4 {
		t.Fatal(term, calls.Load(), maximum.Load())
	}
	if string(term.Response.Body) != `{"dropped":1,"message":"Processed","primary":3,"skipped":1,"unattempted":0}` {
		t.Fatal(string(term.Response.Body))
	}
	// Cancellation interrupts publishers and prevents scheduling the rest. The
	// process and node semaphores must also release their capacity.
	for len(started) > 0 {
		<-started
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan graph.Payload, 1)
	go func() { o, _ := n.Run(ctx, graph.Envelope{Body: children}); done <- o }()
	<-started
	cancel()
	select {
	case result := <-done:
		var agg map[string]any
		_ = json.Unmarshal(result.(graph.Terminal).Response.Body, &agg)
		if agg["unattempted"].(float64) == 0 {
			t.Fatal(agg)
		}
	case <-time.After(time.Second):
		t.Fatal("cancel did not stop child delivery")
	}
	if len(n.slots) != 0 || len(n.fleet) != 0 {
		t.Fatal("leaked execution capacity")
	}
}
func TestBatchCompilerRejectsUnsafeChildrenAndCapture(t *testing.T) {
	o := batchTestOptions("shared")
	for _, typ := range []string{"batch", "auth", "http_trigger", "respond", "http_request", "register_route"} {
		copy := o
		copy.Child.Nodes = []map[string]any{{"id": "batch.bad", "type": typ}}
		if err := validateBatch(copy); err == nil {
			t.Fatalf("accepted child %s", typ)
		}
	}
	copy := o
	copy.Child.Entry = "missing"
	if _, err := newBatchNode("batch", copy, RegistryOptions{ValidateOnly: true}); err == nil {
		t.Fatal("missing entry accepted")
	}
	copy = o
	copy.Child.Nodes = append(copy.Child.Nodes, map[string]any{"id": "batch.orphan", "type": "transform", "kind": "strip_fields", "fields": []string{"content"}})
	if _, err := newBatchNode("batch", copy, RegistryOptions{ValidateOnly: true}); err == nil {
		t.Fatal("unreachable child accepted")
	}
	copy = o
	copy.Child.Edges = []string{"batch.send>batch.send"}
	if _, err := newBatchNode("batch", copy, RegistryOptions{ValidateOnly: true}); err == nil {
		t.Fatal("cyclic child accepted")
	}
	n := batchTestNode(t, o, batchPublisherFunc(func(context.Context, string, string, graph.Envelope) error { return nil }))
	for _, body := range []any{"bad", make([]any, 11)} {
		if _, err := n.Run(context.Background(), graph.Envelope{Body: body}); err == nil {
			t.Fatal("bad shape/count accepted")
		}
	}
	nc := nodeConfigFromYAML(t, "batch", validNodeYAML["batch"])
	fc := graph.FlowConfig{Nodes: []graph.NodeConfig{nc}}
	cfg := Config{Routing: routeTestConfig(), Flows: map[string]graph.FlowConfig{"sample": fc}}
	cfg.Replay.Configured = true
	issues := RoutingConfigurationIssues(cfg)
	if len(issues) == 0 || !strings.Contains(fmt.Sprint(issues), "capture_failures: false") {
		t.Fatal(issues)
	}
	off := false
	fc.CaptureFailures = &off
	cfg.Flows["sample"] = fc
	if issues := RoutingConfigurationIssues(cfg); len(issues) != 0 {
		t.Fatal(issues)
	}
}

func TestBatchFleetLimitAcrossConcurrentParentsAndNodes(t *testing.T) {
	fleet := make(chan struct{}, 1)
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	var active, peak atomic.Int32
	publisher := batchPublisherFunc(func(ctx context.Context, _, _ string, _ graph.Envelope) error {
		count := active.Add(1)
		defer active.Add(-1)
		for {
			old := peak.Load()
			if count <= old || peak.CompareAndSwap(old, count) {
				break
			}
		}
		entered <- struct{}{}
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	opts := RegistryOptions{RoutingRegistry: batchTestRegistry(t), Publishers: staticResolver{pub: publisher}, BatchSlots: fleet}
	first, err := newBatchNode("batch", batchTestOptions("shared"), opts)
	if err != nil {
		t.Fatal(err)
	}
	second, err := newBatchNode("batch", batchTestOptions("shared"), opts)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 2)
	for _, node := range []graph.Node{first, second} {
		go func() {
			_, err := node.Run(context.Background(), graph.Envelope{Body: map[string]any{"environment": "preview", "event_id": "one"}})
			done <- err
		}()
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("no child started")
	}
	select {
	case <-entered:
		t.Fatal("fleet limit exceeded")
	case <-time.After(25 * time.Millisecond):
	}
	close(release)
	for range 2 {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			t.Fatal("blocked batch")
		}
	}
	if peak.Load() != 1 || len(fleet) != 0 {
		t.Fatal("fleet concurrency or capacity leak", peak.Load(), len(fleet))
	}
}

func TestBatchDedicatedSkipsOtherAndUnknownEnvironmentsBeforeAdmission(t *testing.T) {
	var published int
	n := batchTestNode(t, batchTestOptions("dedicated"), batchPublisherFunc(func(context.Context, string, string, graph.Envelope) error { published++; return nil }))
	result, err := n.Run(context.Background(), graph.Envelope{Body: []any{
		map[string]any{"environment": "production", "event_id": "foreign"},
		map[string]any{"environment": "unregistered", "event_id": "unknown"},
		map[string]any{"event_id": "missing"},
		map[string]any{"environment": "staging", "event_id": "eligible"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	terminal := result.(graph.Terminal)
	var response map[string]any
	if err := json.Unmarshal(terminal.Response.Body, &response); err != nil {
		t.Fatal(err)
	}
	if terminal.Response.Status != 200 || response["skipped"] != float64(3) || response["primary"] != float64(1) || published != 1 {
		t.Fatal(terminal.Response, response, published)
	}
}
