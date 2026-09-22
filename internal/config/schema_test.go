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
	for _, key := range []string{"server", "security", "metrics", "logging", "replay", "destinations"} {
		if _, ok := props[key]; !ok {
			t.Errorf("schema properties missing %q (present: %v)", key, keys(props))
		}
	}
}

func TestDestinationTypeSchemasCoverKnownTypes(t *testing.T) {
	schemas := DestinationTypeSchemas()
	for _, name := range []string{"webhook", "rabbitmq", "redis_stream", "mongodb", "temporal"} {
		got, ok := schemas[name]
		if !ok || got.Block == "" || got.Schema == nil {
			t.Errorf("%s destination schema = %+v, found=%v", name, got, ok)
		}
	}
	if schemas["redis_kv"].Block != "redis" || schemas["mongodb"].Block != "mongodb" {
		t.Fatalf("block keys = redis_kv:%q mongodb:%q", schemas["redis_kv"].Block, schemas["mongodb"].Block)
	}
	if len(schemas) != 13 {
		t.Fatalf("destination schemas=%d, want 13", len(schemas))
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
