package uicontract

import (
	"strings"
	"testing"
)

func TestResolutionEvidenceValidation(t *testing.T) {
	for _, tc := range []struct {
		name, note, reference string
		invalid               bool
	}{
		{"receipt", "Confirmed receipt in destination logs\nEvent evt-123", "ticket-42", false},
		{"blank", " \n\t", "", true},
		{"too long", strings.Repeat("a", 4097), "", true},
		{"unicode byte limit", strings.Repeat("é", 2049), "", true},
		{"bad utf8", string([]byte{0xff}), "", true},
		{"nul", "receipt\x00hidden", "", true},
		{"escape", "\x1b[0m", "", true},
		{"ref blank", "receipt", "  ", true},
		{"ref newline", "receipt", "foo\nbar", true},
		{"ref limit", "receipt", strings.Repeat("a", 1025), true},
		{"exact limits", strings.Repeat("é", 2048), strings.Repeat("b", 1024), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ValidateReplayAction(ReplayActionInput{Action: ActionResolve, IdempotencyKey: "resolve-1", Note: tc.note, Reference: tc.reference})
			if (len(got.Issues) > 0) != tc.invalid {
				t.Fatalf("issues: %+v", got)
			}
		})
	}
	if got := ValidateReplayAction(ReplayActionInput{Action: "unknown", IdempotencyKey: "k"}); len(got.Issues) != 1 {
		t.Fatal(got)
	}
	if got := ValidateReplayAction(ReplayActionInput{Action: ActionResolve, IdempotencyKey: "k", Note: "receipt", RecordIDs: []string{"id"}}); len(got.Issues) != 1 {
		t.Fatal(got)
	}
}
