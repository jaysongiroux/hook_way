package transform

import (
	"fmt"
	"strings"
	"testing"
)

func hiddenDynamicAnchorSchema(levels int, name string) map[string]any {
	definitions := map[string]any{"d0": map[string]any{"type": "string"}}
	for i := 1; i <= levels; i++ {
		ref := map[string]any{"$ref": fmt.Sprintf("#/$defs/d%d", i-1)}
		definitions[fmt.Sprintf("d%d", i)] = map[string]any{"allOf": []any{ref, ref}}
	}
	// This anchor is in the outer resource's private anchor index, even though
	// no ordinary $ref or compiled child points at it. It overrides inner.base.
	definitions[name] = map[string]any{"$dynamicAnchor": "node", "$ref": fmt.Sprintf("#/$defs/d%d", levels)}
	definitions["inner"] = map[string]any{
		"$id": "https://hookway.invalid/inner", "$dynamicRef": "#node",
		"$defs": map[string]any{"base": map[string]any{"$dynamicAnchor": "node", "type": "string"}},
	}
	return map[string]any{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"$ref":    "#/$defs/inner", "$defs": definitions,
	}
}

func TestHiddenDynamicAnchorAmplificationIsBounded(t *testing.T) {
	for _, name := range []string{"hidden", "slash/~ space 雪"} {
		for _, levels := range []int{16, 30, 50} {
			if _, err := NewValidator(hiddenDynamicAnchorSchema(levels, name), nil); err == nil || !strings.Contains(err.Error(), "work limit") {
				t.Fatalf("%q d%d must fail before validation, got %v", name, levels, err)
			}
		}
		v, err := NewValidator(hiddenDynamicAnchorSchema(3, name), nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := v.Validate("valid"); err != nil {
			t.Fatal(err)
		}
		if _, err := v.Validate(map[string]any{}); err == nil {
			t.Fatal("hidden anchor must retain its ordinary validation semantics")
		}
	}
}

func TestAnchorCollectionIgnoresInstanceLiterals(t *testing.T) {
	for _, keyword := range []string{"const", "default", "examples", "x-annotation"} {
		literal := map[string]any{"$dynamicAnchor": "ignored", "$ref": "https://unavailable.invalid/schema"}
		var value any = literal
		if keyword == "examples" {
			value = []any{literal}
		}
		if _, err := NewValidator(map[string]any{
			"$schema": "https://json-schema.org/draft/2020-12/schema", keyword: value,
		}, nil); err != nil {
			t.Fatalf("anchor-looking %s value must not be compiled: %v", keyword, err)
		}
	}
}

func TestRecursiveAnchorInstanceSchemaRemainsSupported(t *testing.T) {
	v, err := NewValidator(map[string]any{
		"$schema": "https://json-schema.org/draft/2019-09/schema", "$recursiveAnchor": true,
		"type": "object", "properties": map[string]any{
			"children": map[string]any{"type": "array", "items": map[string]any{"$recursiveRef": "#"}},
		},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := v.Validate(map[string]any{"children": []any{map[string]any{"children": []any{map[string]any{}}}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := v.Validate(map[string]any{"children": []any{"invalid"}}); err == nil {
		t.Fatal("recursive child type must still be checked")
	}
}

func TestRecursiveReferenceEntryAmplificationIsBounded(t *testing.T) {
	// The library's recursive resolver can select a reference-entry descendant
	// in an anchored resource. The descendant does not itself declare an anchor.
	branch := map[string]any{"properties": map[string]any{"next": map[string]any{"$recursiveRef": "https://hookway.invalid/base#"}}}
	schema := map[string]any{
		"$schema": "https://json-schema.org/draft/2019-09/schema", "$ref": "#/$defs/resource/$defs/entry",
		"$defs": map[string]any{
			"base": map[string]any{"$id": "https://hookway.invalid/base", "$recursiveAnchor": true, "type": "object"},
			"resource": map[string]any{
				"$id": "https://hookway.invalid/resource", "$recursiveAnchor": true,
				"$defs": map[string]any{"entry": map[string]any{"type": "object", "allOf": []any{branch, branch}}},
			},
		},
	}
	if _, err := NewValidator(schema, nil); err == nil || !strings.Contains(err.Error(), "work limit") {
		t.Fatalf("recursive entry branches must be included in work bound, got %v", err)
	}
}
