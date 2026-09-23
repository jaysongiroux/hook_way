package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// SharedConfig is the process-scoped configuration: server, security, metrics,
// logging, replay, and destination connections. Rate limiting is a flow node,
// not a top-level limiter, and request pipelines are named flows.
type SharedConfig struct {
	Server       ServerConfig
	Security     SecurityConfig
	Metrics      MetricsConfig
	Logging      LoggingConfig
	Replay       ReplayConfig
	Destinations map[string]DestinationConfig
	// ConnectionOf maps an owner path ("destinations.events", "replay") to the
	// connection it resolved through. Only the name and type are kept: the
	// values themselves have already been merged into Destinations and Replay,
	// and retaining them would put a second copy of every credential in memory
	// and a second thing that could be logged or serialized. ValidationIssues
	// uses this to re-point issues at the path the operator actually wrote.
	ConnectionOf map[string]ConnectionRef
}

// ConnectionRef identifies the connection an owner resolved through. It
// deliberately carries no credential material.
type ConnectionRef struct {
	Name string
	Type string
}

// rawServerConfig is the authored YAML shape. Runtime ServerConfig cannot be
// decoded directly because durations are strings on disk and defaults are
// applied during conversion.
type rawServerConfig = struct {
	Addr                string `yaml:"addr"`
	ReadTimeout         string `yaml:"read_timeout"`
	WriteTimeout        string `yaml:"write_timeout"`
	IdleTimeout         string `yaml:"idle_timeout"`
	ShutdownTimeout     string `yaml:"shutdown_timeout"`
	PublishTimeout      string `yaml:"publish_timeout"`
	MaxBodyBytes        int64  `yaml:"max_body_bytes"`
	MaxInflightRequests int    `yaml:"max_inflight_requests"`
	FanoutConcurrency   int    `yaml:"fanout_concurrency"`
	ReadinessInterval   string `yaml:"readiness_interval"`
	ReadinessTimeout    string `yaml:"readiness_timeout"`
	ReadinessRequireAll bool   `yaml:"readiness_require_all"`
}

type rawReplayConfig = struct {
	Enabled             bool   `yaml:"enabled"`
	Addr                string `yaml:"addr"`
	SkipUnauthenticated bool   `yaml:"skip_unauthenticated"`
	TTL                 string `yaml:"ttl"`
	MaxEvents           int64  `yaml:"max_events"`
	CleanupInterval     string `yaml:"cleanup_interval"`
	// Connection names a postgres entry in the top-level connections block,
	// spelled the same way a destination references one. The connection owns
	// url / max_conns / tls; the postgres block below keeps schema and table.
	Connection string            `yaml:"connection"`
	Postgres   rawPostgresConfig `yaml:"postgres"`
}

type rawSharedConfig struct {
	Server       rawServerConfig                 `yaml:"server"`
	Security     SecurityConfig                  `yaml:"security"`
	Metrics      MetricsConfig                   `yaml:"metrics"`
	Logging      LoggingConfig                   `yaml:"logging"`
	Replay       rawReplayConfig                 `yaml:"replay"`
	Connections  map[string]rawConnectionConfig  `yaml:"connections"`
	Destinations map[string]rawDestinationConfig `yaml:"destinations"`
}

var sharedYAMLFields = map[string]struct{}{
	"server": {}, "security": {}, "metrics": {}, "logging": {},
	"replay": {}, "connections": {}, "destinations": {},
}

// DecodeSharedYAML strictly decodes the common process-scoped sections from a
// complete config document. allowedExtras names top-level keys owned by the
// caller (flows). Their values are ignored here and must be decoded by the
// flow loader.
func DecodeSharedYAML(data []byte, source string, allowedExtras ...string) (SharedConfig, []ValidationIssue) {
	allowed := make(map[string]struct{}, len(sharedYAMLFields)+len(allowedExtras))
	for field := range sharedYAMLFields {
		allowed[field] = struct{}{}
	}
	for _, field := range allowedExtras {
		allowed[field] = struct{}{}
	}

	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return SharedConfig{}, normalizeIssueSeverities([]ValidationIssue{yamlIssue(source, err)})
	}
	if len(document.Content) == 0 {
		shared, issues := convertShared(rawSharedConfig{})
		return shared, normalizeIssueSeverities(issues)
	}
	root := document.Content[0]
	if root.Kind != yaml.MappingNode {
		return SharedConfig{}, normalizeIssueSeverities([]ValidationIssue{validationError("config", fmt.Sprintf("invalid YAML structure in %s: top level must be a mapping", source), "")})
	}

	filtered := yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	var issues []ValidationIssue
	for i := 0; i < len(root.Content); i += 2 {
		key, value := root.Content[i], root.Content[i+1]
		if _, ok := allowed[key.Value]; !ok {
			issues = append(issues, validationError(key.Value,
				fmt.Sprintf("unknown or invalid YAML field in %s: field %s not found", source, key.Value), "remove the field or correct its spelling"))
			continue
		}
		if _, shared := sharedYAMLFields[key.Value]; shared {
			filtered.Content = append(filtered.Content, key, value)
		}
	}

	issues = append(issues, unknownYAMLFields(&filtered, reflect.TypeOf(rawSharedConfig{}), "")...)
	// Runs on the node tree rather than the decoded struct: a zero value
	// cannot tell "absent" from "written as 0/false", which is exactly what
	// the connection/destination split has to distinguish.
	issues = append(issues, connectionBindingIssues(&filtered)...)

	encoded, err := yaml.Marshal(&filtered)
	if err != nil {
		return SharedConfig{}, normalizeIssueSeverities(append(issues, yamlIssue(source, err)))
	}
	var raw rawSharedConfig
	decoder := yaml.NewDecoder(bytes.NewReader(encoded))
	if err := decoder.Decode(&raw); err != nil && !errors.Is(err, io.EOF) {
		var typeErr *yaml.TypeError
		if !errors.As(err, &typeErr) {
			return SharedConfig{}, normalizeIssueSeverities(append(issues, yamlIssue(source, err)))
		}
		issues = append(issues, yamlTypeIssues(source, typeErr)...)
	}
	shared, conversionIssues := convertShared(raw)
	return shared, normalizeIssueSeverities(append(issues, conversionIssues...))
}

func yamlIssue(source string, err error) ValidationIssue {
	message := fmt.Sprintf("invalid YAML syntax in %s: %v", source, err)
	if strings.Contains(err.Error(), "field") && strings.Contains(err.Error(), "not found") {
		message = fmt.Sprintf("unknown or invalid YAML field in %s: %v", source, err)
	}
	return validationError("config", message, "")
}

func yamlTypeIssues(source string, err *yaml.TypeError) []ValidationIssue {
	if err == nil {
		return nil
	}
	issues := make([]ValidationIssue, 0, len(err.Errors))
	for _, message := range err.Errors {
		issues = append(issues, yamlIssue(source, errors.New(message)))
	}
	if len(issues) == 0 {
		return []ValidationIssue{yamlIssue(source, err)}
	}
	return issues
}

func unknownYAMLFields(node *yaml.Node, typ reflect.Type, path string) []ValidationIssue {
	if node == nil {
		return nil
	}
	if node.Kind == yaml.DocumentNode {
		if len(node.Content) == 0 {
			return nil
		}
		return unknownYAMLFields(node.Content[0], typ, path)
	}
	if node.Kind == yaml.AliasNode {
		return unknownYAMLFields(node.Alias, typ, path)
	}
	typ = unwrapYAMLType(typ)
	switch node.Kind {
	case yaml.MappingNode:
		if typ.Kind() == reflect.Map {
			elem := typ.Elem()
			var issues []ValidationIssue
			for i := 0; i < len(node.Content); i += 2 {
				key, value := node.Content[i], node.Content[i+1]
				issues = append(issues, unknownYAMLFields(value, elem, joinYAMLPath(path, key.Value))...)
			}
			return issues
		}
		if typ.Kind() != reflect.Struct {
			return nil
		}
		known := yamlFieldTypes(typ)
		var issues []ValidationIssue
		for i := 0; i < len(node.Content); i += 2 {
			key, value := node.Content[i], node.Content[i+1]
			child := joinYAMLPath(path, key.Value)
			fieldType, ok := known[key.Value]
			if !ok {
				issues = append(issues, validationError(child,
					fmt.Sprintf("unknown or invalid YAML field: field %s not found", key.Value),
					"remove the field or correct its spelling"))
				continue
			}
			issues = append(issues, unknownYAMLFields(value, fieldType, child)...)
		}
		return issues
	case yaml.SequenceNode:
		if typ.Kind() != reflect.Slice && typ.Kind() != reflect.Array {
			return nil
		}
		elem := typ.Elem()
		var issues []ValidationIssue
		for i, child := range node.Content {
			issues = append(issues, unknownYAMLFields(child, elem, fmt.Sprintf("%s[%d]", path, i))...)
		}
		return issues
	default:
		return nil
	}
}

func unwrapYAMLType(typ reflect.Type) reflect.Type {
	for typ != nil && typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	return typ
}

func yamlFieldTypes(typ reflect.Type) map[string]reflect.Type {
	fields := map[string]reflect.Type{}
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if field.PkgPath != "" && !field.Anonymous {
			continue
		}
		tag := field.Tag.Get("yaml")
		if tag == "-" {
			continue
		}
		name, _, _ := strings.Cut(tag, ",")
		if name == "" {
			name = strings.ToLower(field.Name)
		}
		fields[name] = field.Type
	}
	return fields
}

func joinYAMLPath(prefix, key string) string {
	if prefix == "" {
		return key
	}
	return prefix + "." + key
}

func normalizeIssueSeverities(issues []ValidationIssue) []ValidationIssue {
	if len(issues) == 0 {
		return issues
	}
	out := make([]ValidationIssue, len(issues))
	for i, issue := range issues {
		if issue.Severity == "" {
			issue.Severity = issue.EffectiveSeverity()
		}
		out[i] = issue
	}
	return out
}

func convertShared(raw rawSharedConfig) (SharedConfig, []ValidationIssue) {
	var issues []ValidationIssue
	read, issue := parsePositiveDefaultDuration("server.read_timeout", raw.Server.ReadTimeout, 5*time.Second)
	issues = appendNonEmptyIssue(issues, issue)
	write, issue := parsePositiveDefaultDuration("server.write_timeout", raw.Server.WriteTimeout, 10*time.Second)
	issues = appendNonEmptyIssue(issues, issue)
	idle, issue := parsePositiveDefaultDuration("server.idle_timeout", raw.Server.IdleTimeout, 60*time.Second)
	issues = appendNonEmptyIssue(issues, issue)
	shutdown, issue := parsePositiveDefaultDuration("server.shutdown_timeout", raw.Server.ShutdownTimeout, 10*time.Second)
	issues = appendNonEmptyIssue(issues, issue)
	publish, issue := parsePositiveDefaultDuration("server.publish_timeout", raw.Server.PublishTimeout, 10*time.Second)
	issues = appendNonEmptyIssue(issues, issue)
	readinessInterval, issue := parsePositiveDefaultDuration("server.readiness_interval", raw.Server.ReadinessInterval, 10*time.Second)
	issues = appendNonEmptyIssue(issues, issue)
	readinessTimeout, issue := parsePositiveDefaultDuration("server.readiness_timeout", raw.Server.ReadinessTimeout, 5*time.Second)
	issues = appendNonEmptyIssue(issues, issue)
	replayTTL, issue := parseDuration("replay.ttl", raw.Replay.TTL, 0)
	issues = appendNonEmptyIssue(issues, issue)
	replayCleanupInterval, issue := parseDuration("replay.cleanup_interval", raw.Replay.CleanupInterval, 0)
	issues = appendNonEmptyIssue(issues, issue)
	if raw.Replay.Enabled && replayCleanupInterval == 0 {
		replayCleanupInterval = time.Hour
	}

	// Connections are resolved into the raw blocks before conversion, so
	// duration parsing and defaulting still happen once, in convertDestination.
	connectionOf := make(map[string]ConnectionRef)
	replayPostgres := raw.Replay.Postgres
	if name := raw.Replay.Connection; name != "" {
		if conn, ok := raw.Connections[name]; ok && conn.Type == "postgres" {
			mergeByYAMLTag(reflect.ValueOf(&replayPostgres).Elem(), reflect.ValueOf(conn), "", ownedConnPaths(conn.Type))
			connectionOf["replay"] = ConnectionRef{Name: name, Type: conn.Type}
		}
	}

	shared := SharedConfig{
		Server: ServerConfig{
			Addr: raw.Server.Addr, ReadTimeout: read, WriteTimeout: write,
			IdleTimeout: idle, ShutdownTimeout: shutdown, PublishTimeout: publish,
			MaxBodyBytes:        raw.Server.MaxBodyBytes,
			MaxInflightRequests: maxInflightRequests(raw.Server.MaxInflightRequests),
			FanoutConcurrency:   raw.Server.FanoutConcurrency,
			ReadinessInterval:   readinessInterval, ReadinessTimeout: readinessTimeout,
			ReadinessRequireAll: raw.Server.ReadinessRequireAll,
		},
		Security: raw.Security,
		Metrics:  raw.Metrics,
		Logging:  raw.Logging,
		Replay: ReplayConfig{
			Enabled: raw.Replay.Enabled, Addr: raw.Replay.Addr,
			SkipUnauthenticated: raw.Replay.SkipUnauthenticated,
			TTL:                 replayTTL, MaxEvents: raw.Replay.MaxEvents,
			CleanupInterval: replayCleanupInterval,
			Postgres:        convertPostgresConfig(replayPostgres),
		},
		Destinations: map[string]DestinationConfig{},
		ConnectionOf: connectionOf,
	}
	// Sorted so issues from the destination loop are emitted deterministically.
	for _, id := range sortedStringKeys(raw.Destinations) {
		rawDestination := raw.Destinations[id]
		if name := rawDestination.Connection; name != "" {
			if conn, ok := raw.Connections[name]; ok && conn.Type == connTypeForDestType[rawDestination.Type] {
				applyConnection(&rawDestination, conn)
				connectionOf["destinations."+id] = ConnectionRef{Name: name, Type: conn.Type}
			}
		}
		destination, destinationIssues := convertDestination("destinations."+id, rawDestination)
		issues = append(issues, destinationIssues...)
		shared.Destinations[id] = destination
	}
	return shared, issues
}

func appendNonEmptyIssue(issues []ValidationIssue, issue ValidationIssue) []ValidationIssue {
	if issue.Field != "" {
		return append(issues, issue)
	}
	return issues
}

func (s SharedConfig) asConfig() Config {
	return Config{
		Server: s.Server, Security: s.Security, Metrics: s.Metrics,
		Logging: s.Logging, Replay: s.Replay, Destinations: s.Destinations,
	}
}
