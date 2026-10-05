package flowconfig

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/jaysongiroux/hook_way/internal/config"
	"github.com/jaysongiroux/hook_way/internal/transform"
)

func schemaEnvFlow(legacy bool, schema string) string {
	declaration := "type: json_validate\n        schema:"
	if legacy {
		declaration = "type: transform\n        kind: json_schema\n        json_schema:"
	}
	return "server: {addr: ':8080', max_body_bytes: 1048576}\nsecurity: {api_key: '${SCHEMA_TEST_KEY}'}\nflows:\n  sample:\n    nodes:\n      - {id: in, type: http_trigger, path: /sample, methods: [POST]}\n      - {id: body, type: read_body, max_bytes: 1048576}\n      - {id: auth, type: no_auth}\n      - {id: env, type: envelope, body_format: json}\n      - id: check\n        " + declaration + "\n" + schema + "\n      - {id: done, type: respond, status: 200, body: '$$literal'}\n    edges: [in>body, body>auth, auth>env, env>check, check>done]\n"
}
func schemaEnvUnset(t *testing.T, name string) {
	t.Helper()
	t.Setenv(name, "")
	if err := os.Unsetenv(name); err != nil {
		t.Fatal(err)
	}
}
func TestLoadBytesSchemaKeywordsStayLiteral(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		for _, varsSet := range []bool{false, true} {
			t.Run(fmt.Sprintf("legacy_%t_env_set_%t", legacy, varsSet), func(t *testing.T) {
				t.Setenv("SCHEMA_TEST_KEY", "secret-value")
				for _, name := range []string{"schema", "ref", "defs", "id", "anchor"} {
					if varsSet {
						t.Setenv(name, "must-not-replace-keyword")
					} else {
						schemaEnvUnset(t, name)
					}
				}
				schema := `          $schema: https://json-schema.org/draft/2020-12/schema
          $id: https://hookway.invalid/authored-schema
          $defs:
            identifier: {type: integer, minimum: 9007199254740993}
          type: object
          properties:
            café: {"$ref": '#/$defs/identifier'}
          required: [café]`
				src := schemaEnvFlow(legacy, schema)
				cfg, issues := LoadBytes([]byte(src), "schema-env", true)
				if len(issues) > 0 {
					t.Fatalf("schema keywords became env references: %+v", issues)
				}
				if cfg.Security.APIKey != "secret-value" {
					t.Fatal("ordinary environment variable was not expanded")
				}
				nodes := cfg.Flows["sample"].Nodes
				var parsed transform.JSONSchema
				if legacy {
					opts, err := decodeOptions[TransformOptions](nodes[4])
					if err != nil {
						t.Fatal(err)
					}
					parsed = opts.JSONSchema
				} else {
					opts, err := decodeOptions[JSONValidateOptions](nodes[4])
					if err != nil {
						t.Fatal(err)
					}
					parsed = opts.Schema
				}
				if parsed["$schema"] != "https://json-schema.org/draft/2020-12/schema" {
					t.Fatal(parsed)
				}
				minimum := parsed["$defs"].(map[string]any)["identifier"].(map[string]any)["minimum"]
				if minimum != json.Number("9007199254740993") {
					t.Fatalf("numeric text changed: %#v", minimum)
				}
				validator, err := transform.NewValidator(parsed, nil)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = validator.Validate(map[string]any{"café": json.Number("9007199254740992")}); err == nil {
					t.Fatal("local $defs reference was not enforced")
				}
				respond, err := decodeOptions[RespondOptions](nodes[5])
				if err != nil {
					t.Fatal(err)
				}
				if respond.Body != "$literal" {
					t.Fatalf("$$ behavior changed: %#v", respond.Body)
				}
			})
		}
	}
}
func TestSchemaEnvQuotingBlockReferencesAndEscapes(t *testing.T) {
	t.Setenv("SCHEMA_TEST_KEY", "secret-value")
	schemaEnvUnset(t, "schema")
	schemaEnvUnset(t, "defs")
	schemaEnvUnset(t, "ref")
	for _, ref := range []string{"\"#/$defs/id\"", "'#/$defs/id'", "|-\n                #/$defs/id", ">-\n                #/$defs/id"} {
		t.Run(ref, func(t *testing.T) {
			schema := `          '$schema': https://json-schema.org/draft/2020-12/schema
          "$defs":
            id: {type: string}
          type: object
          properties:
            id:
              '$ref': ` + ref
			_, issues := LoadBytes([]byte(schemaEnvFlow(false, schema)), "schema-env", true)
			if len(issues) > 0 {
				t.Fatalf("reference quoting/block scalar changed: %+v", issues)
			}
		})
	}
	schema := `          "$$schema": https://json-schema.org/draft/2020-12/schema
          $$defs: {id: {type: string}}
          type: object
          properties: {id: {$$ref: '#/$$defs/id'}}`
	_, issues := LoadBytes([]byte(schemaEnvFlow(true, schema)), "escaped-schema-env", true)
	if len(issues) > 0 {
		t.Fatalf("existing escaped schema keywords changed: %+v", issues)
	}
}
func TestSchemaEnvLeavesOtherValuesAndMissingVariableBehavior(t *testing.T) {
	t.Setenv("SCHEMA_TEST_KEY", "secret-value")
	t.Setenv("SCHEMA_TITLE", "authored-title")
	schemaEnvUnset(t, "SCHEMA_MISSING")
	src := schemaEnvFlow(false, "          type: string\n          title: '${SCHEMA_TITLE}'\n          description: '$$literal'\n          properties: {'$SCHEMA_MISSING': {type: string}}")
	expanded, missing := ExpandConfigEnv(src, config.ExpandEnvStrict)
	if len(missing) > 0 || !strings.Contains(expanded, "title: 'authored-title'") || !strings.Contains(expanded, "description: '$literal'") || !strings.Contains(expanded, "'$SCHEMA_MISSING'") {
		t.Fatalf("value/keyword expansion changed: %v\n%s", missing, expanded)
	}
	src = strings.Replace(src, "'${SCHEMA_TITLE}'", "'${SCHEMA_MISSING}'", 1)
	_, issues := LoadBytes([]byte(src), "schema-env", true)
	if len(issues) != 1 || issues[0].Field != "config.env.SCHEMA_MISSING" {
		t.Fatalf("ordinary missing reference no longer reported: %+v", issues)
	}
	expanded, missing = ExpandConfigEnv(src, config.ExpandEnvLenient)
	if len(missing) != 1 || missing[0] != "SCHEMA_MISSING" || !strings.Contains(expanded, "$SCHEMA_MISSING") {
		t.Fatalf("lenient behavior changed: %v %s", missing, expanded)
	}
}
func TestSchemaEnvDoesNotEnableExternalReferences(t *testing.T) {
	t.Setenv("SCHEMA_TEST_KEY", "secret-value")
	schemaEnvUnset(t, "ref")
	for _, legacy := range []bool{false, true} {
		_, issues := LoadBytes([]byte(schemaEnvFlow(legacy, "          $ref: https://external.invalid/schema")), "external-schema-env", true)
		if len(issues) == 0 {
			t.Fatal("external reference became allowed")
		}
		for _, issue := range issues {
			if strings.HasPrefix(issue.Field, "config.env.") {
				t.Fatalf("external reference rejected for the wrong reason: %+v", issues)
			}
		}
	}
}
