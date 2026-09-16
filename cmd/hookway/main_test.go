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
	"runtime"
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
	if err := os.WriteFile(path, []byte("schema_version: 2\nserver:\n  addr: ''\n"), 0o600); err != nil {
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

func TestRun_SchemaNodesListsCatalog(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"schema", "--nodes"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d (stderr: %s)", code, stderr.String())
	}
	var doc map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &doc); err != nil {
		t.Fatalf("node schema output not valid JSON: %v", err)
	}
	for _, name := range []string{"http_trigger", "publish", "respond", "auth", "read_body"} {
		if _, ok := doc[name]; !ok {
			t.Errorf("schema --nodes missing %s", name)
		}
	}
	if len(doc) != 17 {
		t.Fatalf("schema --nodes listed %d types, want 17", len(doc))
	}
}

func TestRun_SchemaDestinationsListsTypes(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"schema", "--destinations"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d (stderr: %s)", code, stderr.String())
	}
	var doc map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &doc); err != nil {
		t.Fatalf("destination schema output not valid JSON: %v", err)
	}
	for _, name := range []string{"webhook", "rabbitmq", "redis_kv"} {
		entry, ok := doc[name].(map[string]any)
		if !ok {
			t.Errorf("schema --destinations missing %s", name)
			continue
		}
		if _, ok := entry["block"]; !ok {
			t.Errorf("%s missing block", name)
		}
	}
	if len(doc) != 13 {
		t.Fatalf("schema --destinations listed %d types, want 13", len(doc))
	}
}

func TestCommittedWebSchemasMatchCLI(t *testing.T) {
	assertCommittedSchema(t, "web/src/graph/nodeSchema.json", []string{"schema", "--nodes"})
	assertCommittedSchema(t, "web/src/graph/destinationSchema.json", []string{"schema", "--destinations"})
}

func assertCommittedSchema(t *testing.T, rel string, args []string) {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller")
	}
	path := filepath.Join(filepath.Dir(thisFile), "../..", rel)
	committed, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v (run make node-schema)", rel, err)
	}
	var stdout, stderr bytes.Buffer
	if code := run(args, &stdout, &stderr); code != 0 {
		t.Fatalf("%v exit %d: %s", args, code, stderr.String())
	}
	if strings.TrimSpace(string(committed)) != strings.TrimSpace(stdout.String()) {
		t.Fatalf("%s is stale; run `make node-schema`", rel)
	}
}

func TestRunValidateRejectsV1Config(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "v1.yaml")
	body := `server:
  addr: ":8080"
  max_body_bytes: 1048576
destinations:
  primary:
    type: webhook
    webhook: {url: "https://example.test/hooks"}
endpoints:
  github:
    enabled: true
    path: /webhooks/github
    destinations:
      - destination: primary
        target: q
    auth:
      type: hmac_sha256
      scheme: hmac_sha256_hex
      secret: s
      signature_header: X-Sig
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"validate", "--config", path}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("code = %d, want 1 (stdout=%s stderr=%s)", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "schema_version 1 (endpoints:) is no longer supported") {
		t.Fatalf("stderr = %q, want V1 migration error", stderr.String())
	}
	if !strings.Contains(stderr.String(), "docs/configuration.md") {
		t.Fatalf("stderr = %q, want docs pointer", stderr.String())
	}
}

func TestRunValidateV2Examples(t *testing.T) {
	t.Setenv("WEBHOOK_URL", "https://example.test/hooks")
	t.Setenv("GITHUB_WEBHOOK_SECRET", "test-secret")

	root := filepath.Join("..", "..", "examples", "v2")
	var stdout, stderr bytes.Buffer
	code := run([]string{"validate", "--config", filepath.Join(root, "github.yaml")}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("github.yaml code = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "config valid") {
		t.Fatalf("stdout = %q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = run([]string{"validate", "--config", filepath.Join(root, "invalid-gate-order.yaml")}, &stdout, &stderr)
	if code == 0 {
		t.Fatal("invalid-gate-order.yaml exited 0")
	}
	joined := stderr.String()
	if !strings.Contains(joined, "RequestHead") || !strings.Contains(joined, "RawRequest") {
		t.Fatalf("stderr = %q, want both port types", joined)
	}

	stdout.Reset()
	stderr.Reset()
	code = run([]string{"validate", "--config", filepath.Join(root, "invalid-many.yaml")}, &stdout, &stderr)
	if code == 0 {
		t.Fatal("invalid-many.yaml exited 0")
	}
	joined = stderr.String()
	for _, want := range []string{"not_a_real_node", "duplicate", "missing-dest"} {
		if !strings.Contains(joined, want) {
			t.Errorf("invalid-many missing %q in %q", want, joined)
		}
	}
	if !strings.Contains(joined, "Terminal") && !strings.Contains(joined, "silently dropped") {
		t.Errorf("invalid-many missing dangling leaf diagnostic in %q", joined)
	}
}

func TestEditorAPIKeyFromConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.yaml")
	body := `schema_version: 2
server:
  addr: ":8080"
  max_body_bytes: 1048576
security:
  api_key: "admin-key"
destinations:
  billing:
    type: "webhook"
    webhook:
      url: "https://example.test/hooks"
flows:
  test:
    nodes:
      - {id: in, type: http_trigger, path: /test, methods: [POST]}
      - {id: rl, type: rate_limit, rpm: 60}
      - {id: body, type: read_body, max_bytes: 1048576}
      - {id: auth, type: auth, scheme: hmac_sha256_hex, secret: "test-secret", signature_header: X-Signature}
      - {id: env, type: envelope, body_format: json}
      - {id: out, type: publish, destination: billing, target: test-target}
    edges: [in>rl, rl>body, body>auth, auth>env, env>out]
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
const configMissingEnvVarYAML = `schema_version: 2
server:
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
flows:
  test:
    nodes:
      - {id: in, type: http_trigger, path: /test, methods: [POST]}
      - {id: rl, type: rate_limit, rpm: 60}
      - {id: body, type: read_body, max_bytes: 1048576}
      - {id: auth, type: auth, scheme: hmac_sha256_hex, secret: "test-secret", signature_header: X-Signature}
      - {id: env, type: envelope, body_format: json}
      - {id: out, type: publish, destination: billing, target: test-target}
    edges: [in>rl, rl>body, body>auth, auth>env, env>out]
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
	if err := os.WriteFile(path, []byte("schema_version: 2\nserver:\n  max_body_bytes: 1048576\n"), 0o600); err != nil {
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
	body := fmt.Sprintf(`schema_version: 2
server:
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
flows:
  test:
    nodes:
      - {id: in, type: http_trigger, path: /test, methods: [POST]}
      - {id: body, type: read_body, max_bytes: 1048576}
      - {id: auth, type: no_auth}
      - {id: env, type: envelope, body_format: json}
      - {id: out, type: publish, destination: notify, target: orders}
    edges: [in>body, body>auth, auth>env, env>out]
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

// TestRunServe_ReplayEnabledWithoutAPIKeyFailsFast exercises the replay
// wiring's entry point end-to-end without a Postgres connection: config.Load
// (called from runServe) rejects replay.enabled with an empty
// security.api_key before replaystore.New ever gets a chance to dial a
// database (see TestReplayEnabledRequiresAPIKey in internal/config for the
// underlying validation-issue unit test). This is the cheapest available
// seam to prove runServe's replay wiring is reachable and fails closed,
// short of standing up a real Postgres (left to the Task 7 CI integration
// path per the brief -- a Docker-dependent unit test here would be flaky).
func TestRunServe_ReplayEnabledWithoutAPIKeyFailsFast(t *testing.T) {
	free, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	serverAddr := free.Addr().String()
	free.Close()

	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.yaml")
	body := fmt.Sprintf(`schema_version: 2
server:
  addr: %q
  max_body_bytes: 1048576
destinations:
  notify:
    type: "webhook"
    webhook:
      url: "https://example.invalid/hooks"
flows:
  test:
    nodes:
      - {id: in, type: http_trigger, path: /test, methods: [POST]}
      - {id: body, type: read_body, max_bytes: 1048576}
      - {id: auth, type: no_auth}
      - {id: env, type: envelope, body_format: json}
      - {id: out, type: publish, destination: notify, target: orders}
    edges: [in>body, body>auth, auth>env, env>out]
replay:
  enabled: true
  addr: ":0"
  postgres:
    url: "postgres://user:pass@127.0.0.1:1/hookway_replay"
`, serverAddr)
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
		// runServe's logger writes JSON to stdout (not stderr) -- see the
		// slog.NewJSONHandler(stdout, ...) construction at the top of
		// runServe -- so the config-load failure shows up there.
		if !strings.Contains(stdout.String(), "replay.enabled") {
			t.Fatalf("expected stdout to mention replay.enabled validation failure; got: %s", stdout.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runServe did not fail fast on an unauthenticated replay config (did it try to dial Postgres?)")
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

func TestHostPortRendersLocalhostSuffix(t *testing.T) {
	cases := map[string]string{
		":8090":          ":8090",
		"0.0.0.0:8090":   ":8090",
		"127.0.0.1:9000": ":9000",
		"[::1]:8080":     ":8080",
		"not-an-address": "not-an-address",
	}
	for addr, want := range cases {
		if got := hostPort(addr); got != want {
			t.Errorf("hostPort(%q) = %q, want %q", addr, got, want)
		}
	}
}
