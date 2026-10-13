package graph

import "testing"

func TestFlagMetadataIsIndependentAcrossFanout(t *testing.T) {
	in := Envelope{Flags: map[string]any{"rollout": map[string]any{"settings": map[string]any{"value": map[string]any{"items": []any{map[string]any{"limit": 3.0}}}}}}}
	cloned, err := clonePayload(in)
	if err != nil {
		t.Fatal(err)
	}
	value := cloned.(Envelope).Flags["rollout"].(map[string]any)["settings"].(map[string]any)["value"].(map[string]any)
	value["items"].([]any)[0].(map[string]any)["limit"] = 99.0
	original := in.Flags["rollout"].(map[string]any)["settings"].(map[string]any)["value"].(map[string]any)
	if original["items"].([]any)[0].(map[string]any)["limit"] != 3.0 {
		t.Fatal("fanout shared flag JSON")
	}
}
