package uicontract

import (
	"strings"
	"testing"
)

func TestInfoOnlyAdvertisesImplementedCapabilities(t *testing.T) {
	info := Info()
	if info.ContractVersion != ContractVersion {
		t.Fatalf("version %d", info.ContractVersion)
	}
	want := map[string]bool{
		"mcp_event_action_input": true, "core_yaml_validate": true, "core_yaml_bundle_validate": true, "core_schema": true, "node_schemas": true,
		"replay_yaml_validate": true, "replay_schema": true, "replay_action_input": true,
		"openapi_inspect": true, "openapi_generate": true, "json_sample_validate": true, "replay_resolution_input": true,
	}
	for _, cap := range info.Capabilities {
		if !want[cap] {
			t.Fatalf("unexpected capability %q", cap)
		}
		delete(want, cap)
	}
	if len(want) != 0 {
		t.Fatalf("missing capabilities %v", want)
	}
}

func TestValidateReplayAction(t *testing.T) {
	cases := []struct {
		name   string
		input  ReplayActionInput
		issues int
	}{
		{"single key", ReplayActionInput{IdempotencyKey: "k"}, 0},
		{"batch unknown id accepted", ReplayActionInput{IdempotencyKey: "k", RecordIDs: []string{"any-string"}}, 0},
		{"missing key", ReplayActionInput{}, 1},
		{"space", ReplayActionInput{IdempotencyKey: "bad key"}, 1},
		{"non ascii", ReplayActionInput{IdempotencyKey: "é"}, 1},
		{"too long", ReplayActionInput{IdempotencyKey: strings.Repeat("a", 256)}, 1},
		{"empty batch", ReplayActionInput{IdempotencyKey: "k", RecordIDs: []string{}}, 1},
		{"duplicate", ReplayActionInput{IdempotencyKey: "k", RecordIDs: []string{"x", "x"}}, 1},
		{"too many", ReplayActionInput{IdempotencyKey: "k", RecordIDs: make([]string, 101)}, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := len(ValidateReplayAction(tc.input).Issues); got != tc.issues {
				t.Fatalf("issues=%d want=%d", got, tc.issues)
			}
		})
	}
}
