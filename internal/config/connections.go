package config

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// Connections let one credential serve many destinations. A `connections:`
// entry holds everything needed to reach and authenticate a backend -- region
// and keys for AWS, url and password for Redis, brokers and SASL for Kafka --
// and a destination references it by name:
//
//	connections:
//	  aws_prod:
//	    type: aws
//	    region: us-east-1
//	    access_key_id: ${AWS_ACCESS_KEY_ID}
//	    secret_access_key: ${AWS_SECRET_ACCESS_KEY}
//	  destinations:
//	    events: {type: s3, connection: aws_prod, s3: {bucket: events}}
//
// Resolution happens entirely here, at load time: applyConnection copies the
// connection's fields into the destination's raw block before
// convertDestination runs, so everything downstream (internal/queue, the
// publishers, readiness, ServingConfig) sees an ordinary DestinationConfig and
// needs no knowledge of connections at all.
//
// The split is strict, not an override-merge: a field belongs either to the
// connection or to the destination, never both. Setting a connection-owned
// field on a destination that also names a connection is a validation error
// (see connectionBindingIssues), because "which of these two places did this
// value come from" is exactly the question a readable config should never
// raise.

// connTypeForDestType maps a destination type to the connection type that can
// serve it. The sibling of destTypeBlocks in schema.go: that one says which
// YAML block a destination writes, this one says which connection may fill it.
// The four AWS destinations share one credential shape, as do the four redis
// variants.
//
// There is deliberately no "webhook" connection. It would be {auth, tls} with
// no host, so unlike every other type the credential would not travel with the
// address of the thing it authenticates to -- one profile could be attached to
// destinations pointing at different vendors. Shared outbound webhook auth is
// an auth profile, not a connection, and needs its own design.
var connTypeForDestType = map[string]string{
	"s3":           "aws",
	"sqs":          "aws",
	"sns":          "aws",
	"lambda":       "aws",
	"redis_stream": "redis",
	"redis_list":   "redis",
	"redis_pubsub": "redis",
	"redis_kv":     "redis",
	"postgres":     "postgres",
	"kafka":        "kafka",
	"mongodb":      "mongodb",
	"rabbitmq":     "rabbitmq",
	"temporal":     "temporal",
}

// connectionTypes lists the supported connection types in the order they are
// named in error suggestions.
var connectionTypes = []string{"aws", "redis", "postgres", "kafka", "mongodb", "rabbitmq", "temporal"}

// rawTLSConfig is the TLS block for backends whose transport security is
// implied by the connection string (postgres sslmode, amqps://, mongodb+srv).
// There is no `enabled` flag: the block only carries custom CA and mTLS
// material.
type rawTLSConfig struct {
	CACert             string `yaml:"ca_cert"`
	ClientCert         string `yaml:"client_cert"`
	ClientKey          string `yaml:"client_key"`
	ServerName         string `yaml:"server_name"`
	InsecureSkipVerify bool   `yaml:"insecure_skip_verify"`
}

// rawEnabledTLSConfig is the TLS block for backends where TLS is switched on
// explicitly rather than implied by a URL scheme.
type rawEnabledTLSConfig struct {
	Enabled            bool   `yaml:"enabled"`
	CACert             string `yaml:"ca_cert"`
	ClientCert         string `yaml:"client_cert"`
	ClientKey          string `yaml:"client_key"`
	ServerName         string `yaml:"server_name"`
	InsecureSkipVerify bool   `yaml:"insecure_skip_verify"`
}

type rawKafkaSASL struct {
	Mechanism string `yaml:"mechanism"`
	Username  string `yaml:"username"`
	Password  string `yaml:"password"`
}

// The per-type connection structs below are the authoritative declaration of
// which fields a connection owns. They are reflected for three purposes: the
// legal-key set used by validation, the field set copied by applyConnection,
// and the per-type JSON Schema served to the config UI.
//
// They intentionally repeat field names from rawDestinationConfig rather than
// being shared with it. Sharing would mean either extracting eleven anonymous
// structs (whose reflected property order the committed
// web/src/graph/destinationSchema.json depends on) or embedding with
// yaml:",inline" (which yamlFieldTypes does not understand, so every inlined
// key would be reported as an unknown field). connectionFieldsMatchDestinations
// in connections_test.go mechanically pins the two in lockstep instead.

type rawAWSConnection struct {
	Region          string `yaml:"region"`
	Endpoint        string `yaml:"endpoint"`
	AccessKeyID     string `yaml:"access_key_id"`
	SecretAccessKey string `yaml:"secret_access_key"`
}

type rawRedisConnection struct {
	Mode       string              `yaml:"mode"`
	URL        string              `yaml:"url"`
	Addrs      []string            `yaml:"addrs"`
	MasterName string              `yaml:"master_name"`
	Username   string              `yaml:"username"`
	Password   string              `yaml:"password"`
	DB         int                 `yaml:"db"`
	TLS        rawEnabledTLSConfig `yaml:"tls"`
}

type rawPostgresConnection struct {
	URL      string       `yaml:"url"`
	MaxConns int32        `yaml:"max_conns"`
	TLS      rawTLSConfig `yaml:"tls"`
}

type rawKafkaConnection struct {
	Brokers []string            `yaml:"brokers"`
	SASL    rawKafkaSASL        `yaml:"sasl"`
	TLS     rawEnabledTLSConfig `yaml:"tls"`
}

type rawMongoConnection struct {
	URI         string       `yaml:"uri"`
	MaxPoolSize uint64       `yaml:"max_pool_size"`
	TLS         rawTLSConfig `yaml:"tls"`
}

type rawRabbitMQConnection struct {
	URL               string       `yaml:"url"`
	PublisherChannels int          `yaml:"publisher_channels"`
	TLS               rawTLSConfig `yaml:"tls"`
}

type rawTemporalConnection struct {
	HostPort       string              `yaml:"host_port"`
	Namespace      string              `yaml:"namespace"`
	APIKey         string              `yaml:"api_key"`
	ConnectTimeout string              `yaml:"connect_timeout"`
	TLS            rawEnabledTLSConfig `yaml:"tls"`
}

// connectionShapes maps each connection type to a zero value of its struct.
// Reflection over these drives ownership, merging, and schema generation.
var connectionShapes = map[string]any{
	"aws":      rawAWSConnection{},
	"redis":    rawRedisConnection{},
	"postgres": rawPostgresConnection{},
	"kafka":    rawKafkaConnection{},
	"mongodb":  rawMongoConnection{},
	"rabbitmq": rawRabbitMQConnection{},
	"temporal": rawTemporalConnection{},
}

// rawConnectionConfig is the decode target for one `connections:` entry. A
// connection is authored flat -- `type:` alongside the fields themselves,
// rather than nested under a per-type block -- so this struct is the union of
// every type's fields. A flat Go struct cannot express "these keys are legal
// only when type is aws", so connectionBindingIssues enforces that separately
// against the per-type shapes above; unknownYAMLFields only answers the weaker
// question of whether a key is known at all.
//
// Fields shared by several types (url, tls) appear once. TLS is the superset
// shape; the types that have no `enabled` flag reject it via ownedConnPaths.
type rawConnectionConfig struct {
	Type string `yaml:"type"`

	// aws
	Region          string `yaml:"region"`
	Endpoint        string `yaml:"endpoint"`
	AccessKeyID     string `yaml:"access_key_id"`
	SecretAccessKey string `yaml:"secret_access_key"`

	// redis, postgres, rabbitmq
	URL string `yaml:"url"`

	// redis
	Mode       string   `yaml:"mode"`
	Addrs      []string `yaml:"addrs"`
	MasterName string   `yaml:"master_name"`
	Username   string   `yaml:"username"`
	Password   string   `yaml:"password"`
	DB         int      `yaml:"db"`

	// postgres
	MaxConns int32 `yaml:"max_conns"`

	// kafka
	Brokers []string     `yaml:"brokers"`
	SASL    rawKafkaSASL `yaml:"sasl"`

	// mongodb
	URI         string `yaml:"uri"`
	MaxPoolSize uint64 `yaml:"max_pool_size"`

	// rabbitmq
	PublisherChannels int `yaml:"publisher_channels"`

	// temporal
	HostPort       string `yaml:"host_port"`
	Namespace      string `yaml:"namespace"`
	APIKey         string `yaml:"api_key"`
	ConnectTimeout string `yaml:"connect_timeout"`

	// redis, kafka, temporal (with enabled); postgres, mongodb, rabbitmq (without)
	TLS rawEnabledTLSConfig `yaml:"tls"`
}

// ownedConnPaths returns the dotted yaml paths a connection of the given type
// owns, e.g. {"region", "tls.ca_cert"}. Derived by reflection so the per-type
// structs above stay the single source of truth. An unknown type yields nil.
func ownedConnPaths(connType string) map[string]struct{} {
	shape, ok := connectionShapes[connType]
	if !ok {
		return nil
	}
	out := make(map[string]struct{})
	collectYAMLPaths(reflect.TypeOf(shape), "", out)
	return out
}

// knownConnectionPaths returns every yaml path rawConnectionConfig can decode,
// across all types. Used to tell "this key belongs to a different connection
// type" apart from "this key is a typo" -- the latter is already reported by
// unknownYAMLFields, and reporting both would be noise.
func knownConnectionPaths() map[string]struct{} {
	out := make(map[string]struct{})
	collectYAMLPaths(reflect.TypeOf(rawConnectionConfig{}), "", out)
	return out
}

// collectYAMLPaths walks a struct's yaml-tagged fields, recursing into nested
// structs so a path like "tls.client_key" is produced. Slices and scalars are
// leaves.
func collectYAMLPaths(t reflect.Type, prefix string, out map[string]struct{}) {
	t = unwrapYAMLType(t)
	if t.Kind() != reflect.Struct {
		return
	}
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		tag := strings.Split(field.Tag.Get("yaml"), ",")[0]
		if tag == "" || tag == "-" {
			continue
		}
		path := joinYAMLPath(prefix, tag)
		out[path] = struct{}{}
		if unwrapYAMLType(field.Type).Kind() == reflect.Struct {
			collectYAMLPaths(field.Type, path, out)
		}
	}
}

// applyConnection copies the connection's owned fields into the destination's
// raw backend block, leaving the destination's own sink fields (bucket, table,
// topic, key_prefix) untouched. It runs before convertDestination, so duration
// parsing and defaulting still happen once, in the existing code path.
//
// The copy is done by reflection rather than by hand. That is a deliberate
// break from the explicit style of convertDestination: the requirement here is
// that seven connection structs stay in exact lockstep with their destination
// blocks across ~45 fields, forever, and a tag-matching copy plus the lockstep
// test enforces that structurally, where 250 lines of hand-copying would be
// correct only until the next field is added to one side.
func applyConnection(dest *rawDestinationConfig, conn rawConnectionConfig) {
	block, ok := destTypeBlocks[dest.Type]
	if !ok {
		return
	}
	owned := ownedConnPaths(conn.Type)
	if owned == nil {
		return
	}
	target := fieldByYAMLTag(reflect.ValueOf(dest).Elem(), block)
	if !target.IsValid() {
		return
	}
	mergeByYAMLTag(target, reflect.ValueOf(conn), "", owned)
}

// mergeByYAMLTag copies every field of src whose yaml path is in owned onto
// the same-tagged field of dst. Nested structs of differing Go types (the
// connection's named TLS struct versus the destination's anonymous one) are
// merged recursively rather than assigned.
func mergeByYAMLTag(dst, src reflect.Value, prefix string, owned map[string]struct{}) {
	dstFields := yamlFieldIndex(dst.Type())
	srcType := src.Type()
	for i := 0; i < srcType.NumField(); i++ {
		tag := strings.Split(srcType.Field(i).Tag.Get("yaml"), ",")[0]
		if tag == "" || tag == "-" {
			continue
		}
		path := joinYAMLPath(prefix, tag)
		di, ok := dstFields[tag]
		if !ok {
			continue
		}
		sv, dv := src.Field(i), dst.Field(di)
		if sv.Kind() == reflect.Struct && dv.Kind() == reflect.Struct && sv.Type() != dv.Type() {
			mergeByYAMLTag(dv, sv, path, owned)
			continue
		}
		if _, isOwned := owned[path]; !isOwned {
			continue
		}
		if dv.CanSet() && dv.Type() == sv.Type() {
			dv.Set(sv)
		}
	}
}

// yamlFieldIndex maps a struct's yaml tag names to field indexes.
func yamlFieldIndex(t reflect.Type) map[string]int {
	out := make(map[string]int, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		tag := strings.Split(t.Field(i).Tag.Get("yaml"), ",")[0]
		if tag == "" || tag == "-" {
			continue
		}
		out[tag] = i
	}
	return out
}

// fieldByYAMLTag returns the struct field carrying the given yaml tag.
func fieldByYAMLTag(v reflect.Value, tag string) reflect.Value {
	if i, ok := yamlFieldIndex(v.Type())[tag]; ok {
		return v.Field(i)
	}
	return reflect.Value{}
}

// quotedList renders values as a comma-separated quoted list for use in
// validation suggestions.
func quotedList(values []string) string {
	quoted := make([]string, 0, len(values))
	for _, v := range values {
		quoted = append(quoted, fmt.Sprintf("%q", v))
	}
	return strings.Join(quoted, ", ")
}

// sortedStringKeys returns a map's keys in sorted order, so issues emitted from
// a map walk are deterministic.
func sortedStringKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
