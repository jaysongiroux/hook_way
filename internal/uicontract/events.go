package uicontract

import "github.com/jaysongiroux/hook_way/internal/eventops"

// ValidateEventAction adapts the operator wire input to the shared browser/native
// action validator. Wire types stay independent of authoring and runtime code.
func ValidateEventAction(action, key string, input eventops.ActionInput) ReplayActionResult {
	return ValidateReplayAction(ReplayActionInput{Action: "event_" + action, IdempotencyKey: key, Version: input.Version, Note: input.Reason, AcknowledgeDuplicateRisk: input.AcknowledgeDuplicateRisk})
}
