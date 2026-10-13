package flowconfig

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jaysongiroux/hook_way/internal/config"
	"github.com/jaysongiroux/hook_way/internal/graph"
)

// LookupProviderConfig is authoring-safe: SDKs and live clients belong to the native server.
type LookupProviderConfig struct {
	Timeout  string                `yaml:"timeout,omitempty" json:"timeout,omitempty" jsonschema:"default=2s"`
	Cache    LookupProviderCache   `yaml:"cache,omitempty" json:"cache,omitempty"`
	HTTP     *HTTPLookupConfig     `yaml:"http,omitempty" json:"http,omitempty"`
	Postgres *PostgresLookupConfig `yaml:"postgres,omitempty" json:"postgres,omitempty"`
	DynamoDB *DynamoDBLookupConfig `yaml:"dynamodb,omitempty" json:"dynamodb,omitempty"`
}
type LookupProviderCache struct {
	TTL         string `yaml:"ttl,omitempty" json:"ttl,omitempty" jsonschema:"default=5m"`
	NegativeTTL string `yaml:"negative_ttl,omitempty" json:"negative_ttl,omitempty" jsonschema:"default=0s"`
	MaxEntries  *int   `yaml:"max_entries,omitempty" json:"max_entries,omitempty" jsonschema:"minimum=1,maximum=1000000,default=10000"`
}
type HTTPLookupConfig struct {
	URL                 string                      `yaml:"url" json:"url" jsonschema:"required"`
	Method              string                      `yaml:"method,omitempty" json:"method,omitempty" jsonschema:"enum=GET,enum=POST,default=GET"`
	Headers             map[string]string           `yaml:"headers,omitempty" json:"headers,omitempty"`
	SecretHeaders       map[string]CredentialHeader `yaml:"secret_headers,omitempty" json:"secret_headers,omitempty"`
	ValuePath           string                      `yaml:"value_path" json:"value_path" jsonschema:"required"`
	AllowPrivateTargets bool                        `yaml:"allow_private_targets,omitempty" json:"allow_private_targets,omitempty"`
}
type PostgresLookupConfig struct {
	URL   string `yaml:"url" json:"url" jsonschema:"required"`
	Query string `yaml:"query" json:"query" jsonschema:"required"`
}
type DynamoDBLookupConfig struct {
	Table           string `yaml:"table" json:"table" jsonschema:"required"`
	Index           string `yaml:"index,omitempty" json:"index,omitempty"`
	KeyAttribute    string `yaml:"key_attribute,omitempty" json:"key_attribute,omitempty" jsonschema:"default=id"`
	ValueAttribute  string `yaml:"value_attribute" json:"value_attribute" jsonschema:"required"`
	FilterAttribute string `yaml:"filter_attribute,omitempty" json:"filter_attribute,omitempty"`
	FilterValue     string `yaml:"filter_value,omitempty" json:"filter_value,omitempty"`
	ConsistentRead  bool   `yaml:"consistent_read,omitempty" json:"consistent_read,omitempty"`
	PageLimit       int32  `yaml:"page_limit,omitempty" json:"page_limit,omitempty" jsonschema:"minimum=1,maximum=1000,default=25"`
	Region          string `yaml:"region,omitempty" json:"region,omitempty"`
	Endpoint        string `yaml:"endpoint,omitempty" json:"endpoint,omitempty"`
}

func (p LookupProviderConfig) Validate() error {
	count := 0
	if p.HTTP != nil {
		count++
	}
	if p.Postgres != nil {
		count++
	}
	if p.DynamoDB != nil {
		count++
	}
	if count != 1 {
		return errors.New("configure exactly one http, postgres, or dynamodb provider")
	}
	for _, v := range []struct {
		name, value string
		allowZero   bool
		maximum     time.Duration
	}{
		{"timeout", p.Timeout, false, 30 * time.Second}, {"cache.ttl", p.Cache.TTL, false, 24 * time.Hour}, {"cache.negative_ttl", p.Cache.NegativeTTL, true, 24 * time.Hour},
	} {
		if v.value != "" {
			d, e := time.ParseDuration(v.value)
			if e != nil || d < 0 || (!v.allowZero && d == 0) || d > v.maximum {
				minimum := "positive"
				if v.allowZero {
					minimum = "non-negative"
				}
				return fmt.Errorf("%s must be a %s duration of at most %s", v.name, minimum, v.maximum)
			}
		}
	}
	if p.Cache.MaxEntries != nil && (*p.Cache.MaxEntries < 1 || *p.Cache.MaxEntries > 1000000) {
		return errors.New("cache.max_entries must be between 1 and 1000000")
	}
	if h := p.HTTP; h != nil {
		if strings.Count(h.URL, "{key}") != 1 {
			return errors.New("http.url requires exactly one {key} in its path or query")
		}
		// Authoring preserves environment references; serving validates after strict expansion.
		if !strings.Contains(h.URL, "${") {
			u, e := url.Parse(strings.ReplaceAll(h.URL, "{key}", "LOOKUPKEY"))
			if e != nil || u.Hostname() == "" || u.User != nil || u.Fragment != "" || strings.Contains(urlAuthority(h.URL), "{key}") {
				return errors.New("http.url requires a fixed host, no userinfo or fragment, and {key} in the path or query")
			}
			if _, e := CredentialOrigin(u.Scheme+"://"+u.Host, h.AllowPrivateTargets); e != nil {
				return fmt.Errorf("http.url: %w", e)
			}
		}
		if h.Method != "" && h.Method != "GET" && h.Method != "POST" {
			return errors.New("http.method must be GET or POST")
		}
		if strings.TrimSpace(h.ValuePath) == "" {
			return errors.New("http.value_path is required")
		}
		if len(h.Headers)+len(h.SecretHeaders) > 32 {
			return errors.New("http headers exceed 32")
		}
		seen := map[string]bool{}
		for name, value := range h.Headers {
			canon := http.CanonicalHeaderKey(name)
			if !validAuthHeader(name) || isHopByHop(canon) || canon == "Host" || canon == "Content-Length" || strings.ContainsAny(value, "\r\n\x00") || seen[canon] {
				return errors.New("invalid or duplicate http header")
			}
			seen[canon] = true
		}
		for name, value := range h.SecretHeaders {
			canon := http.CanonicalHeaderKey(name)
			if !validAuthHeader(name) || isHopByHop(canon) || canon == "Host" || canon == "Content-Length" || strings.ContainsAny(value.Prefix, "\r\n\x00") || seen[canon] {
				return errors.New("invalid or duplicate secret header")
			}
			seen[canon] = true
			if e := validateCredentialSecret(value.SecretRef); e != nil {
				return e
			}
		}
	}
	if p.Postgres != nil {
		u, e := url.Parse(p.Postgres.URL)
		if !strings.Contains(p.Postgres.URL, "${") && (e != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Host == "") {
			return errors.New("postgres.url requires a PostgreSQL connection URL (use an environment reference for credentials)")
		}
		if strings.Count(p.Postgres.Query, "{key}") != 1 || strings.Contains(p.Postgres.Query, "$1") {
			return errors.New("postgres.query requires exactly one {key} parameter; do not use $1")
		}
	}
	if d := p.DynamoDB; d != nil {
		if d.Table == "" || d.ValueAttribute == "" {
			return errors.New("dynamodb.table and value_attribute are required")
		}
		if d.PageLimit < 0 || d.PageLimit > 1000 {
			return errors.New("dynamodb.page_limit must be between 1 and 1000 when set")
		}
		if (d.FilterAttribute == "") != (d.FilterValue == "") {
			return errors.New("dynamodb.filter_attribute and filter_value must be set together")
		}
		if d.Endpoint != "" && !strings.Contains(d.Endpoint, "${") {
			if e := validateHTTPURL(d.Endpoint); e != nil {
				return errors.New("dynamodb.endpoint must be an HTTP URL")
			}
		}
	}
	return nil
}

func LookupConfigurationIssues(cfg Config) []config.ValidationIssue {
	var issues []config.ValidationIssue
	for _, name := range sortedKeys(cfg.Lookups) {
		if !graph.ValidID(name) {
			issues = append(issues, cfgIssue("lookups."+name, "invalid lookup provider name", ""))
		}
		if e := cfg.Lookups[name].Validate(); e != nil {
			issues = append(issues, cfgIssue("lookups."+name, e.Error(), ""))
		}
	}
	return issues
}
