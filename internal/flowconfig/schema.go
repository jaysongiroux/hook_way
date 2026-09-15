package flowconfig

import (
	"github.com/invopop/jsonschema"
	"github.com/jasongiroux/hookway/internal/config"
)

type NodeSchema struct {
	Input    string             `json:"input"`
	Output   string             `json:"output"`
	Detached bool               `json:"detached"`
	Schema   *jsonschema.Schema `json:"schema"`
}

// NodeSchemas exposes the same definitions used by Register for the CLI and graph editor.
func NodeSchemas() map[string]NodeSchema {
	reflector := config.NewSchemaReflector()
	reflector.Anonymous = true
	reflector.DoNotReference = true
	reflector.RequiredFromJSONSchemaTags = true
	out := make(map[string]NodeSchema, 17)
	for _, def := range catalog(RegistryOptions{}) {
		schema := reflector.Reflect(def.newOptions())
		switch def.name {
		case "auth":
			schema.OneOf = exclusivePropertyContract("secret", "secret_ref")
		case "respond":
			schema.OneOf = exclusivePropertyContract("status", "passthrough")
		}
		out[def.name] = NodeSchema{
			Input: def.in.String(), Output: def.out.String(), Detached: def.detached,
			Schema: schema,
		}
	}
	return out
}

func exclusivePropertyContract(a, b string) []*jsonschema.Schema {
	return []*jsonschema.Schema{
		{Required: []string{a}, Not: &jsonschema.Schema{Required: []string{b}}},
		{Required: []string{b}, Not: &jsonschema.Schema{Required: []string{a}}},
	}
}
