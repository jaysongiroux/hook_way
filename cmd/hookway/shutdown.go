package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
)

type servingResources struct {
	logger  *slog.Logger
	signals chan os.Signal
	servers []*http.Server
	after   []func()
	closers []io.Closer
}

// drain stops every listener, waits for in-flight work, then closes replay,
// flow, and publisher resources in that order. signal.Stop runs first so a
// second SIGTERM during drain is not queued. Material Shutdown/Close errors
// are joined.
func (r servingResources) drain(ctx context.Context) error {
	if r.signals != nil {
		signal.Stop(r.signals)
	}
	var errs []error
	for _, srv := range r.servers {
		if srv == nil {
			continue
		}
		if err := srv.Shutdown(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errs = append(errs, err)
		}
	}
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
