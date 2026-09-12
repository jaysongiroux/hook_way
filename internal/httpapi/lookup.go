package httpapi

import (
	"context"
	"log/slog"
	"time"

	"github.com/jasongiroux/hookway/internal/config"
	"github.com/jasongiroux/hookway/internal/lookup"
	"github.com/jasongiroux/hookway/internal/metrics"
)

// LookupFactory builds the provider behind a lookup-source variable. The
// default is lookup.New; tests inject a fake.
type LookupFactory func(ctx context.Context, opts lookup.Options, logger *slog.Logger) (lookup.Provider, error)

// lookupOptions maps a variable's config onto lookup.Options.
func lookupOptions(name string, lc config.LookupConfig) lookup.Options {
	opts := lookup.Options{
		Name:    name,
		Timeout: lc.Timeout,
		Cache:   lookup.CacheOptions{TTL: lc.Cache.TTL, NegativeTTL: lc.Cache.NegativeTTL, MaxEntries: lc.Cache.MaxEntries},
	}
	switch lc.Provider {
	case "dynamodb":
		if d := lc.DynamoDB; d != nil {
			opts.DynamoDB = &lookup.DynamoDBOptions{
				Table: d.Table, Index: d.Index, KeyAttribute: d.KeyAttribute,
				FilterAttribute: d.FilterAttribute, FilterValue: d.FilterValue, ValueAttribute: d.ValueAttribute,
				ConsistentRead: d.ConsistentRead, Region: d.Region, Endpoint: d.Endpoint,
				AccessKeyID: d.AccessKeyID, SecretAccessKey: d.SecretAccessKey,
			}
		}
	case "http":
		if h := lc.HTTP; h != nil {
			opts.HTTP = &lookup.HTTPOptions{URL: h.URL, Method: h.Method, Headers: h.Headers, ValuePath: h.ValuePath, Timeout: lc.Timeout}
		}
	case "postgres":
		if p := lc.Postgres; p != nil {
			opts.Postgres = &lookup.PostgresOptions{URL: p.URL, Query: p.Query}
		}
	}
	return opts
}

// cacheInspector is implemented by the cached provider so metrics can tell a
// cache hit from a provider call.
type cacheInspector interface{ Cached(key string) bool }

// meteredLookup adapts a lookup.Provider to vars.Lookup and records the
// hookway_variable_lookup metrics per call.
type meteredLookup struct {
	inner    lookup.Provider
	endpoint string
	variable string
	provider string
	metrics  *metrics.Metrics
}

func (m *meteredLookup) Lookup(ctx context.Context, key string) (string, bool, error) {
	start := time.Now()
	cached := false
	if c, ok := m.inner.(cacheInspector); ok {
		cached = c.Cached(key)
	}
	value, found, err := m.inner.Lookup(ctx, key)
	result := "hit"
	switch {
	case err != nil:
		result = "error"
	case cached:
		result = "cached"
	case !found:
		result = "miss"
	}
	if m.metrics != nil {
		m.metrics.VariableLookup(m.endpoint, m.variable, m.provider, result, time.Since(start))
	}
	return value, found, err
}
