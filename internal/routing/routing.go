// Package routing defines typed routing contracts without native dependencies.
package routing

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

type Key struct {
	ID     string `json:"id"`
	IDType string `json:"id_type"`
}
type Record struct {
	Key
	Environment string `json:"env_name"`
	CreatedAt   int64  `json:"created_at"`
}
type Reader interface {
	Get(context.Context, Key) (Record, bool, error)
}
type Writer interface {
	Put(context.Context, Record) error
}

// StoreConfig uses the normal AWS credential chain. No credentials are retained
// in the document; endpoint is an optional local-test override.
type StoreConfig struct {
	Table            string `yaml:"table" json:"table"`
	Region           string `yaml:"region,omitempty" json:"region,omitempty"`
	Endpoint         string `yaml:"endpoint,omitempty" json:"endpoint,omitempty"`
	Timeout          string `yaml:"timeout,omitempty" json:"timeout,omitempty"`
	IDField          string `yaml:"id_field,omitempty" json:"id_field,omitempty"`
	TypeField        string `yaml:"type_field,omitempty" json:"type_field,omitempty"`
	EnvironmentField string `yaml:"environment_field,omitempty" json:"environment_field,omitempty"`
	CreatedAtField   string `yaml:"created_at_field,omitempty" json:"created_at_field,omitempty"`
}
type Profile struct {
	AccountID      string `yaml:"account_id" json:"account_id"`
	CredentialMode string `yaml:"credential_mode" json:"credential_mode"`
}
type Environment struct {
	Aliases           []string `yaml:"aliases,omitempty" json:"aliases,omitempty"`
	Profiles          []string `yaml:"profiles" json:"profiles"`
	Accounts          []string `yaml:"accounts" json:"accounts"`
	DestinationPrefix string   `yaml:"destination_prefix" json:"destination_prefix"`
	BackendHost       string   `yaml:"backend_host,omitempty" json:"backend_host,omitempty"`
}
type Config struct {
	Stores       map[string]StoreConfig `yaml:"stores,omitempty" json:"stores,omitempty"`
	Profiles     map[string]Profile     `yaml:"profiles,omitempty" json:"profiles,omitempty"`
	Environments map[string]Environment `yaml:"environments,omitempty" json:"environments,omitempty"`
}
type Binding struct{ Name, DestinationPrefix, BackendHost, AccountID, CredentialMode string }

var ErrDenied = errors.New("environment is unknown or not admitted for this profile")
var namePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
var accountPattern = regexp.MustCompile(`^[0-9]{12}$`)
var typePattern = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_-]{0,63}$`)
var prefixPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)
var hostLabelPattern = regexp.MustCompile(`^[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$`)

func ValidateKey(k Key) error {
	if k.ID == "" || len(k.ID) > 512 || !utf8.ValidString(k.ID) || strings.IndexFunc(k.ID, unicode.IsControl) >= 0 || !typePattern.MatchString(k.IDType) {
		return errors.New("invalid typed routing key")
	}
	return nil
}
func ValidateRecord(r Record) error {
	if err := ValidateKey(r.Key); err != nil {
		return err
	}
	if !namePattern.MatchString(r.Environment) || r.CreatedAt < 0 {
		return errors.New("invalid routing record")
	}
	return nil
}
func (s StoreConfig) Defaults() StoreConfig {
	if s.IDField == "" {
		s.IDField = "id"
	}
	if s.TypeField == "" {
		s.TypeField = "id_type"
	}
	if s.EnvironmentField == "" {
		s.EnvironmentField = "env_name"
	}
	if s.CreatedAtField == "" {
		s.CreatedAtField = "created_at"
	}
	if s.Timeout == "" {
		s.Timeout = "2s"
	}
	return s
}
func (s StoreConfig) Validate() error {
	s = s.Defaults()
	if strings.TrimSpace(s.Table) == "" {
		return errors.New("table is required")
	}
	d, err := time.ParseDuration(s.Timeout)
	if err != nil || d <= 0 || d > 30*time.Second {
		return errors.New("timeout must be positive and at most 30s")
	}
	fields := []string{s.IDField, s.TypeField, s.EnvironmentField, s.CreatedAtField}
	seen := map[string]bool{}
	for _, f := range fields {
		if f == "" || len(f) > 255 || seen[f] {
			return errors.New("routing fields must be distinct nonempty names")
		}
		seen[f] = true
	}
	if s.Endpoint != "" {
		u, err := url.Parse(s.Endpoint)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return errors.New("endpoint must be an HTTP(S) URL without credentials, query or fragment")
		}
	}
	return nil
}

// Registry.Replace atomically installs a validated allowlist. Serving uses a
// configuration snapshot; process restarts deploy changes to all replicas.
// Embedders may replace the snapshot explicitly, without routing-row caches.
type Registry struct {
	mu           sync.RWMutex
	profiles     map[string]Profile
	environments map[string]Environment
	aliases      map[string]string
}

func NewRegistry(c Config) (*Registry, error) {
	r := &Registry{}
	if err := r.Replace(c); err != nil {
		return nil, err
	}
	return r, nil
}
func (r *Registry) Replace(c Config) error {
	aliases := map[string]string{}
	envs := map[string]Environment{}
	for n, p := range c.Profiles {
		if !namePattern.MatchString(n) || !accountPattern.MatchString(p.AccountID) || !namePattern.MatchString(p.CredentialMode) {
			return fmt.Errorf("invalid routing profile %q", n)
		}
	}
	for n := range c.Environments {
		if !namePattern.MatchString(n) {
			return fmt.Errorf("invalid environment name %q", n)
		}
		aliases[n] = n
	}
	for n, e := range c.Environments {
		if !prefixPattern.MatchString(e.DestinationPrefix) || len(e.Profiles) == 0 || len(e.Accounts) == 0 {
			return fmt.Errorf("environment %q requires prefix, profiles and accounts", n)
		}
		for _, a := range e.Accounts {
			if !accountPattern.MatchString(a) {
				return fmt.Errorf("environment %q has invalid account", n)
			}
		}
		for _, p := range e.Profiles {
			profile, ok := c.Profiles[p]
			if !ok || !slices.Contains(e.Accounts, profile.AccountID) {
				return fmt.Errorf("environment %q has unknown or disallowed profile %q", n, p)
			}
		}
		if e.BackendHost != "" {
			u, err := url.Parse("https://" + e.BackendHost)
			if err != nil || u.Hostname() != e.BackendHost || u.Path != "" || u.User != nil || !validHost(e.BackendHost) {
				return fmt.Errorf("environment %q backend_host must be a DNS host", n)
			}
		}
		for _, a := range e.Aliases {
			if !namePattern.MatchString(a) || aliases[a] != "" {
				return fmt.Errorf("duplicate or invalid environment alias %q", a)
			}
			aliases[a] = n
		}
		e.Aliases = slices.Clone(e.Aliases)
		e.Profiles = slices.Clone(e.Profiles)
		e.Accounts = slices.Clone(e.Accounts)
		envs[n] = e
	}
	for n, s := range c.Stores {
		if !namePattern.MatchString(n) {
			return fmt.Errorf("invalid routing store name %q", n)
		}
		if err := s.Validate(); err != nil {
			return fmt.Errorf("routing store %q: %w", n, err)
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.profiles = maps.Clone(c.Profiles)
	r.environments = envs
	r.aliases = aliases
	return nil
}
func validHost(h string) bool {
	if len(h) > 253 {
		return false
	}
	for _, p := range strings.Split(h, ".") {
		if !hostLabelPattern.MatchString(p) {
			return false
		}
	}
	return true
}
func (r *Registry) Admit(name, profile string) (Binding, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	n := r.aliases[name]
	e, ok := r.environments[n]
	p, pok := r.profiles[profile]
	if !ok || !pok || !slices.Contains(e.Profiles, profile) || !slices.Contains(e.Accounts, p.AccountID) {
		return Binding{}, ErrDenied
	}
	return Binding{n, e.DestinationPrefix, e.BackendHost, p.AccountID, p.CredentialMode}, nil
}

func (r *Registry) HasProfile(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.profiles[name]
	return ok
}

// CanonicalEnvironment resolves an alias for comparison without granting a
// binding. Dedicated batch filtering still calls Admit before publication.
func (r *Registry) CanonicalEnvironment(name string) (string, bool) {
	if r == nil {
		return "", false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	canonical, ok := r.aliases[name]
	return canonical, ok
}
