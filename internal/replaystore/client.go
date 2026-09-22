package replaystore

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jaysongiroux/hook_way/internal/config"
)

// pgOps is the superset of pgxpool operations the replay store depends on: the
// write path this task implements (Exec, for DDL/Capture/RecordOutcome) plus
// the read path (Query/QueryRow) the next task (List/Get) will use. This is a
// deliberately different, wider interface than internal/queue's write-only
// pgOps -- the two packages are not meant to share it, since queue's
// destination never reads rows back.
//
// *pgxpool.Pool satisfies this interface's method signatures directly, so
// newClient can hand back the pool itself with no wrapper struct.
type pgOps interface {
	// Exec runs a statement (DDL or DML), returning the command tag (used by
	// callers that need e.g. RowsAffected).
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	// Query runs a statement expected to return rows.
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	// QueryRow runs a statement expected to return at most one row.
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	// Ping checks liveness of the connection pool.
	Ping(ctx context.Context) error
	// Close releases the underlying pool.
	Close()
}

// newClient builds a pgOps from the replay store's PostgresConfig. This
// mirrors internal/queue/postgres_client.go's pool construction exactly (URL
// parse, optional MaxConns cap, optional TLS override via
// postgresTLSSet/buildPostgresTLSConfig, fail-fast Ping) but returns the pool
// directly instead of a wrapper, since *pgxpool.Pool already implements the
// wider pgOps declared above.
func newClient(ctx context.Context, cfg config.PostgresConfig) (pgOps, error) {
	poolCfg, err := pgxpool.ParseConfig(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("replaystore: parse postgres url: %w", err)
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
		return nil, fmt.Errorf("replaystore: connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("replaystore: ping: %w", err)
	}
	return pool, nil
}

func postgresTLSSet(c config.PostgresTLSConfig) bool {
	return c.CACert != "" || c.ClientCert != "" || c.ClientKey != "" ||
		c.ServerName != "" || c.InsecureSkipVerify
}

// buildPostgresTLSConfig assembles a *tls.Config from the Postgres TLS
// options. Copied (not imported) from internal/queue/postgres_client.go: that
// function is unexported there, and the task brief calls for copying this
// small builder rather than exporting queue internals for one caller. Keep
// the two in sync if Postgres TLS handling changes.
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
