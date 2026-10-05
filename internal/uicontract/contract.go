// Package uicontract defines the small, environment-independent contract
// shared by the browser WASM bridge and the editor HTTP API.
package uicontract

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

const ContractVersion = 1
const MaxIdempotencyKeyLength = 255
const MaxBatchSize = 100
const MaxResolutionNoteBytes = 4096
const MaxResolutionReferenceBytes = 1024
const ActionResolve = "resolve"

type ResolutionInput struct {
	Note      string `json:"note"`
	Reference string `json:"reference,omitempty"`
}

type InfoDTO struct {
	ContractVersion int      `json:"contract_version"`
	Capabilities    []string `json:"capabilities"`
}

func Info() InfoDTO {
	return InfoDTO{ContractVersion: ContractVersion, Capabilities: []string{
		"core_yaml_validate", "core_schema", "node_schemas",
		"replay_yaml_validate", "replay_schema", "replay_action_input", "replay_resolution_input", "json_sample_validate",
	}}
}

type ActionIssue struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

type ReplayActionInput struct {
	Action         string   `json:"action,omitempty"`
	Note           string   `json:"note,omitempty"`
	Reference      string   `json:"reference,omitempty"`
	IdempotencyKey string   `json:"idempotency_key"`
	FailureIDs     []string `json:"failure_ids,omitempty"`
}

type ReplayActionResult struct {
	Issues []ActionIssue `json:"issues" openapi:"nonnullable"`
}

// ValidateReplayAction checks only the inputs a browser can know before a
// recovery action. The replay API remains responsible for auth and record state.
// An unknown record ID is deliberately passed through for per-item 404s.
func ValidateReplayAction(input ReplayActionInput) ReplayActionResult {
	result := ReplayActionResult{Issues: []ActionIssue{}}
	key := input.IdempotencyKey
	if key == "" {
		result.Issues = append(result.Issues, ActionIssue{"idempotency_key", "the Idempotency-Key header is required"})
	} else if len(key) > MaxIdempotencyKeyLength || strings.IndexFunc(key, func(c rune) bool { return c < 0x21 || c > 0x7e }) >= 0 {
		result.Issues = append(result.Issues, ActionIssue{"idempotency_key", fmt.Sprintf("Idempotency-Key must be 1-%d printable ASCII characters without spaces", MaxIdempotencyKeyLength)})
	}
	switch input.Action {
	case "", "retry":
		if input.Note != "" || input.Reference != "" {
			result.Issues = append(result.Issues, ActionIssue{"action", "evidence fields require action resolve"})
		}
	case ActionResolve:
		result.Issues = append(result.Issues, ValidateResolutionInput(ResolutionInput{Note: input.Note, Reference: input.Reference}).Issues...)
		if input.FailureIDs != nil {
			result.Issues = append(result.Issues, ActionIssue{"failure_ids", "resolution applies to one failure; batch resolution is not supported"})
		}
	default:
		result.Issues = append(result.Issues, ActionIssue{"action", "action must be retry or resolve"})
	}
	if input.FailureIDs != nil && input.Action != ActionResolve {
		if len(input.FailureIDs) == 0 || len(input.FailureIDs) > MaxBatchSize {
			result.Issues = append(result.Issues, ActionIssue{"failure_ids", fmt.Sprintf("failure_ids must list 1 to %d record ids", MaxBatchSize)})
		}
		seen := make(map[string]bool, len(input.FailureIDs))
		for _, id := range input.FailureIDs {
			if seen[id] {
				result.Issues = append(result.Issues, ActionIssue{"failure_ids", fmt.Sprintf("failure_ids lists %q more than once", id)})
				break
			}
			seen[id] = true
		}
	}
	return result
}

// ValidateResolutionInput validates evidence text only. Eligibility, permissions
// and receipt authenticity are server/operator responsibilities.
func ValidateResolutionInput(input ResolutionInput) ReplayActionResult {
	result := ReplayActionResult{Issues: []ActionIssue{}}
	if strings.TrimSpace(input.Note) == "" || len(input.Note) > MaxResolutionNoteBytes || !utf8.ValidString(input.Note) || strings.IndexFunc(input.Note, func(c rune) bool { return unicode.IsControl(c) && c != '\n' && c != '\r' && c != '\t' }) >= 0 {
		result.Issues = append(result.Issues, ActionIssue{"note", fmt.Sprintf("note must contain investigation evidence, be valid UTF-8 and at most %d bytes; only line breaks and tabs are allowed as controls", MaxResolutionNoteBytes)})
	}
	if len(input.Reference) > MaxResolutionReferenceBytes || !utf8.ValidString(input.Reference) || strings.IndexFunc(input.Reference, unicode.IsControl) >= 0 || (input.Reference != "" && strings.TrimSpace(input.Reference) == "") {
		result.Issues = append(result.Issues, ActionIssue{"reference", fmt.Sprintf("reference must be at most %d bytes of printable UTF-8; omit it when unavailable", MaxResolutionReferenceBytes)})
	}
	return result
}
