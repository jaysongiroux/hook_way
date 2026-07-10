// Package configapi exposes Hookway config validation and schema as UI-facing
// JSON. It adds no validation logic — it wraps internal/config so a client (a
// future config UI, an editor, scripts) gets a stable JSON contract.
package configapi

import "github.com/jasongiroux/hookway/internal/config"

// Issue is one validation problem, anchored to a dotted config path.
type Issue struct {
	Field      string `json:"field"`
	Message    string `json:"message"`
	Suggestion string `json:"suggestion,omitempty"`
	Severity   string `json:"severity"`
}

// ValidateResponse is the /validate reply. An empty Issues array means valid.
type ValidateResponse struct {
	Issues []Issue `json:"issues"`
}

// Validate parses and validates raw YAML config bytes, returning structured
// issues. All issues are currently severity "error".
func Validate(data []byte) ValidateResponse {
	issues := config.NewService().ValidateBytes(data)
	out := ValidateResponse{Issues: []Issue{}}
	for _, iss := range issues {
		out.Issues = append(out.Issues, Issue{
			Field:      iss.Field,
			Message:    iss.Message,
			Suggestion: iss.Suggestion,
			Severity:   "error",
		})
	}
	return out
}

// Schema returns the JSON Schema describing the config YAML.
func Schema() ([]byte, error) {
	return config.SchemaJSON()
}
