package flowconfig

import (
	"strings"
	"testing"
)

func TestRenderMessageAttributesTypes(t *testing.T) {
	got, err := renderMessageAttributes(map[string]any{
		"event":          "opened",
		"count":          3,
		"fields_changed": []any{"status", "amount"},
		"diff":           "{changed}",
		"typed": map[string]any{
			"data_type": "String.Array",
			"value":     "{changed}",
		},
	}, map[string]string{"changed": `["status"]`})
	if err != nil {
		t.Fatal(err)
	}
	if got["event"].DataType != "String" || got["event"].Value != "opened" {
		t.Fatalf("event = %+v", got["event"])
	}
	if got["count"].DataType != "Number" || got["count"].Value != "3" {
		t.Fatalf("count = %+v", got["count"])
	}
	if got["fields_changed"].DataType != "String.Array" || got["fields_changed"].Value != `["status","amount"]` {
		t.Fatalf("fields_changed = %+v", got["fields_changed"])
	}
	if got["diff"].DataType != "String.Array" || got["diff"].Value != `["status"]` {
		t.Fatalf("templated JSON array should infer String.Array: %+v", got["diff"])
	}
	if got["typed"].DataType != "String.Array" || got["typed"].Value != `["status"]` {
		t.Fatalf("typed = %+v", got["typed"])
	}
}

func TestRenderMessageAttributesRejectsOversizeValue(t *testing.T) {
	_, err := renderMessageAttributes(map[string]any{
		"too_big": strings.Repeat("x", maxMessageAttributeValueBytes+1),
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "AWS limit") {
		t.Fatalf("err = %v, want AWS size-limit error", err)
	}
}

func TestRenderMessageAttributesRejectsTooMany(t *testing.T) {
	raw := map[string]any{}
	for i := 0; i < maxMessageAttributes+1; i++ {
		raw[strings.Repeat("a", i+1)] = "v"
	}
	_, err := renderMessageAttributes(raw, nil)
	if err == nil || !strings.Contains(err.Error(), "at most 10") {
		t.Fatalf("err = %v, want 10-attribute limit", err)
	}
}

func TestRenderMessageAttributesRejectsUnboundTemplate(t *testing.T) {
	_, err := renderMessageAttributes(map[string]any{"x": "{missing}"}, map[string]string{})
	if err == nil {
		t.Fatal("expected unbound template error")
	}
}

func TestBinaryAttributeRenderingPreservesEncodedBytes(t *testing.T) {
	attrs, err := renderMessageAttributes(map[string]any{"sample": map[string]any{"data_type": "Binary", "value": "AP8="}, "count": map[string]any{"data_type": "Number.int", "value": 42}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if attrs["sample"].Value != "AP8=" || attrs["sample"].DataType != "Binary" || attrs["count"].DataType != "Number.int" {
		t.Fatalf("attributes = %+v", attrs)
	}
	if _, err := renderMessageAttributes(map[string]any{"sample": map[string]any{"data_type": "Binary", "value": "bad"}}, nil); err == nil {
		t.Fatal("invalid binary accepted")
	}
}

func TestTypedStringArrayOmitsOnlyExplicitEmptyValues(t *testing.T) {
	for _, value := range []string{"[]", `["status"]`} {
		attrs, err := renderMessageAttributes(map[string]any{"fields_changed": map[string]any{"data_type": "String.Array", "value": "{fields}", "omit_if_empty": true}}, map[string]string{"fields": value})
		if err != nil {
			t.Fatal(err)
		}
		if (len(attrs) == 0) != (value == "[]") {
			t.Fatal(attrs)
		}
	}
	if _, err := renderMessageAttributes(map[string]any{"x": map[string]any{"data_type": "String", "value": "", "omit_if_empty": true}}, nil); err == nil {
		t.Fatal("non-array omit accepted")
	}
}
