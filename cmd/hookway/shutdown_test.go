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
			orderCloser{name: "replay", order: &order, err: errors.New("replay close")},
			orderCloser{name: "flow", order: &order, err: errors.New("flow close")},
			orderCloser{name: "publisher", order: &order},
		},
	}
	err := res.drain(context.Background())
	if !afterCalled {
		t.Fatal("in-flight wait (after) was not called")
	}
	if err == nil || !strings.Contains(err.Error(), "replay close") || !strings.Contains(err.Error(), "flow close") {
		t.Fatalf("joined errors = %v", err)
	}
	if len(order) != 3 || order[0] != "replay" || order[1] != "flow" || order[2] != "publisher" {
		t.Fatalf("close order = %v, want replay, flow, publisher", order)
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
	res := servingResources{servers: []*http.Server{mainSrv, auxSrv}}
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
