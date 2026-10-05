package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"slices"
	"sync"
	"time"

	"github.com/jaysongiroux/hook_way/internal/queue"
)

// shutdowner is a listener that drains: an *http.Server, or core's control
// listener, whose Shutdown also waits for in-flight redrives.
type shutdowner interface {
	Shutdown(ctx context.Context) error
}

type servingResources struct {
	logger          *slog.Logger
	signals         chan os.Signal
	servers         []shutdowner
	after           []func()
	closers         []io.Closer
	listenerTimeout time.Duration
}

// drain stops every listener at once and waits for their in-flight work —
// the control listener's redrives included — then runs the after hooks and
// closes the flow and publisher resources in that order, so no publisher
// closes under a request or redrive still using it. signal.Stop runs first
// so a second SIGTERM during drain is not queued. Material Shutdown/Close
// errors are joined. The context bounds the complete sequence. On expiration,
// dependent resources remain open until process exit rather than closing beneath
// active work. The caller must treat this as fatal, not resume serving.
func (r servingResources) drain(ctx context.Context) error {
	if r.signals != nil {
		signal.Stop(r.signals)
	}
	listenerCtx := ctx
	if r.listenerTimeout > 0 {
		var cancel context.CancelFunc
		listenerCtx, cancel = context.WithTimeout(ctx, r.listenerTimeout)
		defer cancel()
	}
	shutdownErrs := make([]error, len(r.servers))
	var wg sync.WaitGroup
	for i, srv := range r.servers {
		if srv == nil {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := srv.Shutdown(listenerCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
				shutdownErrs[i] = err
			}
		}()
	}
	listenersDone := make(chan struct{})
	go func() { wg.Wait(); close(listenersDone) }()
	select {
	case <-listenersDone:
	case <-ctx.Done():
		return fmt.Errorf("shutdown budget exhausted waiting for listeners; dependent resources left open: %w", ctx.Err())
	}
	errs := slices.DeleteFunc(shutdownErrs, func(err error) bool { return err == nil })
	if len(errs) > 0 {
		return fmt.Errorf("listeners did not drain; dependent resources left open: %w", errors.Join(errs...))
	}
	for _, fn := range r.after {
		if fn != nil {
			if err := boundedShutdownStep(ctx, func() error { fn(); return nil }); err != nil {
				return err
			}
		}
	}
	for _, c := range r.closers {
		if c == nil {
			continue
		}
		if err := boundedShutdownStep(ctx, c.Close); err != nil {
			errs = append(errs, err)
			if errors.Is(err, queue.ErrWebhookDrainTimeout) {
				return errors.Join(errs...)
			}
		}
		if err := ctx.Err(); err != nil {
			return errors.Join(append(errs, fmt.Errorf("shutdown budget exhausted: %w", err))...)
		}
	}
	return errors.Join(errs...)
}

// Close has no context in the publisher contract. Bound waiting here, while
// preserving ordering: if a step outlives the budget, never start the next one.
func boundedShutdownStep(ctx context.Context, fn func() error) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("shutdown budget exhausted: %w", err)
	}
	done := make(chan error, 1)
	go func() { done <- fn() }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return fmt.Errorf("shutdown budget exhausted during resource cleanup: %w", ctx.Err())
	}
}

func withdrawBeforeDrain(ctx context.Context, delay time.Duration, withdraw func()) error {
	withdraw()
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
