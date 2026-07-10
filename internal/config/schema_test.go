package config

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSchemaJSON_IsValidJSONWithTopLevelKeys(t *testing.T) {
	data, err := SchemaJSON()
	if err != nil {
		t.Fatalf("SchemaJSON returned error: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("schema is not valid JSON: %v", err)
	}
	props, ok := doc["properties"].(map[string]any)
	if !ok {
		t.Fatalf("schema has no top-level properties object: %v", doc)
	}
	for _, key := range []string{"server", "destinations", "endpoints"} {
		if _, ok := props[key]; !ok {
			t.Errorf("schema properties missing %q (present: %v)", key, keys(props))
		}
	}
}

func TestSchemaJSON_UsesYAMLFieldNames(t *testing.T) {
	// server.max_body_bytes proves yaml tags (not Go field names) drive the schema.
	data, _ := SchemaJSON()
	if !strings.Contains(string(data), "max_body_bytes") {
		t.Fatalf("schema does not use yaml field names (missing max_body_bytes)")
	}
}

func keys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
