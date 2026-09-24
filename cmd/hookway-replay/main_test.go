package main

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const (
	controlToken = "control-token-0123456789abcdefghij"
	captureToken = "capture-token-0123456789abcdefghijkl"
	opsToken     = "ops-token-0123456789abcdefghijklmnopq"
)

func testConfig(databaseURL, addr, extra string) string {
	return `
server: {addr: "` + addr + `"}
database: {url: "` + databaseURL + `"}
core: {control_url: "http://core-control:9092", control_token: "` + controlToken + `"}
keys:
  - {name: core-capture, token: "` + captureToken + `", scopes: [capture]}
  - {name: ops, token: "` + opsToken + `", scopes: [read, payload, retry, metrics]}
` + extra
}

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "replay.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func runCmd(args ...string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

const unreachableDB = "postgres://replay@127.0.0.1:1/replay?connect_timeout=1"

func TestRunDispatch(t *testing.T) {
	if code, _, stderr := runCmd("frobnicate"); code != 2 || !strings.Contains(stderr, `unknown command "frobnicate"`) {
		t.Fatalf("unknown command: %d %q", code, stderr)
	}
	if code, stdout, _ := runCmd("version"); code != 0 || !strings.HasPrefix(stdout, "hookway-replay dev (none)") {
		t.Fatalf("version: %d %q", code, stdout)
	}
	if code, stdout, _ := runCmd("help"); code != 0 || !strings.Contains(stdout, "migrate") {
		t.Fatalf("help: %d %q", code, stdout)
	}
	if code, stdout, _ := runCmd("help"); code != 0 || !strings.Contains(stdout, "gc ") {
		t.Fatalf("help does not list gc: %q", stdout)
	}
}

func TestValidate(t *testing.T) {
	path := writeConfig(t, testConfig(unreachableDB, ":8080", ""))
	if code, stdout, _ := runCmd("validate", "--config", path); code != 0 || !strings.Contains(stdout, "config valid") {
		t.Fatalf("valid: %d %q", code, stdout)
	}
	t.Setenv("HOOKWAY_REPLAY_CONFIG", path)
	if code, _, _ := runCmd("validate"); code != 0 {
		t.Fatalf("env path: %d", code)
	}
	warn := writeConfig(t, strings.Replace(testConfig(unreachableDB, ":8080", "metrics: {enabled: true, path: /metrics}\n"), ", metrics]", "]", 1))
	if code, _, stderr := runCmd("validate", "--config", warn); code != 0 || !strings.Contains(stderr, "warning: metrics") {
		t.Fatalf("warning: %d %q", code, stderr)
	}
	bad := writeConfig(t, strings.Replace(testConfig(unreachableDB, ":8080", ""), controlToken, "short", 1))
	if code, _, stderr := runCmd("validate", "--config", bad); code != 1 || !strings.Contains(stderr, "core.control_token") {
		t.Fatalf("invalid: %d %q", code, stderr)
	}
}

func TestConfigPathIsRequired(t *testing.T) {
	t.Setenv("HOOKWAY_REPLAY_CONFIG", "")
	for _, cmd := range []string{"validate", "migrate", "serve"} {
		if code, _, stderr := runCmd(cmd); code != 1 || !strings.Contains(stderr, "HOOKWAY_REPLAY_CONFIG") {
			t.Errorf("%s: %d %q", cmd, code, stderr)
		}
	}
	if code, _, _ := runCmd("validate", "--nope"); code != 2 {
		t.Errorf("bad flag: %d", code)
	}
}

func TestHealthcheck(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }))
	defer ok.Close()
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(503) }))
	defer down.Close()
	if code, stdout, _ := runCmd("healthcheck", "--url", ok.URL); code != 0 || !strings.Contains(stdout, "healthy") {
		t.Fatalf("ok: %d %q", code, stdout)
	}
	if code, _, stderr := runCmd("healthcheck", "--url", down.URL); code != 1 || !strings.Contains(stderr, "status 503") {
		t.Fatalf("down: %d %q", code, stderr)
	}
	if code, _, _ := runCmd("healthcheck", "--url", "http://127.0.0.1:1/healthz", "--timeout", "1s"); code != 1 {
		t.Fatalf("unreachable: %d", code)
	}
}

func TestMigrateAndServeReportAnUnreachableDatabase(t *testing.T) {
	path := writeConfig(t, testConfig(unreachableDB, "127.0.0.1:0", ""))
	if code, _, stderr := runCmd("migrate", "--config", path); code != 1 || !strings.Contains(stderr, "database connection failed") {
		t.Fatalf("migrate: %d %q", code, stderr)
	}
	if code, stdout, _ := runCmd("serve", "--config", path); code != 1 || !strings.Contains(stdout, "database connection failed") {
		t.Fatalf("serve: %d %q", code, stdout)
	}
}

func TestServeRejectsAnInvalidConfig(t *testing.T) {
	path := writeConfig(t, "database: {url: x}\n")
	if code, stdout, _ := runCmd("serve", "--config", path); code != 1 || !strings.Contains(stdout, "config load failed") {
		t.Fatalf("serve: %d %q", code, stdout)
	}
}

func TestServeShutsDownOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var backgroundStopped atomic.Bool
	srv := newServer("127.0.0.1:0", http.NotFoundHandler())
	done := make(chan int, 1)
	go func() {
		done <- serve(ctx, slog.New(slog.DiscardHandler), []*http.Server{srv}, func(bg context.Context) {
			<-bg.Done()
			backgroundStopped.Store(true)
		})
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case code := <-done:
		if code != 0 || !backgroundStopped.Load() {
			t.Fatalf("serve = %d, background stopped %v", code, backgroundStopped.Load())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not return after cancel")
	}
}

func TestServeReportsAListenFailure(t *testing.T) {
	srv := newServer("127.0.0.1:-1", http.NotFoundHandler())
	code := serve(context.Background(), slog.New(slog.DiscardHandler), []*http.Server{srv}, func(bg context.Context) { <-bg.Done() })
	if code != 1 {
		t.Fatalf("serve = %d, want 1", code)
	}
}

func TestGCReportsAnUnreachableDatabase(t *testing.T) {
	path := writeConfig(t, testConfig(unreachableDB, ":8080", ""))
	if code, _, stderr := runCmd("gc", "--config", path); code != 1 || !strings.Contains(stderr, "database connection failed") {
		t.Fatalf("gc = %d %q", code, stderr)
	}
}

func TestWorkerIDIsUniquePerProcess(t *testing.T) {
	a, b := workerID(), workerID()
	if a == "" || a == b {
		t.Fatalf("workerID = %q, %q", a, b)
	}
}
