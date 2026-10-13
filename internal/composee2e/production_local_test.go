package composee2e

import (
	"bufio"
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func productionCommand(t *testing.T, root, directory, project string, env []string, action string, options ...string) string {
	t.Helper()
	return mustRun(t, root, env, "make", productionArguments(directory, project, action, options...)...)
}

func productionArguments(directory, project, action string, options ...string) []string {
	args := []string{"--no-print-directory", "prod-local-" + action, "PROD_LOCAL_DIRECTORY=" + directory, "PROD_LOCAL_PROJECT=" + project}
	for i := 0; i < len(options); i++ {
		switch options[i] {
		case "--mode":
			if i+1 >= len(options) {
				panic("fixture --mode requires a value")
			}
			args = append(args, "PROD_LOCAL_MODE="+options[i+1]) // #nosec G602 -- i+1 explicitly checked against len above
			i++
		case "--protection":
			args = append(args, "PROD_LOCAL_PROTECTION=1")
		case "--no-build":
			args = append(args, "PROD_LOCAL_BUILD=0")
		case "--confirm-reset":
			if i+1 >= len(options) {
				panic("fixture --confirm-reset requires a value")
			}
			args = append(args, "PROD_LOCAL_CONFIRM_RESET="+options[i+1]) // #nosec G602 -- i+1 explicitly checked against len above
			i++
		}
	}
	return args
}

func productionSecrets(t *testing.T, directory string) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(directory, ".env")) // #nosec G304 -- private fixture-owned bootstrap directory
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]string{}
	for line := range strings.SplitSeq(string(raw), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if ok && !strings.HasPrefix(key, "#") {
			values[key] = strings.Trim(value, "\"'")
		}
	}
	return values
}

// Bootstrap must work offline, preserve operator edits and distinguish projects.
// This runs in ordinary Go CI without requiring Docker.
func TestProductionBootstrapPreservesState(t *testing.T) {
	root, directory := repoRoot(t), filepath.Join(t.TempDir(), "state")
	project := "hookway-bootstrap-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	productionCommand(t, root, directory, project, nil, "bootstrap", "--mode", "full", "--protection")
	secrets := productionSecrets(t, directory)
	seen := map[string]bool{}
	for _, name := range []string{"GITHUB_WEBHOOK_SECRET", "RABBITMQ_PASSWORD", "POSTGRES_PASSWORD", "REPLAY_MIGRATION_PASSWORD", "REPLAY_RUNTIME_PASSWORD", "REDIS_PASSWORD", "HOOKWAY_API_KEY", "REPLAY_CAPTURE_TOKEN", "REPLAY_CONTROL_TOKEN", "REPLAY_OPERATOR_TOKEN", "REPLAY_METRICS_TOKEN"} {
		value := secrets[name]
		if len(value) < 32 || seen[value] {
			t.Fatalf("%s must have an independent generated credential of at least 32 characters", name)
		}
		seen[value] = true
	}
	info, err := os.Stat(filepath.Join(directory, ".env"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("credential file permissions: info=%v error=%v; want 0600", info, err)
	}
	configPath := filepath.Join(directory, "core.yaml")
	config, err := os.ReadFile(configPath) // #nosec G304 -- fixed core.yaml in fixture-owned directory
	if err != nil {
		t.Fatal(err)
	}
	config = append(config, []byte("\n# operator-owned edit retained across bootstrap\n")...)
	if err := os.WriteFile(configPath, config, 0o644); err != nil { // #nosec G306 G703 -- fixed fixture path, env-reference config read by unprivileged container
		t.Fatal(err)
	}
	snapshot := map[string][32]byte{}
	for _, name := range []string{".env", "core.yaml", "replay.yaml", "migrate.yaml"} {
		raw, err := os.ReadFile(filepath.Join(directory, name)) // #nosec G304 -- fixed fixture filename list under t.TempDir
		if err != nil {
			t.Fatal(err)
		}
		snapshot[name] = sha256.Sum256(raw)
	}
	productionCommand(t, root, directory, project, nil, "bootstrap", "--mode", "full", "--protection")
	// A mistaken rerun must refuse a component change, rather than partially
	// regenerating files and leaving credentials inconsistent with the manifest.
	args := productionArguments(directory, project, "bootstrap", "--mode", "core")
	if _, err := command(root, nil, "make", args...); err == nil {
		t.Fatal("bootstrap silently changed the selected components")
	}
	for name, want := range snapshot {
		raw, err := os.ReadFile(filepath.Join(directory, name)) // #nosec G304 -- fixed fixture filename list under t.TempDir
		if err != nil || sha256.Sum256(raw) != want {
			t.Fatalf("bootstrap changed existing %s: %v", name, err)
		}
	}
	other := filepath.Join(t.TempDir(), "other")
	productionCommand(t, root, other, project+"-other", nil, "bootstrap", "--mode", "core")
	if productionSecrets(t, other)["GITHUB_WEBHOOK_SECRET"] == secrets["GITHUB_WEBHOOK_SECRET"] {
		t.Fatal("independent projects reused the webhook credential")
	}
}

func TestProductionCredentialUpgradePreservesExistingSetup(t *testing.T) {
	root, directory := repoRoot(t), t.TempDir()
	project := "hookway-upgrade-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	productionCommand(t, root, directory, project, nil, "bootstrap", "--mode", "full")
	// Reproduce the earlier local recipe: administrator URL and no separate roles.
	values := productionSecrets(t, directory)
	raw, err := os.ReadFile(filepath.Join(directory, ".env")) // #nosec G304 -- private fixture-owned bootstrap directory
	if err != nil {
		t.Fatal(err)
	}
	var legacy strings.Builder
	for line := range strings.SplitSeq(string(raw), "\n") {
		if strings.HasPrefix(line, "REPLAY_MIGRATION_") || strings.HasPrefix(line, "REPLAY_RUNTIME_") {
			continue
		}
		if strings.HasPrefix(line, "REPLAY_DATABASE_URL=") {
			line = "REPLAY_DATABASE_URL=postgres://replay:" + values["POSTGRES_PASSWORD"] + "@postgres:5432/replay?sslmode=disable"
		}
		if line != "" {
			legacy.WriteString(line + "\n")
		}
	}
	if err := os.WriteFile(filepath.Join(directory, ".env"), []byte(legacy.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(directory, "migrate.yaml")); err != nil {
		t.Fatal(err)
	}
	before := productionSecrets(t, directory)
	config, err := os.ReadFile(filepath.Join(directory, "replay.yaml")) // #nosec G304 -- fixed replay.yaml in fixture-owned directory
	if err != nil {
		t.Fatal(err)
	}
	productionCommand(t, root, directory, project, nil, "credentials-upgrade")
	after := productionSecrets(t, directory)
	for key, value := range before {
		if after[key] != value {
			t.Fatalf("credential upgrade replaced existing %s", key)
		}
	}
	if len(after["REPLAY_MIGRATION_PASSWORD"]) != 64 || len(after["REPLAY_RUNTIME_PASSWORD"]) != 64 || after["REPLAY_MIGRATION_PASSWORD"] == after["REPLAY_RUNTIME_PASSWORD"] {
		t.Fatal("upgrade did not add distinct owner/runtime credentials")
	}
	if got, err := os.ReadFile(filepath.Join(directory, "replay.yaml")); err != nil || !bytes.Equal(got, config) { // #nosec G304 -- fixed fixture-owned configuration
		t.Fatal("credential upgrade changed operator-owned replay config")
	}
	upgraded, err := os.ReadFile(filepath.Join(directory, ".env")) // #nosec G304 -- fixed fixture-owned credential file
	if err != nil {
		t.Fatal(err)
	}
	productionCommand(t, root, directory, project, nil, "credentials-upgrade")
	if got, err := os.ReadFile(filepath.Join(directory, ".env")); err != nil || !bytes.Equal(got, upgraded) { // #nosec G304 -- fixed fixture-owned credential file
		t.Fatal("repeated credential upgrade changed the completed setup")
	}
}

// Browser addresses can differ from container DNS and published local ports,
// for example when a reverse proxy fronts the API and console.
func TestProductionBootstrapPreservesExplicitPublicAddresses(t *testing.T) {
	root, directory := repoRoot(t), t.TempDir()
	project := "hookway-public-addresses-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	env := []string{"REPLAY_API_URL=https://api.example.test", "REPLAY_CONSOLE_ORIGIN=https://console.example.test", "REPLAY_HTTP_PORT=0", "CONSOLE_HTTP_PORT=0"}
	productionCommand(t, root, directory, project, env, "bootstrap", "--mode", "full")
	values := productionSecrets(t, directory)
	if values["REPLAY_API_URL"] != "https://api.example.test" || values["REPLAY_CONSOLE_ORIGIN"] != "https://console.example.test" {
		t.Fatal("bootstrap did not preserve explicit browser-facing addresses")
	}
	if values["REPLAY_HTTP_PORT"] != "0" || values["CONSOLE_HTTP_PORT"] != "0" {
		t.Fatal("public address overrides changed the selected host port mappings")
	}
}

func productionStack(t *testing.T, root, directory, project string, env []string, full, protection bool) *stack {
	t.Helper()
	args := []string{"compose", "--project-name", project, "--env-file", filepath.Join(directory, ".env"), "--file", filepath.Join(root, "examples", "production-local", "docker-compose.yaml")}
	if full {
		args = append(args, "--profile", "replay")
	}
	if protection {
		args = append(args, "--profile", "protection")
	}
	return &stack{root: root, args: args, env: append(slices.Clone(env), "HOOKWAY_LOCAL_DIRECTORY="+directory)}
}

func productionPost(t *testing.T, s *stack, secret, body string) int {
	t.Helper()
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(body))
	code, _, err := do(http.MethodPost, s.url(t, "hookway", 8080)+"/webhooks/github", []byte(body),
		"Content-Type", "application/json", "X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	if err != nil {
		t.Fatal(err)
	}
	return code
}

// Inspect with requeue so persistence is proven by the original delivery after
// every container is recreated, not by a new event sent after restart.
func productionRabbitIDs(t *testing.T, s *stack, password string) []string {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		s.url(t, "rabbitmq", 15672)+"/api/queues/%2F/github-webhooks/get",
		strings.NewReader(`{"count":100,"ackmode":"ack_requeue_true","encoding":"auto"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.SetBasicAuth("hookway", password)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("RabbitMQ inspection answered %d", resp.StatusCode)
	}
	var messages []struct{ Payload string }
	if err := json.NewDecoder(resp.Body).Decode(&messages); err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, message := range messages {
		var envelope struct{ ID string }
		if err := json.Unmarshal([]byte(message.Payload), &envelope); err != nil || envelope.ID == "" {
			t.Fatalf("RabbitMQ has invalid envelope: %v", err)
		}
		ids = append(ids, envelope.ID)
	}
	slices.Sort(ids)
	return ids
}

func productionRecords(t *testing.T, s *stack, token string) []failure {
	t.Helper()
	code, raw, err := do(http.MethodGet, s.url(t, "replay", 8080)+"/v1/records?kind=failure", nil, "Authorization", "Bearer "+token)
	if err != nil || code != http.StatusOK {
		t.Fatalf("replay records: status=%d error=%v", code, err)
	}
	var result struct{ Items []failure }
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	return result.Items
}

func productionRuntimePrivileges(t *testing.T, s *stack, password string) {
	t.Helper()
	access := *s
	access.env = append(slices.Clone(s.env), "PGPASSWORD="+password)
	args := []string{"exec", "-T", "--env", "PGPASSWORD", "postgres", "psql", "--host", "127.0.0.1",
		"--username", "replay_runtime", "--dbname", "replay", "--no-psqlrc", "--set", "ON_ERROR_STOP=1", "--tuples-only", "--no-align"}
	query := `SELECT current_user, has_database_privilege(current_user,current_database(),'CREATE'),
		has_schema_privilege(current_user,'public','CREATE'), has_table_privilege(current_user,'public.goose_db_version','UPDATE'),
		pg_has_role(current_user,'replay_owner','MEMBER');`
	if got := strings.TrimSpace(access.compose(t, append(slices.Clone(args), "--command", query)...)); got != "replay_runtime|f|f|f|f" {
		t.Fatalf("runtime role privileges: %s", got)
	}
	for _, query := range []string{`CREATE TABLE public.forbidden(id int)`, `UPDATE public.goose_db_version SET version_id=999`, `ALTER TABLE public.records ADD COLUMN forbidden int`} {
		if _, err := access.run(append(slices.Clone(args), "--command", query)...); err == nil {
			t.Fatalf("restricted runtime role accepted forbidden statement: %s", query)
		}
	}
}

func productionSchemaFact(t *testing.T, s *stack, user, password string) string {
	t.Helper()
	access := *s
	access.env = append(slices.Clone(s.env), "PGPASSWORD="+password)
	// Existing migration audit rows and the audit sequence are persistent facts,
	// independent of ownership. Advancing an identity sequence creates a legal gap.
	query := `SELECT concat(string_agg(id::text || ':' || version_id::text || ':' || is_applied::text || ':' || tstamp::text, ',' ORDER BY id),
		'|', (SELECT last_value FROM public.record_events_id_seq), '|', (SELECT is_called FROM public.record_events_id_seq)) FROM public.goose_db_version;`
	return strings.TrimSpace(access.compose(t, "exec", "-T", "--env", "PGPASSWORD", "postgres", "psql", "--host", "127.0.0.1",
		"--username", user, "--dbname", "replay", "--no-psqlrc", "--set", "ON_ERROR_STOP=1", "--tuples-only", "--no-align", "--command", query))
}

func productionPrepareAdministratorSchema(t *testing.T, s *stack, credentials map[string]string) string {
	t.Helper()
	// Rehearse only the current schema's prior local administrator ownership.
	// The same shipped binary migrates it; this does not model another release.
	s.compose(t, "up", "--detach", "--wait", "postgres")
	prior := *s
	prior.env = append(slices.Clone(s.env),
		"REPLAY_MIGRATION_DATABASE_URL=postgres://replay:"+credentials["POSTGRES_PASSWORD"]+"@postgres:5432/replay?sslmode=disable",
		"REPLAY_MIGRATION_PASSWORD="+credentials["POSTGRES_PASSWORD"], "PGPASSWORD="+credentials["POSTGRES_PASSWORD"])
	// Pass overrides by environment name so password-bearing URLs never enter argv.
	prior.compose(t, "run", "--rm", "--no-deps", "--pull", "never", "--env", "REPLAY_MIGRATION_DATABASE_URL", "--env", "REPLAY_MIGRATION_PASSWORD", "migrate")
	owner := strings.TrimSpace(prior.compose(t, "exec", "-T", "--env", "PGPASSWORD", "postgres", "psql", "--host", "127.0.0.1",
		"--username", "replay", "--dbname", "replay", "--no-psqlrc", "--tuples-only", "--no-align", "--command",
		`SELECT pg_get_userbyid(relowner) FROM pg_class WHERE oid='public.records'::regclass;`))
	if owner != "replay" {
		t.Fatalf("prior local schema owner: %s; want replay", owner)
	}
	prior.compose(t, "exec", "-T", "--env", "PGPASSWORD", "postgres", "psql", "--host", "127.0.0.1",
		"--username", "replay", "--dbname", "replay", "--no-psqlrc", "--set", "ON_ERROR_STOP=1", "--command",
		`SELECT setval('public.record_events_id_seq', 41, false);`)
	return productionSchemaFact(t, s, "replay", credentials["POSTGRES_PASSWORD"])
}

// Exercise the documented Make workflow, not just Compose's dependency graph.
// A valid but unreachable migration connection passes offline validation and
// fails the one-shot job. No serving component may start behind that failure.
func productionFailedMigrationGate(t *testing.T, s *stack, directory, project string) {
	t.Helper()
	broken := append(slices.Clone(s.env), "REPLAY_MIGRATION_DATABASE_URL=postgres://replay_owner@postgres:1/replay?sslmode=disable")
	started := time.Now()
	out, err := command(s.root, broken, "make", productionArguments(directory, project, "up", "--no-build")...)
	if err == nil {
		t.Fatal("production up accepted a failed migration")
	}
	if !strings.Contains(out, "database connection failed") {
		t.Fatalf("migration gate failed before reaching the real one-shot job: %s", out)
	}
	for _, service := range []string{"hookway", "replay", "console"} {
		if running := strings.TrimSpace(s.compose(t, "ps", "--quiet", service)); running != "" {
			t.Fatalf("failed migration started serving %s", service)
		}
	}
	t.Logf("failed migration stopped runtime rollout elapsed=%s; PostgreSQL/provisioning may remain; no core/replay/console serving", time.Since(started))
}

// Validation precedes an ordinary restart, preserving a healthy existing stack
// when a copied operator configuration is invalid. This is not schema rollback.
func productionInvalidCandidateGate(t *testing.T, s *stack, directory, project string) {
	t.Helper()
	path := filepath.Join(directory, "core.yaml")
	original, err := os.ReadFile(path) // #nosec G304 -- fixed core.yaml in private fixture directory
	if err != nil {
		t.Fatal(err)
	}
	before := strings.TrimSpace(s.compose(t, "ps", "--quiet", "hookway"))
	if err := os.WriteFile(path, []byte("server: {addr: ':8080', max_inflight_requests: -1}\nflows: {}\n"), 0o644); err != nil { // #nosec G306 -- credential-free config read by unprivileged validation container
		t.Fatal(err)
	}
	out, rejected := command(s.root, s.env, "make", productionArguments(directory, project, "restart", "--no-build")...)
	if err := os.WriteFile(path, original, 0o644); err != nil { // #nosec G306 G703 -- fixed fixture path, restore env-reference config for unprivileged container
		t.Fatal(err)
	}
	if rejected == nil || !strings.Contains(out, "config validation failed") {
		t.Fatalf("invalid candidate did not fail offline validation: %v %s", rejected, out)
	}
	if after := strings.TrimSpace(s.compose(t, "ps", "--quiet", "hookway")); before == "" || after != before {
		t.Fatal("invalid candidate disturbed the healthy core before validation")
	}
	if code, _, err := do(http.MethodGet, s.url(t, "hookway", 8080)+"/readyz", nil); err != nil || code != http.StatusOK {
		t.Fatalf("invalid candidate changed existing readiness=%d %v", code, err)
	}
	t.Log("invalid candidate rejected before restart; original running container and readiness retained")
}

func productionArchiveCommand(t *testing.T, s *stack, input []byte, args ...string) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", append(slices.Clone(s.args), args...)...) // #nosec G204 -- fixture-scoped Compose arguments
	cmd.Dir, cmd.Env, cmd.Stdin = s.root, append(os.Environ(), s.env...), bytes.NewReader(input)
	var output, diagnostics bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &diagnostics
	if err := cmd.Run(); err != nil {
		t.Fatalf("fixture archive command failed: %v\n%s", err, diagnostics.String())
	}
	if diagnostics.Len() > 0 {
		t.Logf("archive diagnostics: %s", diagnostics.String())
	}
	return output.Bytes()
}

func productionOfflineRestore(t *testing.T, s *stack, credentials map[string]string) {
	t.Helper()
	// This fixture has already reconciled its retry before stopping writers.
	// Keep replay capture reachable during core cleanup, then stop workers/API.
	s.compose(t, "stop", "hookway")
	s.compose(t, "stop", "replay")
	started := time.Now()
	query := func(database, sql string) []byte {
		return productionArchiveCommand(t, s, nil, "exec", "-T", "postgres", "psql", "--username=replay", "--dbname="+database,
			"--no-psqlrc", "--set=ON_ERROR_STOP=1", "--tuples-only", "--no-align", "--command", sql)
	}
	logical := func(database string) [32]byte {
		var rows bytes.Buffer
		for _, table := range []string{"goose_db_version", "records", "record_artifacts", "retry_runs", "record_events", "delivery_resolutions", "usage"} {
			rows.Write(query(database, "SELECT COALESCE(jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text),'[]'::jsonb)::text FROM public."+table+" t"))
		}
		rows.Write(query(database, "SELECT last_value,is_called FROM public.record_events_id_seq"))
		return sha256.Sum256(rows.Bytes())
	}
	checkpoint := logical("replay")
	archive := productionArchiveCommand(t, s, nil, "exec", "-T", "postgres", "pg_dump", "--username=replay", "--dbname=replay", "--format=custom")
	if len(archive) == 0 {
		t.Fatal("backup was empty")
	}
	productionArchiveCommand(t, s, archive, "exec", "-T", "postgres", "pg_restore", "--list")
	const restored = "hookway_restore_rehearsal"
	s.compose(t, "exec", "-T", "postgres", "createdb", "--username=replay", "--owner=replay_owner", restored)
	productionArchiveCommand(t, s, archive, "exec", "-T", "postgres", "pg_restore", "--username=replay", "--role=replay_owner", "--dbname="+restored, "--no-owner", "--no-acl", "--single-transaction")
	if got := logical(restored); got != checkpoint {
		t.Fatalf("restored logical rows/artifacts/audit/usage/sequence differ: %x != %x", got, checkpoint)
	}
	// Provision CONNECT explicitly on the NEW target; apply the existing reviewed
	// DML grants as its owner. No runtime DDL or migration-version UPDATE granted.
	query(restored, "REVOKE CONNECT ON DATABASE "+restored+" FROM PUBLIC; GRANT CONNECT ON DATABASE "+restored+" TO replay_runtime")
	grants, err := os.ReadFile(filepath.Join(s.root, "examples", "production-local", "database", "runtime-grants.sql")) // #nosec G304 -- fixed maintained SQL asset within repository root
	if err != nil {
		t.Fatal(err)
	}
	productionArchiveCommand(t, s, grants, "exec", "-T", "postgres", "psql", "--username=replay_owner", "--dbname="+restored, "--no-psqlrc")
	access := *s
	access.env = append(slices.Clone(s.env), "PGPASSWORD="+credentials["REPLAY_RUNTIME_PASSWORD"])
	privileges := strings.TrimSpace(string(productionArchiveCommand(t, &access, nil, "exec", "-T", "--env", "PGPASSWORD", "postgres", "psql", "--host=127.0.0.1", "--username=replay_runtime", "--dbname="+restored, "--no-psqlrc", "--tuples-only", "--no-align", "--command",
		"SELECT current_user,has_schema_privilege(current_user,'public','CREATE'),has_table_privilege(current_user,'public.records','INSERT'),has_table_privilege(current_user,'public.goose_db_version','UPDATE')")))
	if privileges != "replay_runtime|f|t|f" {
		t.Fatalf("restored runtime grants=%s", privileges)
	}
	t.Logf("operator pg_dump/pg_restore rehearsal: archive_bytes=%d archive_sha256=%x logical_sha256=%x recovery_elapsed=%s; restored NEW isolated DB as replay_owner; records/artifacts/runs/audit/resolutions/usage/sequence equal; restricted runtime grants verified; workers NOT started against restore", len(archive), sha256.Sum256(archive), checkpoint, time.Since(started))
	// Do not point workers at the restored database: original reconciled state is
	// preserved, and the offline restore remains available until fixture cleanup.
	s.compose(t, "start", "hookway", "replay")
	waitFor(t, "original core/replay resume after isolated restore", time.Minute, func() bool {
		for _, service := range []string{"hookway", "replay"} {
			code, _, err := do(http.MethodGet, s.url(t, service, 8080)+"/readyz", nil)
			if err != nil || code != http.StatusOK {
				return false
			}
		}
		return true
	})
}

func productionSecretBoundaries(t *testing.T, s *stack, credentials map[string]string) {
	t.Helper()
	for service, forbidden := range map[string][]string{
		"hookway":   {"POSTGRES_PASSWORD", "REPLAY_MIGRATION_PASSWORD", "REPLAY_RUNTIME_PASSWORD", "REPLAY_OPERATOR_TOKEN", "REPLAY_METRICS_TOKEN"},
		"migrate":   {"POSTGRES_PASSWORD", "REPLAY_RUNTIME_PASSWORD", "GITHUB_WEBHOOK_SECRET", "REPLAY_CAPTURE_TOKEN", "REPLAY_CONTROL_TOKEN", "REPLAY_OPERATOR_TOKEN", "REPLAY_METRICS_TOKEN"},
		"db-grants": {"POSTGRES_PASSWORD", "REPLAY_RUNTIME_PASSWORD", "REPLAY_CAPTURE_TOKEN", "REPLAY_CONTROL_TOKEN", "REPLAY_OPERATOR_TOKEN", "REPLAY_METRICS_TOKEN"},
		"replay":    {"POSTGRES_PASSWORD", "REPLAY_MIGRATION_PASSWORD", "GITHUB_WEBHOOK_SECRET", "RABBITMQ_PASSWORD", "REDIS_PASSWORD", "HOOKWAY_API_KEY"},
		"console":   {"POSTGRES_PASSWORD", "REPLAY_MIGRATION_PASSWORD", "REPLAY_RUNTIME_PASSWORD", "GITHUB_WEBHOOK_SECRET", "RABBITMQ_PASSWORD", "REDIS_PASSWORD", "HOOKWAY_API_KEY", "REPLAY_CAPTURE_TOKEN", "REPLAY_CONTROL_TOKEN", "REPLAY_OPERATOR_TOKEN", "REPLAY_METRICS_TOKEN"},
	} {
		container := strings.TrimSpace(s.compose(t, "ps", "--all", "--quiet", service))
		configured := mustRun(t, s.root, nil, "docker", "inspect", "--format", "{{json .Config.Env}}", container)
		for _, key := range forbidden {
			if credentials[key] != "" && strings.Contains(configured, credentials[key]) {
				t.Errorf("%s received unrelated credential %s", service, key)
			}
		}
	}
}

// This proves trusted TLS transport, HTTP auth, exact CORS and packaged public
// configuration. Browser trust-store installation/interaction is a separate check.
func productionLocalTLS(t *testing.T, s *stack, directory, operatorToken string) {
	t.Helper()
	certificate, key := filepath.Join(directory, "localhost.crt"), filepath.Join(directory, "localhost.key")
	mustRun(t, s.root, nil, "openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "1",
		"-subj", "/CN=localhost", "-addext", "subjectAltName=DNS:localhost,IP:127.0.0.1", "-keyout", key, "-out", certificate)
	if err := os.Chmod(key, 0o600); err != nil {
		t.Fatal(err)
	}
	proxy := *s
	proxy.args = append(slices.Clone(s.args), "--file", filepath.Join(s.root, "examples", "production-local", "tls", "docker-compose.yaml"))
	proxy.env = append(slices.Clone(s.env), "HOOKWAY_PROXY_UID="+strconv.Itoa(os.Getuid()), "HOOKWAY_TLS_PORT=0", "REPLAY_TLS_PORT=0", "CONSOLE_TLS_PORT=0")
	proxy.compose(t, "up", "--detach", "--no-build", "--wait", "tls-proxy")
	base := func(port int) string {
		return strings.Replace(proxy.url(t, "tls-proxy", port), "http://127.0.0.1:", "https://localhost:", 1)
	}
	coreURL, apiURL, consoleURL := base(8443), base(8444), base(8445)
	proxy.env = append(proxy.env, "REPLAY_API_URL="+apiURL, "REPLAY_CONSOLE_ORIGIN="+consoleURL)
	proxy.compose(t, "up", "--detach", "--no-build", "--wait", "replay", "console")
	defer func() {
		if out, err := proxy.run("rm", "--stop", "--force", "tls-proxy"); err != nil {
			t.Errorf("TLS fixture cleanup: %v\n%s", err, out)
		}
	}()
	pem, err := os.ReadFile(certificate) // #nosec G304 -- certificate generated by this fixture in t.TempDir
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(pem) {
		t.Fatal("cannot load local TLS trust anchor")
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}
	t.Cleanup(transport.CloseIdleConnections)
	trusted := &http.Client{Transport: transport, Timeout: 10 * time.Second}
	request := func(method, url string, headers ...string) (int, http.Header, []byte) {
		t.Helper()
		var body io.Reader
		if method == http.MethodPost {
			body = strings.NewReader(`{}`)
		}
		req, err := http.NewRequestWithContext(t.Context(), method, url, body)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < len(headers); i += 2 {
			req.Header.Set(headers[i], headers[i+1])
		}
		resp, err := trusted.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		raw, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		return resp.StatusCode, resp.Header, raw
	}
	untrustedRequest, err := http.NewRequestWithContext(t.Context(), http.MethodGet, coreURL+"/healthz", nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp, err := client.Do(untrustedRequest); err == nil {
		_ = resp.Body.Close()
		t.Fatal("self-signed localhost certificate worked without configured trust")
	}
	if code, _, _ := request(http.MethodGet, coreURL+"/healthz"); code != http.StatusOK {
		t.Fatalf("trusted local TLS health returned %d", code)
	}
	if code, _, _ := request(http.MethodPost, coreURL+"/webhooks/github", "Content-Type", "application/json"); code != http.StatusUnauthorized {
		t.Fatalf("TLS proxy bypassed webhook auth: %d", code)
	}
	if code, _, _ := request(http.MethodGet, apiURL+"/v1/me"); code != http.StatusUnauthorized {
		t.Fatalf("TLS proxy bypassed replay auth: %d", code)
	}
	if code, headers, _ := request(http.MethodGet, apiURL+"/v1/me", "Authorization", "Bearer "+operatorToken, "Origin", consoleURL); code != http.StatusOK || headers.Get("Access-Control-Allow-Origin") != consoleURL {
		t.Fatalf("exact TLS console origin failed: %d", code)
	}
	if code, _, _ := request(http.MethodGet, apiURL+"/v1/me", "Authorization", "Bearer "+operatorToken, "Origin", consoleURL+".unapproved"); code != http.StatusForbidden {
		t.Fatalf("unapproved TLS browser origin accepted: %d", code)
	}
	code, _, raw := request(http.MethodGet, consoleURL+"/runtime-config.json")
	var config struct {
		URL string `json:"replayApiUrl"`
	}
	if code != http.StatusOK || json.Unmarshal(raw, &config) != nil || config.URL != apiURL {
		t.Fatal("packaged console did not advertise the actual TLS API URL")
	}
	productionProxyTermination(t, &proxy, coreURL, roots, productionSecrets(t, directory))
}

func productionProxyTermination(t *testing.T, proxy *stack, coreURL string, roots *x509.CertPool, secrets map[string]string) {
	t.Helper()
	checkpoint := productionRabbitIDs(t, proxy, secrets["RABBITMQ_PASSWORD"])
	// A completed TLS handshake is an accepted connection. Hold the final body
	// byte across SIGTERM; only finishing the admitted request can produce202.
	dialer := tls.Dialer{NetDialer: &net.Dialer{Timeout: 5 * time.Second}, Config: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}
	conn, err := dialer.DialContext(t.Context(), "tcp", strings.TrimPrefix(coreURL, "https://"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if err := conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	body := `{"event_id":"proxy-sigterm-canary"}`
	mac := hmac.New(sha256.New, []byte(secrets["GITHUB_WEBHOOK_SECRET"]))
	_, _ = mac.Write([]byte(body))
	if _, err := fmt.Fprintf(conn, "POST /webhooks/github HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\nContent-Type: application/json\r\nX-Hub-Signature-256: sha256=%s\r\nContent-Length: %d\r\n\r\n%s", hex.EncodeToString(mac.Sum(nil)), len(body), body[:len(body)-1]); err != nil {
		t.Fatal(err)
	}
	container := strings.TrimSpace(proxy.compose(t, "ps", "--quiet", "tls-proxy"))
	started := time.Now()
	type stopResult struct {
		output string
		err    error
	}
	stopped := make(chan stopResult, 1)
	go func() { output, err := proxy.run("stop", "tls-proxy"); stopped <- stopResult{output, err} }()
	waitFor(t, "Caddy observes Compose SIGTERM", 5*time.Second, func() bool {
		return strings.Contains(proxy.compose(t, "logs", "--no-color", "--tail", "20", "tls-proxy"), "shutting down")
	})
	if running := strings.TrimSpace(mustRun(t, proxy.root, nil, "docker", "inspect", "--format", "{{.State.Running}}", container)); running != "true" {
		t.Fatal("proxy exited before the accepted request completed")
	}
	if _, err := io.WriteString(conn, body[len(body)-1:]); err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("proxy admitted SIGTERM request HTTP=%d", resp.StatusCode)
	}
	select {
	case result := <-stopped:
		if result.err != nil {
			t.Fatalf("proxy Compose stop failed: %v %s", result.err, result.output)
		}
	case <-time.After(70 * time.Second):
		t.Fatal("proxy stop exceeded70s")
	}
	if code := strings.TrimSpace(mustRun(t, proxy.root, nil, "docker", "inspect", "--format", "{{.State.ExitCode}}", container)); code != "0" {
		t.Fatalf("proxy termination exit=%s", code)
	}
	if got := productionRabbitIDs(t, proxy, secrets["RABBITMQ_PASSWORD"]); len(got) != len(checkpoint)+1 {
		t.Fatalf("proxy admitted request receipts=%v previous=%v", got, checkpoint)
	}
	t.Logf("Caddy actual SIGTERM drained accepted TLS request HTTP202, independent durable broker receipt +1, exit0 elapsed=%s; HTTP grace60s container70s", time.Since(started))
}

func TestProductionLocalPersistenceAndReset(t *testing.T) {
	if os.Getenv("HOOKWAY_COMPOSE_E2E") != "1" {
		t.Skip("set HOOKWAY_COMPOSE_E2E=1 (make compose-e2e-test) for production-local acceptance")
	}
	root, directory := repoRoot(t), t.TempDir()
	id := strconv.FormatInt(time.Now().UnixNano(), 36)
	mode := os.Getenv("HOOKWAY_COMPOSE_E2E_BUILD")
	if mode == "" {
		mode = "binaries"
	}
	if mode != "binaries" && mode != "dockerfile" {
		t.Fatalf("unknown compose build mode %q", mode)
	}
	input, err := productionImageInputs(os.Getenv("HOOKWAY_COMPOSE_E2E_PLATFORM"), os.Getenv("HOOKWAY_COMPOSE_E2E_VERSION"), os.Getenv("HOOKWAY_COMPOSE_E2E_COMMIT"))
	if err != nil {
		t.Fatal(err)
	}
	if mode != "dockerfile" && (input.platform != "" || input.version != "dev" || input.commit != "none") {
		t.Fatal("explicit production image inputs require HOOKWAY_COMPOSE_E2E_BUILD=dockerfile")
	}
	images := map[string]string{"hookway": "hookway-production-core:" + id, "replay": "hookway-production-replay:" + id, "console": "hookway-production-console:" + id}
	t.Logf("owned installation fixture id=%s tags=%v projects=hookway-production-e2e-%s-{core,full} sibling_volume=hookway-production-sibling-%s", id, images, id, id)
	t.Cleanup(func() {
		for _, image := range images {
			_, _ = command(root, nil, "docker", "image", "rm", image)
		}
	})
	arch := dockerArch(t)
	if mode == "binaries" {
		buildImage(t, root, directory, arch, "./cmd/hookway", "hookway", "core", images["hookway"])
		buildImage(t, root, directory, arch, "./cmd/hookway-replay", "hookway-replay", "replay", images["replay"])
	} else {
		for _, target := range []string{"core", "replay"} {
			service := target
			if target == "core" {
				service = "hookway"
			}
			args := input.arguments("Dockerfile", target, images[service])
			t.Logf("local unsigned build: docker %s", strings.Join(args, " "))
			mustRun(t, root, nil, "docker", args...)
		}
	}
	consoleArgs := input.arguments("web/Dockerfile", "", images["console"])
	t.Logf("local unsigned build: docker %s", strings.Join(consoleArgs, " "))
	mustRun(t, root, nil, "docker", consoleArgs...)
	if input.platform == "" {
		input.platform = "linux/" + arch
	}
	for _, service := range []string{"hookway", "replay", "console"} {
		identity := strings.TrimSpace(mustRun(t, root, nil, "docker", "image", "inspect", "--format", "{{.Id}} {{.Os}}/{{.Architecture}}", images[service]))
		_, platform, ok := strings.Cut(identity, " ")
		if !ok || platform != input.platform {
			t.Fatalf("image platform mismatch: %s %s; want %s", images[service], identity, input.platform)
		}
		if mode == "dockerfile" && service != "console" {
			binary := "hookway"
			if service == "replay" {
				binary = "hookway-replay"
			}
			got := strings.TrimSpace(mustRun(t, root, nil, "docker", "run", "--rm", "--platform", input.platform, images[service], "version"))
			if got != binary+" "+input.version+" ("+input.commit+")" {
				t.Fatalf("embedded binary identity mismatch: %s", got)
			}
		}
		t.Logf("local unsigned image service=%s tag=%s identity=%s daemon_arch=%s requested_platform=%s version=%s commit=%s (dirty source provenance recorded separately)", service, images[service], identity, arch, input.platform, input.version, input.commit)
	}
	// Synthetic valid public addresses keep exact-origin validation meaningful.
	// Tests call discovered random ports directly; this is API/persistence proof,
	// not a browser routing claim for these placeholder public addresses.
	env := []string{"HOOKWAY_CORE_IMAGE=" + images["hookway"], "HOOKWAY_REPLAY_IMAGE=" + images["replay"], "HOOKWAY_CONSOLE_IMAGE=" + images["console"], "HOOKWAY_HTTP_PORT=0", "REPLAY_HTTP_PORT=0", "CONSOLE_HTTP_PORT=0", "RABBITMQ_HTTP_PORT=0", "REPLAY_API_URL=http://localhost:18081", "REPLAY_CONSOLE_ORIGIN=http://localhost:18082"}
	// This independently labelled fixture stands in for another user's stack.
	// Reset must leave its data volume intact even while deleting this project's.
	siblingVolume := "hookway-production-sibling-" + id
	mustRun(t, root, nil, "docker", "volume", "create", "--label", "com.docker.compose.project="+siblingVolume, siblingVolume)
	t.Cleanup(func() { _, _ = command(root, nil, "docker", "volume", "rm", siblingVolume) })
	for _, selection := range []string{"core", "full"} {
		t.Run(selection, func(t *testing.T) {
			state, project := filepath.Join(directory, selection), "hookway-production-e2e-"+id+"-"+selection
			options := []string{"--mode", selection}
			if selection == "full" {
				options = append(options, "--protection")
			}
			productionCommand(t, root, state, project, env, "bootstrap", options...)
			s := productionStack(t, root, state, project, env, selection == "full", selection == "full")
			t.Cleanup(func() {
				if t.Failed() {
					out, _ := s.run("logs", "--no-color", "--tail", "100")
					t.Log(out)
				}
				if out, err := s.run("down", "--volumes", "--remove-orphans", "--timeout", "10"); err != nil {
					t.Errorf("fixture cleanup: %v\n%s", err, out)
				}
			})
			productionCommand(t, root, state, project, env, "validate", "--no-build")
			secrets := productionSecrets(t, state)
			for _, name := range []string{".env", "core.yaml", "replay.yaml", "migrate.yaml"} {
				raw, err := os.ReadFile(filepath.Join(state, name)) // #nosec G304 -- exact filename list in fixture-owned bootstrap directory
				optionalMissing := selection == "core" && (name == "replay.yaml" || name == "migrate.yaml") && os.IsNotExist(err)
				if err != nil && !optionalMissing {
					t.Fatal(err)
				}
				if err == nil {
					t.Logf("production config mode=%s file=%s sha256=%x", selection, name, sha256.Sum256(raw))
				}
			}
			var priorSchema string
			if selection == "full" {
				priorSchema = productionPrepareAdministratorSchema(t, s, secrets)
				productionFailedMigrationGate(t, s, state, project)
			}
			productionCommand(t, root, state, project, env, "up", "--no-build")
			for _, service := range []string{"hookway", "rabbitmq"} {
				container := strings.TrimSpace(s.compose(t, "ps", "--quiet", service))
				identity := mustRun(t, root, nil, "docker", "inspect", "--format", "{{.Image}} memory={{.HostConfig.Memory}} nano_cpus={{.HostConfig.NanoCpus}} stop_seconds={{.Config.StopTimeout}}", container)
				t.Logf("production-local mode=%s service=%s daemon_arch=%s identity=%s", selection, service, arch, strings.TrimSpace(identity))
			}
			if selection == "full" {
				for _, service := range []string{"replay", "postgres", "redis"} {
					container := strings.TrimSpace(s.compose(t, "ps", "--quiet", service))
					identity := mustRun(t, root, nil, "docker", "inspect", "--format", "{{.Image}} memory={{.HostConfig.Memory}} nano_cpus={{.HostConfig.NanoCpus}} stop_seconds={{.Config.StopTimeout}}", container)
					t.Logf("production-local service=%s daemon_arch=%s identity=%s", service, arch, strings.TrimSpace(identity))
				}
				if got := productionSchemaFact(t, s, "replay_runtime", secrets["REPLAY_RUNTIME_PASSWORD"]); got != priorSchema {
					t.Fatalf("role provisioning changed existing migration audit/sequence facts: got=%s want=%s", got, priorSchema)
				}
				productionRuntimePrivileges(t, s, secrets["REPLAY_RUNTIME_PASSWORD"])
				productionSecretBoundaries(t, s, secrets)
			}
			productionInvalidCandidateGate(t, s, state, project)
			code, _, err := do(http.MethodPost, s.url(t, "hookway", 8080)+"/webhooks/github", []byte(`{}`), "Content-Type", "application/json")
			if err != nil || code != http.StatusUnauthorized {
				t.Fatalf("unsigned webhook: status=%d error=%v; want 401", code, err)
			}
			if selection == "core" {
				for _, name := range []string{"replay", "postgres", "redis", "console"} {
					if container := strings.TrimSpace(s.compose(t, "ps", "--all", "--quiet", name)); container != "" {
						t.Fatalf("core-only stack started optional service %s", name)
					}
				}
			} else {
				s.compose(t, "stop", "notify-sink")
				if code, _, err := do(http.MethodGet, s.url(t, "replay", 8080)+"/v1/me", nil); err != nil || code != http.StatusUnauthorized {
					t.Fatalf("unauthenticated replay identity: status=%d error=%v", code, err)
				}
				if code, _, err := do(http.MethodGet, s.url(t, "console", 8080)+"/healthz", nil); err != nil || code != http.StatusOK {
					t.Fatalf("console health: status=%d error=%v", code, err)
				}
				if mode == "dockerfile" {
					code, raw, err := do(http.MethodGet, s.url(t, "console", 8080)+"/build-info.json", nil)
					var info struct{ Version, Commit string }
					if err != nil || code != http.StatusOK || json.Unmarshal(raw, &info) != nil {
						t.Fatalf("packaged console build info: status=%d error=%v", code, err)
					}
					if err := productionIdentityMismatch(input, input.platform, info.Version, info.Commit); err != nil {
						t.Fatal(err)
					}
					s.checkConsole(t, secrets["REPLAY_OPERATOR_TOKEN"])
				}
			}
			if code := productionPost(t, s, secrets["GITHUB_WEBHOOK_SECRET"], `{"action":"opened","issue":{"number":57}}`); code != http.StatusAccepted {
				t.Fatalf("signed local webhook: status=%d; want 202", code)
			}
			wantIDs := productionRabbitIDs(t, s, secrets["RABBITMQ_PASSWORD"])
			if len(wantIDs) != 1 {
				t.Fatalf("broker received %d events; want one", len(wantIDs))
			}
			// A failed signed-canary check cannot establish delivery. Keep the
			// previously observed receipt checkpoint unchanged; the recipe binds
			// only loopback and never resumes a production edge on the user's behalf.
			if code := productionPost(t, s, secrets["GITHUB_WEBHOOK_SECRET"]+"-wrong", `{"event_id":"failed-canary"}`); code != http.StatusUnauthorized {
				t.Fatalf("failed canary HTTP=%d; want401", code)
			}
			if got := productionRabbitIDs(t, s, secrets["RABBITMQ_PASSWORD"]); !slices.Equal(got, wantIDs) {
				t.Fatal("failed canary changed broker receipt checkpoint")
			}
			t.Log("failed authentication canary HTTP401; no new broker receipt; public-edge resumption remains an explicit operator decision")
			var recordID string
			if selection == "full" {
				waitFor(t, "full stack failure capture", time.Minute, func() bool {
					records := productionRecords(t, s, secrets["REPLAY_OPERATOR_TOKEN"])
					if len(records) != 1 {
						return false
					}
					recordID = records[0].ID
					return records[0].EventID == wantIDs[0]
				})
			}
			args := productionArguments(state, project, "reset", "--confirm-reset", project+"-wrong")
			before := strings.TrimSpace(s.compose(t, "ps", "--quiet", "hookway"))
			if _, err := command(root, env, "make", args...); err == nil {
				t.Fatal("reset accepted a mismatched project confirmation")
			}
			if after := strings.TrimSpace(s.compose(t, "ps", "--quiet", "hookway")); before == "" || after != before {
				t.Fatal("refused reset changed the running core container")
			}
			productionCommand(t, root, state, project, env, "down")
			if selection == "full" {
				productionFailedMigrationGate(t, s, state, project)
			}
			productionCommand(t, root, state, project, env, "up", "--no-build")
			if got := productionRabbitIDs(t, s, secrets["RABBITMQ_PASSWORD"]); !slices.Equal(got, wantIDs) {
				t.Fatalf("broker events changed across down/up: got=%v want=%v", got, wantIDs)
			}
			if selection == "full" {
				records := productionRecords(t, s, secrets["REPLAY_OPERATOR_TOKEN"])
				if len(records) != 1 || records[0].ID != recordID || records[0].EventID != wantIDs[0] {
					t.Fatal("replay record changed across container recreation")
				}
				code, raw, err := do(http.MethodGet, s.url(t, "replay", 8080)+"/v1/records/"+recordID+"/payload", nil,
					"Authorization", "Bearer "+secrets["REPLAY_OPERATOR_TOKEN"])
				var envelope struct{ ID string }
				if err != nil || code != http.StatusOK || json.Unmarshal(raw, &envelope) != nil || envelope.ID != wantIDs[0] {
					t.Fatalf("stored replay payload changed across recreation: status=%d error=%v", code, err)
				}
			}
			productionCommand(t, root, state, project, env, "restart")
			waitFor(t, "restarted core readiness", time.Minute, func() bool {
				code, _, err := do(http.MethodGet, s.url(t, "hookway", 8080)+"/readyz", nil)
				return err == nil && code == http.StatusOK
			})
			if got := productionRabbitIDs(t, s, secrets["RABBITMQ_PASSWORD"]); !slices.Equal(got, wantIDs) {
				t.Fatal("restart lost the durable broker event")
			}
			if selection == "full" {
				var run struct {
					ID string `json:"run_id"`
				}
				code, raw, err := do(http.MethodPost, s.url(t, "replay", 8080)+"/v1/records/"+recordID+"/retries", []byte(`{}`),
					"Authorization", "Bearer "+secrets["REPLAY_OPERATOR_TOKEN"], "Idempotency-Key", "least-privilege-retry", "Content-Type", "application/json")
				if err != nil || code != http.StatusAccepted || json.Unmarshal(raw, &run) != nil || run.ID == "" {
					t.Fatalf("runtime retry enqueue: status=%d error=%v", code, err)
				}
				waitFor(t, "restricted runtime retry completion", time.Minute, func() bool {
					code, raw, err := do(http.MethodGet, s.url(t, "replay", 8080)+"/v1/retries/"+run.ID, nil, "Authorization", "Bearer "+secrets["REPLAY_OPERATOR_TOKEN"])
					var result struct{ Status string }
					return err == nil && code == http.StatusOK && json.Unmarshal(raw, &result) == nil && result.Status == "succeeded"
				})
				productionRuntimePrivileges(t, s, secrets["REPLAY_RUNTIME_PASSWORD"])
				if got := productionRabbitIDs(t, s, secrets["RABBITMQ_PASSWORD"]); !slices.Equal(got, wantIDs) {
					t.Fatal("restricted runtime retry resent the successful broker delivery")
				}
				productionOfflineRestore(t, s, secrets)
				productionLocalTLS(t, s, state, secrets["REPLAY_OPERATOR_TOKEN"])
			}
			productionCommand(t, root, state, project, env, "reset", "--confirm-reset", project)
			if containers := strings.TrimSpace(s.compose(t, "ps", "--all", "--quiet")); containers != "" {
				t.Fatal("explicit reset left fixture containers")
			}
			volumes := mustRun(t, root, nil, "docker", "volume", "ls", "--quiet", "--filter", "label=com.docker.compose.project="+project)
			if strings.TrimSpace(volumes) != "" {
				t.Fatal("explicit reset left fixture volumes")
			}
			if _, err := command(root, nil, "docker", "volume", "inspect", siblingVolume); err != nil {
				t.Fatalf("reset removed another project's volume: %v", err)
			}
		})
	}
}

// Bootstrap output must not accidentally dump generated credentials.
func TestProductionBootstrapDoesNotPrintCredentials(t *testing.T) {
	root, directory := repoRoot(t), t.TempDir()
	project := fmt.Sprintf("hookway-output-%d", time.Now().UnixNano())
	out := productionCommand(t, root, directory, project, nil, "bootstrap", "--mode", "core")
	for key, value := range productionSecrets(t, directory) {
		if strings.Contains(key, "TOKEN") || strings.Contains(key, "PASSWORD") || strings.Contains(key, "SECRET") || key == "HOOKWAY_API_KEY" {
			if value != "" && bytes.Contains([]byte(out), []byte(value)) {
				t.Fatalf("bootstrap printed credential %s", key)
			}
		}
	}
}
