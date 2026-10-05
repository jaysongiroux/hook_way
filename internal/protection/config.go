package protection

import (
	"errors"
	"time"
)

type TLSConfig struct {
	Enabled            bool   `yaml:"enabled" json:"enabled"`
	CACert             string `yaml:"ca_cert" json:"ca_cert"`
	ClientCert         string `yaml:"client_cert" json:"client_cert"`
	ClientKey          string `yaml:"client_key" json:"client_key"`
	ServerName         string `yaml:"server_name" json:"server_name"`
	InsecureSkipVerify bool   `yaml:"insecure_skip_verify" json:"insecure_skip_verify"`
}
type MemoryConfig struct{}
type RedisConfig struct {
	URL        string    `yaml:"url" json:"url"`
	Mode       string    `yaml:"mode" json:"mode" jsonschema:"enum=single,enum=cluster,enum=sentinel"`
	Addrs      []string  `yaml:"addrs" json:"addrs"`
	MasterName string    `yaml:"master_name" json:"master_name"`
	Username   string    `yaml:"username" json:"username"`
	Password   string    `yaml:"password" json:"password"`
	DB         int       `yaml:"db" json:"db"`
	PoolSize   int       `yaml:"pool_size" json:"pool_size"`
	TLS        TLSConfig `yaml:"tls" json:"tls"`
}
type PostgresConfig struct {
	URL      string    `yaml:"url" json:"url"`
	MaxConns int32     `yaml:"max_conns" json:"max_conns"`
	TLS      TLSConfig `yaml:"tls" json:"tls"`
}
type CleanupConfig struct {
	Interval  time.Duration `yaml:"interval" json:"interval" jsonschema:"type=string"`
	Budget    time.Duration `yaml:"budget" json:"budget" jsonschema:"type=string"`
	BatchSize int           `yaml:"batch_size" json:"batch_size"`
}
type Config struct {
	Namespace      string          `yaml:"namespace" json:"namespace" jsonschema:"required"`
	Timeout        time.Duration   `yaml:"timeout" json:"timeout" jsonschema:"type=string"`
	StartupTimeout time.Duration   `yaml:"startup_timeout" json:"startup_timeout" jsonschema:"type=string"`
	Cleanup        CleanupConfig   `yaml:"cleanup" json:"cleanup"`
	Memory         *MemoryConfig   `yaml:"memory,omitempty" json:"memory,omitempty"`
	Redis          *RedisConfig    `yaml:"redis,omitempty" json:"redis,omitempty"`
	Postgres       *PostgresConfig `yaml:"postgres,omitempty" json:"postgres,omitempty"`
}

func (c Config) Defaults() Config {
	if c.Timeout == 0 {
		c.Timeout = 250 * time.Millisecond
	}
	if c.StartupTimeout == 0 {
		c.StartupTimeout = 5 * time.Second
	}
	if c.Cleanup.Interval == 0 {
		c.Cleanup.Interval = 30 * time.Second
	}
	if c.Cleanup.Budget == 0 {
		c.Cleanup.Budget = 100 * time.Millisecond
	}
	if c.Cleanup.BatchSize == 0 {
		c.Cleanup.BatchSize = 100
	}
	if c.Redis != nil {
		v := *c.Redis
		if v.PoolSize == 0 {
			v.PoolSize = 10
		}
		c.Redis = &v
	}
	if c.Postgres != nil {
		v := *c.Postgres
		if v.MaxConns == 0 {
			v.MaxConns = 10
		}
		c.Postgres = &v
	}
	return c
}
func (c Config) Validate() error {
	c = c.Defaults()
	n := 0
	if c.Memory != nil {
		n++
	}
	if c.Redis != nil {
		n++
	}
	if c.Postgres != nil {
		n++
	}
	if n != 1 {
		return errors.New("state store requires exactly one of memory, redis, postgres")
	}
	if !validName(c.Namespace, 128) {
		return errors.New("state store namespace is required and at most 128 bytes")
	}
	if c.Timeout < 10*time.Millisecond || c.Timeout > 5*time.Second || c.StartupTimeout < 100*time.Millisecond || c.StartupTimeout > 60*time.Second {
		return errors.New("state store timeout must be 10ms..5s; startup_timeout 100ms..60s")
	}
	if c.Cleanup.Interval < time.Second || c.Cleanup.Interval > 24*time.Hour || c.Cleanup.Budget < 10*time.Millisecond || c.Cleanup.Budget > 5*time.Second || c.Cleanup.BatchSize < 1 || c.Cleanup.BatchSize > 1000 {
		return errors.New("state store cleanup interval, budget, or batch_size out of range")
	}
	if c.Redis != nil {
		r := c.Redis
		if r.PoolSize < 1 || r.PoolSize > 100 || r.DB < 0 || (r.URL == "" && len(r.Addrs) == 0) || len(r.Addrs) > 100 || (r.Mode != "" && r.Mode != "single" && r.Mode != "cluster" && r.Mode != "sentinel") || (r.Mode == "sentinel" && r.MasterName == "") {
			return errors.New("invalid state store redis connection")
		}
	}
	if c.Postgres != nil && (c.Postgres.URL == "" || c.Postgres.MaxConns < 1 || c.Postgres.MaxConns > 100) {
		return errors.New("invalid state store postgres connection")
	}
	return nil
}
