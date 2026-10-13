package queue

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jaysongiroux/hook_way/internal/config"
	"github.com/jaysongiroux/hook_way/internal/pgtls"
	"github.com/jaysongiroux/hook_way/internal/tlsfiles"
)

// pgOps is the thin, mockable interface the Postgres publisher depends on. It
// wraps the subset of pgxpool operations used by the publisher so unit tests
// can inject a fake without a live database.
type pgOps interface {
	// Exec runs a statement (DDL or DML) discarding the command tag.
	Exec(ctx context.Context, sql string, args ...any) error
	// Ping checks liveness of the connection pool.
	Ping(ctx context.Context) error
	// Close releases the underlying pool.
	Close() error
}

// pgxPoolOps wraps a *pgxpool.Pool and implements pgOps.
type pgxPoolOps struct {
	pool *pgxpool.Pool
}

func (p *pgxPoolOps) Exec(ctx context.Context, sql string, args ...any) error {
	_, err := p.pool.Exec(ctx, sql, args...)
	return err
}

func (p *pgxPoolOps) Ping(ctx context.Context) error { return p.pool.Ping(ctx) }

func (p *pgxPoolOps) Close() error { p.pool.Close(); return nil }

// newPostgresClient builds a pgOps from the destination's PostgresConfig. The
// url carries sslmode; an explicit TLS block (any field set) overrides the
// parsed TLS config. MaxConns, when > 0, caps the pool. A Ping confirms the
// connection before returning (fail-fast on misconfiguration).
func newPostgresClient(ctx context.Context, cfg config.PostgresConfig) (pgOps, error) {
	poolCfg, err := pgxpool.ParseConfig(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("postgres: parse url: %w", err)
	}
	if cfg.MaxConns > 0 {
		poolCfg.MaxConns = cfg.MaxConns
	}
	if err := pgtls.Apply(&poolCfg.ConnConfig.Config, tlsfiles.Files(cfg.TLS), "postgres tls"); err != nil {
		return nil, err
	}
	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("postgres: connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres: ping: %w", err)
	}
	return &pgxPoolOps{pool: pool}, nil
}
