package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"
)

// PostgresTarget is a single route target. For Postgres the target name is
// stored verbatim in the row's "target" column; there is no per-target resource.
type PostgresTarget struct {
	Name string
}

// PostgresConfig carries everything PostgresPublisher needs. The factory builds
// this from the parsed config and the collected targets.
type PostgresConfig struct {
	Schema        string
	Table         string
	DestinationID string
	Logger        *slog.Logger
	Targets       []PostgresTarget
}

// pgInsertColumns is the ordered column list used by the INSERT. The four jsonb
// columns (headers, query, body, auth) get ::jsonb placeholder casts.
var pgInsertColumns = []string{
	"id", "schema_version", "idempotency_key", "request_id", "trace_id",
	"endpoint", "destination_id", "target", "received_at", "method", "path",
	"headers", "query", "body", "raw_body_base64", "source_ip", "auth",
}

var pgJSONColumns = map[string]bool{"headers": true, "query": true, "body": true, "auth": true}

// PostgresPublisher inserts each webhook envelope as one row in a shared table.
// targets is written only in the constructor and read-only during Publish, so
// concurrent fan-out reads are safe without locking (mirrors the Redis/S3 model).
type PostgresPublisher struct {
	ops       pgOps
	destID    string
	logger    *slog.Logger
	tableExpr string // sanitized "schema"."table"
	insertSQL string
	targets   map[string]struct{}
}

// NewPostgresPublisher ensures the destination table (and its indexes) exist via
// CREATE TABLE/INDEX IF NOT EXISTS, then records the configured target names.
func NewPostgresPublisher(ctx context.Context, ops pgOps, cfg PostgresConfig) (*PostgresPublisher, error) {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	schema := cfg.Schema
	if schema == "" {
		schema = "public"
	}
	tableExpr := pgx.Identifier{schema, cfg.Table}.Sanitize()
	targetIdx := pgx.Identifier{cfg.Table + "_target_idx"}.Sanitize()
	receivedIdx := pgx.Identifier{cfg.Table + "_received_at_idx"}.Sanitize()

	ddl := "CREATE TABLE IF NOT EXISTS " + tableExpr + ` (
	id text PRIMARY KEY,
	schema_version text,
	idempotency_key text,
	request_id text,
	trace_id text,
	endpoint text NOT NULL,
	destination_id text NOT NULL,
	target text NOT NULL,
	received_at timestamptz NOT NULL,
	method text,
	path text,
	headers jsonb,
	query jsonb,
	body jsonb,
	raw_body_base64 text,
	source_ip text,
	auth jsonb,
	inserted_at timestamptz NOT NULL DEFAULT now()
)`
	if err := ops.Exec(ctx, ddl); err != nil {
		return nil, fmt.Errorf("postgres %q: create table: %w", cfg.DestinationID, err)
	}
	if err := ops.Exec(ctx, "CREATE INDEX IF NOT EXISTS "+targetIdx+" ON "+tableExpr+" (target)"); err != nil {
		return nil, fmt.Errorf("postgres %q: create target index: %w", cfg.DestinationID, err)
	}
	if err := ops.Exec(ctx, "CREATE INDEX IF NOT EXISTS "+receivedIdx+" ON "+tableExpr+" (received_at)"); err != nil {
		return nil, fmt.Errorf("postgres %q: create received_at index: %w", cfg.DestinationID, err)
	}

	logger.Info("ensured table", slog.String("destination", cfg.DestinationID), slog.String("target", cfg.Table))

	targets := make(map[string]struct{}, len(cfg.Targets))
	for _, t := range cfg.Targets {
		targets[t.Name] = struct{}{}
	}

	return &PostgresPublisher{
		ops:       ops,
		destID:    cfg.DestinationID,
		logger:    logger,
		tableExpr: tableExpr,
		insertSQL: buildInsertSQL(tableExpr),
		targets:   targets,
	}, nil
}

// buildInsertSQL builds "INSERT INTO <table> (cols) VALUES ($1,$2,...,$n::jsonb,...) ON CONFLICT (id) DO NOTHING".
func buildInsertSQL(tableExpr string) string {
	cols := ""
	vals := ""
	for i, c := range pgInsertColumns {
		if i > 0 {
			cols += ", "
			vals += ", "
		}
		cols += c
		ph := fmt.Sprintf("$%d", i+1)
		if pgJSONColumns[c] {
			ph += "::jsonb"
		}
		vals += ph
	}
	return "INSERT INTO " + tableExpr + " (" + cols + ") VALUES (" + vals + ") ON CONFLICT (id) DO NOTHING"
}

// Publish inserts msg as one row. Duplicate ids are no-ops (ON CONFLICT DO NOTHING).
// targets is written only in NewPostgresPublisher; this read is safe to perform
// concurrently across the server's publish fan-out — mirrors the redis_stream race invariant.
func (p *PostgresPublisher) Publish(ctx context.Context, destinationQueueID, target string, msg Message) error {
	if _, ok := p.targets[target]; !ok {
		return fmt.Errorf("postgres %q: target %q is not configured", p.destID, target)
	}

	headers, err := jsonbArg(len(msg.Headers) == 0, msg.Headers)
	if err != nil {
		return fmt.Errorf("postgres %q: marshal headers: %w", p.destID, err)
	}
	query, err := jsonbArg(len(msg.Query) == 0, msg.Query)
	if err != nil {
		return fmt.Errorf("postgres %q: marshal query: %w", p.destID, err)
	}
	body, err := jsonbArg(msg.Body == nil, msg.Body)
	if err != nil {
		return fmt.Errorf("postgres %q: marshal body: %w", p.destID, err)
	}
	auth, err := jsonbArg(msg.Auth == nil, msg.Auth)
	if err != nil {
		return fmt.Errorf("postgres %q: marshal auth: %w", p.destID, err)
	}

	args := []any{
		msg.ID, msg.SchemaVersion, msg.IdempotencyKey, msg.RequestID, msg.TraceID,
		msg.Endpoint, msg.DestinationQueueID, target, msg.ReceivedAt, msg.Method, msg.Path,
		headers, query, body, msg.RawBodyBase64, msg.SourceIP, auth,
	}
	return p.ops.Exec(ctx, p.insertSQL, args...)
}

// jsonbArg returns nil (SQL NULL) when isNil is true, otherwise the JSON-encoded
// value as a string for binding to a $n::jsonb placeholder. The explicit isNil
// guard avoids the typed-nil trap (a nil http.Header/map wrapped in `any` is not
// == nil and would otherwise marshal to the JSON literal "null").
func jsonbArg(isNil bool, v any) (any, error) {
	if isNil {
		return nil, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return string(b), nil
}

// Ready confirms the connection pool is reachable.
func (p *PostgresPublisher) Ready(ctx context.Context) error { return p.ops.Ping(ctx) }

// Close releases the underlying pool.
func (p *PostgresPublisher) Close() error { return p.ops.Close() }
