// Package configapi exposes replay-service config validation and schema in the
// same JSON contract used by the browser editor for core config.
package configapi

import (
	"encoding/json"

	coreapi "github.com/jaysongiroux/hook_way/internal/configapi"
	replayconfig "github.com/jaysongiroux/hook_way/internal/replay/config"
)

// Validate parses and validates browser-supplied replay YAML without expanding
// environment references against the editor host.
func Validate(data []byte) coreapi.ValidateResponse {
	_, issues := replayconfig.ValidateBytes(data)
	out := coreapi.ValidateResponse{Issues: []coreapi.Issue{}}
	for _, issue := range issues {
		severity := issue.Severity
		if severity == "" {
			severity = "error"
		}
		out.Issues = append(out.Issues, coreapi.Issue{
			Field: issue.Field, Message: issue.Message, Suggestion: issue.Suggestion, Severity: severity,
		})
	}
	return out
}

// Schema returns the JSON Schema describing replay.yaml.
func Schema() ([]byte, error) {
	return replayconfig.SchemaJSON()
}

// ValidateJSON returns Validate's stable JSON wire representation for WASM.
func ValidateJSON(data []byte) (string, error) {
	b, err := json.Marshal(Validate(data))
	if err != nil {
		return "", err
	}
	return string(b), nil
}
