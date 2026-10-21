package openapiimport

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// Each tool schema has its own reference namespace. References remain references,
// so recursive models do not expand indefinitely or lose validation constraints.
type schemaConverter struct {
	root      map[string]any
	direction string
	defs      map[string]any
	count     int
}

func converter(root map[string]any, direction string) *schemaConverter {
	return &schemaConverter{root: root, direction: direction, defs: map[string]any{}}
}
func (c *schemaConverter) finish(schema map[string]any) map[string]any {
	if len(c.defs) > 0 {
		schema["$defs"] = c.defs
	}
	return schema
}
func reference(root map[string]any, ref string) (any, error) {
	if !strings.HasPrefix(ref, "#/") {
		return nil, errors.New("only local JSON Pointer references are supported")
	}
	fragment, err := url.PathUnescape(ref[2:])
	if err != nil {
		return nil, errors.New("invalid schema reference encoding")
	}
	var value any = root
	for _, part := range strings.Split(fragment, "/") {
		part = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
		m := object(value)
		v, ok := m[part]
		if !ok {
			return nil, errors.New("unresolved schema reference")
		}
		value = v
	}
	return value, nil
}
func (c *schemaConverter) convert(value any, depth int) (any, error) {
	c.count++
	if depth > 64 || c.count > 10000 {
		return nil, errors.New("schema exceeds traversal limits")
	}
	if b, ok := value.(bool); ok {
		if strings.HasPrefix(text(c.root["openapi"]), "3.0.") {
			return nil, errors.New("boolean schemas require OpenAPI 3.1")
		}
		return b, nil
	}
	m := object(value)
	if m == nil {
		return nil, errors.New("schema must be an object or boolean")
	}
	out := map[string]any{}
	for _, k := range keys(m) {
		v := m[k]
		switch k {
		case "$ref":
			ref, ok := v.(string)
			if !ok {
				return nil, errors.New("invalid schema reference")
			}
			name := fmt.Sprintf("ref_%x", sha256.Sum256([]byte(ref)))[:24]
			if _, ok := c.defs[name]; !ok {
				c.defs[name] = map[string]any{}
				target, err := reference(c.root, ref)
				if err != nil {
					return nil, err
				}
				converted, err := c.convert(target, depth+1)
				if err != nil {
					return nil, err
				}
				c.defs[name] = converted
			}
			out[k] = "#/$defs/" + name
		case "properties", "$defs", "definitions", "patternProperties":
			props := object(v)
			if props == nil {
				return nil, errors.New("schema properties must be an object")
			}
			converted := map[string]any{}
			for _, name := range keys(props) {
				if k == "properties" && c.excluded(props[name]) {
					continue
				}
				child, err := c.convert(props[name], depth+1)
				if err != nil {
					return nil, err
				}
				converted[name] = child
			}
			// References are collected into the schema root's namespace, not embedded.
			if k != "$defs" && k != "definitions" {
				out[k] = converted
			}
		case "items", "additionalProperties", "not", "contains", "propertyNames", "if", "then", "else":
			child, err := c.convert(v, depth+1)
			if k == "additionalProperties" {
				if b, ok := v.(bool); ok {
					child, err = b, nil
				}
			}
			if err != nil {
				return nil, err
			}
			out[k] = child
		case "allOf", "anyOf", "oneOf", "prefixItems":
			list, ok := v.([]any)
			if !ok {
				return nil, errors.New("schema composition must be an array")
			}
			children := []any{}
			for _, child := range list {
				converted, err := c.convert(child, depth+1)
				if err != nil {
					return nil, err
				}
				children = append(children, converted)
			}
			out[k] = children
		case "required":
			list, ok := v.([]any)
			if !ok {
				return nil, errors.New("required must be an array")
			}
			required := []any{}
			for _, name := range list {
				s, ok := name.(string)
				if !ok {
					return nil, errors.New("required entries must be strings")
				}
				if !c.excluded(object(m["properties"])[s]) {
					required = append(required, s)
				}
			}
			if len(required) > 0 {
				out[k] = required
			}
		case "nullable": // OpenAPI 3.0 type adjustment below; never broaden an enum.
			if !strings.HasPrefix(text(c.root["openapi"]), "3.0.") {
				return nil, errors.New("nullable requires OpenAPI 3.0; use a null type in 3.1")
			}
			if _, ok := v.(bool); !ok {
				return nil, errors.New("nullable must be boolean")
			}
		case "discriminator", "$id", "$anchor", "$dynamicRef", "$dynamicAnchor", "xml":
			return nil, fmt.Errorf("unsupported schema keyword %s", k)
		case "$schema":
			if v != "https://json-schema.org/draft/2020-12/schema" && v != "https://spec.openapis.org/oas/3.1/dialect/base" {
				return nil, errors.New("unsupported JSON Schema dialect")
			}
			// The schema is embedded into a root using this same dialect.
		case "type", "enum", "const", "minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum", "multipleOf", "minLength", "maxLength", "pattern", "format", "minItems", "maxItems", "uniqueItems", "minProperties", "maxProperties", "minContains", "maxContains", "title", "description", "default", "example", "examples", "deprecated", "readOnly", "writeOnly":
			out[k] = v
		case "externalDocs": // Annotation only, never fetched.
		default:
			if !strings.HasPrefix(k, "x-") {
				return nil, fmt.Errorf("unsupported schema keyword %s", k)
			}
		}
	}
	if strings.HasPrefix(text(c.root["openapi"]), "3.0.") {
		if nullable, _ := m["nullable"].(bool); nullable && text(m["type"]) != "" {
			out["type"] = []any{m["type"], "null"}
		}
		for _, side := range []string{"Minimum", "Maximum"} {
			key := "exclusive" + side
			if b, ok := m[key].(bool); ok {
				delete(out, key)
				if b {
					bound := strings.ToLower(side)
					v, ok := m[bound]
					if !ok {
						return nil, errors.New("exclusive bound requires a bound")
					}
					out[key] = v
					delete(out, bound)
				}
			}
		}
	}
	return out, nil
}
func (c *schemaConverter) excluded(v any) bool {
	m := object(v)
	if m == nil {
		return false
	}
	if _, ok := m["$ref"]; ok {
		if resolved, err := resolve(c.root, m); err == nil {
			m = resolved
		}
	}
	key := "readOnly"
	if c.direction == "response" {
		key = "writeOnly"
	}
	b, _ := m[key].(bool)
	return b
}
