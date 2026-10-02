package transform

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/jaysongiroux/hook_way/internal/jsoncanon"
)

// ScalarCoercion is explicit: union/anyOf identifiers are not silently changed.
type ScalarCoercion struct {
	Path string `yaml:"path" json:"path" jsonschema:"required"`
	Type string `yaml:"type" json:"type" jsonschema:"required,enum=string,enum=number,enum=integer,enum=boolean,enum=null"`
}
type Validator struct {
	schema    *jsonschema.Schema
	coercions []ScalarCoercion
}
type offlineSchemaLoader struct{}

func (offlineSchemaLoader) Load(string) (any, error) {
	return nil, errors.New("external schema resources are disabled")
}
func NewValidator(schema map[string]any, coercions []ScalarCoercion) (*Validator, error) {
	raw, err := json.Marshal(schema)
	if err != nil || len(raw) > 256<<10 || len(schema) == 0 {
		return nil, errors.New("JSON schema must be nonempty and at most 256 KiB")
	}
	doc, err := decodeJSONCopy(raw)
	if err != nil {
		return nil, err
	}
	count := 0
	if err = boundJSON(doc, 0, &count, 10000, 64); err != nil {
		return nil, err
	}
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft7)
	c.UseLoader(offlineSchemaLoader{})
	c.AssertFormat()
	const location = "https://hookway.invalid/schema.json"
	if err = c.AddResource(location, doc); err != nil {
		return nil, errors.New("invalid JSON schema resource")
	}
	compiled, err := c.Compile(location)
	if err != nil {
		return nil, fmt.Errorf("invalid JSON schema: %w", err)
	}
	if len(coercions) > 64 {
		return nil, errors.New("at most 64 explicit scalar coercions are supported")
	}
	for _, rule := range coercions {
		if !JSONFieldPath(rule.Path) {
			return nil, errors.New("invalid coercion path")
		}
		switch rule.Type {
		case "string", "number", "integer", "boolean", "null":
		default:
			return nil, errors.New("invalid coercion type")
		}
		// Only a literal, single type at a properties path can authorize coercion.
		current := doc
		for _, part := range strings.Split(rule.Path, ".") {
			m, ok := current.(map[string]any)
			if !ok {
				return nil, errors.New("coercion requires literal properties")
			}
			properties, _ := m["properties"].(map[string]any)
			current = properties[part]
		}
		node, ok := current.(map[string]any)
		if !ok || node["type"] != rule.Type {
			return nil, errors.New("coercion type must match a single declared property type")
		}
	}
	return &Validator{schema: compiled, coercions: append([]ScalarCoercion(nil), coercions...)}, nil
}
func (v *Validator) Validate(body any) (any, error) {
	raw, err := json.Marshal(body)
	if err != nil || len(raw) > 20<<20 {
		return nil, errors.New("JSON validation body limit")
	}
	out, err := decodeJSONCopy(raw)
	if err != nil {
		return nil, err
	}
	count := 0
	if err = boundJSON(out, 0, &count, 1000000, 128); err != nil {
		return nil, err
	}
	for _, rule := range v.coercions {
		value, found := GetPath(out, rule.Path)
		if !found {
			continue
		}
		next, ok := coerceScalar(value, rule.Type)
		if !ok {
			continue
		}
		out, err = SetPath(out, rule.Path, next)
		if err != nil {
			return nil, err
		}
	}
	if err = v.schema.Validate(out); err != nil {
		return nil, errors.New("JSON schema validation failed")
	}
	return out, nil
}
func decodeJSONCopy(raw []byte) (any, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var v any
	err := d.Decode(&v)
	return v, err
}
func boundJSON(v any, depth int, count *int, maxNodes, maxDepth int) error {
	*count++
	if depth > maxDepth || *count > maxNodes {
		return errors.New("JSON structure limit exceeded")
	}
	switch x := v.(type) {
	case map[string]any:
		for _, child := range x {
			if err := boundJSON(child, depth+1, count, maxNodes, maxDepth); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range x {
			if err := boundJSON(child, depth+1, count, maxNodes, maxDepth); err != nil {
				return err
			}
		}
	}
	return nil
}
func coerceScalar(v any, target string) (any, bool) {
	switch target {
	case "string":
		switch x := v.(type) {
		case string:
			return x, true
		case nil:
			return "", true
		case bool:
			return strconv.FormatBool(x), true
		case json.Number:
			raw, err := jsoncanon.Stringify(context.Background(), []byte(x.String()))
			if err != nil {
				return nil, false
			}
			return string(raw), true
		}
	case "boolean":
		switch x := v.(type) {
		case bool:
			return x, true
		case nil:
			return false, true
		case string:
			if x == "true" {
				return true, true
			}
			if x == "false" {
				return false, true
			}
		case json.Number:
			if x == "1" {
				return true, true
			}
			if x == "0" {
				return false, true
			}
		}
	case "null":
		switch x := v.(type) {
		case nil:
			return nil, true
		case string:
			if x == "" {
				return nil, true
			}
		case bool:
			if !x {
				return nil, true
			}
		case json.Number:
			if f, err := x.Float64(); err == nil && f == 0 {
				return nil, true
			}
		}
	case "number", "integer":
		var n float64
		var err error
		switch x := v.(type) {
		case json.Number:
			n, err = x.Float64()
		case nil:
			n = 0
		case bool:
			if x {
				n = 1
			}
		case string:
			if strings.TrimSpace(x) == "" {
				return nil, false
			}
			n, err = strconv.ParseFloat(strings.TrimSpace(x), 64)
		default:
			return nil, false
		}
		if err != nil || math.IsInf(n, 0) || math.IsNaN(n) || (target == "integer" && math.Trunc(n) != n) {
			return nil, false
		}
		return json.Number(strconv.FormatFloat(n, 'f', -1, 64)), true
	}
	return nil, false
}
func JSONFieldPath(path string) bool {
	if len(path) == 0 || len(path) > 256 {
		return false
	}
	for _, p := range strings.Split(path, ".") {
		if p == "" {
			return false
		}
	}
	return true
}

// StripFields copies only the modified ancestor maps. Other fields and scalar
// representations remain unchanged; callers never mutate retained subtrees.
func StripFields(body any, paths []string) (any, error) {
	root, ok := body.(map[string]any)
	if !ok {
		return nil, errors.New("strip_fields requires a JSON object")
	}
	out := cloneMap(root)
	for _, path := range paths {
		if !JSONFieldPath(path) {
			return nil, errors.New("invalid strip path")
		}
		parts := strings.Split(path, ".")
		m := out
		for i, part := range parts {
			if i == len(parts)-1 {
				delete(m, part)
				break
			}
			next, ok := m[part].(map[string]any)
			if !ok {
				break
			}
			copy := cloneMap(next)
			m[part] = copy
			m = copy
		}
	}
	return out, nil
}
