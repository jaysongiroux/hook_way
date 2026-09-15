package config

import (
	"encoding/json"

	"github.com/invopop/jsonschema"
)

// SchemaJSON returns a JSON Schema describing the Hookway YAML config. It
// reflects rawConfig (the on-disk YAML shape) using yaml tags so property names
// match the keys users actually write. Required-ness and enums are intentionally
// NOT derived here — config.Validate is the authoritative validator; this schema
// describes shape for form rendering and obvious field-type checks.
func SchemaJSON() ([]byte, error) {
	r := NewSchemaReflector()
	schema := r.Reflect(&rawConfig{})
	return json.MarshalIndent(schema, "", "  ")
}

// NewSchemaReflector returns Hookway's configured JSON Schema reflector so
// V1 whole-config and V2 per-node schemas use identical YAML field naming and
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
