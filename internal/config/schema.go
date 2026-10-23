package config

import (
	"encoding/json"
	"reflect"
	"strings"

	"github.com/invopop/jsonschema"
)

// SchemaJSON returns a JSON Schema describing the Hookway YAML config. It
// reflects rawConfig (the on-disk YAML shape) using yaml tags so property names
// match the keys users actually write. Required-ness and enums are intentionally
// NOT derived here — config.Validate is the authoritative validator; this schema
// describes shape for form rendering and obvious field-type checks.
func SchemaJSON() ([]byte, error) {
	r := NewSchemaReflector()
	// Inline every named sub-struct instead of emitting $defs/$ref. The
	// connection blocks share named TLS and SASL types, which would otherwise
	// turn this previously ref-free document into one no current consumer
	// knows how to dereference. Set here rather than on NewSchemaReflector so
	// the per-node and per-destination schemas keep their own settings.
	r.DoNotReference = true
	schema := r.Reflect(&rawConfig{})
	return json.MarshalIndent(schema, "", "  ")
}

// NewSchemaReflector returns Hookway's configured JSON Schema reflector so
// whole-config and per-node schemas use identical YAML field naming and
// required-field behavior.
func NewSchemaReflector() *jsonschema.Reflector {
	return &jsonschema.Reflector{
		FieldNameTag: "yaml",
		// Do not mark fields required from struct tags; the validator owns
		// required-ness. This keeps a UI from blocking on optional fields.
		RequiredFromJSONSchemaTags: true,
		// Expand the root struct so the schema's root IS the config object.
		ExpandedStruct: true,
	}
}

// DestTypeSchema is the JSON Schema for one destination type's nested block.
type DestTypeSchema struct {
	Block string `json:"block"`
	// Connection is the connection type that can serve this destination, or
	// "" for a destination with no connection form (webhook). Published so
	// the config UI reads the pairing from the generated catalog instead of
	// hand-mirroring connTypeForDestType in TypeScript.
	Connection string             `json:"connection,omitempty"`
	Schema     *jsonschema.Schema `json:"schema"`
}

// destTypeBlocks maps the user-facing destination type name to the YAML block
// key under that destination entry. Redis variants share one connection block.
var destTypeBlocks = map[string]string{
	"mcp_events":   "mcp_events",
	"rabbitmq":     "rabbitmq",
	"sqs":          "sqs",
	"sns":          "sns",
	"s3":           "s3",
	"lambda":       "lambda",
	"postgres":     "postgres",
	"mongodb":      "mongodb",
	"kafka":        "kafka",
	"webhook":      "webhook",
	"temporal":     "temporal",
	"redis_stream": "redis",
	"redis_list":   "redis",
	"redis_pubsub": "redis",
	"redis_kv":     "redis",
}

// DestinationTypeSchemas reflects each destination connection block so the
// config UI can render a form without a hand-maintained field list.
func DestinationTypeSchemas() map[string]DestTypeSchema {
	reflector := NewSchemaReflector()
	reflector.Anonymous = true
	reflector.DoNotReference = true
	fieldByYAML := yamlFields(reflect.TypeOf(rawDestinationConfig{}))
	out := make(map[string]DestTypeSchema, len(destTypeBlocks))
	for typeName, block := range destTypeBlocks {
		field, ok := fieldByYAML[block]
		if !ok {
			continue
		}
		out[typeName] = DestTypeSchema{
			Block:      block,
			Connection: connTypeForDestType[typeName],
			Schema:     reflector.ReflectFromType(field.Type),
		}
	}
	return out
}

// ConnectionTypeSchemas reflects each connection type's field set so the
// config UI can render a form per type without a hand-maintained field list.
// Unlike a destination, a connection is authored flat -- `type:` alongside its
// fields -- so there is no block name to report.
func ConnectionTypeSchemas() map[string]*jsonschema.Schema {
	reflector := NewSchemaReflector()
	reflector.Anonymous = true
	reflector.DoNotReference = true
	out := make(map[string]*jsonschema.Schema, len(connectionShapes))
	for typeName, shape := range connectionShapes {
		out[typeName] = reflector.ReflectFromType(reflect.TypeOf(shape))
	}
	return out
}

func yamlFields(t reflect.Type) map[string]reflect.StructField {
	out := make(map[string]reflect.StructField)
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		tag := strings.Split(field.Tag.Get("yaml"), ",")[0]
		if tag == "" || tag == "-" {
			continue
		}
		out[tag] = field
	}
	return out
}
