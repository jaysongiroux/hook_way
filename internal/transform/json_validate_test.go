package transform

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestJSONValidationPreservesValuesWithoutCoercion(t *testing.T) {
	validator, err := NewValidator(map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "integer"}, "missing": map[string]any{"default": "injected"}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	body, err := DecodeJSON([]byte(`{"id":9007199254740993,"decimal":0.123456789012345678901,"unknown":{"keep":[null,true]}}`))
	if err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(body)
	out, err := validator.Validate(body)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(out)
	if string(before) != string(after) || out.(map[string]any)["id"] != json.Number("9007199254740993") {
		t.Fatalf("body changed: %s -> %s", before, after)
	}
	if _, ok := out.(map[string]any)["missing"]; ok {
		t.Fatal("default was injected")
	}
}
func TestExplicitScalarCoercionsExact(t *testing.T) {
	cases := []struct {
		value  any
		target string
		want   any
		ok     bool
	}{
		{"9007199254740993", "integer", json.Number("9007199254740993"), true},
		{"922337203685477580812345", "number", json.Number("9.22337203685477580812345e+23"), true},
		{"0.123456789012345678901", "number", json.Number("0.123456789012345678901"), true},
		{"9007199254740993.1", "integer", nil, false},
		{"  +.5000e1  ", "number", json.Number("5"), true},
		{"1e1000000000", "number", nil, false},
		{"1e-1000000000", "integer", nil, false},
		{"0x1p2", "number", json.Number("4"), true},
		{"0x20000000000001p0", "integer", json.Number("9007199254740993"), true},
		{"0x20000000000001p-1", "number", json.Number("4503599627370496.5"), true},
		{"-0x1.8p-1", "number", json.Number("-0.75"), true},
		{"0x1p1000000000", "number", nil, false},
		{"0x1p-1000000000", "number", nil, false},
		{"1_000", "number", json.Number("1000"), true},
		{"1__0", "number", nil, false},
		{nil, "number", json.Number("0"), true}, {true, "integer", json.Number("1"), true},
		{"", "number", nil, false}, {"NaN", "number", nil, false}, {"Inf", "number", nil, false},
		{json.Number("9007199254740993"), "string", "9007199254740993", true},
		{json.Number("0.123456789012345678901"), "string", "0.123456789012345678901", true},
		{json.Number("1e21"), "string", "1e+21", true}, {json.Number("1e-6"), "string", "0.000001", true},
		{json.Number("1e-7"), "string", "1e-7", true}, {nil, "string", "", true},
		{false, "string", "false", true}, {"true", "boolean", true, true}, {"TRUE", "boolean", nil, false},
		{json.Number("1"), "boolean", true, true}, {json.Number("1.0"), "boolean", nil, false},
		{nil, "boolean", false, true}, {"", "null", nil, true}, {false, "null", nil, true},
		{json.Number("0.0"), "null", nil, true}, {json.Number("1e-4096"), "null", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.target+"/"+stringifyForTest(tc.value), func(t *testing.T) {
			got, ok := coerceScalar(tc.value, tc.target)
			if ok != tc.ok || (ok && !reflect.DeepEqual(got, tc.want)) {
				t.Fatalf("got %#v,%v want %#v,%v", got, ok, tc.want, tc.ok)
			}
		})
	}
}
func stringifyForTest(v any) string { raw, _ := json.Marshal(v); return string(raw) }
func TestJSONValidationCopiesCoercedInputAndRejectsAmbiguousRules(t *testing.T) {
	schema := map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "integer"}}}
	v, err := NewValidator(schema, []ScalarCoercion{{Path: "id", Type: "integer"}})
	if err != nil {
		t.Fatal(err)
	}
	body := map[string]any{"id": "9007199254740993", "extra": "keep"}
	out, err := v.Validate(body)
	if err != nil {
		t.Fatal(err)
	}
	if out.(map[string]any)["id"] != json.Number("9007199254740993") || body["id"] != "9007199254740993" {
		t.Fatal("coercion rounded or mutated input")
	}
	for _, property := range []map[string]any{{"type": []string{"integer", "string"}}, {"anyOf": []any{map[string]any{"type": "integer"}, map[string]any{"type": "string"}}}, {"$ref": "#/definitions/id"}} {
		if _, err := NewValidator(map[string]any{"properties": map[string]any{"id": property}, "definitions": map[string]any{"id": map[string]any{"type": "integer"}}}, []ScalarCoercion{{Path: "id", Type: "integer"}}); err == nil {
			t.Fatalf("ambiguous coercion accepted: %#v", property)
		}
	}
}
func TestJSONDraftsReferencesAndFormats(t *testing.T) {
	for _, draft := range []string{"", "http://json-schema.org/draft-04/schema#", "https://json-schema.org/draft-06/schema", "http://json-schema.org/draft-07/schema#", "https://json-schema.org/draft/2019-09/schema", "https://json-schema.org/draft/2020-12/schema"} {
		t.Run(draft, func(t *testing.T) {
			schema := map[string]any{"type": "object", "properties": map[string]any{"email": map[string]any{"$ref": "#/definitions/email"}}, "definitions": map[string]any{"email": map[string]any{"type": "string", "format": "email"}}}
			if draft != "" {
				schema["$schema"] = draft
			}
			v, err := NewValidator(schema, nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := v.Validate(map[string]any{"email": "person@example.com"}); err != nil {
				t.Fatal(err)
			}
			if _, err := v.Validate(map[string]any{"email": "not-an-email"}); err == nil {
				t.Fatal("format ignored")
			}
		})
	}
	for _, schema := range []map[string]any{{"$schema": "https://example.test/schema"}, {"$ref": "https://example.test/schema"}, {"$ref": "file:///etc/passwd"}, {"type": "invalid"}} {
		if _, err := NewValidator(schema, nil); err == nil {
			t.Fatalf("unsupported schema accepted: %#v", schema)
		}
	}
}
func TestJSONValidationIssuesAreSanitizedPointers(t *testing.T) {
	v, err := NewValidator(map[string]any{"type": "object", "properties": map[string]any{"a/b~c": map[string]any{"enum": []string{"configured-secret"}}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = v.Validate(map[string]any{"a/b~c": "sample-secret"})
	var failure *ValidationError
	if !errors.As(err, &failure) || len(failure.Issues) != 1 {
		t.Fatalf("issues missing: %v", err)
	}
	issue := failure.Issues[0]
	raw, _ := json.Marshal(failure)
	if issue.Path != "/a~1b~0c" || issue.Keyword != "enum" || strings.Contains(string(raw), "secret") {
		t.Fatalf("bad issues: %s", raw)
	}
}
func TestJSONValidationBoundsAndInvalidInputs(t *testing.T) {
	v, err := NewValidator(map[string]any{"type": []string{"object", "array", "null", "number", "string"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{`{} {}`, ``, `NaN`, `1e1000000000`, `1e-1000000000`, strings.Repeat("[", 129) + "0" + strings.Repeat("]", 129)} {
		if _, err := DecodeJSON([]byte(raw)); err == nil {
			t.Fatal("invalid/oversize JSON accepted")
		}
	}
	for _, body := range []any{[]byte("raw"), struct{ Value string }{"secret"}, json.Number("1e1000000000")} {
		if _, err := v.Validate(body); err == nil {
			t.Fatalf("nonJSON/oversize body accepted: %T", body)
		}
	}
	schema := map[string]any{"type": "object", "minimum": json.Number("1e1000000000")}
	if _, err := NewValidator(schema, nil); err == nil {
		t.Fatal("schema exponent unbounded")
	}
}
func TestJSONSchemaYAMLNumericPrecisionAndRoundTrip(t *testing.T) {
	var schema JSONSchema
	raw := []byte("type: object\nproperties:\n  id: {type: integer, minimum: 9007199254740993}\n  fraction: {const: 0.123456789012345678901}\n  huge: {const: 922337203685477580812345}\n  hex: {const: 0x10}\n")
	if err := yaml.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	props := schema["properties"].(map[string]any)
	if props["id"].(map[string]any)["minimum"] != json.Number("9007199254740993") || props["fraction"].(map[string]any)["const"] != json.Number("0.123456789012345678901") || props["huge"].(map[string]any)["const"] != json.Number("9.22337203685477580812345e+23") || props["hex"].(map[string]any)["const"] != json.Number("16") {
		t.Fatalf("schema precision lost: %#v", schema)
	}
	encoded, err := yaml.Marshal(schema)
	if err != nil {
		t.Fatal(err)
	}
	var roundtrip JSONSchema
	if err := yaml.Unmarshal(encoded, &roundtrip); err != nil || !reflect.DeepEqual(schema, roundtrip) {
		t.Fatalf("numeric YAML roundtrip failed: %s (%v) %#v", encoded, err, roundtrip)
	}
	v, err := NewValidator(schema, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := v.Validate(map[string]any{"id": json.Number("9007199254740992")}); err == nil {
		t.Fatal("rounded minimum allowed lower integer")
	}
	for _, raw := range []string{"const: .inf", "const: 1e1000000000", "type: string\ntype: object"} {
		if err := yaml.Unmarshal([]byte(raw), &schema); err == nil {
			t.Fatalf("invalid YAML schema accepted: %s", raw)
		}
	}
}
func TestJSONValidatorConcurrentUse(t *testing.T) {
	v, err := NewValidator(map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "integer"}}}, []ScalarCoercion{{Path: "id", Type: "integer"}})
	if err != nil {
		t.Fatal(err)
	}
	body := map[string]any{"id": "9007199254740993"}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Go(func() {
			if _, err := v.Validate(body); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if body["id"] != "9007199254740993" {
		t.Fatal("input mutated concurrently")
	}
}
