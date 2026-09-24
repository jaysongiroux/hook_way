// Package config loads the replay service's YAML configuration: strict
// decoding, ${VAR} expansion, defaults, and validation. Issues use core's
// config.ValidationIssue, so both binaries report problems the same way.
package config

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	hookconfig "github.com/jaysongiroux/hook_way/internal/config"
	"github.com/jaysongiroux/hook_way/internal/replay/auth"
	"github.com/jaysongiroux/hook_way/internal/tlsfiles"
)

const (
	defaultAddr                   = ":8080"
	defaultMaxConns         int32 = 10
	defaultTTL                    = 168 * time.Hour
	defaultMaxRecords       int64 = 100000
	defaultMaxBytes         int64 = 10 << 30
	defaultMaxRecordBytes   int64 = 16 << 20
	defaultGCInterval             = 15 * time.Minute
	defaultConcurrency            = 4
	defaultExecutionTimeout       = 30 * time.Second
	defaultLease                  = 60 * time.Second
	minTokenLen                   = 32
	maxConcurrency                = 256
	// executionMargin is what the worker keeps back from execution_timeout;
	// core's budget for one redrive is the difference (spec: 30s → 25s).
	executionMargin = 5 * time.Second
	// leaseMargin is the slack between the worker's deadline and lease expiry.
	leaseMargin = 15 * time.Second
)

var keyNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,63}$`)

type Config struct {
	Server    ServerConfig
	Logging   LoggingConfig
	Metrics   hookconfig.MetricsConfig
	Database  DatabaseConfig
	Core      CoreConfig
	Retention RetentionConfig
	Workers   WorkersConfig
	Keys      []KeyConfig
}

type ServerConfig struct{ Addr string }

type LoggingConfig struct{ Level string }

type TLSConfig struct {
	CACert             string
	ClientCert         string
	ClientKey          string
	ServerName         string
	InsecureSkipVerify bool
}

type DatabaseConfig struct {
	URL      string
	MaxConns int32
	TLS      TLSConfig
}

// CoreConfig is how replay reaches core's control listener (used by the
// phase 5 worker).
type CoreConfig struct {
	ControlURL   string
	ControlToken string
	TLS          TLSConfig
}

type RetentionConfig struct {
	TTL            time.Duration
	MaxRecords     int64
	MaxBytes       int64
	MaxRecordBytes int64
	GCInterval     time.Duration // 0 disables the in-process GC loop
}

type WorkersConfig struct {
	Concurrency      int
	ExecutionTimeout time.Duration
	Lease            time.Duration
}

type KeyConfig struct {
	Name   string
	Token  string
	Scopes []string
}

// Files returns the block in the form internal/tlsfiles builds.
func (t TLSConfig) Files() tlsfiles.Files { return tlsfiles.Files(t) }

// AuthKeys converts the configured keys for internal/replay/auth.
func (c Config) AuthKeys() []auth.Key {
	keys := make([]auth.Key, 0, len(c.Keys))
	for _, k := range c.Keys {
		scopes := make([]auth.Scope, 0, len(k.Scopes))
		for _, s := range k.Scopes {
			scopes = append(scopes, auth.Scope(s))
		}
		keys = append(keys, auth.Key{Name: k.Name, Token: k.Token, Scopes: scopes})
	}
	return keys
}

type rawTLS struct {
	CACert             string `yaml:"ca_cert"`
	ClientCert         string `yaml:"client_cert"`
	ClientKey          string `yaml:"client_key"`
	ServerName         string `yaml:"server_name"`
	InsecureSkipVerify bool   `yaml:"insecure_skip_verify"`
}

// rawConfig is the YAML shape. Pointers distinguish an omitted field (use the
// default) from an explicit zero (validate it).
type rawConfig struct {
	Server struct {
		Addr string `yaml:"addr"`
	} `yaml:"server"`
	Logging struct {
		Level string `yaml:"level"`
	} `yaml:"logging"`
	Metrics  hookconfig.MetricsConfig `yaml:"metrics"`
	Database struct {
		URL      string `yaml:"url"`
		MaxConns *int32 `yaml:"max_conns"`
		TLS      rawTLS `yaml:"tls"`
	} `yaml:"database"`
	Core struct {
		ControlURL   string `yaml:"control_url"`
		ControlToken string `yaml:"control_token"`
		TLS          rawTLS `yaml:"tls"`
	} `yaml:"core"`
	Retention struct {
		TTL            string  `yaml:"ttl"`
		MaxRecords     *int64  `yaml:"max_records"`
		MaxBytes       *int64  `yaml:"max_bytes"`
		MaxRecordBytes *int64  `yaml:"max_record_bytes"`
		GCInterval     *string `yaml:"gc_interval"`
	} `yaml:"retention"`
	Workers struct {
		Concurrency      *int   `yaml:"concurrency"`
		ExecutionTimeout string `yaml:"execution_timeout"`
		Lease            string `yaml:"lease"`
	} `yaml:"workers"`
	Keys []struct {
		Name   string   `yaml:"name"`
		Token  string   `yaml:"token"`
		Scopes []string `yaml:"scopes"`
	} `yaml:"keys"`
}

type issues []hookconfig.ValidationIssue

func (is *issues) errorf(field, format string, args ...any) {
	*is = append(*is, hookconfig.ValidationIssue{Field: field, Message: fmt.Sprintf(format, args...)})
}

func (is *issues) warnf(field, format string, args ...any) {
	*is = append(*is, hookconfig.ValidationIssue{Field: field, Message: fmt.Sprintf(format, args...), Severity: hookconfig.SeverityWarning})
}

// Load reads and validates the file at path.
func Load(path string) (Config, []hookconfig.ValidationIssue) {
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return Config{}, []hookconfig.ValidationIssue{{Field: "config", Message: fmt.Sprintf("read %s: %v", path, err)}}
	}
	return LoadBytes(data)
}

// LoadBytes expands ${VAR} references strictly, decodes with unknown fields
// rejected, applies defaults, and validates. Any issue that is not a warning
// means the Config must not be used.
func LoadBytes(data []byte) (Config, []hookconfig.ValidationIssue) {
	expanded, missing := hookconfig.ExpandEnvStrict(string(data))
	if len(missing) > 0 {
		var is issues
		for _, name := range missing {
			is.errorf("config.env."+name, "environment variable %s is referenced but not set", name)
		}
		return Config{}, is
	}
	var raw rawConfig
	dec := yaml.NewDecoder(strings.NewReader(expanded))
	dec.KnownFields(true)
	if err := dec.Decode(&raw); err != nil && !errors.Is(err, io.EOF) {
		return Config{}, []hookconfig.ValidationIssue{{Field: "config", Message: err.Error()}}
	}
	var is issues
	cfg := raw.resolve(&is)
	cfg.validate(&is)
	return cfg, is
}

func (raw rawConfig) resolve(is *issues) Config {
	cfg := Config{
		Server:   ServerConfig{Addr: raw.Server.Addr},
		Logging:  LoggingConfig{Level: raw.Logging.Level},
		Metrics:  raw.Metrics,
		Database: DatabaseConfig{URL: raw.Database.URL, MaxConns: defaultMaxConns, TLS: TLSConfig(raw.Database.TLS)},
		Core:     CoreConfig{ControlURL: raw.Core.ControlURL, ControlToken: raw.Core.ControlToken, TLS: TLSConfig(raw.Core.TLS)},
		Retention: RetentionConfig{
			TTL: duration(is, "retention.ttl", raw.Retention.TTL, defaultTTL), MaxRecords: defaultMaxRecords,
			MaxBytes: defaultMaxBytes, MaxRecordBytes: defaultMaxRecordBytes, GCInterval: defaultGCInterval,
		},
		Workers: WorkersConfig{
			Concurrency:      defaultConcurrency,
			ExecutionTimeout: duration(is, "workers.execution_timeout", raw.Workers.ExecutionTimeout, defaultExecutionTimeout),
			Lease:            duration(is, "workers.lease", raw.Workers.Lease, defaultLease),
		},
	}
	if cfg.Server.Addr == "" {
		cfg.Server.Addr = defaultAddr
	}
	if raw.Database.MaxConns != nil {
		cfg.Database.MaxConns = *raw.Database.MaxConns
	}
	if raw.Retention.MaxRecords != nil {
		cfg.Retention.MaxRecords = *raw.Retention.MaxRecords
	}
	if raw.Retention.MaxBytes != nil {
		cfg.Retention.MaxBytes = *raw.Retention.MaxBytes
	}
	if raw.Retention.MaxRecordBytes != nil {
		cfg.Retention.MaxRecordBytes = *raw.Retention.MaxRecordBytes
	}
	if raw.Retention.GCInterval != nil {
		cfg.Retention.GCInterval = duration(is, "retention.gc_interval", *raw.Retention.GCInterval, defaultGCInterval)
	}
	if raw.Workers.Concurrency != nil {
		cfg.Workers.Concurrency = *raw.Workers.Concurrency
	}
	for _, k := range raw.Keys {
		cfg.Keys = append(cfg.Keys, KeyConfig{Name: k.Name, Token: k.Token, Scopes: k.Scopes})
	}
	return cfg
}

// duration parses value, returning fallback when it is empty.
func duration(is *issues, field, value string, fallback time.Duration) time.Duration {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	d, err := time.ParseDuration(value)
	if err != nil {
		is.errorf(field, "%q is not a duration (for example 30s, 15m, 168h)", value)
		return fallback
	}
	return d
}

// reservedPath reports whether a metrics path mounted on the API listener
// would shadow or be shadowed by an API route.
func reservedPath(path string) bool {
	return path == "/" || path == "/healthz" || path == "/readyz" || path == "/v1" || strings.HasPrefix(path, "/v1/")
}

func (c Config) validate(is *issues) {
	switch c.Logging.Level {
	case "", "debug", "info", "warn", "error":
	default:
		is.errorf("logging.level", "%q is not supported; use debug, info, warn, or error", c.Logging.Level)
	}
	if c.Metrics.Enabled {
		switch {
		case c.Metrics.Path == "" || c.Metrics.Path[0] != '/':
			is.errorf("metrics.path", "must start with / when metrics are enabled")
		case c.Metrics.Addr == "" && reservedPath(c.Metrics.Path):
			is.errorf("metrics.path", "%s collides with the API on server.addr; choose another path or set metrics.addr", c.Metrics.Path)
		}
		if c.Metrics.Addr != "" && c.Metrics.Addr == c.Server.Addr {
			is.errorf("metrics.addr", "must differ from server.addr (leave it empty to serve metrics on server.addr)")
		}
		if !c.anyKeyHas(auth.ScopeMetrics) {
			is.warnf("metrics", "no key has the metrics scope, so the metrics endpoint is unauthenticated")
		}
	}
	if strings.TrimSpace(c.Database.URL) == "" {
		is.errorf("database.url", "is required")
	}
	if c.Database.MaxConns < 1 {
		is.errorf("database.max_conns", "must be at least 1")
	}
	if u, err := url.Parse(c.Core.ControlURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		is.errorf("core.control_url", "must be an absolute http or https URL")
	}
	if len(c.Core.ControlToken) < minTokenLen {
		is.errorf("core.control_token", "is required and must be at least %d characters", minTokenLen)
	}
	r := c.Retention
	if r.TTL <= 0 {
		is.errorf("retention.ttl", "must be positive")
	}
	if r.MaxRecords < 1 {
		is.errorf("retention.max_records", "must be at least 1")
	}
	if r.MaxBytes < 1 {
		is.errorf("retention.max_bytes", "must be at least 1")
	}
	if r.MaxRecordBytes < 1 || r.MaxRecordBytes > r.MaxBytes {
		is.errorf("retention.max_record_bytes", "must be at least 1 and at most retention.max_bytes")
	}
	if r.GCInterval < 0 {
		is.errorf("retention.gc_interval", "must be >= 0 (0 disables the in-process GC loop)")
	}
	w := c.Workers
	if w.Concurrency < 1 || w.Concurrency > maxConcurrency {
		is.errorf("workers.concurrency", "must be between 1 and %d", maxConcurrency)
	}
	if w.ExecutionTimeout <= executionMargin {
		is.errorf("workers.execution_timeout", "must exceed %s: core's budget for a redrive is execution_timeout minus %s", executionMargin, executionMargin)
	}
	if w.Lease < w.ExecutionTimeout+leaseMargin {
		is.errorf("workers.lease", "must be at least workers.execution_timeout + %s", leaseMargin)
	}
	for _, tls := range []struct {
		field string
		skip  bool
	}{
		{"core.tls.insecure_skip_verify", c.Core.TLS.InsecureSkipVerify},
		{"database.tls.insecure_skip_verify", c.Database.TLS.InsecureSkipVerify},
	} {
		if tls.skip {
			is.warnf(tls.field, "certificate verification is disabled; set a ca_cert instead and do not use this in production")
		}
	}
	c.validateKeys(is)
}

func (c Config) validateKeys(is *issues) {
	if len(c.Keys) == 0 {
		is.errorf("keys", "at least one key is required")
		return
	}
	known := map[string]bool{}
	for _, s := range auth.Scopes() {
		known[string(s)] = true
	}
	names := map[string]int{}
	tokens := map[string]int{}
	for i, k := range c.Keys {
		prefix := fmt.Sprintf("keys[%d]", i)
		if !keyNamePattern.MatchString(k.Name) {
			is.errorf(prefix+".name", "must be 1-64 lowercase letters, digits, '.', '_', or '-', starting with a letter or digit")
		} else if j, dup := names[k.Name]; dup {
			is.errorf(prefix+".name", "duplicates keys[%d].name", j)
		} else {
			names[k.Name] = i
		}
		switch j, dup := tokens[k.Token]; {
		case len(k.Token) < minTokenLen:
			is.errorf(prefix+".token", "must be at least %d characters", minTokenLen)
		case dup:
			is.errorf(prefix+".token", "is the same token as keys[%d].token", j)
		case k.Token == c.Core.ControlToken:
			is.errorf(prefix+".token", "must not reuse core.control_token")
		default:
			tokens[k.Token] = i
		}
		if len(k.Scopes) == 0 {
			is.errorf(prefix+".scopes", "at least one scope is required")
		}
		seen := map[string]bool{}
		for _, s := range k.Scopes {
			switch {
			case !known[s]:
				is.errorf(prefix+".scopes", "unknown scope %q; use capture, read, payload, retry, or metrics", s)
			case seen[s]:
				is.errorf(prefix+".scopes", "scope %q is listed twice", s)
			}
			seen[s] = true
		}
	}
}

func (c Config) anyKeyHas(scope auth.Scope) bool {
	for _, k := range c.Keys {
		for _, s := range k.Scopes {
			if s == string(scope) {
				return true
			}
		}
	}
	return false
}
