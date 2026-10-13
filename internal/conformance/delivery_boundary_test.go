package conformance

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jaysongiroux/hook_way/internal/config"
	"github.com/jaysongiroux/hook_way/internal/queue"
)

// Exercise a real TCP disconnect after publication starts, rather than merely
// passing a canceled context into the graph executor. Publication must finish,
// and the request's replay reservation must survive the lost acknowledgement.
func TestDeliveryContinuesAfterCallerDisconnect(t *testing.T) {
	entered, release := make(chan struct{}, 1), make(chan struct{})
	pub := &testPublisher{entered: entered, release: release}
	handler := newServer(t, gatedYAML(), pub)
	finished := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler.ServeHTTP(w, r)
		finished <- struct{}{}
	}))
	defer server.Close()
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	address, _ := url.Parse(server.URL)
	conn, err := (&net.Dialer{Timeout: time.Second}).DialContext(context.Background(), "tcp", address.Host)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	body := []byte(`{"id":"disconnect-event"}`)
	req := signedRequest(http.MethodPost, "/webhooks/github", body)
	req.Host = address.Host
	if err := req.Write(conn); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("publish not entered")
	}
	_ = conn.Close()
	unblock()
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("publication did not finish after disconnect")
	}
	pub.mu.Lock()
	count := pub.published
	pub.mu.Unlock()
	if count != 1 || pub.sawCancel.Load() {
		t.Fatalf("published = %d, canceled = %v", count, pub.sawCancel.Load())
	}
	// The original publish committed even though its HTTP response was lost.
	if rec := post(handler, signedRequest(http.MethodPost, "/webhooks/github", body)); rec.Code != http.StatusConflict {
		t.Fatalf("retry = %d, want replay guard conflict after committed publication", rec.Code)
	}
}

// The helper is the same test binary running a real async publisher and flow
// server in a separate process. The parent kills it while work is in memory.
func TestAsyncCrashHelper(t *testing.T) {
	target := os.Getenv("HOOKWAY_ASYNC_CRASH_TARGET")
	if target == "" {
		return
	}
	pub, err := queue.NewWebhookPublisher(config.WebhookConfig{
		URL: target, DeliveryMode: "async", MaxQueue: 10, Timeout: time.Minute,
		Retry: config.RetryPolicyConfig{MaxAttempts: 1},
	}, "primary", []queue.WebhookTarget{{Name: "github-webhooks"}}, queue.WebhookPublisherOptions{
		Workers: 1, DefaultDrain: 5 * time.Second, Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(newServer(t, gatedYAML(), pub))
	_, _ = fmt.Fprintln(os.Stdout, server.URL)
	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() && scanner.Text() == "close" {
		server.Close()
		if err := pub.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

type crashProcess struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
	url   string
}

func startCrashProcess(t *testing.T, target string) crashProcess {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestAsyncCrashHelper$") // #nosec G204 -- only the current test executable and fixed test selector are run.
	cmd.Env = append(os.Environ(), "HOOKWAY_ASYNC_CRASH_TARGET="+target)
	cmd.Stderr = os.Stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	line := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		if scanner.Scan() {
			line <- scanner.Text()
		} else {
			line <- ""
		}
		_, _ = io.Copy(io.Discard, stdout)
	}()
	select {
	case address := <-line:
		if !strings.HasPrefix(address, "http://") {
			t.Fatalf("helper failed to start: %q", address)
		}
		return crashProcess{cmd: cmd, stdin: stdin, url: address}
	case <-ctx.Done():
		t.Fatal("helper startup timed out")
	}
	return crashProcess{}
}

func crashPost(t *testing.T, address, body string) {
	t.Helper()
	req := signedRequest(http.MethodPost, address+"/webhooks/github", []byte(body))
	req.RequestURI = ""
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusAccepted {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d: %s", resp.StatusCode, b)
	}
}

// This test documents a supported loss model, not a durability promise. The
// second event was acknowledged but never reaches the destination after SIGKILL
// and restart; a new event does. No wall-clock sleep is used to infer absence:
// the restarted engine is fully drained before inspecting received requests.
func TestAsyncAcknowledgedQueueIsLostOnProcessCrash(t *testing.T) {
	firstEntered, releaseFirst := make(chan struct{}, 1), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(releaseFirst) }) }
	var mu sync.Mutex
	var received []string
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		received = append(received, string(body))
		mu.Unlock()
		if strings.Contains(string(body), "first-in-flight") {
			firstEntered <- struct{}{}
			<-releaseFirst
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer destination.Close()
	defer unblock()
	process := startCrashProcess(t, destination.URL)
	crashPost(t, process.url, `{"id":"first-in-flight"}`)
	select {
	case <-firstEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("first delivery not started")
	}
	crashPost(t, process.url, `{"id":"second-queued"}`)
	if err := process.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := process.cmd.Wait(); err == nil {
		t.Fatal("expected killed process to fail")
	}
	unblock()
	restarted := startCrashProcess(t, destination.URL)
	crashPost(t, restarted.url, `{"id":"third-after-restart"}`)
	if _, err := io.WriteString(restarted.stdin, "close\n"); err != nil {
		t.Fatal(err)
	}
	if err := restarted.cmd.Wait(); err != nil {
		t.Fatalf("restart/drain: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(received) != 2 || !strings.Contains(received[0], "first-in-flight") || !strings.Contains(received[1], "third-after-restart") {
		t.Fatalf("unexpected deliveries after crash and restart: %v", received)
	}
}
