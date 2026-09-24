package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jaysongiroux/hook_way/internal/capture"
	"github.com/jaysongiroux/hook_way/internal/config"
	"github.com/jaysongiroux/hook_way/internal/control"
	"github.com/jaysongiroux/hook_way/internal/deliverysnapshot"
	"github.com/jaysongiroux/hook_way/internal/flowconfig"
	"github.com/jaysongiroux/hook_way/internal/graph"
	"github.com/jaysongiroux/hook_way/internal/metrics"
	"github.com/jaysongiroux/hook_way/internal/replaywire"
)

const testControlToken = "control-token-0123456789abcdefghij"

func TestNewControlServerFollowsTheReplayBlock(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	if s := newControlServer(flowconfig.Config{}, nil, metrics.New(), logger); s != nil {
		t.Fatal("a control listener without the replay block")
	}
	cfg := flowconfig.Config{Replay: config.ReplayConfig{Configured: true, Control: config.ReplayControlConfig{
		Addr: "127.0.0.1:9092", Token: testControlToken, MaxConcurrent: 2}}}
	s := newControlServer(cfg, nil, metrics.New(), logger)
	if s == nil || s.HTTPServer().Addr != "127.0.0.1:9092" {
		t.Fatalf("control server = %+v", s)
	}
}

type blockingRedriver struct{ entered, release chan struct{} }

func (b blockingRedriver) Redrive(_ context.Context, req replaywire.DeliverRequest, _ deliverysnapshot.Snapshot) (control.Result, error) {
	b.entered <- struct{}{}
	<-b.release
	return control.Result{Nodes: []replaywire.NodeOutcome{{NodeID: req.NodeID, Outcome: replaywire.NodeDelivered, Attempts: 1}}}, nil
}

type flagCloser struct{ closed atomic.Bool }

func (c *flagCloser) Close() error {
	c.closed.Store(true)
	return nil
}

func deliverBody(t *testing.T) ([]byte, string) {
	t.Helper()
	dto, err := capture.SnapshotFromEnvelope(graph.Envelope{Body: map[string]any{"a": 1.0}, BodyFormat: "json", Vars: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	snap, err := deliverysnapshot.Encode(deliverysnapshot.Snapshot{Kind: deliverysnapshot.KindResume, Envelope: &dto})
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(snap)
	req, err := json.Marshal(replaywire.DeliverRequest{RunID: "run-1", FailureID: "fail-1", Kind: replaywire.KindResume,
		Flow: "orders", NodeID: "out", NodeType: "publish", SnapshotSHA256: hex.EncodeToString(sum[:]),
		SnapshotVersion: deliverysnapshot.Version, DeadlineMS: 5000, InboundStatus: 202})
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for _, p := range []struct {
		name string
		data []byte
	}{{replaywire.PartRequest, req}, {replaywire.PartSnapshot, snap}} {
		pw, _ := w.CreateFormField(p.name)
		_, _ = pw.Write(p.data)
	}
	_ = w.Close()
	return buf.Bytes(), w.FormDataContentType()
}

// startRedrive posts one deliver request to the control listener at addr and
// reports its status on the returned channel (0 on a transport error).
func startRedrive(t *testing.T, addr string) chan int {
	t.Helper()
	body, contentType := deliverBody(t)
	status := make(chan int, 1)
	go func() {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, "http://"+addr+replaywire.DeliverPath, bytes.NewReader(body))
		req.Header.Set("Content-Type", contentType)
		req.Header.Set("Authorization", "Bearer "+testControlToken)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			status <- 0
			return
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		status <- resp.StatusCode
	}()
	return status
}

func serveControl(t *testing.T, r control.Redriver) (*control.Server, string) {
	t.Helper()
	ctl := control.New(control.Options{Token: testControlToken, MaxRecordBytes: 1 << 20, Redriver: r, Logger: slog.New(slog.DiscardHandler)})
	l, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = ctl.HTTPServer().Serve(l) }()
	return ctl, l.Addr().String()
}

// Spec "Control API → Shutdown": in-flight redrives finish before the
// publishers close — even when they outlast server.shutdown_timeout.
func TestControlServerDrainsFirst(t *testing.T) {
	r := blockingRedriver{entered: make(chan struct{}, 1), release: make(chan struct{})}
	ctl, addr := serveControl(t, r)
	status := startRedrive(t, addr)
	<-r.entered
	pub := &flagCloser{}
	res := servingResources{logger: slog.New(slog.DiscardHandler), servers: []shutdowner{ctl}, closers: []io.Closer{pub}}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond) // shorter than the redrive
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- res.drain(ctx) }()
	time.Sleep(300 * time.Millisecond)
	if pub.closed.Load() {
		t.Fatal("publishers closed while a redrive was still running")
	}
	close(r.release)
	if code := <-status; code != http.StatusOK {
		t.Fatalf("the in-flight redrive answered %d", code)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !pub.closed.Load() {
		t.Fatal("publishers never closed")
	}
}

// Every listener stops accepting at once: the webhook listener does not keep
// taking requests while the control listener waits for a redrive.
func TestDrainStopsEveryListenerAtOnce(t *testing.T) {
	r := blockingRedriver{entered: make(chan struct{}, 1), release: make(chan struct{})}
	ctl, addr := serveControl(t, r)
	status := startRedrive(t, addr)
	<-r.entered
	webLn := mustListen(t)
	web := &http.Server{Handler: http.NewServeMux(), ReadHeaderTimeout: time.Second}
	go func() { _ = web.Serve(webLn) }()
	waitAccepting(t, webLn.Addr().String())
	res := servingResources{logger: slog.New(slog.DiscardHandler), servers: []shutdowner{ctl, web}}
	done := make(chan error, 1)
	go func() { done <- res.drain(context.Background()) }()
	deadline := time.Now().Add(time.Second)
	for {
		conn, err := (&net.Dialer{Timeout: 50 * time.Millisecond}).DialContext(context.Background(), "tcp", webLn.Addr().String())
		if err != nil {
			break
		}
		_ = conn.Close()
		if time.Now().After(deadline) {
			t.Fatal("the webhook listener kept accepting while the control listener drained")
		}
		time.Sleep(10 * time.Millisecond)
	}
	close(r.release)
	<-status
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
