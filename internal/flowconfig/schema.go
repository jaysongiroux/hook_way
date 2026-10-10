package flowconfig

import (
	"strings"

	"github.com/invopop/jsonschema"

	"github.com/jaysongiroux/hook_way/internal/auth"
	"github.com/jaysongiroux/hook_way/internal/config"
	"github.com/jaysongiroux/hook_way/internal/graph"
	"github.com/jaysongiroux/hook_way/internal/jsonbudget"
)

// AuthSchemeSchema describes registry capabilities for strategy-driven authoring.
// This metadata guides the form; runtime auth validation remains authoritative.
type AuthSchemeSchema struct {
	RequestSignature  bool   `json:"request_signature"`
	SeparateTimestamp bool   `json:"separate_timestamp"`
	JWT               bool   `json:"jwt"`
	KeyFormat         string `json:"key_format"`
}

type NodeSchema struct {
	AllowMultipleInputs bool                        `json:"allow_multiple_inputs,omitempty"`
	AllowLeaf           bool                        `json:"allow_leaf,omitempty"`
	BatchChildOrder     int                         `json:"batch_child_order,omitempty"`
	StagePorts          map[string]StagePorts       `json:"stage_ports,omitempty"`
	Input               string                      `json:"input"`
	Output              string                      `json:"output"`
	Detached            bool                        `json:"detached"`
	OnError             bool                        `json:"on_error"`
	Schema              *jsonschema.Schema          `json:"schema"`
	AuthSchemes         map[string]AuthSchemeSchema `json:"auth_schemes,omitempty"`
}
type StagePorts struct {
	Input  string `json:"input"`
	Output string `json:"output"`
}

// NodeSchemas exposes the same definitions used by Register for the CLI and graph editor.
func NodeSchemas() map[string]NodeSchema {
	reflector := config.NewSchemaReflector()
	reflector.Anonymous = true
	reflector.DoNotReference = true
	reflector.RequiredFromJSONSchemaTags = true
	definitions := catalog(RegistryOptions{})
	out := make(map[string]NodeSchema, len(definitions))
	for _, def := range definitions {
		metadata := NodeSchema{
			Input: def.in.String(), Output: def.out.String(), Detached: def.detached,
			AllowLeaf:           def.allowLeaf,
			AllowMultipleInputs: def.allowMultipleInputs,
			OnError:             def.in != graph.PortNone && !def.noErrorEdge,
			BatchChildOrder:     def.batchChildOrder,
			Schema:              reflector.Reflect(def.newOptions()),
		}
		applyFormDefaults(metadata.Schema, def.formDefaults)
		if def.configureSchema != nil {
			def.configureSchema(&metadata)
		}
		if len(def.stagePorts) > 0 {
			metadata.StagePorts = make(map[string]StagePorts, len(def.stagePorts))
			for stage, ports := range def.stagePorts {
				metadata.StagePorts[stage] = StagePorts{Input: ports.in.String(), Output: ports.out.String()}
			}
		}
		out[def.name] = metadata
	}
	return out
}

func exclusivePropertyContract(a, b string) []*jsonschema.Schema {
	return []*jsonschema.Schema{
		{Required: []string{a}, Not: &jsonschema.Schema{Required: []string{b}}},
		{Required: []string{b}, Not: &jsonschema.Schema{Required: []string{a}}},
	}
}

func authSchemeSchemas(schema *jsonschema.Schema) map[string]AuthSchemeSchema {
	out := make(map[string]AuthSchemeSchema)
	var names []any
	for _, scheme := range auth.Schemes() {
		if scheme == auth.SchemeNoAuth {
			continue
		}
		format := "shared_secret"
		switch scheme {
		case auth.SchemeJWTRS256, auth.SchemeJWTES256:
			format = "pem"
		case auth.SchemeECDSAP256SHA256Headers:
			format = "base64_der"
		}
		out[scheme] = AuthSchemeSchema{
			RequestSignature:  auth.IsRequestScheme(scheme),
			SeparateTimestamp: auth.SeparateTimestamp(scheme),
			JWT:               auth.SchemeAllowedForAuthType(auth.AuthTypeJWT, scheme),
			KeyFormat:         format,
		}
		names = append(names, scheme)
	}
	if property, ok := schema.Properties.Get("scheme"); ok {
		property.Enum = names
	}
	return out
}

func envelopeFormDefaults() map[string]any {
	limits := (MultipartOptions{}).limits()
	return map[string]any{
		"body_format":               "json",
		"json.max_depth":            jsonbudget.DefaultMaxDepth,
		"json.max_nodes":            jsonbudget.DefaultMaxNodes,
		"multipart.include_content": limits.IncludeContent,
		"multipart.max_parts":       limits.MaxParts,
		"multipart.max_part_bytes":  limits.MaxPartBytes,
		"xml.max_bytes":             1 << 20, "xml.max_depth": 256, "xml.max_nodes": 100000,
	}
}

func publishFormDefaults() map[string]any {
	initial, max := publishBackoffs(&PublishRetryOptions{})
	return map[string]any{
		"temporal.input": "envelope", "wire_body": "envelope", "payload": "envelope",
		"retry.initial_backoff": initial.String(), "retry.max_backoff": max.String(),
	}
}

func batchFormDefaults() map[string]any {
	options := (BatchOptions{}).normalized()
	return map[string]any{"max_children": options.MaxChildren, "max_inflight": options.MaxInflight, "timeout": options.Timeout}
}

// Only context-independent effective defaults are represented here. For example,
// rate-limit burst depends on rpm and batch concurrency depends on its mode, so
// neither is assigned a universal schema default.
func applyFormDefaults(schema *jsonschema.Schema, defaults map[string]any) {
	for path, value := range defaults {
		current := schema
		for _, part := range strings.Split(path, ".") {
			if current == nil || current.Properties == nil {
				current = nil
				break
			}
			next, ok := current.Properties.Get(part)
			if !ok {
				current = nil
				break
			}
			current = next
		}
		if current != nil {
			current.Default = value
		}
	}
}
