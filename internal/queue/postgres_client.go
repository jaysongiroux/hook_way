package queue

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jaysongiroux/hook_way/internal/config"
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
	if postgresTLSSet(cfg.TLS) {
		tlsCfg, err := buildPostgresTLSConfig(cfg.TLS)
		if err != nil {
			return nil, err
		}
		poolCfg.ConnConfig.TLSConfig = tlsCfg
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

func postgresTLSSet(c config.PostgresTLSConfig) bool {
	return c.CACert != "" || c.ClientCert != "" || c.ClientKey != "" ||
		c.ServerName != "" || c.InsecureSkipVerify
}

// buildPostgresTLSConfig assembles a *tls.Config from the Postgres TLS options.
// It mirrors buildRedisTLSConfig so the two implementations stay consistent.
func buildPostgresTLSConfig(c config.PostgresTLSConfig) (*tls.Config, error) {
	cfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if c.ServerName != "" {
		cfg.ServerName = c.ServerName
	}
	if c.InsecureSkipVerify {
		cfg.InsecureSkipVerify = true //nolint:gosec // intentional, user-opted in
	}
	if c.CACert != "" {
		pem, err := os.ReadFile(c.CACert)
		if err != nil {
			return nil, fmt.Errorf("read postgres tls ca_cert: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("postgres tls ca_cert %q contained no valid certificates", c.CACert)
		}
		cfg.RootCAs = pool
	}
	if c.ClientCert != "" || c.ClientKey != "" {
		if c.ClientCert == "" || c.ClientKey == "" {
			return nil, errors.New("postgres tls client_cert and client_key must both be set for mutual TLS")
		}
		cert, err := tls.LoadX509KeyPair(c.ClientCert, c.ClientKey)
		if err != nil {
			return nil, fmt.Errorf("load postgres tls client cert/key: %w", err)
		}
		cfg.Certificates = []tls.Certificate{cert}
	}
	return cfg, nil
}
