package config

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	hookconfig "github.com/jaysongiroux/hook_way/internal/config"
	"github.com/jaysongiroux/hook_way/internal/replay/auth"
	"github.com/jaysongiroux/hook_way/internal/tlsfiles"
)

const (
	controlToken = "control-token-0123456789abcdefghij"
	captureToken = "capture-token-0123456789abcdefghijkl"
	opsToken     = "ops-token-0123456789abcdefghijklmnopq"
)

const validYAML = `
server: {addr: ":8080"}
logging: {level: warn}
metrics: {enabled: true, path: /metrics, addr: ":9090", runtime_metrics: false}
database:
  url: "postgres://replay:pw@db:5432/replay"
  max_conns: 12
  tls: {server_name: db.internal}
core:
  control_url: "http://core-control:9092"
  control_token: "` + controlToken + `"
retention: {ttl: 72h, max_records: 500, max_bytes: 1048576, max_record_bytes: 65536, gc_interval: 5m}
workers: {concurrency: 2, execution_timeout: 20s, lease: 40s}
keys:
  - {name: core-capture, token: "` + captureToken + `", scopes: [capture]}
  - {name: ops, token: "` + opsToken + `", scopes: [read, payload, retry, metrics]}
`

const minimalYAML = `
database: {url: "postgres://replay@db/replay"}
core: {control_url: "https://core-control:9092", control_token: "` + controlToken + `"}
keys:
  - {name: core-capture, token: "` + captureToken + `", scopes: [capture]}
`

func errorsOnly(issues []hookconfig.ValidationIssue) []hookconfig.ValidationIssue {
	var out []hookconfig.ValidationIssue
	for _, i := range issues {
		if i.EffectiveSeverity() != hookconfig.SeverityWarning {
			out = append(out, i)
		}
	}
	return out
}

func TestLoadBytesParsesAFullConfig(t *testing.T) {
	cfg, issues := LoadBytes([]byte(validYAML))
	if len(issues) != 0 {
		t.Fatalf("issues = %+v", issues)
	}
	off := false
	want := Config{
		Server:   ServerConfig{Addr: ":8080"},
		Logging:  LoggingConfig{Level: "warn"},
		Metrics:  hookconfig.MetricsConfig{Enabled: true, Path: "/metrics", Addr: ":9090", RuntimeMetrics: &off},
		Database: DatabaseConfig{URL: "postgres://replay:pw@db:5432/replay", MaxConns: 12, TLS: TLSConfig{ServerName: "db.internal"}}, //nolint:gosec // G101: test fixture, not a real credential
		Core:     CoreConfig{ControlURL: "http://core-control:9092", ControlToken: controlToken},
		Retention: RetentionConfig{TTL: 72 * time.Hour, MaxRecords: 500, MaxBytes: 1048576, MaxRecordBytes: 65536,
			GCInterval: 5 * time.Minute},
		Workers: WorkersConfig{Concurrency: 2, ExecutionTimeout: 20 * time.Second, Lease: 40 * time.Second},
		Keys: []KeyConfig{
			{Name: "core-capture", Token: captureToken, Scopes: []string{"capture"}},
			{Name: "ops", Token: opsToken, Scopes: []string{"read", "payload", "retry", "metrics"}},
		},
	}
	if !reflect.DeepEqual(cfg, want) {
		t.Fatalf("cfg = %+v\nwant  %+v", cfg, want)
	}
}

func TestLoadBytesAppliesDefaults(t *testing.T) {
	cfg, issues := LoadBytes([]byte(minimalYAML))
	if len(issues) != 0 {
		t.Fatalf("issues = %+v", issues)
	}
	if cfg.Server.Addr != ":8080" || cfg.Database.MaxConns != 10 {
		t.Fatalf("server/database defaults: %+v %+v", cfg.Server, cfg.Database)
	}
	want := RetentionConfig{TTL: 168 * time.Hour, MaxRecords: 100000, MaxBytes: 10 << 30, MaxRecordBytes: 16 << 20, GCInterval: 15 * time.Minute}
	if cfg.Retention != want {
		t.Fatalf("retention = %+v, want %+v", cfg.Retention, want)
	}
	if cfg.Workers != (WorkersConfig{Concurrency: 4, ExecutionTimeout: 30 * time.Second, Lease: 60 * time.Second}) {
		t.Fatalf("workers = %+v", cfg.Workers)
	}
}

func TestLoadBytesExpandsEnvironment(t *testing.T) {
	t.Setenv("REPLAY_TEST_DB_URL", "postgres://from-env/replay")
	cfg, issues := LoadBytes([]byte(strings.Replace(minimalYAML, `"postgres://replay@db/replay"`, `"${REPLAY_TEST_DB_URL}"`, 1)))
	if len(issues) != 0 || cfg.Database.URL != "postgres://from-env/replay" {
		t.Fatalf("url = %q, issues = %+v", cfg.Database.URL, issues)
	}
}

func TestLoadBytesReportsMissingEnvironment(t *testing.T) {
	_, issues := LoadBytes([]byte(strings.Replace(minimalYAML, captureToken, "${REPLAY_TEST_UNSET_TOKEN}", 1)))
	if len(issues) != 1 || issues[0].Field != "config.env.REPLAY_TEST_UNSET_TOKEN" {
		t.Fatalf("issues = %+v", issues)
	}
}

func TestLoadBytesRejectsUnknownFields(t *testing.T) {
	_, issues := LoadBytes([]byte(minimalYAML + "server: {addr: \":8080\", bogus: 1}\n"))
	if len(errorsOnly(issues)) == 0 || !strings.Contains(issues[0].Message, "bogus") {
		t.Fatalf("issues = %+v", issues)
	}
}

func TestRejectsMultipleYAMLDocuments(t *testing.T) {
	data := []byte(minimalYAML + "---\nserver: {addr: ':9999'}\n")
	for name, validate := range map[string]func([]byte) (Config, []hookconfig.ValidationIssue){
		"runtime": LoadBytes,
		"browser": ValidateBytes,
	} {
		t.Run(name, func(t *testing.T) {
			_, issues := validate(data)
			if len(issues) != 1 || issues[0].Field != "config" || !strings.Contains(issues[0].Message, "multiple YAML documents") {
				t.Fatalf("issues = %+v", issues)
			}
		})
	}
}

func TestLoadBytesAllowsDisablingTheGCLoop(t *testing.T) {
	cfg, issues := LoadBytes([]byte(minimalYAML + "retention: {gc_interval: 0}\n"))
	if len(issues) != 0 || cfg.Retention.GCInterval != 0 {
		t.Fatalf("gc_interval = %v, issues = %+v", cfg.Retention.GCInterval, issues)
	}
}

func TestMetricsWithoutAMetricsKeyWarns(t *testing.T) {
	_, issues := LoadBytes([]byte(minimalYAML + "metrics: {enabled: true, path: /metrics, addr: \":9090\"}\n"))
	if len(issues) != 1 || issues[0].Severity != hookconfig.SeverityWarning || issues[0].Field != "metrics" {
		t.Fatalf("issues = %+v", issues)
	}
}

func TestValidationRules(t *testing.T) {
	cases := map[string]struct{ old, new, field string }{
		"log level":           {"level: warn", "level: loud", "logging.level"},
		"metrics path":        {"path: /metrics", "path: metrics", "metrics.path"},
		"metrics addr clash":  {`addr: ":9090"`, `addr: ":8080"`, "metrics.addr"},
		"metrics path on api": {`path: /metrics, addr: ":9090"`, `path: /v1/metrics, addr: ""`, "metrics.path"},
		"database url":        {`url: "postgres://replay:pw@db:5432/replay"`, `url: ""`, "database.url"},
		"max conns":           {"max_conns: 12", "max_conns: 0", "database.max_conns"},
		"control url":         {`control_url: "http://core-control:9092"`, `control_url: "core-control:9092"`, "core.control_url"},
		"control token":       {`control_token: "` + controlToken + `"`, `control_token: "short"`, "core.control_token"},
		"ttl":                 {"ttl: 72h", "ttl: 0s", "retention.ttl"},
		"bad duration":        {"ttl: 72h", "ttl: soon", "retention.ttl"},
		"max records":         {"max_records: 500", "max_records: 0", "retention.max_records"},
		"max bytes":           {"max_bytes: 1048576", "max_bytes: 0", "retention.max_bytes"},
		"record bytes":        {"max_record_bytes: 65536", "max_record_bytes: 2097152", "retention.max_record_bytes"},
		"gc interval":         {"gc_interval: 5m", "gc_interval: -1m", "retention.gc_interval"},
		"concurrency":         {"concurrency: 2", "concurrency: 0", "workers.concurrency"},
		"execution timeout":   {"execution_timeout: 20s, lease: 40s", "execution_timeout: 5s, lease: 40s", "workers.execution_timeout"},
		"lease":               {"lease: 40s", "lease: 30s", "workers.lease"},
		"key name":            {"name: ops", "name: Ops Team", "keys[1].name"},
		"duplicate name":      {"name: ops", "name: core-capture", "keys[1].name"},
		"short token":         {`token: "` + opsToken + `"`, `token: "short"`, "keys[1].token"},
		"duplicate token":     {`token: "` + opsToken + `"`, `token: "` + captureToken + `"`, "keys[1].token"},
		"reused control":      {`token: "` + opsToken + `"`, `token: "` + controlToken + `"`, "keys[1].token"},
		"no scopes":           {"scopes: [read, payload, retry, metrics]", "scopes: []", "keys[1].scopes"},
		"unknown scope":       {"scopes: [read, payload, retry, metrics]", "scopes: [read, admin]", "keys[1].scopes"},
		"duplicate scope":     {"scopes: [read, payload, retry, metrics]", "scopes: [read, read]", "keys[1].scopes"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if !strings.Contains(validYAML, c.old) {
				t.Fatalf("fixture does not contain %q", c.old)
			}
			_, issues := LoadBytes([]byte(strings.Replace(validYAML, c.old, c.new, 1)))
			for _, issue := range errorsOnly(issues) {
				if issue.Field == c.field {
					if strings.Contains(issue.Message, opsToken) || strings.Contains(issue.Message, controlToken) {
						t.Fatalf("issue message leaks a token: %q", issue.Message)
					}
					return
				}
			}
			t.Fatalf("no error for %s; issues = %+v", c.field, issues)
		})
	}
}

func TestLoadBytesRequiresAKey(t *testing.T) {
	yaml := minimalYAML[:strings.Index(minimalYAML, "keys:")]
	_, issues := LoadBytes([]byte(yaml))
	if len(issues) != 1 || issues[0].Field != "keys" {
		t.Fatalf("issues = %+v", issues)
	}
}

func TestLoadReadsAFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "replay.yaml")
	if err := os.WriteFile(path, []byte(minimalYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, issues := Load(path); len(issues) != 0 {
		t.Fatalf("issues = %+v", issues)
	}
	if _, issues := Load(filepath.Join(t.TempDir(), "missing.yaml")); len(issues) != 1 || issues[0].Field != "config" {
		t.Fatalf("missing file issues = %+v", issues)
	}
}

func TestAuthKeysAndTLSFiles(t *testing.T) {
	cfg, _ := LoadBytes([]byte(validYAML))
	keys := cfg.AuthKeys()
	if len(keys) != 2 || keys[1].Name != "ops" || !reflect.DeepEqual(keys[1].Scopes, []auth.Scope{auth.ScopeRead, auth.ScopePayload, auth.ScopeRetry, auth.ScopeMetrics}) {
		t.Fatalf("keys = %+v", keys)
	}
	if got := cfg.Database.TLS.Files(); got != (tlsfiles.Files{ServerName: "db.internal"}) {
		t.Fatalf("tls = %+v", got)
	}
}

// examples/replay/replay.yaml is the Compose example's replay config; it must
// pass the same validation `hookway-replay validate` runs.
func TestTheReplayExamplesValidate(t *testing.T) {
	t.Setenv("REPLAY_DATABASE_URL", "postgres://replay:replay@postgres:5432/replay?sslmode=disable")
	t.Setenv("REPLAY_CONTROL_TOKEN", "control-token-0123456789abcdefghij")
	t.Setenv("REPLAY_CAPTURE_TOKEN", "capture-token-0123456789abcdefghijkl")
	t.Setenv("REPLAY_OPERATOR_TOKEN", "operator-token-0123456789abcdefghi")
	t.Setenv("REPLAY_METRICS_TOKEN", "metrics-token-0123456789abcdefghijk")
	for _, path := range []string{"../../../examples/replay/replay.yaml", "../../../examples/dev/replay.yaml"} {
		_, issues := Load(path)
		for _, issue := range issues {
			if issue.EffectiveSeverity() == hookconfig.SeverityError {
				t.Fatalf("%s: issues = %+v", path, issues)
			}
		}
	}
}

// deploy/kubernetes/replay-config.yaml embeds the replay config in a ConfigMap.
func TestTheKubernetesReplayConfigValidates(t *testing.T) {
	raw, err := os.ReadFile("../../../deploy/kubernetes/replay-config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	start := strings.Index(src, "  replay.yaml: |\n")
	if start < 0 {
		t.Fatal("no replay.yaml key in the ConfigMap")
	}
	var body strings.Builder
	for _, line := range strings.Split(src[start+len("  replay.yaml: |\n"):], "\n") {
		if line != "" && !strings.HasPrefix(line, "    ") {
			break
		}
		body.WriteString(strings.TrimPrefix(line, "    ") + "\n")
	}
	t.Setenv("REPLAY_DATABASE_URL", "postgres://replay@db:5432/replay")
	t.Setenv("REPLAY_CONTROL_TOKEN", "control-token-0123456789abcdefghij")
	t.Setenv("REPLAY_CAPTURE_TOKEN", "capture-token-0123456789abcdefghijkl")
	t.Setenv("REPLAY_OPERATOR_TOKEN", "operator-token-0123456789abcdefghi")
	t.Setenv("REPLAY_METRICS_TOKEN", "metrics-token-0123456789abcdefghijk")
	_, issues := LoadBytes([]byte(body.String()))
	for _, issue := range issues {
		if issue.EffectiveSeverity() == hookconfig.SeverityError {
			t.Fatalf("issues = %+v", issues)
		}
	}
}

func TestInsecureTLSIsAWarning(t *testing.T) {
	edits := map[string][2]string{
		"core":     {"core:\n", "core:\n  tls: {insecure_skip_verify: true}\n"},
		"database": {"tls: {server_name: db.internal}", "tls: {server_name: db.internal, insecure_skip_verify: true}"},
	}
	for block, e := range edits {
		doc := strings.Replace(validYAML, e[0], e[1], 1)
		if doc == validYAML {
			t.Fatalf("fixture has no %s block", block)
		}
		_, issues := LoadBytes([]byte(doc))
		var warned bool
		for _, issue := range issues {
			if issue.EffectiveSeverity() == hookconfig.SeverityError {
				t.Fatalf("%s: issues = %+v", block, issues)
			}
			if issue.Field == block+".tls.insecure_skip_verify" && issue.EffectiveSeverity() == hookconfig.SeverityWarning {
				warned = true
			}
		}
		if !warned {
			t.Errorf("no warning on %s.tls.insecure_skip_verify: %+v", block, issues)
		}
	}
}

// The example configs give Prometheus its own metrics-only key: the scrape
// credential must not also download payloads or trigger redrives.
func TestExampleMetricsKeysAreMetricsOnly(t *testing.T) {
	t.Setenv("REPLAY_DATABASE_URL", "postgres://replay@db:5432/replay")
	t.Setenv("REPLAY_CONTROL_TOKEN", "control-token-0123456789abcdefghij")
	t.Setenv("REPLAY_CAPTURE_TOKEN", "capture-token-0123456789abcdefghijkl")
	t.Setenv("REPLAY_OPERATOR_TOKEN", "operator-token-0123456789abcdefghi")
	t.Setenv("REPLAY_METRICS_TOKEN", "metrics-token-0123456789abcdefghijk")
	cfg, _ := Load("../../../examples/replay/replay.yaml")
	if len(cfg.Keys) == 0 {
		t.Fatal("the example config did not load")
	}
	var metricsKeys int
	for _, k := range cfg.Keys {
		if !slices.Contains(k.Scopes, "metrics") {
			continue
		}
		metricsKeys++
		if len(k.Scopes) != 1 {
			t.Errorf("key %q holds metrics together with %v", k.Name, k.Scopes)
		}
	}
	if metricsKeys == 0 {
		t.Error("no key can scrape the metrics endpoint")
	}
}
