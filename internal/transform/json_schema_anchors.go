package transform

import (
	"net/url"
	"strconv"
	"strings"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

// Anchor roots may be compiled into the library's private resource indexes
// without appearing in the exported graph. Collect them from schema keyword
// positions only, never from instance literals in const/default/examples.
type declaredSchemaAnchors struct {
	roots                  []*jsonschema.Schema
	recursiveResourceNodes map[string]bool
}

func compileDeclaredSchemaAnchors(c *jsonschema.Compiler, location string, doc any) (declaredSchemaAnchors, error) {
	anchors := declaredSchemaAnchors{recursiveResourceNodes: map[string]bool{}}
	var paths []string
	var walk func(any, string, bool)
	walk = func(value any, path string, recursive bool) {
		object, ok := value.(map[string]any)
		if !ok {
			if _, ok := value.(bool); ok {
				anchors.recursiveResourceNodes[path] = recursive
			}
			return
		}
		if id, ok := object["$id"].(string); ok && id != "" {
			recursive = false
		}
		if enabled, ok := object["$recursiveAnchor"].(bool); ok && enabled {
			recursive = true
			paths = append(paths, path)
		}
		if name, ok := object["$dynamicAnchor"].(string); ok && name != "" {
			paths = append(paths, path)
		}
		anchors.recursiveResourceNodes[path] = recursive
		child := func(key string, value any) { walk(value, path+"/"+schemaPointerToken(key), recursive) }
		for _, key := range []string{"$defs", "definitions", "properties", "patternProperties", "dependentSchemas"} {
			if children, ok := object[key].(map[string]any); ok {
				for name, value := range children {
					walk(value, path+"/"+schemaPointerToken(key)+"/"+schemaPointerToken(name), recursive)
				}
			}
		}
		if dependencies, ok := object["dependencies"].(map[string]any); ok {
			for name, value := range dependencies {
				walk(value, path+"/dependencies/"+schemaPointerToken(name), recursive)
			}
		}
		for _, key := range []string{"allOf", "anyOf", "oneOf", "prefixItems"} {
			if children, ok := object[key].([]any); ok {
				for i, value := range children {
					walk(value, path+"/"+key+"/"+strconv.Itoa(i), recursive)
				}
			}
		}
		for _, key := range []string{"not", "if", "then", "else", "propertyNames", "additionalProperties", "additionalItems", "unevaluatedProperties", "unevaluatedItems", "contains", "contentSchema"} {
			if value, exists := object[key]; exists {
				child(key, value)
			}
		}
		if items, ok := object["items"].([]any); ok {
			for i, value := range items {
				walk(value, path+"/items/"+strconv.Itoa(i), recursive)
			}
		} else if value, exists := object["items"]; exists {
			child("items", value)
		}
	}
	walk(doc, "", false)
	seen := map[*jsonschema.Schema]bool{}
	for _, path := range paths {
		fragment := (&url.URL{Fragment: path}).EscapedFragment()
		compiled, err := c.Compile(location + "#" + fragment)
		if err != nil {
			return declaredSchemaAnchors{}, err
		}
		if !seen[compiled] {
			seen[compiled] = true
			anchors.roots = append(anchors.roots, compiled)
		}
	}
	return anchors, nil
}
func schemaPointerToken(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "~", "~0"), "/", "~1")
}
func (a declaredSchemaAnchors) recursiveEntry(s *jsonschema.Schema) bool {
	if s == nil {
		return false
	}
	location, err := url.Parse(s.Location)
	if err != nil {
		return false
	}
	return a.recursiveResourceNodes[location.Fragment]
}
