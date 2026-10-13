package configapi

import (
	"strings"
	"testing"
)

const validReplay = `
server: {addr: ":8080"}
database: {url: "${REPLAY_DATABASE_URL}", max_conns: 10}
core: {control_url: "http://hookway:9092", control_token: "${REPLAY_CONTROL_TOKEN}"}
retention: {ttl: 168h}
workers: {concurrency: 4, execution_timeout: 30s, lease: 60s}
keys:
  - {name: core, token: "${REPLAY_CAPTURE_TOKEN}", scopes: [capture]}
`

func TestValidateKeepsEnvironmentReferencesOpaque(t *testing.T) {
	t.Setenv("REPLAY_CONTROL_TOKEN", "host-secret-that-must-never-be-read-by-browser-validation")
	t.Setenv("REPLAY_CAPTURE_TOKEN", "another-host-secret-that-must-never-be-read")
	resp := Validate([]byte(validReplay))
	for _, issue := range resp.Issues {
		if strings.Contains(issue.Message, "host-secret") || issue.Severity != "warning" {
			t.Fatalf("unexpected issue: %#v", issue)
		}
	}
}

func TestValidateRejectsUnknownFieldsAndShortLiteralTokens(t *testing.T) {
	doc := strings.Replace(validReplay, `server: {addr: ":8080"}`, `server: {addr: ":8080", mystery: true}`, 1)
	joined := Validate([]byte(doc))
	if len(joined.Issues) == 0 || !strings.Contains(joined.Issues[0].Message, "field mystery not found") {
		t.Fatalf("expected unknown-field issue, got %#v", joined.Issues)
	}

	doc = strings.Replace(validReplay, `"${REPLAY_CONTROL_TOKEN}"`, `"short"`, 1)
	joined = Validate([]byte(doc))
	if len(joined.Issues) == 0 || !strings.Contains(joined.Issues[0].Message, "between 32 and 256 bytes") {
		t.Fatalf("expected short-token issue, got %#v", joined.Issues)
	}
}

func TestSchemaContainsDocumentSections(t *testing.T) {
	b, err := Schema()
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"server"`, `"database"`, `"core"`, `"retention"`, `"workers"`, `"keys"`, `"client_cert"`} {
		if !strings.Contains(string(b), field) {
			t.Fatalf("schema missing %s", field)
		}
	}
}
