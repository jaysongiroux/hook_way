package flowconfig

import (
	"github.com/invopop/jsonschema"
	"github.com/jasongiroux/hookway/internal/config"
	"github.com/jasongiroux/hookway/internal/graph"
)

type NodeSchema struct {
	Input    string             `json:"input"`
	Output   string             `json:"output"`
	Detached bool               `json:"detached"`
	OnError  bool               `json:"on_error"`
	Schema   *jsonschema.Schema `json:"schema"`
}

// allowsOnError is whether a catalog type can take a useful on_error edge.
// http_trigger has no input. no_auth never fails a well-typed RawRequest.
// read_body consumes the request body on a failed read, so re-reading is not a recovery path.
// if_else cannot fail a well-typed Envelope either: a condition that does not
// hold is the false branch, so there is no failure left for on_error to catch.
func allowsOnError(name string, in graph.PortType) bool {
	if in == graph.PortNone {
		return false
	}
	switch name {
	case "no_auth", "read_body", "if_else":
		return false
	default:
		return true
	}
}

// NodeSchemas exposes the same definitions used by Register for the CLI and graph editor.
func NodeSchemas() map[string]NodeSchema {
	reflector := config.NewSchemaReflector()
	reflector.Anonymous = true
	reflector.DoNotReference = true
	reflector.RequiredFromJSONSchemaTags = true
	out := make(map[string]NodeSchema, 18)
	for _, def := range catalog(RegistryOptions{}) {
		schema := reflector.Reflect(def.newOptions())
		switch def.name {
		case "auth":
			schema.OneOf = exclusivePropertyContract("secret", "secret_ref")
		case "respond":
			schema.OneOf = exclusivePropertyContract("status", "passthrough")
		case "switch":
			// The exclusivity is per case, not on the node. Without the Not
			// clauses the builder cannot tell that when and conditions are two
			// spellings of one rule rather than two fields to fill in.
			if cases, ok := schema.Properties.Get("cases"); ok && cases.Items != nil {
				cases.Items.OneOf = exclusivePropertyContract("when", "conditions")
			}
		}
		out[def.name] = NodeSchema{
			Input:    def.in.String(),
			Output:   def.out.String(),
			Detached: def.detached,
			OnError:  allowsOnError(def.name, def.in),
			Schema:   schema,
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
