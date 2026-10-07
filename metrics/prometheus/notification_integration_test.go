package prometheus

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jaysongiroux/hook_way/internal/flowconfig"
	"github.com/jaysongiroux/hook_way/internal/httpapi"
	"github.com/jaysongiroux/hook_way/internal/metrics"
	"github.com/jaysongiroux/hook_way/internal/queue"
	"github.com/jaysongiroux/hook_way/internal/testenv"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"
	"gopkg.in/yaml.v3"
)

// Only the sink adapter is simulated. Authentication, bounded admission,
// readiness, metric authentication and exposition use the production handler.
type notificationPublisher struct {
	down    atomic.Bool
	block   atomic.Bool
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (p *notificationPublisher) Publish(ctx context.Context, _, _ string, _ queue.Message) error {
	if p.block.Load() {
		p.once.Do(func() { close(p.entered) })
		select {
		case <-p.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}
func (p *notificationPublisher) Ready(context.Context) error { return nil }
func (p *notificationPublisher) Close() error                { return nil }
func (p *notificationPublisher) ReadyByID(context.Context) map[string]error {
	if p.down.Load() {
		return map[string]error{"sink": fmt.Errorf("fixture destination unavailable")}
	}
	return map[string]error{"sink": nil}
}

type notificationAlert struct {
	Status      string            `json:"status"`
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
}

// TestAlertNotifications exercises the shipped expressions with real
// Prometheus/Alertmanager. Windows/hold times are accelerated in a fixture copy,
// not weakened in the shipped rules. Database/GC/capture inputs are explicitly
// synthetic; auth/admission/destination signals come from the runtime handler.
func TestAlertNotifications(t *testing.T) {
	testenv.SkipIfShort(t)
	ctx := t.Context()
	var mu sync.Mutex
	seen := map[string]map[string]bool{"firing": {}, "resolved": {}}
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Alerts []notificationAlert `json:"alerts"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
			http.Error(w, "invalid notification", http.StatusBadRequest)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		for _, a := range body.Alerts {
			if !strings.Contains(a.Annotations["runbook_url"], "/docs/operations/observability.md#") || a.Annotations["summary"] == "" || a.Annotations["description"] == "" {
				t.Errorf("notification lacks actionable annotations: %+v", a)
			}
			if statuses := seen[a.Status]; statuses != nil {
				statuses[a.Labels["alertname"]] = true
			}
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(receiver.Close)

	m := metrics.New()
	pub := &notificationPublisher{entered: make(chan struct{}), release: make(chan struct{})}
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(pub.release) }) }
	t.Cleanup(release)
	const source = `
server: {addr: ':8080', max_body_bytes: 1024, max_inflight_requests: 1, readiness_interval: 100ms, readiness_timeout: 100ms, publish_timeout: 60s, write_timeout: 65s, shutdown_timeout: 65s}
metrics: {enabled: true, path: /metrics}
security: {api_key: notification-metrics-fixture}
destinations: {sink: {type: webhook, webhook: {url: 'https://fixture.example.test'}}}
flows:
  notification:
    nodes:
      - {id: in, type: http_trigger, path: /events, methods: [POST]}
      - {id: body, type: read_body, max_bytes: 1024}
      - {id: auth, type: auth, scheme: hmac_sha256_hex, secret: notification-signing-fixture, signature_header: X-Signature}
      - {id: envelope, type: envelope, body_format: json}
      - {id: out, type: publish, destination: sink, target: fixture}
    edges: [in>body, body>auth, auth>envelope, envelope>out]
`
	cfg, issues := flowconfig.LoadBytes([]byte(source), "notification-fixture", false)
	for _, issue := range issues {
		if issue.Severity != "warning" {
			t.Fatalf("runtime fixture configuration: %+v", issues)
		}
	}
	core, err := httpapi.NewFlowServer(cfg, pub, slog.New(slog.NewTextHandler(io.Discard, nil)), m)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = core.Close() })
	probeCtx, cancelProbe := context.WithCancel(ctx)
	probeDone := core.StartReadinessProbe(probeCtx)
	t.Cleanup(func() { cancelProbe(); <-probeDone })
	runtimeServer := httptest.NewServer(core)
	t.Cleanup(func() { release(); runtimeServer.Close() })

	var faults atomic.Bool
	var counter atomic.Int64
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/scrape-failure" && faults.Load() {
			http.Error(w, "fixture metrics temporarily unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		unknown, usage, db, age := 0, 10, 1, 0
		last := time.Now().Unix()
		if faults.Load() {
			unknown, usage, db, age, last = 1, 95, 0, 600, last-7200
		}
		records := 7
		gcInterval := 900
		count := counter.Load()
		gcLast := last
		if r.URL.Path == "/stale-gc-peer" && !faults.Load() {
			gcLast = time.Now().Unix() - 7200
		}
		if r.URL.Path == "/metrics-b" {
			records = 11
		}
		if r.URL.Path == "/gc-disabled" || r.URL.Path == "/gc-long" {
			gcInterval, count, unknown, usage, db, age = 0, 0, 0, 10, 1, 0
			last = time.Now().Unix() - 7200
			gcLast = last
			if r.URL.Path == "/gc-long" {
				gcInterval = 604800
			}
		}
		_, _ = fmt.Fprintf(w, `# TYPE hookway_capture_dropped_total counter
hookway_capture_dropped_total{reason="upload_failed"} %d
# TYPE hookway_replay_captures_total counter
hookway_replay_captures_total{outcome="capacity"} %d
# TYPE hookway_replay_gc_runs_total counter
hookway_replay_gc_runs_total{outcome="error"} 0
hookway_replay_gc_runs_total{outcome="skipped_locked"} 1
# TYPE hookway_replay_store_failures_total counter
hookway_replay_store_failures_total{reason="storage_exhausted"} %d
# TYPE hookway_protection_cleanup_total counter
hookway_protection_cleanup_total{result="error",store="fixture"} %d
# TYPE hookway_logs_suppressed_total counter
hookway_logs_suppressed_total %d
# TYPE hookway_inspection_dropped_total counter
hookway_inspection_dropped_total{reason="upload_failed"} %d
# TYPE hookway_replay_auth_failures_total counter
hookway_replay_auth_failures_total{reason="scope"} %d
hookway_replay_records{kind="failure",delivery_status="open",dismissed="false"} %d
hookway_replay_records{kind="failure",delivery_status="unknown",dismissed="false"} %d
hookway_replay_usage_records %d
hookway_replay_capacity_records 100
hookway_replay_usage_bytes %d
hookway_replay_capacity_bytes 100
hookway_replay_oldest_queued_run_seconds %d
hookway_replay_db_up %d
hookway_protection_store_ready{store="fixture"} %d
hookway_replay_gc_interval_seconds %d
hookway_replay_gc_last_success_timestamp_seconds %d
hookway_replay_state_last_success_timestamp_seconds %d
`, count, count, count, count, count, count, count, records, unknown, usage, usage, age, db, db, gcInterval, gcLast, last)
	}))
	t.Cleanup(fixture.Close)
	port := func(s *httptest.Server) int { return s.Listener.Addr().(*net.TCPAddr).Port }
	nw, err := network.New(ctx)
	testenv.RequireContainer(t, err, "monitoring network")
	testcontainers.CleanupNetwork(t, nw)
	file := func(path, body string) testcontainers.ContainerFile {
		return testcontainers.ContainerFile{Reader: strings.NewReader(body), ContainerFilePath: path, FileMode: 0o644}
	}
	readAsset := func(path string) string {
		t.Helper()
		raw, readErr := os.ReadFile(path) // #nosec G304 -- fixed repository-owned monitoring fixture paths
		if readErr != nil {
			t.Fatal(readErr)
		}
		return string(raw)
	}
	start := func(image, alias, exposed string, files []testcontainers.ContainerFile, ports []int, args ...string) *testcontainers.DockerContainer {
		t.Helper()
		c, runErr := testcontainers.Run(ctx, image, network.WithNetwork([]string{alias}, nw), testcontainers.WithExposedPorts(exposed), testcontainers.WithFiles(files...), testcontainers.WithHostPortAccess(ports...), testcontainers.WithCmd(args...), testcontainers.WithWaitStrategy(wait.ForHTTP("/-/ready").WithPort(exposed).WithStartupTimeout(time.Minute)))
		if c != nil {
			testcontainers.CleanupContainer(t, c)
			t.Cleanup(func() {
				if t.Failed() {
					if logs, logErr := c.Logs(context.Background()); logErr == nil {
						data, _ := io.ReadAll(logs)
						_ = logs.Close()
						t.Logf("%s logs: %s", alias, data)
					}
				}
			})
		}
		testenv.RequireContainer(t, runErr, alias)
		return c
	}
	amConfig := fmt.Sprintf("route:\n  receiver: fixture\n  group_by: [alertname, job]\n  group_wait: 0s\n  group_interval: 1s\n  repeat_interval: 1h\nreceivers:\n  - name: fixture\n    webhook_configs:\n      - url: http://host.testcontainers.internal:%d/\n        send_resolved: true\n", port(receiver))
	am := start("prom/alertmanager:v0.31.1", "alertmanager", "9093/tcp", []testcontainers.ContainerFile{file("/etc/alertmanager/fixture.yaml", amConfig), file("/etc/alertmanager/alertmanager.yaml", readAsset("../../examples/production-local/monitoring/alertmanager.yaml"))}, []int{port(receiver)}, "--config.file=/etc/alertmanager/fixture.yaml", "--cluster.listen-address=")
	raw, err := os.ReadFile("alerts.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var shippedRules prometheusRules
	if err := yaml.Unmarshal(raw, &shippedRules); err != nil {
		t.Fatal(err)
	}
	gcExpr := ""
	for _, group := range shippedRules.Groups {
		for _, rule := range group.Rules {
			if rule.Alert == "HookwayReplayGCStalled" {
				gcExpr = rule.Expr
			}
		}
	}
	if gcExpr == "" {
		t.Fatal("missing shipped GC alert")
	}
	accelerated := regexp.MustCompile(`\[(5|10|15|30)m\]`).ReplaceAllString(string(raw), "[15s]")
	accelerated = regexp.MustCompile(`(?m)^(\s+for:) .+$`).ReplaceAllString(accelerated, "$1 0s")
	pmConfig := fmt.Sprintf(`global: {scrape_interval: 1s, evaluation_interval: 1s}
rule_files: [/etc/prometheus/fixture-rules.yaml]
alerting:
  alertmanagers:
    - static_configs: [{targets: ['alertmanager:9093']}]
scrape_configs:
  - job_name: hookway-core
    authorization: {credentials: notification-metrics-fixture}
    static_configs: [{targets: ['host.testcontainers.internal:%d']}]
  - job_name: hookway-replay-a
    static_configs:
      - targets: ['host.testcontainers.internal:%d']
        labels: {replica: one, __metrics_path__: /stale-gc-peer}
      - targets: ['host.testcontainers.internal:%d']
        labels: {replica: two}
  - job_name: hookway-replay-b
    metrics_path: /metrics-b
    static_configs: [{targets: ['host.testcontainers.internal:%d']}]
  - job_name: hookway-gc-disabled
    metrics_path: /gc-disabled
    static_configs: [{targets: ['host.testcontainers.internal:%d']}]
  - job_name: hookway-gc-long
    metrics_path: /gc-long
    static_configs: [{targets: ['host.testcontainers.internal:%d']}]
  - job_name: hookway-scrape-fixture
    metrics_path: /scrape-failure
    static_configs: [{targets: ['host.testcontainers.internal:%d']}]
`, port(runtimeServer), port(fixture), port(fixture), port(fixture), port(fixture), port(fixture), port(fixture))
	pm := start("prom/prometheus:v3.14.0", "prometheus", "9090/tcp", []testcontainers.ContainerFile{file("/etc/prometheus/fixture.yaml", pmConfig), file("/etc/prometheus/fixture-rules.yaml", accelerated), file("/etc/prometheus/prometheus.yaml", readAsset("../../examples/production-local/monitoring/prometheus.yaml")), file("/etc/prometheus/alerts.yaml", string(raw)), file("/etc/prometheus/core-token", "fixture-only"), file("/etc/prometheus/replay-token", "fixture-only")}, []int{port(runtimeServer), port(fixture)}, "--config.file=/etc/prometheus/fixture.yaml", "--storage.tsdb.path=/prometheus")
	for _, check := range []struct {
		container *testcontainers.DockerContainer
		args      []string
	}{
		{pm, []string{"promtool", "check", "config", "/etc/prometheus/prometheus.yaml"}},
		{am, []string{"amtool", "check-config", "/etc/alertmanager/alertmanager.yaml"}},
	} {
		code, output, checkErr := check.container.Exec(ctx, check.args)
		if checkErr != nil {
			t.Fatal(checkErr)
		}
		data, readErr := io.ReadAll(output)
		if readErr != nil || code != 0 {
			t.Fatalf("shipped config validation exit=%d err=%v: %s", code, readErr, data)
		}
		t.Logf("shipped config validation %v: %s", check.args, data)
	}
	pmURL, err := pm.PortEndpoint(ctx, "9090/tcp", "http")
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: 5 * time.Second}
	request := func(method, address, signature string) int {
		req, reqErr := http.NewRequestWithContext(ctx, method, address, strings.NewReader(`{"event":"notification"}`))
		if reqErr != nil {
			t.Error(reqErr)
			return 0
		}
		req.Header.Set("X-Signature", signature)
		resp, reqErr := client.Do(req)
		if reqErr != nil {
			t.Error(reqErr)
			return 0
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	mac := hmac.New(sha256.New, []byte("notification-signing-fixture"))
	mac.Write([]byte(`{"event":"notification"}`))
	signature := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if code := request(http.MethodPost, runtimeServer.URL+"/events", signature); code != http.StatusAccepted {
		t.Fatalf("healthy runtime request=%d", code)
	}
	query := func(expression string) map[string]float64 {
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, pmURL+"/api/v1/query", nil)
		if reqErr != nil {
			t.Fatal(reqErr)
		}
		q := req.URL.Query()
		q.Set("query", expression)
		req.URL.RawQuery = q.Encode()
		resp, reqErr := client.Do(req)
		if reqErr != nil {
			return nil
		}
		defer func() { _ = resp.Body.Close() }()
		var result struct {
			Status string `json:"status"`
			Data   struct {
				Result []struct {
					Metric map[string]string `json:"metric"`
					Value  []json.RawMessage `json:"value"`
				} `json:"result"`
			} `json:"data"`
		}
		if decodeErr := json.NewDecoder(resp.Body).Decode(&result); decodeErr != nil || result.Status != "success" {
			return nil
		}
		values := map[string]float64{}
		for _, sample := range result.Data.Result {
			if len(sample.Value) != 2 {
				t.Fatal("invalid query sample")
			}
			var text string
			_ = json.Unmarshal(sample.Value[1], &text)
			values[sample.Metric["job"]], _ = strconv.ParseFloat(text, 64)
		}
		return values
	}
	waitFor := func(what string, cond func() bool) {
		t.Helper()
		deadline := time.Now().Add(45 * time.Second)
		for !cond() {
			if time.Now().After(deadline) {
				mu.Lock()
				t.Logf("notification states at timeout: %+v", seen)
				mu.Unlock()
				t.Fatalf("timed out waiting for %s", what)
			}
			time.Sleep(250 * time.Millisecond)
		}
	}
	shared := `max by (job) (hookway_replay_records{delivery_status="open"})`
	waitFor("all authenticated scrapes and independent database totals", func() bool {
		values := query(shared)
		up := query(`min by (job) (up)`)
		counts := query(`count by (job) (hookway_replay_records{delivery_status="open"})`)
		return values["hookway-replay-a"] == 7 && values["hookway-replay-b"] == 11 && counts["hookway-replay-a"] == 2 && counts["hookway-replay-b"] == 1 && up["hookway-core"] == 1 && up["hookway-replay-a"] == 1 && up["hookway-replay-b"] == 1
	})
	t.Log("two replicas of database A report 7, independent database B reports 11; max by job returns 7/11 rather than 14/11 or one collapsed total")
	gcBefore := query(gcExpr)
	if _, ok := gcBefore["hookway-replay-a"]; ok {
		t.Fatal("healthy peer's GC success must cover a replica repeatedly skipping the advisory lock")
	}
	t.Log("one replica has a two-hour-old GC timestamp/skipped_locked, peer succeeds recently: no shared-database GC stall")
	// Warm the runtime's dynamically created counter series, then let this event
	// leave the accelerated window so firing is tied to the following incident.
	if code := request(http.MethodPost, runtimeServer.URL+"/events", "wrong"); code != http.StatusUnauthorized {
		t.Fatalf("actual auth rejection=%d", code)
	}
	time.Sleep(2 * time.Second)
	if code := request(http.MethodPost, runtimeServer.URL+"/events", "wrong"); code != http.StatusUnauthorized {
		t.Fatalf("repeated actual auth rejection=%d", code)
	}
	pub.block.Store(true)
	pending := make(chan int, 1)
	go func() {
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodPost, runtimeServer.URL+"/events", strings.NewReader(`{"event":"notification"}`))
		if reqErr != nil {
			t.Error(reqErr)
			pending <- 0
			return
		}
		req.Header.Set("X-Signature", signature)
		resp, reqErr := (&http.Client{Timeout: 65 * time.Second}).Do(req)
		if reqErr != nil {
			t.Error(reqErr)
			pending <- 0
			return
		}
		_ = resp.Body.Close()
		pending <- resp.StatusCode
	}()
	select {
	case <-pub.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("runtime publish did not enter")
	}
	if code := request(http.MethodPost, runtimeServer.URL+"/events", signature); code != http.StatusServiceUnavailable {
		t.Fatalf("actual bounded admission rejection=%d", code)
	}
	time.Sleep(2 * time.Second)
	if code := request(http.MethodPost, runtimeServer.URL+"/events", signature); code != http.StatusServiceUnavailable {
		t.Fatalf("repeated actual bounded admission rejection=%d", code)
	}
	faults.Store(true)
	pub.down.Store(true)
	want := []string{"HookwayAuthRejections", "HookwayIngressSaturated", "HookwayDestinationNotReady", "HookwayCaptureDrops", "HookwayInspectionDrops", "HookwayReplayAuthRejections", "HookwayReplayUsageHigh", "HookwayReplayCapacityRejections", "HookwayReplayQueuedRunStale", "HookwayReplayUnknownRecords", "HookwayReplayDatabaseDown", "HookwayReplayGCStalled", "HookwayReplayStateStale", "HookwayReplayPhysicalStorageExhausted", "HookwayProtectionCleanupFailures", "HookwayTelemetrySuppressed", "HookwayProtectionStoreNotReady", "HookwayMetricsScrapeFailed"}
	// Repeat counter events across scrapes; the first newly created vector series
	// cannot demonstrate increase until Prometheus has a prior sample.
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for faults.Load() {
			counter.Add(1)
			request(http.MethodPost, runtimeServer.URL+"/events", signature)
			select {
			case <-ticker.C:
			case <-ctx.Done():
				return
			}
		}
	}()
	t.Cleanup(func() { faults.Store(false); <-done })
	all := func(status string) bool {
		mu.Lock()
		defer mu.Unlock()
		for _, alert := range want {
			if !seen[status][alert] {
				return false
			}
		}
		return true
	}
	waitFor("representative firing notifications", func() bool { return all("firing") })
	gcAlerts := query(`ALERTS{alertname="HookwayReplayGCStalled",alertstate="firing"}`)
	if _, ok := gcAlerts["hookway-gc-disabled"]; ok {
		t.Fatal("disabled GC generated a false stall alert")
	}
	if _, ok := gcAlerts["hookway-gc-long"]; ok {
		t.Fatal("healthy long-cadence GC generated a false stall alert")
	}
	faults.Store(false)
	<-done
	pub.down.Store(false)
	pub.block.Store(false)
	release()
	// The held request has a five-second client timeout; cancellation itself is
	// irrelevant to telemetry. Actual admission recovery is checked separately.
	<-pending
	if code := request(http.MethodPost, runtimeServer.URL+"/events", signature); code != http.StatusAccepted {
		t.Fatalf("runtime recovery=%d", code)
	}
	waitFor("resolved notifications after faults recover and counter windows expire", func() bool { return all("resolved") })
	t.Logf("fixture notifications firing+resolved=%v; runtime auth/admission/destination signals verified; replay/capture/GC inputs synthetic; accelerated lookback=15s hold=0s scrape/evaluation=1s", want)
	t.Logf("rules source_sha256=%x accelerated_sha256=%x runtime_config_sha256=%x Prometheus=v3.14.0 Alertmanager=v0.31.1", sha256.Sum256(raw), sha256.Sum256([]byte(accelerated)), sha256.Sum256([]byte(source)))
}

func TestMonitoringComposeConfig(t *testing.T) {
	testenv.SkipIfShort(t)
	probe := exec.CommandContext(t.Context(), "docker", "info", "--format", "{{.ServerVersion}}")
	testenv.RequireContainer(t, probe.Run(), "Docker Compose monitoring validation")
	cmd := exec.CommandContext(t.Context(), "docker", "compose", "--project-name", "hookway-monitoring-config-test", "-f", "../../examples/production-local/docker-compose.yaml", "-f", "../../examples/production-local/monitoring/compose.yaml", "--profile", "monitoring", "config", "--quiet")
	cmd.Env = append(os.Environ(), "HOOKWAY_LOCAL_DIRECTORY="+t.TempDir(), "HOOKWAY_MONITOR_UID=1000", "HOOKWAY_MONITOR_GID=1000")
	for _, name := range []string{"REPLAY_CAPTURE_TOKEN", "REPLAY_CONTROL_TOKEN", "REPLAY_OPERATOR_TOKEN", "REPLAY_METRICS_TOKEN", "RABBITMQ_PASSWORD", "POSTGRES_PASSWORD", "REDIS_PASSWORD", "GITHUB_WEBHOOK_SECRET", "HOOKWAY_API_KEY", "RABBITMQ_URL", "PROTECTION_REDIS_URL"} {
		cmd.Env = append(cmd.Env, name+"=fixture-only")
	}
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("monitoring Compose validation: %v %s", err, output)
	}
}
