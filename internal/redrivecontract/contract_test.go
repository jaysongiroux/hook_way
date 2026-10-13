package redrivecontract

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jaysongiroux/hook_way/internal/capture"
	"github.com/jaysongiroux/hook_way/internal/control"
	"github.com/jaysongiroux/hook_way/internal/deliverysnapshot"
	"github.com/jaysongiroux/hook_way/internal/flowconfig"
	"github.com/jaysongiroux/hook_way/internal/httpapi"
	"github.com/jaysongiroux/hook_way/internal/metrics"
	"github.com/jaysongiroux/hook_way/internal/queue"
	"github.com/jaysongiroux/hook_way/internal/replay/store"
	"github.com/jaysongiroux/hook_way/internal/replay/worker"
	"github.com/jaysongiroux/hook_way/internal/replaywire"
)

const controlToken = "control-token-0123456789abcdefghij"

const flowYAML = `
server: {addr: ":8080", max_body_bytes: 1048576}
replay:
  url: "REPLAY_URL"
  capture_token: "capture-token-0123456789abcdefghijkl"
  control: {addr: ":9092", token: "` + controlToken + `"}
destinations:
  primary: {type: webhook, webhook: {url: "https://example.com/a"}}
  broken: {type: webhook, webhook: {url: "https://example.com/b"}}
  third: {type: webhook, webhook: {url: "https://example.com/c"}}
flows:
  orders:
    delivery_failure_policy: skip_failed
    nodes:
      - {id: in, type: http_trigger, path: /hooks/orders, methods: [POST]}
      - {id: body, type: read_body, max_bytes: 65536}
      - {id: auth, type: no_auth}
      - {id: env, type: envelope}
      - {id: ok, type: publish, destination: primary, target: a}
      - {id: bad, type: publish, destination: broken, target: b}
      - {id: after, type: publish, destination: third, target: c}
    edges: [in>body, body>auth, auth>env, env>ok, env>bad, bad>after]
`

// switchPublisher fails the destinations in fail.
type switchPublisher struct {
	mu   sync.Mutex
	fail map[string]bool
	ids  []string
}

func (p *switchPublisher) Publish(_ context.Context, dest, _ string, msg queue.Message) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.fail[dest] {
		return errors.New(dest + " is down")
	}
	p.ids = append(p.ids, msg.ID)
	return nil
}
func (p *switchPublisher) Ready(context.Context) error { return nil }
func (p *switchPublisher) Close() error                { return nil }

func (p *switchPublisher) set(fail ...string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.fail, p.ids = map[string]bool{}, nil
	for _, d := range fail {
		p.fail[d] = true
	}
}

type record struct {
	meta     replaywire.RecordMetadata
	snapshot []byte
}

type sink struct {
	mu  sync.Mutex
	got []record
}

func (s *sink) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	mr, err := r.MultipartReader()
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	var rec record
	for {
		p, err := mr.NextPart()
		if err != nil {
			break
		}
		data, _ := io.ReadAll(p)
		switch p.FormName() {
		case replaywire.PartMetadata:
			_ = json.Unmarshal(data, &rec.meta)
		case replaywire.PartSnapshot:
			rec.snapshot = data
		}
	}
	s.mu.Lock()
	s.got = append(s.got, rec)
	s.mu.Unlock()
	w.WriteHeader(http.StatusCreated)
}

type env struct {
	pub      *switchPublisher
	executor *worker.Executor
	record   record
}

// setup captures one failure at node "bad" through core, then points replay's
// real executor at core's real control listener.
func setup(t *testing.T, token string, edit func(string) string) env {
	t.Helper()
	s := &sink{}
	replay := httptest.NewServer(s)
	t.Cleanup(replay.Close)
	src := strings.ReplaceAll(flowYAML, "REPLAY_URL", replay.URL)
	pub := &switchPublisher{}
	pub.set("broken")
	capturing := buildCore(t, src, pub)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/hooks/orders", bytes.NewReader([]byte(`{"id":"42"}`)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	capturing.app.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status %d", rec.Code)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		s.mu.Lock()
		n := len(s.got)
		s.mu.Unlock()
		if n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no captured record")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if edit != nil {
		src = edit(src)
	}
	redriving := buildCore(t, src, pub)
	e, err := worker.NewExecutor(worker.ExecutorOptions{ControlURL: redriving.control.URL, Token: token,
		ExecutionTimeout: 10 * time.Second, MaxRecordBytes: 16 << 20})
	if err != nil {
		t.Fatal(err)
	}
	return env{pub: pub, executor: e, record: s.got[0]}
}

type coreParts struct {
	app     *httpapi.FlowServer
	control *httptest.Server
}

func buildCore(t *testing.T, src string, pub queue.Publisher) coreParts {
	t.Helper()
	cfg, _ := flowconfig.LoadBytes([]byte(src), "contract", false)
	app, err := httpapi.NewFlowServer(cfg, pub, slog.New(slog.DiscardHandler), metrics.New())
	if err != nil {
		t.Fatal(err)
	}
	c, err := capture.New(capture.Options{Config: cfg, Metrics: metrics.New(), Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	app.SetCapturer(c)
	ctl := control.New(control.Options{Token: controlToken, MaxRecordBytes: cfg.Replay.MaxRecordBytes,
		Flows: []string{"orders"}, Redriver: app, Logger: slog.New(slog.DiscardHandler)})
	srv := httptest.NewServer(ctl.HTTPServer().Handler)
	t.Cleanup(func() {
		srv.Close()
		_ = app.Close()
		_ = c.Close()
	})
	return coreParts{app: app, control: srv}
}

func (e env) run() store.ClaimedRun {
	m := e.record.meta
	return store.ClaimedRun{
		Run:    store.Run{RunID: "01926d3c-9999-7b3a-9c1e-2f4a5b6c7d8e", RecordID: m.ID, Status: store.RunRunning},
		Record: store.Record{RecordMetadata: m, DeliveryStatus: store.StatusRetrying, RootID: m.ID},
	}
}

func TestExecutorAndControlListenerAgree(t *testing.T) {
	t.Run("the destination recovered", func(t *testing.T) {
		e := setup(t, controlToken, nil)
		e.pub.set()
		out := e.executor.Deliver(context.Background(), e.run(), e.record.snapshot)
		if out.Status != store.RunSucceeded || len(out.Children) != 0 {
			t.Fatalf("outcome = %+v", out)
		}
		if len(e.pub.ids) != 2 || e.pub.ids[0] != e.record.meta.EventID {
			t.Fatalf("delivered ids %v; want the original envelope id, then the continuation", e.pub.ids)
		}
	})
	t.Run("a downstream failure becomes a child record", func(t *testing.T) {
		e := setup(t, controlToken, nil)
		e.pub.set("third")
		out := e.executor.Deliver(context.Background(), e.run(), e.record.snapshot)
		if out.Status != store.RunSucceeded || len(out.Children) != 1 {
			t.Fatalf("outcome = %+v", out)
		}
		child := out.Children[0]
		if child.Metadata.NodeID != "after" || child.Metadata.EventID != e.record.meta.EventID || child.Snapshot == nil {
			t.Fatalf("child = %+v", child.Metadata)
		}
		if _, err := deliverysnapshot.Decode(child.Snapshot); err != nil {
			t.Fatalf("child snapshot: %v", err)
		}
	})
	t.Run("the node still fails", func(t *testing.T) {
		e := setup(t, controlToken, nil)
		e.pub.set("broken")
		out := e.executor.Deliver(context.Background(), e.run(), e.record.snapshot)
		if out.Status != store.RunFailed || out.Reason != worker.ReasonNodeFailed {
			t.Fatalf("outcome = %+v", out)
		}
	})
	t.Run("the configuration moved on", func(t *testing.T) {
		e := setup(t, controlToken, func(s string) string {
			return strings.Replace(strings.Replace(s, "{id: bad,", "{id: moved,", 1), "env>bad, bad>after", "env>moved, moved>after", 1)
		})
		out := e.executor.Deliver(context.Background(), e.run(), e.record.snapshot)
		if out.Status != store.RunBlocked || out.Reason != replaywire.BlockNodeMissing {
			t.Fatalf("outcome = %+v", out)
		}
	})
	t.Run("a wrong token", func(t *testing.T) {
		e := setup(t, strings.Repeat("x", len(controlToken)), nil)
		out := e.executor.Deliver(context.Background(), e.run(), e.record.snapshot)
		if out.Status != store.RunFailed || out.Reason != worker.ReasonExecutorAuth {
			t.Fatalf("outcome = %+v", out)
		}
	})
}
