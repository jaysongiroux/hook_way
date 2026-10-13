package transform

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func repeatedSchema(levels int) map[string]any {
	definitions := map[string]any{"d0": map[string]any{"type": "string"}}
	for i := 1; i <= levels; i++ {
		ref := map[string]any{"$ref": fmt.Sprintf("#/definitions/d%d", i-1)}
		definitions[fmt.Sprintf("d%d", i)] = map[string]any{"allOf": []any{ref, ref}}
	}
	return map[string]any{"$ref": fmt.Sprintf("#/definitions/d%d", levels), "definitions": definitions}
}

func TestSchemaReferenceAmplificationBoundedBeforeValidation(t *testing.T) {
	for _, levels := range []int{16, 30, 50} {
		if _, err := NewValidator(repeatedSchema(levels), nil); err == nil || !strings.Contains(err.Error(), "work limit") {
			t.Fatalf("d%d must fail compilation work bound, got %v", levels, err)
		}
	}
	v, err := NewValidator(repeatedSchema(8), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := v.Validate("valid"); err != nil {
		t.Fatal(err)
	}
	if _, err := v.Validate(map[string]any{}); err == nil {
		t.Fatal("bounded invalid sample must still fail")
	}
}

func TestRecursiveInstanceSchemasRemainSupported(t *testing.T) {
	for _, draft := range []string{"http://json-schema.org/draft-07/schema#", "https://json-schema.org/draft/2020-12/schema"} {
		schema := map[string]any{"$schema": draft, "type": "object", "properties": map[string]any{"children": map[string]any{"type": "array", "items": map[string]any{"$ref": "#"}}}}
		if strings.Contains(draft, "2020") {
			schema["$dynamicAnchor"] = "tree"
			schema["properties"] = map[string]any{"children": map[string]any{"type": "array", "items": map[string]any{"$dynamicRef": "#tree"}}}
		}
		v, err := NewValidator(schema, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := v.Validate(map[string]any{"children": []any{map[string]any{"children": []any{map[string]any{}}}}}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestBodyValidationWorkBudget(t *testing.T) {
	schema := repeatedSchema(8)
	delete(schema, "$ref")
	schema["type"] = "array"
	schema["items"] = map[string]any{"$ref": "#/definitions/d8"}
	v, err := NewValidator(schema, nil)
	if err != nil {
		t.Fatal(err)
	}
	// The body is comfortably within byte/depth/node limits. Repeating a large
	// schema across its entries would still amplify the validator's error tree.
	body := make([]any, 2000)
	for i := range body {
		body[i] = "valid"
	}
	if _, err := v.Validate(body); err == nil || !strings.Contains(err.Error(), "work limit") {
		t.Fatalf("expected work rejection, got %v", err)
	}
	unique, err := NewValidator(map[string]any{"type": "array", "uniqueItems": true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := unique.Validate(body); err == nil || !strings.Contains(err.Error(), "work limit") {
		t.Fatalf("expected unique-comparison work rejection, got %v", err)
	}
}

func TestLongStringScanningWorkIsBounded(t *testing.T) {
	branches := make([]any, 1000)
	for i := range branches {
		branches[i] = map[string]any{"pattern": "^a+$"}
	}
	v, err := NewValidator(map[string]any{"allOf": branches}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := v.Validate(strings.Repeat("a", 1<<20)); !errors.Is(err, ErrJSONValidationWork) {
		t.Fatalf("expected scan budget rejection, got %v", err)
	}
	if _, err := v.Validate("small"); err == nil {
		t.Fatal("invalid short sample must still fail pattern validation")
	}
}
