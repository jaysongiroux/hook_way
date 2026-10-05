package uicontract

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/jaysongiroux/hook_way/internal/transform"
)

const MaxJSONSampleBytes = 1 << 20
const MaxJSONSampleOutputBytes = 2 << 20

// The envelope allows JSON escaping of the bounded sample and schema. The
// decoded sample and schema have their own stricter limits.
const MaxJSONSampleRequestBytes = 8 << 20

type JSONSampleValidationInput struct {
	Schema    map[string]any             `json:"schema"`
	Coercions []transform.ScalarCoercion `json:"coercions,omitempty"`
	Sample    string                     `json:"sample"`
}

type JSONSampleIssue struct {
	Field   string `json:"field"`
	Path    string `json:"path"`
	Keyword string `json:"keyword"`
	Message string `json:"message"`
}

type JSONSampleValidationResult struct {
	Valid  bool              `json:"valid"`
	Issues []JSONSampleIssue `json:"issues" openapi:"nonnullable"`
	// Output is JSON text so JavaScript never rounds large integers or decimals
	// while carrying the result between the bridge, API and sample preview.
	Output string `json:"output,omitempty"`
}

var ErrJSONSampleRequestTooLarge = errors.New("JSON sample validation request exceeds 8 MiB")

// DecodeJSONSampleInput is shared by HTTP and WASM, including strict fields,
// exact numbers in schema constraints, one envelope value and safe errors.
func DecodeJSONSampleInput(raw []byte) (JSONSampleValidationInput, error) {
	var input JSONSampleValidationInput
	if len(raw) > MaxJSONSampleRequestBytes {
		return input, ErrJSONSampleRequestTooLarge
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	d.DisallowUnknownFields()
	var wire struct {
		Schema    map[string]any             `json:"schema"`
		Coercions []transform.ScalarCoercion `json:"coercions,omitempty"`
		Sample    *string                    `json:"sample"`
	}
	if err := d.Decode(&wire); err != nil {
		return input, errors.New("invalid JSON sample validation request")
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return input, errors.New("request must contain one JSON object")
	}
	if wire.Schema == nil || wire.Sample == nil {
		return input, errors.New("schema and sample fields are required")
	}
	input = JSONSampleValidationInput{Schema: wire.Schema, Coercions: wire.Coercions, Sample: *wire.Sample}
	return input, nil
}

// ValidateJSONSample is stateless and uses the runtime's offline, bounded JSON
// engine. It never expands environment values or fetches schema resources.
func ValidateJSONSample(input JSONSampleValidationInput) JSONSampleValidationResult {
	result := JSONSampleValidationResult{Issues: []JSONSampleIssue{}}
	issue := func(field, keyword, message string) JSONSampleValidationResult {
		result.Issues = append(result.Issues, JSONSampleIssue{Field: field, Keyword: keyword, Message: message})
		return result
	}
	validator, err := transform.NewValidator(input.Schema, input.Coercions)
	if err != nil {
		field := "schema"
		if strings.Contains(err.Error(), "coercion") {
			field = "coercions"
		}
		return issue(field, "configuration", "Invalid JSON schema or scalar coercion configuration. Schemas must be bounded and use only local references.")
	}
	if len(input.Sample) > MaxJSONSampleBytes {
		return issue("sample", "limit", "Sample must be at most 1 MiB.")
	}
	value, err := transform.DecodeJSON([]byte(input.Sample))
	if err != nil {
		return issue("sample", "parse", "Sample must contain one valid JSON value within the supported structure and number limits.")
	}
	out, err := validator.Validate(value)
	if err != nil {
		if errors.Is(err, transform.ErrJSONValidationWork) {
			return issue("sample", "limit", "Schema and sample exceed the supported validation work limit.")
		}
		var validation *transform.ValidationError
		if errors.As(err, &validation) {
			for _, detail := range validation.Issues {
				result.Issues = append(result.Issues, JSONSampleIssue{Field: "sample", Path: detail.Path, Keyword: detail.Keyword, Message: detail.Message})
			}
			if len(result.Issues) > 0 {
				return result
			}
		}
		return issue("sample", "validation", "Sample failed JSON schema validation or exceeded supported limits.")
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return issue("sample", "output", "Validated sample could not be encoded.")
	}
	if len(raw) > MaxJSONSampleOutputBytes {
		return issue("sample", "limit", "Validated sample output must be at most 2 MiB.")
	}
	result.Valid = true
	result.Output = string(raw)
	return result
}
