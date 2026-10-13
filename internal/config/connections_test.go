package config

import (
	"reflect"
	"strings"
	"testing"
)

// decodeShared is a test helper: decode a YAML document the way the loader
// does and fail on any error-severity issue.
func decodeShared(t *testing.T, doc string) SharedConfig {
	t.Helper()
	shared, issues := DecodeSharedYAML([]byte(doc), "test.yaml")
	for _, issue := range issues {
		if issue.EffectiveSeverity() == SeverityError {
			t.Fatalf("unexpected issue %s: %s", issue.Field, issue.Message)
		}
	}
	return shared
}

// decodeIssues returns every issue keyed by field, for tests that assert on
// validation rather than on the decoded value.
func decodeIssues(t *testing.T, doc string) map[string]ValidationIssue {
	t.Helper()
	shared, issues := DecodeSharedYAML([]byte(doc), "test.yaml")
	issues = append(issues, shared.ValidationIssues()...)
	out := make(map[string]ValidationIssue, len(issues))
	for _, issue := range issues {
		out[issue.Field] = issue
	}
	return out
}

const serverBlock = "server:\n  addr: \":8080\"\n  max_body_bytes: 1048576\n"

// TestConnectionResolvesToTheSameConfigAsInline is the test this whole feature
// rests on. Connections are an authoring layer: after conversion the result
// must be indistinguishable from the equivalent inline config, because that is
// what lets internal/queue, the publishers, readiness, and ServingConfig stay
// completely unaware that connections exist.
//
// If this fails after someone adds a field, the connection struct and the
// destination block have drifted out of the strict split.
func TestConnectionResolvesToTheSameConfigAsInline(t *testing.T) {
	inline := serverBlock + `
destinations:
  events:
    type: s3
    s3:
      region: us-east-1
      endpoint: http://localhost:4566
      access_key_id: AKIA_TEST
      secret_access_key: shhh
      bucket: hookway-events
      sse: AES256
  notify:
    type: sns
    sns:
      region: us-east-1
      endpoint: http://localhost:4566
      access_key_id: AKIA_TEST
      secret_access_key: shhh
`
	viaConnection := serverBlock + `
connections:
  aws_prod:
    type: aws
    region: us-east-1
    endpoint: http://localhost:4566
    access_key_id: AKIA_TEST
    secret_access_key: shhh
destinations:
  events:
    type: s3
    connection: aws_prod
    s3:
      bucket: hookway-events
      sse: AES256
  notify:
    type: sns
    connection: aws_prod
`
	want := decodeShared(t, inline)
	got := decodeShared(t, viaConnection)

	// Connection is the one field that legitimately differs: it records where
	// the values came from so errors can name the right path.
	for id, dest := range got.Destinations {
		if dest.Connection != "aws_prod" {
			t.Errorf("destination %q Connection = %q, want aws_prod", id, dest.Connection)
		}
		dest.Connection = ""
		got.Destinations[id] = dest
	}
	got.ConnectionOf = nil
	want.ConnectionOf = nil

	if !reflect.DeepEqual(want, got) {
		t.Errorf("connection-resolved config differs from inline\n inline: %+v\n   conn: %+v", want.Destinations, got.Destinations)
	}
}

// TestConnectionResolvesEveryBackend exercises one destination per connection
// type, so a new connection type cannot be added without a merge that works.
func TestConnectionResolvesEveryBackend(t *testing.T) {
	doc := serverBlock + `
connections:
  r:
    type: redis
    mode: single
    url: redis://redis:6379
    password: sekret
    db: 3
  pg:
    type: postgres
    url: postgres://db:5432/x
    max_conns: 12
  k:
    type: kafka
    brokers: [broker:9092]
    sasl: {mechanism: plain, username: u, password: p}
    tls: {enabled: true}
  m:
    type: mongodb
    uri: mongodb://mongo:27017
    max_pool_size: 20
  rb:
    type: rabbitmq
    url: amqp://rabbit:5672/
    publisher_channels: 4
  tp:
    type: temporal
    host_port: temporal:7233
    namespace: default
    api_key: tkey
    tls: {enabled: true}
destinations:
  stream: {type: redis_stream, connection: r, redis: {key_prefix: "evt:"}}
  rows: {type: postgres, connection: pg, postgres: {table: events}}
  topic: {type: kafka, connection: k}
  docs: {type: mongodb, connection: m, mongodb: {database: hookway}}
  queue: {type: rabbitmq, connection: rb, rabbitmq: {exchange: events}}
  wf: {type: temporal, connection: tp}
`
	shared := decodeShared(t, doc)

	redis := shared.Destinations["stream"].Redis
	if redis.URL != "redis://redis:6379" || redis.Password != "sekret" || redis.DB != 3 {
		t.Errorf("redis connection not merged: %+v", redis)
	}
	if redis.KeyPrefix != "evt:" {
		t.Errorf("destination key_prefix = %q, want the sink field preserved", redis.KeyPrefix)
	}
	pg := shared.Destinations["rows"].Postgres
	if pg.URL != "postgres://db:5432/x" || pg.MaxConns != 12 || pg.Table != "events" {
		t.Errorf("postgres connection not merged: %+v", pg)
	}
	kafka := shared.Destinations["topic"].Kafka
	if len(kafka.Brokers) != 1 || kafka.SASL.Password != "p" || !kafka.TLS.Enabled {
		t.Errorf("kafka connection not merged: %+v", kafka)
	}
	mongo := shared.Destinations["docs"].Mongo
	if mongo.URI != "mongodb://mongo:27017" || mongo.MaxPoolSize != 20 || mongo.Database != "hookway" {
		t.Errorf("mongodb connection not merged: %+v", mongo)
	}
	rabbit := shared.Destinations["queue"].RabbitMQ
	if rabbit.URL != "amqp://rabbit:5672/" || rabbit.PublisherChannels != 4 || rabbit.Exchange != "events" {
		t.Errorf("rabbitmq connection not merged: %+v", rabbit)
	}
	temporal := shared.Destinations["wf"].Temporal
	if temporal.HostPort != "temporal:7233" || temporal.APIKey != "tkey" || !temporal.TLS.Enabled {
		t.Errorf("temporal connection not merged: %+v", temporal)
	}
}

// TestConnectionMergesNestedTLS covers the one place the merge cannot be a
// plain assignment: the connection's named TLS struct and the destination's
// anonymous one are different Go types, so they have to be merged field by
// field.
func TestConnectionMergesNestedTLS(t *testing.T) {
	doc := serverBlock + `
connections:
  pg:
    type: postgres
    url: postgres://db/x
    tls:
      ca_cert: /run/ca.pem
      client_cert: /run/c.pem
      client_key: /run/k.pem
      server_name: db.internal
destinations:
  rows: {type: postgres, connection: pg, postgres: {table: events}}
`
	tls := decodeShared(t, doc).Destinations["rows"].Postgres.TLS
	if tls.CACert != "/run/ca.pem" || tls.ClientCert != "/run/c.pem" || tls.ClientKey != "/run/k.pem" || tls.ServerName != "db.internal" {
		t.Errorf("nested tls not merged: %+v", tls)
	}
}

// TestConnectionFieldsMatchDestinationBlocks mechanizes the strict split. The
// connection structs deliberately repeat field names from rawDestinationConfig
// instead of sharing types with it (sharing would either reorder the reflected
// schema the UI depends on, or require yaml:",inline", which yamlFieldTypes
// does not understand). This test is what keeps the duplication honest.
func TestConnectionFieldsMatchDestinationBlocks(t *testing.T) {
	destFields := yamlFields(reflect.TypeOf(rawDestinationConfig{}))
	for destType, connType := range connTypeForDestType {
		block, ok := destTypeBlocks[destType]
		if !ok {
			t.Fatalf("destination type %q has a connection type but no block", destType)
		}
		field, ok := destFields[block]
		if !ok {
			t.Fatalf("destination block %q does not exist", block)
		}
		destPaths := map[string]struct{}{}
		collectYAMLPaths(field.Type, "", destPaths)
		for path := range ownedConnPaths(connType) {
			if _, ok := destPaths[path]; !ok {
				t.Errorf("connection type %q owns %q, which destination block %q does not have", connType, path, block)
			}
		}
	}
}

// TestEveryDestinationTypeHasAConnectionType guards the other direction: a new
// backend should not silently be unable to use a connection.
func TestEveryDestinationTypeHasAConnectionType(t *testing.T) {
	for destType := range destTypeBlocks {
		if destType == "webhook" {
			// Deliberately excluded: a webhook credential carries no host, so
			// it is an auth profile rather than a connection. See
			// connTypeForDestType.
			continue
		}
		if _, ok := connTypeForDestType[destType]; !ok {
			t.Errorf("destination type %q has no connection type", destType)
		}
	}
	for destType, connType := range connTypeForDestType {
		if _, ok := connectionShapes[connType]; !ok {
			t.Errorf("destination type %q maps to unknown connection type %q", destType, connType)
		}
	}
}

// TestRawSharedAndRawConfigAgree is a drift guard unrelated to any single
// feature: the two raw top-level structs duplicate their field lists with
// nothing asserting they stay in sync, which is exactly how a key ends up
// accepted by the loader but missing from the published schema.
func TestRawSharedAndRawConfigAgree(t *testing.T) {
	shared := yamlFields(reflect.TypeOf(rawSharedConfig{}))
	whole := yamlFields(reflect.TypeOf(rawConfig{}))
	for name, field := range shared {
		other, ok := whole[name]
		if !ok {
			t.Errorf("rawSharedConfig has %q but rawConfig does not", name)
			continue
		}
		if field.Type != other.Type {
			t.Errorf("field %q is %s in rawSharedConfig but %s in rawConfig", name, field.Type, other.Type)
		}
	}
}

func TestConnectionNameRejectsDots(t *testing.T) {
	// IsSafeName allows dots, which would make the dotted issue paths that
	// configapi resolves ambiguous. Connection names are stricter.
	if safeConnectionName.MatchString("a.b") {
		t.Error("connection names must not allow dots")
	}
	for _, ok := range []string{"aws_prod", "pg-main", "r2"} {
		if !safeConnectionName.MatchString(ok) {
			t.Errorf("%q should be a valid connection name", ok)
		}
	}
}

func TestQuotedListRendersSuggestions(t *testing.T) {
	if got := quotedList([]string{"aws", "redis"}); got != `"aws", "redis"` {
		t.Errorf("quotedList = %s", got)
	}
	if !strings.Contains(quotedList(connectionTypes), `"temporal"`) {
		t.Error("connectionTypes suggestion should name temporal")
	}
}
