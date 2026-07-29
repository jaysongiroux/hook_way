package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunValidateAcceptsValidConfig(t *testing.T) {
	t.Setenv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/")
	t.Setenv("PADDLE_WEBHOOK_SECRET", "test-secret")
	t.Setenv("GITHUB_WEBHOOK_SECRET", "test-secret")
	t.Setenv("REVENUECAT_WEBHOOK_AUTH", "test-secret")

	var stdout, stderr bytes.Buffer
	code := run([]string{"validate", "--config", filepath.Join("..", "..", "examples", "config.yaml")}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("run() code = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "config valid") {
		t.Fatalf("stdout = %q, want config valid message", stdout.String())
	}
}

func TestRunValidateRejectsInvalidConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "invalid.yaml")
	if err := os.WriteFile(path, []byte("server:\n  addr: ''\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := run([]string{"validate", "--config", path}, &stdout, &stderr)

	if code == 0 {
		t.Fatalf("run() code = 0, want nonzero")
	}
	if !strings.Contains(stderr.String(), "config validation failed") {
		t.Fatalf("stderr = %q, want validation failure", stderr.String())
	}
}

func TestRunHealthcheckAcceptsHealthyEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	code := run([]string{"healthcheck", "--url", server.URL}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("run() code = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "healthy") {
		t.Fatalf("stdout = %q, want healthy message", stdout.String())
	}
}

func TestRunHealthcheckRejectsUnhealthyEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	code := run([]string{"healthcheck", "--url", server.URL}, &stdout, &stderr)

	if code == 0 {
		t.Fatal("run() code = 0, want nonzero")
	}
	if !strings.Contains(stderr.String(), "status 503") {
		t.Fatalf("stderr = %q, want status 503", stderr.String())
	}
}

func TestRun_SchemaPrintsJSONSchema(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"schema"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d (stderr: %s)", code, stderr.String())
	}
	var doc map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &doc); err != nil {
		t.Fatalf("schema output not valid JSON: %v", err)
	}
	if _, ok := doc["properties"]; !ok {
		t.Fatalf("schema missing properties")
	}
}

func TestEditorAPIKeyFromConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.yaml")
	body := `server:
  addr: ":8080"
  max_body_bytes: 1048576
security:
  api_key: "admin-key"
destinations:
  billing:
    type: "rabbitmq"
    rabbitmq:
      url: "amqp://guest:guest@localhost:5672/"
      exchange: "webhooks"
      exchange_type: "direct"
      durable: true
endpoints:
  test:
    enabled: true
    path: "/test"
    destinations:
      - destination: "billing"
        target: "test-target"
        create: true
    auth:
      type: "hmac_sha256"
      secret: "test-secret"
      signature_header: "X-Signature"
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	key, err := editorAPIKey(path)
	if err != nil {
		t.Fatalf("editorAPIKey error: %v", err)
	}
	if key != "admin-key" {
		t.Fatalf("key = %q, want \"admin-key\"", key)
	}
}

func TestEditorAPIKeyNoConfigIsUnauthenticated(t *testing.T) {
	t.Setenv("HOOKWAY_CONFIG", "") // ensure no ambient config is resolved
	key, err := editorAPIKey("")
	if err != nil {
		t.Fatalf("editorAPIKey error: %v", err)
	}
	if key != "" {
		t.Fatalf("key = %q, want \"\" (no config ⇒ unauthenticated)", key)
	}
}

// configMissingEnvVarYAML is otherwise-valid except its rabbitmq url
// references an environment variable that is (deliberately) never set in
// these tests.
const configMissingEnvVarYAML = `server:
  addr: ":8080"
  max_body_bytes: 1048576
destinations:
  billing:
    type: "rabbitmq"
    rabbitmq:
      url: "${HOOKWAY_TEST_DEFINITELY_UNSET_VAR}"
      exchange: "webhooks"
      exchange_type: "direct"
      durable: true
endpoints:
  test:
    enabled: true
    path: "/test"
    destinations:
      - destination: "billing"
        target: "test-target"
        create: true
    auth:
      type: "hmac_sha256"
      secret: "test-secret"
      signature_header: "X-Signature"
`

func TestRun_ValidateJSONMissingEnvVarIsWarningAndExitsZero(t *testing.T) {
	os.Unsetenv("HOOKWAY_TEST_DEFINITELY_UNSET_VAR")
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.yaml")
	if err := os.WriteFile(path, []byte(configMissingEnvVarYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"validate", "--config", path, "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run() code = %d, want 0 (stdout: %s, stderr: %s)", code, stdout.String(), stderr.String())
	}
	var resp struct {
		Issues []struct {
			Field    string `json:"field"`
			Severity string `json:"severity"`
		} `json:"issues"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		t.Fatalf("stdout not valid JSON: %v (out: %s)", err, stdout.String())
	}
	if len(resp.Issues) != 1 || resp.Issues[0].Severity != "warning" {
		t.Fatalf("issues = %#v, want exactly one warning-severity issue", resp.Issues)
	}
}

func TestRun_ValidatePlainMissingEnvVarHardFailsExitOne(t *testing.T) {
	os.Unsetenv("HOOKWAY_TEST_DEFINITELY_UNSET_VAR")
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.yaml")
	if err := os.WriteFile(path, []byte(configMissingEnvVarYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"validate", "--config", path}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("run() code = %d, want 1 (stdout: %s, stderr: %s)", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "HOOKWAY_TEST_DEFINITELY_UNSET_VAR") {
		t.Fatalf("stderr = %q, want mention of the missing var", stderr.String())
	}
}

func TestRun_ValidateNoEnvSkipsExpansionOnBothPaths(t *testing.T) {
	os.Unsetenv("HOOKWAY_TEST_DEFINITELY_UNSET_VAR")
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.yaml")
	if err := os.WriteFile(path, []byte(configMissingEnvVarYAML), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := run([]string{"validate", "--config", path, "--no-env"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("plain --no-env: code = %d, want 0 (stderr: %s)", code, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = run([]string{"validate", "--config", path, "--json", "--no-env"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("json --no-env: code = %d, want 0 (stdout: %s, stderr: %s)", code, stdout.String(), stderr.String())
	}
	var resp struct {
		Issues []any `json:"issues"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		t.Fatalf("stdout not valid JSON: %v (out: %s)", err, stdout.String())
	}
	if len(resp.Issues) != 0 {
		t.Fatalf("issues = %#v, want none with --no-env", resp.Issues)
	}
}

func TestRun_ValidateJSONEmitsIssuesEnvelope(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	// Missing server.addr → a validation issue.
	if err := os.WriteFile(path, []byte("server:\n  max_body_bytes: 1048576\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"validate", "--config", path, "--json"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("expected exit 1 for invalid config, got %d", code)
	}
	var resp struct {
		Issues []struct {
			Field    string `json:"field"`
			Severity string `json:"severity"`
		} `json:"issues"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		t.Fatalf("stdout not valid JSON envelope: %v (out: %s)", err, stdout.String())
	}
	if len(resp.Issues) == 0 {
		t.Fatalf("expected issues in envelope")
	}
}

func TestRun_VersionSubcommandPrintsVersionAndCommit(t *testing.T) {
	oldVersion, oldCommit := version, commit
	version, commit = "1.2.3", "abc1234"
	defer func() { version, commit = oldVersion, oldCommit }()

	var stdout, stderr bytes.Buffer
	code := run([]string{"version"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run() code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	want := "hookway 1.2.3 (abc1234)"
	if !strings.Contains(stdout.String(), want) {
		t.Fatalf("stdout = %q, want to contain %q", stdout.String(), want)
	}
}

// TestRunServe_MetricsBindFailureIsFatal exercises D5: a dedicated metrics
// listener that fails to bind (address already in use) must fail the whole
// process, not just log-and-continue with no metrics. The destination is
// "webhook", which — unlike rabbitmq/sqs/etc — never dials out at publisher
// construction time, so this test needs no broker and no network access; it
// only needs the main HTTP port to be free and the metrics port to be
// occupied.
func TestRunServe_MetricsBindFailureIsFatal(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	metricsAddr := occupied.Addr().String()

	free, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	serverAddr := free.Addr().String()
	free.Close()

	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.yaml")
	body := fmt.Sprintf(`server:
  addr: %q
  max_body_bytes: 1048576
metrics:
  enabled: true
  path: "/metrics"
  addr: %q
destinations:
  notify:
    type: "webhook"
    webhook:
      url: "https://example.invalid/hooks"
endpoints:
  test:
    enabled: true
    path: "/test"
    destinations:
      - destination: "notify"
        target: "orders"
    auth:
      type: "no_auth"
      scheme: "no_auth"
`, serverAddr, metricsAddr)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	done := make(chan int, 1)
	go func() {
		done <- run([]string{"--config", path}, &stdout, &stderr)
	}()

	select {
	case code := <-done:
		if code == 0 {
			t.Fatalf("run() code = 0, want nonzero (stdout=%s stderr=%s)", stdout.String(), stderr.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runServe did not return after a metrics-server bind failure")
	}
}

func TestRun_ServeVersionFlagPrintsVersionAndCommit(t *testing.T) {
	oldVersion, oldCommit := version, commit
	version, commit = "1.2.3", "abc1234"
	defer func() { version, commit = oldVersion, oldCommit }()

	var stdout, stderr bytes.Buffer
	code := run([]string{"--version"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run() code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	want := "hookway 1.2.3 (abc1234)"
	if !strings.Contains(stdout.String(), want) {
		t.Fatalf("stdout = %q, want to contain %q", stdout.String(), want)
	}
}
