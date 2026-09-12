// Package lookup implements the providers behind the `lookup` variable source:
// a request-derived key is resolved to a value through DynamoDB, an HTTP
// endpoint, or a Postgres query, with per-provider caching and coalescing of
// concurrent first lookups.
//
// internal/vars sees only the Lookup interface (it must stay stdlib-only for
// the WASM validator); this package owns every SDK dependency.
package lookup

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

// Provider resolves a key to a value. found=false with err=nil is a clean
// miss; err is a backend failure (callers treat both as unresolved for routing
// but count them separately).
type Provider interface {
	Lookup(ctx context.Context, key string) (value string, found bool, err error)
	Close() error
}

// Options selects and configures one provider plus its cache. Exactly one of
// DynamoDB, HTTP, or Postgres is non-nil.
type Options struct {
	Name     string // variable name, for logs and errors
	Timeout  time.Duration
	Cache    CacheOptions
	DynamoDB *DynamoDBOptions
	HTTP     *HTTPOptions
	Postgres *PostgresOptions
}

// New builds the configured provider, wrapped in a cache, or returns an error
// when construction (e.g. a Postgres ping) fails.
func New(ctx context.Context, opts Options, logger *slog.Logger) (Provider, error) {
	if logger == nil {
		logger = slog.Default()
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	// Bound construction (pool connect + ping, AWS config load) so an
	// unreachable host cannot stall server startup for the OS connect timeout.
	ctx, cancel := context.WithTimeout(ctx, max(timeout, 5*time.Second))
	defer cancel()
	var (
		p   Provider
		err error
	)
	switch {
	case opts.DynamoDB != nil:
		p, err = newDynamoDB(ctx, *opts.DynamoDB)
	case opts.HTTP != nil:
		p, err = newHTTP(*opts.HTTP)
	case opts.Postgres != nil:
		p, err = newPostgres(ctx, *opts.Postgres)
	default:
		err = errors.New("no provider configured")
	}
	if err != nil {
		return nil, fmt.Errorf("lookup %q: %w", opts.Name, err)
	}
	return newCached(&timed{inner: p, timeout: timeout}, opts.Cache, logger), nil
}

// timed bounds every provider call by the configured timeout.
type timed struct {
	inner   Provider
	timeout time.Duration
}

func (t *timed) Lookup(ctx context.Context, key string) (string, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()
	return t.inner.Lookup(ctx, key)
}

func (t *timed) Close() error { return t.inner.Close() }
