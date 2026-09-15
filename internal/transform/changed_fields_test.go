package transform

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestChangedFieldsNestedAndStatusPromotion(t *testing.T) {
	prev := map[string]any{
		"status": map[string]any{"code": "A", "detail": "old"},
		"variables": map[string]any{
			"borrower_first_name": "Ann",
			"unchanged":           "x",
		},
	}
	curr := map[string]any{
		"status": map[string]any{"code": "B", "detail": "old"},
		"variables": map[string]any{
			"borrower_first_name": "Bob",
			"unchanged":           "x",
		},
	}
	got := ChangedFields(prev, curr)
	if !contains(got, "status") || !contains(got, "status.code") || !contains(got, "variables.borrower_first_name") {
		t.Fatalf("got %v, want status promotion plus nested paths", got)
	}
	if contains(got, "variables.unchanged") {
		t.Fatalf("unchanged path leaked: %v", got)
	}
}

func TestChangedFieldsAddedRemovedNullsAndTypeChange(t *testing.T) {
	prev := map[string]any{"keep": 1, "gone": "x", "n": nil, "t": "s"}
	curr := map[string]any{"keep": 1, "added": true, "n": "now", "t": 3}
	got := ChangedFields(prev, curr)
	for _, want := range []string{"gone", "added", "n", "t"} {
		if !contains(got, want) {
			t.Fatalf("missing %s in %v", want, got)
		}
	}
	if contains(got, "keep") {
		t.Fatalf("unchanged keep leaked: %v", got)
	}
}

func TestChangedFieldsArrays(t *testing.T) {
	prev := map[string]any{"items": []any{map[string]any{"id": 1}, map[string]any{"id": 2}}}
	curr := map[string]any{"items": []any{map[string]any{"id": 1}, map[string]any{"id": 3}}}
	got := ChangedFields(prev, curr)
	if !contains(got, "items.1.id") {
		t.Fatalf("got %v, want items.1.id", got)
	}
}

func TestChangedFieldsJSONRoundTrip(t *testing.T) {
	s, err := ChangedFieldsJSON(map[string]any{"a": 1}, map[string]any{"a": 2})
	if err != nil {
		t.Fatal(err)
	}
	var arr []string
	if err := json.Unmarshal([]byte(s), &arr); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(arr, []string{"a"}) {
		t.Fatalf("json = %s", s)
	}
}

func TestGetSetPath(t *testing.T) {
	root := map[string]any{"data": map[string]any{"object": map[string]any{"id": "abc"}}}
	got, ok := GetPath(root, "data.object.id")
	if !ok || got != "abc" {
		t.Fatalf("GetPath = %v %v", got, ok)
	}
	next, err := SetPath(root, "data.mapped.id", "xyz")
	if err != nil {
		t.Fatal(err)
	}
	got, ok = GetPath(next, "data.mapped.id")
	if !ok || got != "xyz" {
		t.Fatalf("SetPath = %#v", next)
	}
	if _, ok := GetPath(root, "data.mapped.id"); ok {
		t.Fatal("SetPath mutated the original")
	}
}
