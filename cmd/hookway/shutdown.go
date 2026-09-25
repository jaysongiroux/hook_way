package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"slices"
	"sync"
)

// shutdowner is a listener that drains: an *http.Server, or core's control
// listener, whose Shutdown also waits for in-flight redrives.
type shutdowner interface {
	Shutdown(ctx context.Context) error
}

type servingResources struct {
	logger  *slog.Logger
	signals chan os.Signal
	servers []shutdowner
	after   []func()
	closers []io.Closer
}

// drain stops every listener at once and waits for their in-flight work —
// the control listener's redrives included — then runs the after hooks and
// closes the flow and publisher resources in that order, so no publisher
// closes under a request or redrive still using it. signal.Stop runs first
// so a second SIGTERM during drain is not queued. Material Shutdown/Close
// errors are joined.
func (r servingResources) drain(ctx context.Context) error {
	if r.signals != nil {
		signal.Stop(r.signals)
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
			if err := srv.Shutdown(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
				shutdownErrs[i] = err
			}
		}()
	}
	wg.Wait()
	errs := slices.DeleteFunc(shutdownErrs, func(err error) bool { return err == nil })
	for _, fn := range r.after {
		if fn != nil {
			fn()
		}
	}
	for _, c := range r.closers {
		if c == nil {
			continue
		}
		if err := c.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
