package main

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jaysongiroux/hook_way/internal/queue"
)

type orderCloser struct {
	name  string
	order *[]string
	err   error
}

func (c orderCloser) Close() error {
	*c.order = append(*c.order, c.name)
	return c.err
}

func TestDrainClosesInOrderAndJoinsErrors(t *testing.T) {
	var order []string
	afterCalled := false
	res := servingResources{
		after: []func(){func() { afterCalled = true }},
		closers: []io.Closer{
			orderCloser{name: "first", order: &order, err: errors.New("first close")},
			orderCloser{name: "flow", order: &order, err: errors.New("flow close")},
			orderCloser{name: "publisher", order: &order},
		},
	}
	err := res.drain(context.Background())
	if !afterCalled {
		t.Fatal("in-flight wait (after) was not called")
	}
	if err == nil || !strings.Contains(err.Error(), "first close") || !strings.Contains(err.Error(), "flow close") {
		t.Fatalf("joined errors = %v", err)
	}
	if len(order) != 3 || order[0] != "first" || order[1] != "flow" || order[2] != "publisher" {
		t.Fatalf("close order = %v, want first, flow, publisher", order)
	}
}

func TestDrainStopsSignalNotify(t *testing.T) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGUSR1)
	t.Cleanup(func() { signal.Stop(ch) })
	res := servingResources{signals: ch}
	if err := res.drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(os.Getpid(), syscall.SIGUSR1); err != nil {
		t.Fatal(err)
	}
	select {
	case sig := <-ch:
		t.Fatalf("signal %v delivered after signal.Stop", sig)
	case <-time.After(75 * time.Millisecond):
	}
}

func TestDrainShutsDownEveryListenerIncludingMain(t *testing.T) {
	mainLn := mustListen(t)
	auxLn := mustListen(t)
	mainSrv := &http.Server{Handler: http.NewServeMux(), ReadHeaderTimeout: time.Second}
	auxSrv := &http.Server{Handler: http.NewServeMux(), ReadHeaderTimeout: time.Second}
	errs := make(chan error, 2)
	go func() { errs <- mainSrv.Serve(mainLn) }()
	go func() { errs <- auxSrv.Serve(auxLn) }()
	waitAccepting(t, mainLn.Addr().String())
	waitAccepting(t, auxLn.Addr().String())

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	res := servingResources{servers: []shutdowner{mainSrv, auxSrv}}
	if err := res.drain(ctx); err != nil {
		t.Fatalf("drain: %v", err)
	}
	for i := 0; i < 2; i++ {
		select {
		case err := <-errs:
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				t.Fatalf("Serve() = %v, want ErrServerClosed", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("listener was not drained")
		}
	}
}

type errShutdown struct{ err error }

func (s errShutdown) Shutdown(context.Context) error { return s.err }

func TestDrainReportsEveryListenerShutdownError(t *testing.T) {
	// Distinct errors per index: if the shutdown goroutine captured the loop
	// index by reference, both would land on the final slot and one message
	// would be lost from the joined result.
	res := servingResources{servers: []shutdowner{
		errShutdown{errors.New("main drain failed")},
		errShutdown{errors.New("aux drain failed")},
	}}
	err := res.drain(context.Background())
	if err == nil {
		t.Fatal("drain = nil, want joined listener errors")
	}
	msg := err.Error()
	if !strings.Contains(msg, "main drain failed") || !strings.Contains(msg, "aux drain failed") {
		t.Fatalf("joined errors = %v, want both listener failures", err)
	}
}

func mustListen(t *testing.T) net.Listener {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	return ln
}

func waitAccepting(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := (&net.Dialer{Timeout: 50 * time.Millisecond}).DialContext(context.Background(), "tcp", addr)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("server at %s never accepted connections", addr)
}

type blockedShutdown struct{ release <-chan struct{} }

func (s blockedShutdown) Shutdown(context.Context) error { <-s.release; return nil }

type blockedClose struct {
	started chan<- struct{}
	release <-chan struct{}
}

func (c blockedClose) Close() error { close(c.started); <-c.release; return nil }

func TestTotalBudgetDoesNotCloseResourcesUnderActiveListener(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	var order []string
	res := servingResources{servers: []shutdowner{blockedShutdown{release}}, closers: []io.Closer{orderCloser{name: "publisher", order: &order}}}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := res.drain(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("drain = %v", err)
	}
	if len(order) != 0 {
		t.Fatal("closed publisher while listener was active")
	}
}

func TestTotalBudgetStopsSequentialCleanupAtStalledCloser(t *testing.T) {
	release, started := make(chan struct{}), make(chan struct{})
	defer close(release)
	var order []string
	res := servingResources{closers: []io.Closer{blockedClose{started, release}, orderCloser{name: "capture", order: &order}}}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := res.drain(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("drain = %v", err)
	}
	<-started
	if len(order) != 0 {
		t.Fatal("closed capture while publisher was still draining")
	}
}

func TestWithdrawalHappensBeforeDelayAndCanBeCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	withdrawn := make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- withdrawBeforeDrain(ctx, time.Hour, func() { close(withdrawn) }) }()
	<-withdrawn
	select {
	case <-done:
		t.Fatal("did not wait for withdrawal")
	default:
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("withdraw = %v", err)
	}
}

func TestUndrainedPublisherLeavesCaptureOpen(t *testing.T) {
	var order []string
	res := servingResources{closers: []io.Closer{
		orderCloser{name: "publisher", order: &order, err: queue.ErrWebhookDrainTimeout},
		orderCloser{name: "capture", order: &order},
	}}
	if err := res.drain(context.Background()); !errors.Is(err, queue.ErrWebhookDrainTimeout) {
		t.Fatalf("drain = %v", err)
	}
	if len(order) != 1 || order[0] != "publisher" {
		t.Fatalf("closed dependencies: %v", order)
	}
}
