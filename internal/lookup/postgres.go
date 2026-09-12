package lookup

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresOptions configures a query with exactly one {key} placeholder,
// which is replaced by the $1 parameter (never interpolated). {key} is used
// rather than $1 because hookway expands $NAME environment references in the
// config file at load. The first column of the first row is the value; no row
// is a clean miss.
type PostgresOptions struct {
	URL   string
	Query string
}

// rowQuerier is the pool surface the provider uses, so tests can inject a fake.
type rowQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Close()
}

type postgresProvider struct {
	pool  rowQuerier
	query string
}

func newPostgres(ctx context.Context, opts PostgresOptions) (*postgresProvider, error) {
	if strings.Count(opts.Query, "{key}") != 1 {
		return nil, errors.New("postgres: query must contain exactly one {key} placeholder")
	}
	pool, err := pgxpool.New(ctx, opts.URL)
	if err != nil {
		return nil, fmt.Errorf("postgres: connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres: ping: %w", err)
	}
	return &postgresProvider{pool: pool, query: strings.Replace(opts.Query, "{key}", "$1", 1)}, nil
}

func (p *postgresProvider) Lookup(ctx context.Context, key string) (string, bool, error) {
	var raw any
	if err := p.pool.QueryRow(ctx, p.query, key).Scan(&raw); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("postgres query: %w", err)
	}
	value, ok := scalarString(raw)
	if !ok || value == "" {
		return "", false, nil
	}
	return value, true, nil
}

func (p *postgresProvider) Close() error { p.pool.Close(); return nil }

// scalarString renders a scanned text or numeric column as a string.
func scalarString(v any) (string, bool) {
	switch x := v.(type) {
	case string:
		return x, true
	case []byte:
		return string(x), true
	case int64:
		return strconv.FormatInt(x, 10), true
	case int32:
		return strconv.FormatInt(int64(x), 10), true
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64), true
	case bool:
		return strconv.FormatBool(x), true
	default:
		return "", false
	}
}
