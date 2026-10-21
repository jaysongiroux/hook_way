package openapiimport

import (
	"strings"
	"testing"
)

func TestInspectionMetadataAndExactSchemaText(t *testing.T) {
	source := `openapi: 3.1.0
info: {title: T, version: '1'}
security: [{key: []}]
paths:
  /things:
    get:
      summary: Read things
      description: Original description
      responses:
        '200':
          description: OK
          content:
            application/json:
              schema: {type: integer, maximum: 9007199254740993}
components:
  securitySchemes:
    key: {type: apiKey, in: header, name: X-API-Key}
`
	r := Inspect([]byte(source))
	if len(r.Operations) != 1 {
		t.Fatalf("%+v", r)
	}
	op := r.Operations[0]
	if op.Description != "Read things" || op.Summary != "Read things" || !op.RequiresAuth || len(op.SecurityRequirements) != 1 || op.SecurityRequirements[0][0].Header != "X-API-Key" {
		t.Fatalf("metadata: %+v", op)
	}
	if !strings.Contains(op.OutputSchemaJSON, "9007199254740993") {
		t.Fatal(op.OutputSchemaJSON)
	}
	schema, err := SettingsSchema()
	if err != nil || !strings.Contains(string(schema), `"base_url"`) || strings.Contains(string(schema), `"$ref"`) {
		t.Fatalf("%s %v", schema, err)
	}
}
