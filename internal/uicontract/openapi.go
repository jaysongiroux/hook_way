package uicontract

import (
	"github.com/jaysongiroux/hook_way/internal/apicontract"
	"github.com/jaysongiroux/hook_way/internal/openapiimport"
)

// These are browser-only messages, not HTTP editor routes. Documents remain text
// so JavaScript cannot round numbers before Go inspects their original bytes.
type OpenAPIInspectInput struct {
	Document string `json:"document"`
}
type OpenAPIGenerateInput struct {
	Document     string `json:"document"`
	SettingsYAML string `json:"settings_yaml"`
}
type OpenAPIGenerateResult struct {
	YAML        string                     `json:"yaml,omitempty"`
	Diagnostics []openapiimport.Diagnostic `json:"diagnostics" openapi:"nonnullable"`
}

// AuthoringOpenAPI supplies generated DTO types only; it deliberately has no HTTP paths.
func AuthoringOpenAPI() ([]byte, error) {
	return apicontract.Document("Hookway browser authoring messages (WASM only)", "1", nil, []apicontract.Model{
		{Name: "OpenAPIInspectInput", Value: OpenAPIInspectInput{}, Input: true, Required: []string{"document"}},
		{Name: "OpenAPIGenerateInput", Value: OpenAPIGenerateInput{}, Input: true, Required: []string{"document", "settings_yaml"}},
		{Name: "OpenAPIGenerateResult", Value: OpenAPIGenerateResult{}},
		{Name: "OpenAPIReport", Value: openapiimport.Report{}},
	}, apicontract.Schema{})
}
