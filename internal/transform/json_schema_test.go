package transform

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

func TestSchemaCoercionProjectionAndUnions(t *testing.T) {
	schema := map[string]any{"type": "object", "required": []string{"event", "id", "object"}, "properties": map[string]any{"event": map[string]any{"type": "string"}, "id": map[string]any{"anyOf": []any{map[string]any{"type": "string"}, map[string]any{"type": "number"}}}, "object": map[string]any{"type": "object", "required": []string{"id"}, "properties": map[string]any{"id": map[string]any{"type": "string"}}}}}
	v, err := NewValidator(schema, []ScalarCoercion{{Path: "event", Type: "string"}, {Path: "object.id", Type: "string"}})
	if err != nil {
		t.Fatal(err)
	}
	body := map[string]any{"event": true, "id": json.Number("12"), "object": map[string]any{"id": json.Number("4"), "content": "file", "extra": "keep"}, "extra": []any{"keep"}}
	next, err := v.Validate(body)
	if err != nil {
		t.Fatal(err)
	}
	m := next.(map[string]any)
	if m["event"] != "true" || m["id"] != json.Number("12") || m["object"].(map[string]any)["id"] != "4" {
		t.Fatal(next)
	}
	stripped, err := StripFields(next, []string{"object.content"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := stripped.(map[string]any)["object"].(map[string]any)["content"]; ok {
		t.Fatal("content survived")
	}
	if m["object"].(map[string]any)["content"] != "file" || body["event"] != true {
		t.Fatal("input mutated")
	}
	if _, err := v.Validate(map[string]any{"event": "sample", "object": map[string]any{}}); err == nil {
		t.Fatal("required fields ignored")
	}
	if _, err := NewValidator(schema, []ScalarCoercion{{Path: "id", Type: "string"}}); err == nil {
		t.Fatal("union coercion accepted")
	}
	if _, err := NewValidator(map[string]any{"$ref": "https://example.invalid/schema"}, nil); err == nil {
		t.Fatal("external schema fetched")
	}
}
func TestChangedFieldsOriginalAtomicArraysAndMissingNull(t *testing.T) {
	raw := []byte(`{"previous":{"n":null,"status":{"value":1},"items":[{"b":2,"a":1}]},"current":{"status":{"value":2},"items":[{"a":1,"b":2}],"added":null}}`)
	got, err := ChangedFieldsOriginal(context.Background(), raw, "previous", "current")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []string{"status", "added", "items", "n", "status.value"}) {
		t.Fatal(got)
	}
	if got := ChangedFields(map[string]any{"n": nil}, map[string]any{}); !reflect.DeepEqual(got, []string{"n"}) {
		t.Fatal(got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ChangedFieldsOriginal(ctx, raw, "previous", "current"); err == nil {
		t.Fatal("cancel ignored")
	}
}
