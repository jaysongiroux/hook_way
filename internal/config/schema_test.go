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
	for _, key := range []string{"server", "security", "metrics", "logging", "destinations"} {
		if _, ok := props[key]; !ok {
			t.Errorf("schema properties missing %q (present: %v)", key, keys(props))
		}
	}
	replay, ok := props["replay"].(map[string]any)
	if !ok {
		t.Fatalf("schema lacks the replay block (present: %v)", keys(props))
	}
	replayProps, _ := replay["properties"].(map[string]any)
	for _, key := range []string{"url", "capture_token", "max_record_bytes", "tls", "control"} {
		if _, ok := replayProps[key]; !ok {
			t.Errorf("replay schema lacks %q", key)
		}
	}
	flows, _ := props["flows"].(map[string]any)
	flow, _ := flows["additionalProperties"].(map[string]any)
	flowProps, _ := flow["properties"].(map[string]any)
	for _, key := range []string{"nodes", "edges", "delivery_failure_policy", "capture_failures"} {
		if _, ok := flowProps[key]; !ok {
			t.Errorf("flow schema lacks %q (flows = %v)", key, flows)
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
	if len(schemas) != 15 {
		t.Fatalf("destination schemas=%d, want 15", len(schemas))
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
