package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// Run the real serve/signal path in an isolated process. This also works in a
// race-instrumented test binary and does not require compiling another binary.
func TestLifecycleServeProcess(t *testing.T) {
	if path := os.Getenv("HOOKWAY_LIFECYCLE_TEST_CONFIG"); path != "" {
		os.Exit(run([]string{"serve", "--config", path}, os.Stdout, os.Stderr))
	}
}

func TestProcessSIGTERMWithdrawsDrainsAndReplaces(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	var mu sync.Mutex
	var receipts []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		if strings.Contains(string(body), "before-signal") {
			close(entered)
			<-release
		}
		mu.Lock()
		receipts = append(receipts, string(body))
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()
	defer releaseOnce.Do(func() { close(release) })
	listener, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	config := fmt.Sprintf(`server:
  addr: %q
  max_body_bytes: 1048576
  shutdown_delay: 1500ms
  shutdown_timeout: 10s
  shutdown_total_timeout: 15s
  readiness_interval: 100ms
  readiness_timeout: 50ms
destinations:
  sink:
    type: webhook
    webhook:
      url: %q
      delivery_mode: sync
flows:
  ingest:
    nodes:
      - {id: in, type: http_trigger, path: /hook, methods: [POST]}
      - {id: body, type: read_body, max_bytes: 1048576}
      - {id: auth, type: no_auth}
      - {id: env, type: envelope}
      - {id: out, type: publish, destination: sink, target: test}
    edges: [in>body, body>auth, auth>env, env>out]
`, addr, upstream.URL)
	if err := os.WriteFile(configPath, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: 5 * time.Second}
	defer client.CloseIdleConnections()
	base := "http://" + addr
	getStatus := func(path string) int {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, base+path, nil)
		if err != nil {
			return 0
		}
		resp, err := client.Do(req)
		if err != nil {
			return 0
		}
		defer func() { _ = resp.Body.Close() }()
		_, _ = io.Copy(io.Discard, resp.Body)
		return resp.StatusCode
	}
	waitStatus := func(path string, want int) {
		t.Helper()
		end := time.Now().Add(8 * time.Second)
		for time.Now().Before(end) {
			if getStatus(path) == want {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("%s never returned %d", path, want)
	}
	start := func() (*exec.Cmd, <-chan error) {
		t.Helper()
		cmd := exec.CommandContext(context.Background(), exe, "-test.run=^TestLifecycleServeProcess$")
		cmd.Env = append(os.Environ(), "HOOKWAY_LIFECYCLE_TEST_CONFIG="+configPath)
		output, err := os.CreateTemp(t.TempDir(), "child-*.log")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = output.Close() })
		cmd.Stdout, cmd.Stderr = output, output
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_ = cmd.Process.Kill()
			if t.Failed() {
				data, _ := os.ReadFile(output.Name())
				t.Log(string(data))
			}
		})
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		waitStatus("/readyz", http.StatusOK)
		return cmd, done
	}
	post := func(id string) (int, error) {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, base+"/hook", bytes.NewBufferString(`{"id":"`+id+`"}`))
		if err != nil {
			return 0, err
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			return 0, err
		}
		defer func() { _ = resp.Body.Close() }()
		_, _ = io.Copy(io.Discard, resp.Body)
		return resp.StatusCode, nil
	}
	cmd, done := start()
	first := make(chan error, 1)
	go func() {
		code, err := post("before-signal")
		if err == nil && code != http.StatusAccepted {
			err = fmt.Errorf("first status %d", code)
		}
		first <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("first request never reached destination")
	}
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	waitStatus("/readyz", http.StatusServiceUnavailable)
	if getStatus("/healthz") != http.StatusOK {
		t.Fatal("liveness failed during withdrawal")
	}
	if code, err := post("during-withdrawal"); err != nil || code != http.StatusAccepted {
		t.Fatalf("withdrawal request: %d %v", code, err)
	}
	waitStatus("/healthz", 0) // listener has stopped accepting; first request still owns its publisher
	select {
	case err := <-done:
		t.Fatalf("process exited before admitted delivery finished: %v", err)
	default:
	}
	releaseOnce.Do(func() { close(release) })
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("process failed to drain")
	}
	// Replace using the same build/configuration. This proves lifecycle behavior,
	// not wire/schema compatibility between different releases.
	cmd, done = start()
	if code, err := post("after-replacement"); err != nil || code != http.StatusAccepted {
		t.Fatalf("replacement request: %d %v", code, err)
	}
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("replacement failed to stop")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(receipts) != 3 {
		t.Fatalf("acknowledged 3 requests, destination received %d: %v", len(receipts), receipts)
	}
	for _, id := range []string{"before-signal", "during-withdrawal", "after-replacement"} {
		count := 0
		for _, body := range receipts {
			if strings.Contains(body, id) {
				count++
			}
		}
		if count != 1 {
			t.Fatalf("%s delivered %d times", id, count)
		}
	}
}
