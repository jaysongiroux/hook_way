package httpapi

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jaysongiroux/hook_way/internal/queue"
)

type budgetPublisher struct {
	calls   atomic.Int32
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (p *budgetPublisher) Publish(ctx context.Context, _, _ string, _ queue.Message) error {
	p.calls.Add(1)
	p.once.Do(func() { close(p.entered) })
	select {
	case <-p.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (*budgetPublisher) Ready(context.Context) error { return nil }
func (*budgetPublisher) Close() error                { return nil }

// Use the configured net/http server, not an httptest wrapper that omits its
// read/write deadlines and post-handler request-body behavior.
func serveBudgetFixture(t *testing.T, readTimeout time.Duration, blocked bool) (*FlowServer, *budgetPublisher, string) {
	t.Helper()
	cfg := loadFlowCfg(t, testFlowYAML)
	cfg.Server.ReadTimeout = readTimeout
	cfg.Server.WriteTimeout = 3 * time.Second
	cfg.Server.ShutdownTimeout = 3 * time.Second
	cfg.Server.PublishTimeout = 2 * time.Second
	cfg.Server.MaxInflightRequests = 1
	cfg.Server.MaxPendingRequests = 0
	cfg.Server.ReadinessInterval = 25 * time.Millisecond
	cfg.Server.ReadinessTimeout = 20 * time.Millisecond
	p := &budgetPublisher{entered: make(chan struct{}), release: make(chan struct{})}
	if !blocked {
		close(p.release)
	}
	s, err := NewFlowServer(cfg, p, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		_ = s.Close()
		t.Fatal(err)
	}
	httpServer := s.HTTPServer()
	done := make(chan error, 1)
	go func() { done <- httpServer.Serve(listener) }()
	ctx, cancel := context.WithCancel(context.Background())
	probeDone := s.StartReadinessProbe(ctx)
	t.Cleanup(func() {
		if blocked {
			select {
			case <-p.release:
			default:
				close(p.release)
			}
		}
		cancel()
		_ = httpServer.Close()
		<-done
		<-probeDone
		_ = s.Close()
	})
	base := "http://" + listener.Addr().String()
	deadline := time.Now().Add(time.Second)
	for {
		request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, base+"/ready", nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := (&http.Client{Timeout: 200 * time.Millisecond}).Do(request)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("readiness did not converge")
		}
		time.Sleep(5 * time.Millisecond)
	}
	return s, p, base
}

func budgetPost(ctx context.Context, base string) (*http.Response, error) {
	body := []byte(`{"event":"resource-budget"}`)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/webhooks/github", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Hub-Signature-256", githubHMAC(body))
	return (&http.Client{Timeout: 3 * time.Second}).Do(req)
}

func assertBudgetProbes(t *testing.T, base string) {
	t.Helper()
	for _, path := range []string{"/health", "/ready"} {
		request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, base+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := (&http.Client{Timeout: 400 * time.Millisecond}).Do(request)
		if err != nil {
			t.Fatalf("%s during saturation: %v", path, err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s status=%d", path, resp.StatusCode)
		}
	}
}

func TestHTTPOverloadDoesNotWaitForRejectedRequestBody(t *testing.T) {
	_, pub, base := serveBudgetFixture(t, 2*time.Second, true)
	firstDone := make(chan error, 1)
	go func() {
		resp, err := budgetPost(t.Context(), base)
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}
		firstDone <- err
	}()
	select {
	case <-pub.entered:
	case <-time.After(time.Second):
		t.Fatal("first request never acquired admission")
	}
	conn, err := (&net.Dialer{Timeout: time.Second}).DialContext(t.Context(), "tcp", base[len("http://"):])
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(400 * time.Millisecond))
	// Advertise an ordinary small body but send none. net/http must not drain
	// this rejected body before delivering the overload response.
	_, err = fmt.Fprint(conn, "POST /webhooks/github HTTP/1.1\r\nHost: localhost\r\nContent-Length: 128\r\n\r\n")
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("overload response waited for unread body: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusServiceUnavailable || resp.Header.Get("Retry-After") != "1" {
		t.Fatalf("overload response=%d headers=%v", resp.StatusCode, resp.Header)
	}
	assertBudgetProbes(t, base)
	if pub.calls.Load() != 1 {
		t.Fatal("rejected request reached publisher")
	}
	close(pub.release)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
}

func TestHTTPSlowBodyReclaimsAdmissionAndKeepsProbesResponsive(t *testing.T) {
	s, pub, base := serveBudgetFixture(t, 150*time.Millisecond, false)
	conn, err := (&net.Dialer{Timeout: time.Second}).DialContext(t.Context(), "tcp", base[len("http://"):])
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(time.Second))
	_, err = fmt.Fprint(conn, "POST /webhooks/github HTTP/1.1\r\nHost: localhost\r\nContent-Length: 128\r\n\r\nx")
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(100 * time.Millisecond)
	for len(s.gate.active) != 1 {
		if time.Now().After(deadline) {
			t.Fatal("slow body never acquired admission")
		}
		time.Sleep(time.Millisecond)
	}
	assertBudgetProbes(t, base)
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("read timeout status=%d", resp.StatusCode)
	}
	if pub.calls.Load() != 0 || len(s.gate.active) != 0 {
		t.Fatal("slow body published or leaked admission")
	}
	resp, err = budgetPost(t.Context(), base)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted || pub.calls.Load() != 1 {
		t.Fatal("healthy request did not recover")
	}
}
