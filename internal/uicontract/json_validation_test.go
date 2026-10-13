package uicontract

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/jaysongiroux/hook_way/internal/transform"
)

func TestJSONSampleExactNumbersCoercionsAndImmutableInput(t *testing.T) {
	input, err := DecodeJSONSampleInput([]byte(`{"schema":{"type":"object","properties":{"id":{"type":"integer"},"amount":{"type":"number"},"count":{"type":"integer"}},"required":["id","count"]},"coercions":[{"path":"count","type":"integer"}],"sample":"{\"id\":9007199254740993,\"amount\":0.12345678901234567890123456789,\"count\":\"9007199254740995\"}"}`))
	if err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(input)
	result := ValidateJSONSample(input)
	if !result.Valid || len(result.Issues) != 0 {
		t.Fatalf("%+v", result)
	}
	for _, number := range []string{"9007199254740993", "0.12345678901234567890123456789", "9007199254740995"} {
		if !strings.Contains(result.Output, number) {
			t.Fatalf("number changed: %s", result.Output)
		}
	}
	after, _ := json.Marshal(input)
	if string(before) != string(after) {
		t.Fatal("input was mutated")
	}
}

func TestJSONSampleLocalReferencesAndSanitizedFailures(t *testing.T) {
	schema := map[string]any{"$ref": "#/definitions/id", "definitions": map[string]any{"id": map[string]any{"type": "integer"}}}
	if result := ValidateJSONSample(JSONSampleValidationInput{Schema: schema, Sample: "9007199254740993"}); !result.Valid {
		t.Fatal(result)
	}
	for _, input := range []JSONSampleValidationInput{
		{Schema: schema, Sample: `"sensitive-value"`},
		{Schema: map[string]any{"$ref": "https://sensitive-value.invalid/schema"}, Sample: "{}"},
		{Schema: map[string]any{"type": "object"}, Sample: `{"sensitive-value":`},
		{Schema: map[string]any{"type": "object"}, Sample: "{} {}"},
		{Schema: map[string]any{"type": "object"}, Coercions: []transform.ScalarCoercion{{Path: "secret", Type: "bad"}}, Sample: "{}"},
	} {
		result := ValidateJSONSample(input)
		if result.Valid || len(result.Issues) == 0 || result.Output != "" {
			t.Fatalf("false success: %+v", result)
		}
		for _, issue := range result.Issues {
			if strings.Contains(issue.Message, "sensitive-value") {
				t.Fatalf("value echoed: %+v", issue)
			}
		}
	}
}

func TestJSONSampleStrictRequestAndBounds(t *testing.T) {
	for _, raw := range []string{"null", "[]", "{}", `{"schema":{},"sample":null}`, `{"schema":{},"sample":"{}","unknown":true}`, `{"schema":{},"sample":"{}"} {}`, `{"schema":{},"sample":"{}","coercions":[{"path":"x","type":"string","unknown":true}]}`} {
		if _, err := DecodeJSONSampleInput([]byte(raw)); err == nil {
			t.Fatalf("invalid request accepted: %s", raw)
		}
	}
	if _, err := DecodeJSONSampleInput([]byte(strings.Repeat(" ", MaxJSONSampleRequestBytes+1))); !errors.Is(err, ErrJSONSampleRequestTooLarge) {
		t.Fatal(err)
	}
	input := JSONSampleValidationInput{Schema: map[string]any{"type": "string"}, Sample: `"` + strings.Repeat("a", MaxJSONSampleBytes) + `"`}
	result := ValidateJSONSample(input)
	if result.Valid || len(result.Issues) != 1 || result.Issues[0].Keyword != "limit" {
		t.Fatal(result)
	}
	input.Schema = map[string]any{"description": strings.Repeat("a", 256<<10)}
	input.Sample = "{}"
	result = ValidateJSONSample(input)
	if result.Valid || result.Issues[0].Field != "schema" {
		t.Fatal(result)
	}
}

func TestJSONSampleIssuesMatchRuntime(t *testing.T) {
	input := JSONSampleValidationInput{Schema: map[string]any{"type": "object", "properties": map[string]any{"age": map[string]any{"type": "integer"}}}, Sample: `{"age":"private"}`}
	validator, err := transform.NewValidator(input.Schema, nil)
	if err != nil {
		t.Fatal(err)
	}
	value, _ := transform.DecodeJSON([]byte(input.Sample))
	_, err = validator.Validate(value)
	var validation *transform.ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("runtime issue missing: %v", err)
	}
	result := ValidateJSONSample(input)
	want := []JSONSampleIssue{}
	for _, issue := range validation.Issues {
		want = append(want, JSONSampleIssue{Field: "sample", Path: issue.Path, Keyword: issue.Keyword, Message: issue.Message})
	}
	if result.Valid || !reflect.DeepEqual(want, result.Issues) {
		t.Fatalf("contract/runtime issue drift: %+v", result)
	}
}

func TestJSONSampleReportsWorkLimitWithoutRunningAmplifiedValidation(t *testing.T) {
	branches := make([]any, 200)
	for i := range branches {
		branches[i] = map[string]any{"pattern": "^a+$"}
	}
	result := ValidateJSONSample(JSONSampleValidationInput{Schema: map[string]any{"allOf": branches}, Sample: `"` + strings.Repeat("a", 250000) + `"`})
	if result.Valid || result.Output != "" || len(result.Issues) != 1 || result.Issues[0].Keyword != "limit" {
		t.Fatalf("work limit not reflected in UI contract: %+v", result)
	}
}
