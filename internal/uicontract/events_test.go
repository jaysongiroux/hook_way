package uicontract

import (
	"github.com/jaysongiroux/hook_way/internal/eventops"
	"testing"
)

func TestEventActionValidation(t *testing.T) {
	for _, tc := range []struct {
		action, key string
		input       eventops.ActionInput
		valid       bool
	}{
		{"retry", "key", eventops.ActionInput{Version: "1", AcknowledgeDuplicateRisk: true}, true},
		{"retry", "key", eventops.ActionInput{Version: "1"}, false},
		{"retry", "", eventops.ActionInput{Version: "1", AcknowledgeDuplicateRisk: true}, false},
		{"revoke", "key", eventops.ActionInput{Version: "1", Reason: "Investigating repeated delivery"}, true},
		{"restore", "key", eventops.ActionInput{Version: "1", Reason: "Allow monitoring again"}, true},
		{"restore", "key", eventops.ActionInput{Version: "1"}, false},
		{"revoke", "key", eventops.ActionInput{Version: "not-a-version", Reason: "Stop"}, false},
		{"delete", "key", eventops.ActionInput{Version: "1"}, false},
	} {
		if got := len(ValidateEventAction(tc.action, tc.key, tc.input).Issues) == 0; got != tc.valid {
			t.Fatalf("%s valid=%v", tc.action, got)
		}
	}
}
