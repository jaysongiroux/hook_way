package flowconfig

import (
	"strings"

	"github.com/invopop/jsonschema"

	"github.com/jaysongiroux/hook_way/internal/auth"
	"github.com/jaysongiroux/hook_way/internal/config"
	"github.com/jaysongiroux/hook_way/internal/graph"
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
	Input       string                      `json:"input"`
	Output      string                      `json:"output"`
	Detached    bool                        `json:"detached"`
	OnError     bool                        `json:"on_error"`
	Schema      *jsonschema.Schema          `json:"schema"`
	AuthSchemes map[string]AuthSchemeSchema `json:"auth_schemes,omitempty"`
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
		var authSchemes map[string]AuthSchemeSchema
		applyFormDefaults(def.name, schema)
		switch def.name {
		case "http_request":
			schema.OneOf = exclusivePropertyContract("url", "upstream")
		case "route":
			schema.OneOf = exclusivePropertyContract("fixed", "candidates")
		case "auth":
			authSchemes = authSchemeSchemas(schema)
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
			Input:       def.in.String(),
			Output:      def.out.String(),
			Detached:    def.detached,
			OnError:     allowsOnError(def.name, def.in),
			Schema:      schema,
			AuthSchemes: authSchemes,
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

// Only context-independent effective defaults are represented here. For example,
// rate-limit burst depends on rpm and batch concurrency depends on its mode, so
// neither is assigned a universal schema default.
func applyFormDefaults(name string, schema *jsonschema.Schema) {
	defaults := map[string]any{}
	switch name {
	case "loop_guard":
		defaults["max_hops"] = newLoopGuard("schema", LoopGuardOptions{}).(loopGuardNode).maxHops
	case "rate_limit":
		defaults["key_by"] = "source_ip"
	case "replay_guard":
		defaults["ttl"], defaults["max_entries"] = "5m", 1024
	case "envelope":
		limits := (MultipartOptions{}).limits()
		defaults["body_format"] = "json"
		defaults["multipart.include_content"] = limits.IncludeContent
		defaults["multipart.max_parts"] = limits.MaxParts
		defaults["multipart.max_part_bytes"] = limits.MaxPartBytes
		defaults["xml.max_bytes"], defaults["xml.max_depth"], defaults["xml.max_nodes"] = 1<<20, 256, 100000
	case "filter":
		defaults["on_no_match"] = "drop"
	case "code":
		defaults["timeout"] = defaultCodeTimeout.String()
		defaults["max_call_stack"] = defaultCodeStack
		defaults["max_input_bytes"] = defaultCodeInputSize
		defaults["max_output_bytes"] = defaultCodeOutputSize
	case "http_request":
		defaults["method"] = "POST"
		defaults["timeout"] = defaultHTTPRequestTimeout.String()
		defaults["retry.initial_backoff"], defaults["retry.max_backoff"] = "500ms", "30s"
	case "publish":
		defaults["wire_body"], defaults["payload"] = "envelope", "envelope"
		initial, max := publishBackoffs(&PublishRetryOptions{})
		defaults["retry.initial_backoff"], defaults["retry.max_backoff"] = initial.String(), max.String()
	case "batch":
		options := (BatchOptions{}).normalized()
		defaults["max_children"], defaults["max_inflight"], defaults["timeout"] = options.MaxChildren, options.MaxInflight, options.Timeout
	}
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
