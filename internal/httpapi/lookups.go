package httpapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/jaysongiroux/hook_way/internal/flowconfig"
	"github.com/jaysongiroux/hook_way/internal/lookup"
	"github.com/jaysongiroux/hook_way/internal/metrics"
)

func bindLookups(configs map[string]flowconfig.LookupProviderConfig, logger *slog.Logger, m *metrics.Metrics) (func(flowconfig.LookupOptions) flowconfig.LookupFunc, []io.Closer, error) {
	providers := map[string]lookup.Provider{}
	var closers []io.Closer
	names := make([]string, 0, len(configs))
	for name := range configs {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		c := configs[name]
		opts := lookup.Options{Name: name, Timeout: 2 * time.Second}
		if c.Timeout != "" {
			opts.Timeout, _ = time.ParseDuration(c.Timeout)
		}
		opts.Cache.TTL, _ = time.ParseDuration(c.Cache.TTL)
		opts.Cache.NegativeTTL, _ = time.ParseDuration(c.Cache.NegativeTTL)
		if c.Cache.MaxEntries != nil {
			opts.Cache.MaxEntries = *c.Cache.MaxEntries
		}
		if h := c.HTTP; h != nil {
			opts.HTTP = &lookup.HTTPOptions{URL: h.URL, Method: h.Method, Headers: h.Headers, ValuePath: h.ValuePath, Timeout: opts.Timeout, Client: flowconfig.NewCredentialHTTPClient(!h.AllowPrivateTargets)}
			secrets := map[string]flowconfig.SecretsFunc{}
			for header, value := range h.SecretHeaders {
				fn, closer, err := bindSecretRef(value.SecretRef, slog.New(slog.NewTextHandler(io.Discard, nil)), m, func(value []byte) error {
					if len(value) == 0 || len(value) > 8192 || strings.ContainsAny(string(value), "\r\n\x00") {
						return errors.New("invalid lookup header secret")
					}
					return nil
				})
				if err != nil {
					return nil, closers, fmt.Errorf("lookup %q: header secret unavailable", name)
				}
				closers = append(closers, closer)
				secrets[header] = fn
			}
			opts.HTTP.SecretHeaders = func(ctx context.Context) (http.Header, error) {
				headers := http.Header{}
				for name, fn := range secrets {
					values, err := fn(ctx)
					if err != nil || len(values) == 0 {
						return nil, errors.New("lookup secret unavailable")
					}
					headers.Set(name, h.SecretHeaders[name].Prefix+string(values[0]))
				}
				return headers, nil
			}
		}
		if p := c.Postgres; p != nil {
			opts.Postgres = &lookup.PostgresOptions{URL: p.URL, Query: p.Query}
		}
		if d := c.DynamoDB; d != nil {
			opts.DynamoDB = &lookup.DynamoDBOptions{Table: d.Table, Index: d.Index, KeyAttribute: d.KeyAttribute, ValueAttribute: d.ValueAttribute, FilterAttribute: d.FilterAttribute, FilterValue: d.FilterValue, ConsistentRead: d.ConsistentRead, PageLimit: d.PageLimit, Region: d.Region, Endpoint: d.Endpoint}
		}
		provider, err := lookup.New(context.Background(), opts, logger)
		if err != nil {
			return nil, closers, fmt.Errorf("lookup %q: provider initialization failed (check connection settings and credentials)", name)
		}
		providers[name] = provider
		closers = append(closers, provider)
	}
	return func(o flowconfig.LookupOptions) flowconfig.LookupFunc {
		if p := providers[o.Provider]; p != nil {
			return p.Lookup
		}
		return nil
	}, closers, nil
}
