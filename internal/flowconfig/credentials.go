package flowconfig

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jaysongiroux/hook_way/internal/config"
	"github.com/jaysongiroux/hook_way/internal/graph"
	"github.com/jaysongiroux/hook_way/internal/routing"
)

var ErrCredentialConfiguration = errors.New("outbound credential configuration")

// CredentialProfile contains references only. Resolved values never enter the
// graph, JavaScript input, delivery snapshots or authoring exports.
type CredentialProfile struct {
	Kind                string                      `yaml:"kind" json:"kind"`
	AllowedOrigins      []string                    `yaml:"allowed_origins" json:"allowed_origins"`
	AllowPrivateTargets bool                        `yaml:"allow_private_targets,omitempty" json:"allow_private_targets,omitempty"`
	Headers             map[string]CredentialHeader `yaml:"headers,omitempty" json:"headers,omitempty"`
	TokenURL            string                      `yaml:"token_url,omitempty" json:"token_url,omitempty"`
	ClientID            *SecretRefOptions           `yaml:"client_id,omitempty" json:"client_id,omitempty"`
	ClientSecret        *SecretRefOptions           `yaml:"client_secret,omitempty" json:"client_secret,omitempty"`
	ClientAuth          string                      `yaml:"client_auth,omitempty" json:"client_auth,omitempty"` // basic (default) or form
	Scope               string                      `yaml:"scope,omitempty" json:"scope,omitempty"`
	Parameters          map[string]string           `yaml:"parameters,omitempty" json:"parameters,omitempty"`
	SecretParameters    map[string]SecretRefOptions `yaml:"secret_parameters,omitempty" json:"secret_parameters,omitempty"`
	RequestFormat       string                      `yaml:"request_format,omitempty" json:"request_format,omitempty"` // form (default) or json
	TokenPath           string                      `yaml:"token_path,omitempty" json:"token_path,omitempty"`
	InstancePath        string                      `yaml:"instance_path,omitempty" json:"instance_path,omitempty"`
	ExpiryPath          string                      `yaml:"expiry_path,omitempty" json:"expiry_path,omitempty"`
	TTL                 string                      `yaml:"ttl,omitempty" json:"ttl,omitempty"`
	Timeout             string                      `yaml:"timeout,omitempty" json:"timeout,omitempty"`
	InvalidSession      *SessionMarker              `yaml:"invalid_session,omitempty" json:"invalid_session,omitempty"`
}
type CredentialHeader struct {
	SecretRef SecretRefOptions `yaml:"secret_ref" json:"secret_ref"`
	Prefix    string           `yaml:"prefix,omitempty" json:"prefix,omitempty"`
}
type SessionMarker struct {
	Path  string `yaml:"path" json:"path"` // object keys or decimal array indexes
	Value string `yaml:"value" json:"value"`
}
type HTTPCredentialOptions struct {
	Profile             string            `yaml:"profile,omitempty" json:"profile,omitempty"`
	EnvironmentProfiles map[string]string `yaml:"environment_profiles,omitempty" json:"environment_profiles,omitempty"`
	RoutingProfile      string            `yaml:"routing_profile,omitempty" json:"routing_profile,omitempty"`
	EnvironmentVariable string            `yaml:"environment_variable,omitempty" json:"environment_variable,omitempty"`
}

// CredentialMaterial is a native HTTP boundary value, never a graph payload.
type CredentialMaterial struct {
	Header     http.Header
	Origin     string
	Generation uint64
	Guard      bool
}
type CredentialProvider interface {
	Acquire(context.Context) (CredentialMaterial, error)
	Target(CredentialMaterial, string) (string, error)
	Reject(CredentialMaterial, int, []byte) bool
}

func CredentialOrigin(value string, allowPrivate bool) (string, error) {
	u, err := url.Parse(value)
	if err != nil || u.Host == "" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || (u.Scheme != "https" && (!allowPrivate || u.Scheme != "http")) {
		return "", errors.New("credential origin requires an exact HTTPS origin (HTTP requires explicit private-target permission)")
	}
	if err := validateEgressURL(u.String(), !allowPrivate); err != nil {
		return "", errors.New("credential origin denied")
	}
	return strings.ToLower(u.Scheme + "://" + u.Host), nil
}
func validateCredentialSecret(ref SecretRefOptions) error {
	if ref.Provider != "env" && ref.Provider != "file" && ref.Provider != "aws_secrets_manager" {
		return errors.New("invalid credential secret provider")
	}
	if ref.Name == "" {
		return errors.New("credential secret name is required")
	}
	if ref.Selection != "" && ref.Selection != "exact" && ref.Selection != "newest_matching" {
		return errors.New("invalid secret selection policy")
	}
	if ref.Selection != "" && ref.Provider != "aws_secrets_manager" {
		return errors.New("selection requires aws_secrets_manager")
	}
	for _, value := range []string{ref.Refresh, ref.Overlap} {
		if value != "" {
			d, err := time.ParseDuration(value)
			if err != nil || d < 0 || d > 24*time.Hour {
				return errors.New("invalid credential refresh/overlap duration")
			}
		}
	}
	return nil
}
func (p CredentialProfile) Validate() error {
	if len(p.SecretParameters) > 32 || len(p.Parameters) > 32 || len(p.Headers) > 32 {
		return errors.New("credential fields exceed 32")
	}
	if len(p.AllowedOrigins) == 0 || len(p.AllowedOrigins) > 64 {
		return errors.New("1..64 allowed_origins required")
	}
	for _, o := range p.AllowedOrigins {
		if _, err := CredentialOrigin(o, p.AllowPrivateTargets); err != nil {
			return err
		}
	}
	if p.Kind != "static" && p.Kind != "oauth_client_credentials" && p.Kind != "token_exchange" {
		return errors.New("invalid credential kind")
	}
	if p.Timeout != "" {
		d, e := time.ParseDuration(p.Timeout)
		if e != nil || d <= 0 || d > 30*time.Second {
			return errors.New("credential timeout must be positive and <=30s")
		}
	}
	seenHeaders := map[string]bool{}
	for k, h := range p.Headers {
		canon := http.CanonicalHeaderKey(k)
		if seenHeaders[canon] {
			return errors.New("duplicate managed header")
		}
		seenHeaders[canon] = true
		if !validAuthHeader(k) || isHopByHop(canon) || strings.EqualFold(k, "Host") || strings.EqualFold(k, "Content-Length") || strings.EqualFold(k, config.LoopHopsHeader) || len(h.Prefix) > 8192 || strings.ContainsAny(h.Prefix, "\r\n\x00") {
			return errors.New("invalid managed header")
		}
		if err := validateCredentialSecret(h.SecretRef); err != nil {
			return err
		}
	}
	if p.Kind == "static" {
		if len(p.Headers) == 0 || p.TokenURL != "" || p.ClientID != nil || p.ClientSecret != nil || len(p.SecretParameters) > 0 || len(p.Parameters) > 0 || p.InvalidSession != nil || p.InstancePath != "" || p.TTL != "" || p.Scope != "" || p.ClientAuth != "" || p.RequestFormat != "" || p.TokenPath != "" || p.ExpiryPath != "" {
			return errors.New("static profile requires headers and forbids token acquisition options")
		}
		return nil
	}
	if _, ok := p.Headers["Authorization"]; ok {
		return errors.New("token provider owns Authorization")
	}
	for k := range p.Headers {
		if strings.EqualFold(k, "Authorization") {
			return errors.New("token provider owns Authorization")
		}
	}
	u, e := url.Parse(p.TokenURL)
	if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && (!p.AllowPrivateTargets || u.Scheme != "http")) {
		return errors.New("invalid token_url")
	}
	if e := validateEgressURL(p.TokenURL, !p.AllowPrivateTargets); e != nil {
		return errors.New("token_url denied")
	}
	if p.ClientAuth != "" && p.ClientAuth != "basic" && p.ClientAuth != "form" {
		return errors.New("client_auth must be basic or form")
	}
	if p.RequestFormat != "" && p.RequestFormat != "form" && p.RequestFormat != "json" {
		return errors.New("request_format must be form or json")
	}
	if p.Kind == "oauth_client_credentials" {
		if p.ClientID == nil || p.ClientSecret == nil {
			return errors.New("client_id and client_secret references required")
		}
		if len(p.SecretParameters) > 0 || len(p.Parameters) > 0 || p.RequestFormat == "json" {
			return errors.New("OAuth client credentials uses the standard form request")
		}
		if e := validateCredentialSecret(*p.ClientID); e != nil {
			return e
		}
		if e := validateCredentialSecret(*p.ClientSecret); e != nil {
			return e
		}
	} else {
		if p.ClientID != nil || p.ClientSecret != nil || p.InvalidSession != nil || p.InstancePath != "" || p.TTL != "" || p.ExpiryPath != "" || p.Scope != "" || p.ClientAuth != "" {
			return errors.New("token_exchange is per-request and forbids session options")
		}
		if p.TokenPath == "" || len(p.SecretParameters) == 0 {
			return errors.New("token_exchange needs token_path and secret_parameters")
		}
	}
	for _, path := range []string{p.TokenPath, p.InstancePath, p.ExpiryPath} {
		if len(path) > 256 || strings.ContainsAny(path, "\r\n") {
			return errors.New("invalid credential response path")
		}
	}
	for k, v := range p.Parameters {
		if k == "" || len(k) > 256 || len(v) > 8192 || sensitiveResponseField(k) {
			return errors.New("credential parameters must be bounded nonsecret constants; use secret_parameters for credentials")
		}
	}
	for k, r := range p.SecretParameters {
		if k == "" || len(k) > 256 || strings.ContainsAny(k, "\r\n\x00") {
			return errors.New("invalid token parameter name")
		}
		if _, ok := p.Parameters[k]; ok {
			return errors.New("duplicate token parameter")
		}
		if e := validateCredentialSecret(r); e != nil {
			return e
		}
	}
	if p.TTL != "" {
		d, e := time.ParseDuration(p.TTL)
		if e != nil || d < time.Second || d > 24*time.Hour {
			return errors.New("credential ttl must be 1s..24h")
		}
	}
	if p.InvalidSession != nil && (p.InvalidSession.Path == "" || p.InvalidSession.Value == "" || len(p.InvalidSession.Path) > 256 || len(p.InvalidSession.Value) > 256) {
		return errors.New("invalid session marker")
	}
	return nil
}
func CredentialConfigurationIssues(cfg Config) []config.ValidationIssue {
	var issues []config.ValidationIssue
	if len(cfg.Credentials) > 64 {
		issues = append(issues, cfgIssue("credentials", "at most 64 profiles are supported", ""))
	}
	for _, name := range sortedKeys(cfg.Credentials) {
		p := cfg.Credentials[name]
		if !graph.ValidID(name) {
			issues = append(issues, cfgIssue("credentials", "invalid profile name", ""))
		}
		if err := p.Validate(); err != nil {
			issues = append(issues, cfgIssue("credentials."+name, err.Error(), ""))
		}
	}
	registry, _ := routing.NewRegistry(cfg.Routing)
	for _, name := range sortedKeys(cfg.Flows) {
		for _, nc := range cfg.Flows[name].Nodes {
			if nc.Type != "http_request" {
				continue
			}
			o, e := decodeOptions[HTTPRequestOptions](nc)
			if e != nil || o.Credential == nil {
				continue
			}
			c := o.Credential
			field := "flows." + name + ".nodes." + nc.ID
			if c.Profile != "" {
				if _, ok := cfg.Credentials[c.Profile]; !ok {
					issues = append(issues, cfgIssue(field, "credential profile is not configured", ""))
				}
			}
			for env, profile := range c.EnvironmentProfiles {
				if _, ok := cfg.Credentials[profile]; !ok {
					issues = append(issues, cfgIssue(field, "credential environment profile is not configured", ""))
				}
				if registry == nil {
					continue
				}
				b, e := registry.Admit(env, c.RoutingProfile)
				if e != nil || b.Name != env {
					issues = append(issues, cfgIssue(field, "credential environment must be canonical and admitted", ""))
				}
			}
		}
	}
	return issues
}
func validateHTTPCredential(c *HTTPCredentialOptions) error {
	if c == nil {
		return nil
	}
	if (c.Profile != "") == (len(c.EnvironmentProfiles) > 0) {
		return errors.New("credential requires exactly one profile or environment_profiles")
	}
	if c.Profile != "" {
		if c.RoutingProfile != "" || c.EnvironmentVariable != "" {
			return errors.New("fixed credential cannot select environment")
		}
		return nil
	}
	if c.RoutingProfile == "" || !graph.ValidID(c.EnvironmentVariable) {
		return errors.New("environment credentials require routing_profile and environment_variable")
	}
	return nil
}
func NewCredentialHTTPClient(guard bool) *http.Client { return newProxyHTTPClient(guard) }
func (n httpRequestNode) credentialProvider(env graph.Envelope) (CredentialProvider, error) {
	c := n.opts.Credential
	if c == nil {
		return nil, nil
	}
	profile := c.Profile
	if profile == "" {
		if n.registry == nil {
			return nil, ErrCredentialConfiguration
		}
		b, e := n.registry.Admit(env.Vars[c.EnvironmentVariable], c.RoutingProfile)
		if e != nil {
			return nil, errors.New("credential environment denied")
		}
		profile = c.EnvironmentProfiles[b.Name]
	}
	provider := n.credentials[profile]
	if provider == nil {
		return nil, fmt.Errorf("%w: credential profile unavailable", ErrCredentialConfiguration)
	}
	return provider, nil
}
