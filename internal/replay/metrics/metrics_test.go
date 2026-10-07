package metrics

import (
	"github.com/jaysongiroux/hook_way/internal/replaywire"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func gatheredNames(t *testing.T, m *Metrics) []string {
	t.Helper()
	families, err := m.Registry().Gather()
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(families))
	for _, f := range families {
		names = append(names, f.GetName())
	}
	sort.Strings(names)
	return names
}

func TestNewDeclaresEveryMetric(t *testing.T) {
	m := New()
	m.SetBuildInfo("v1.0.0", "abc123")
	m.Panic("handler")
	m.HTTPRequest("/v1/records", "GET", 200, time.Millisecond)
	m.CaptureArtifactBytes("failure", "snapshot", 10)
	m.Dismissal("failure")
	m.RegisterPool(func() PoolStats { return PoolStats{} })
	m.RunFinished("succeeded", time.Second)
	want := []string{
		"hookway_replay_auth_failures_total", "hookway_replay_build_info",
		"hookway_replay_capacity_bytes", "hookway_replay_capacity_records",
		"hookway_replay_capture_artifact_bytes", "hookway_replay_captures_total",
		"hookway_replay_db_pool_connections", "hookway_replay_db_pool_empty_acquires_total",
		"hookway_replay_db_pool_max_connections", "hookway_replay_db_up",
		"hookway_replay_dismissals_total", "hookway_replay_http_request_duration_seconds",
		"hookway_replay_http_requests_total", "hookway_replay_panics_total",
		"hookway_replay_payload_downloads_total", "hookway_replay_records",
		"hookway_replay_runs_created_total", "hookway_replay_schema_version",
		"hookway_replay_usage_bytes", "hookway_replay_usage_records",
		"hookway_replay_children_created_total", "hookway_replay_executor_calls_total",
		"hookway_replay_gc_deleted_bytes_total", "hookway_replay_gc_deleted_records_total",
		"hookway_replay_gc_duration_seconds", "hookway_replay_gc_last_success_timestamp_seconds",
		"hookway_replay_gc_runs_total", "hookway_replay_late_results_total",
		"hookway_replay_leases_expired_total", "hookway_replay_oldest_queued_run_seconds",
		"hookway_replay_run_duration_seconds", "hookway_replay_runs", "hookway_replay_runs_total",
		"hookway_replay_workers_busy",
	}
	sort.Strings(want)
	if got := gatheredNames(t, m); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("metrics = %v\nwant      %v", got, want)
	}
}

func TestBoundedLabelsStartAtZero(t *testing.T) {
	m := New()
	for _, outcome := range []string{CaptureCreated, CaptureDuplicate, CaptureConflict, CaptureCapacity, CaptureTooLarge, CaptureInvalid} {
		if got := testutil.ToFloat64(m.captures.WithLabelValues("failure", outcome)); got != 0 {
			t.Errorf("captures{%s} = %v", outcome, got)
		}
	}
	if got := testutil.CollectAndCount(m.records); got != 8 {
		t.Fatalf("records series = %d, want eight valid lifecycle combinations", got)
	}
	if got := testutil.CollectAndCount(m.payloadDownloads); got != 4 {
		t.Fatalf("payload_downloads series = %d, want 4", got)
	}
}

func TestRecordersCount(t *testing.T) {
	m := New()
	m.AuthFailure("scope")
	m.Capture("failure", CaptureCreated)
	m.Capture("failure", CaptureCreated)
	m.PayloadDownload(DownloadCorrupt)
	m.RunsCreated(ModeBatch, 3)
	m.Dismissal("failure")
	m.Panic("handler")
	m.HTTPRequest("/v1/records/{id}", "GET", 404, time.Millisecond)
	checks := map[string]float64{
		"auth":      testutil.ToFloat64(m.authFailures.WithLabelValues("scope")),
		"captures":  testutil.ToFloat64(m.captures.WithLabelValues("failure", CaptureCreated)),
		"downloads": testutil.ToFloat64(m.payloadDownloads.WithLabelValues(DownloadCorrupt)),
		"runs":      testutil.ToFloat64(m.runsCreated.WithLabelValues(ModeBatch)),
		"dismiss":   testutil.ToFloat64(m.dismissals.WithLabelValues("failure")),
		"panics":    testutil.ToFloat64(m.panics.WithLabelValues("handler")),
		"http":      testutil.ToFloat64(m.httpRequests.WithLabelValues("/v1/records/{id}", "GET", "404")),
	}
	want := map[string]float64{"auth": 1, "captures": 2, "downloads": 1, "runs": 3, "dismiss": 1, "panics": 1, "http": 1}
	for name, got := range checks {
		if got != want[name] {
			t.Errorf("%s = %v, want %v", name, got, want[name])
		}
	}
}

func TestSetStateZeroesStatusesMissingFromTheSample(t *testing.T) {
	m := New()
	m.SetState(State{RecordsByState: map[replaywire.RecordState]int64{{Kind: "failure", DeliveryStatus: "open"}: 3, {Kind: "failure", DeliveryStatus: "unknown"}: 1, {Kind: "inspection"}: 2, {Kind: "failure", DeliveryStatus: "unknown", Dismissed: true}: 1}, UsageRecords: 4, UsageBytes: 100, SchemaVersion: 1})
	m.SetState(State{RecordsByState: map[replaywire.RecordState]int64{{Kind: "failure", DeliveryStatus: "open"}: 2}, UsageRecords: 2, UsageBytes: 50, SchemaVersion: 1})
	if got := testutil.ToFloat64(m.records.WithLabelValues("failure", "unknown", "false")); got != 0 {
		t.Fatalf("records{unknown} = %v after it left the sample, want 0", got)
	}
	if got := testutil.ToFloat64(m.records.WithLabelValues("failure", "open", "false")); got != 2 {
		t.Fatalf("records{open} = %v", got)
	}
	for _, labels := range [][]string{{"inspection", "", "false"}, {"failure", "unknown", "true"}} {
		if got := testutil.ToFloat64(m.records.WithLabelValues(labels...)); got != 0 {
			t.Fatalf("stale record counts for %v = %v", labels, got)
		}
	}
	if testutil.ToFloat64(m.usageRecords) != 2 || testutil.ToFloat64(m.usageBytes) != 50 || testutil.ToFloat64(m.schemaVersion) != 1 {
		t.Fatal("usage or schema gauges not set")
	}
}

func TestCapacityAndDBUp(t *testing.T) {
	m := New()
	m.SetCapacity(100, 2048)
	m.SetDBUp(true)
	if testutil.ToFloat64(m.capacityRecords) != 100 || testutil.ToFloat64(m.capacityBytes) != 2048 || testutil.ToFloat64(m.dbUp) != 1 {
		t.Fatal("capacity or db_up not set")
	}
	m.SetDBUp(false)
	if testutil.ToFloat64(m.dbUp) != 0 {
		t.Fatal("db_up not cleared")
	}
}

func TestRegisterPoolReadsStatsAtScrapeTime(t *testing.T) {
	m := New()
	stats := PoolStats{Acquired: 1, Idle: 2, Total: 3, Max: 10, EmptyAcquires: 4}
	m.RegisterPool(func() PoolStats { return stats })
	m.RegisterPool(func() PoolStats { return PoolStats{} }) // idempotent: must not panic or replace
	stats.Acquired = 7
	const want = `
# HELP hookway_replay_db_pool_connections Database pool connections, by state.
# TYPE hookway_replay_db_pool_connections gauge
hookway_replay_db_pool_connections{state="acquired"} 7
hookway_replay_db_pool_connections{state="idle"} 2
hookway_replay_db_pool_connections{state="total"} 3
`
	if err := testutil.GatherAndCompare(m.Registry(), strings.NewReader(want), "hookway_replay_db_pool_connections"); err != nil {
		t.Fatal(err)
	}
}

func TestEnableRuntimeCollectorsIsIdempotent(t *testing.T) {
	m := New()
	m.EnableRuntimeCollectors()
	m.EnableRuntimeCollectors()
	found := false
	for _, name := range gatheredNames(t, m) {
		found = found || name == "go_goroutines"
	}
	if !found {
		t.Fatal("go runtime collector not registered")
	}
}

func TestWorkerAndGCRecorders(t *testing.T) {
	m := New()
	m.SetRunState(RunState{Queued: 3, Running: 1, OldestQueued: 90 * time.Second})
	m.RunFinished("unknown", 2*time.Second)
	m.ExecutorCall(CallUnavailable)
	m.ExecutorCall(CallUnavailable)
	m.SetWorkersBusy(2)
	m.LeasesExpired(3)
	m.LateResult()
	m.ChildrenCreated(2)
	m.GCRun(GCOK, time.Second, 5, 1024)
	m.GCRun(GCSkippedLocked, time.Millisecond, 0, 0)
	checks := map[string]float64{
		"queued":     testutil.ToFloat64(m.runs.WithLabelValues("queued")),
		"running":    testutil.ToFloat64(m.runs.WithLabelValues("running")),
		"oldest":     testutil.ToFloat64(m.oldestQueued),
		"runs_total": testutil.ToFloat64(m.runsTotal.WithLabelValues("unknown")),
		"calls":      testutil.ToFloat64(m.executorCalls.WithLabelValues(CallUnavailable)),
		"busy":       testutil.ToFloat64(m.workersBusy),
		"expired":    testutil.ToFloat64(m.leasesExpired),
		"late":       testutil.ToFloat64(m.lateResults),
		"children":   testutil.ToFloat64(m.childrenCreated),
		"gc_ok":      testutil.ToFloat64(m.gcRuns.WithLabelValues(GCOK)),
		"gc_skipped": testutil.ToFloat64(m.gcRuns.WithLabelValues(GCSkippedLocked)),
		"gc_records": testutil.ToFloat64(m.gcDeletedRecords),
		"gc_bytes":   testutil.ToFloat64(m.gcDeletedBytes),
	}
	want := map[string]float64{"queued": 3, "running": 1, "oldest": 90, "runs_total": 1, "calls": 2, "busy": 2,
		"expired": 3, "late": 1, "children": 2, "gc_ok": 1, "gc_skipped": 1, "gc_records": 5, "gc_bytes": 1024}
	for name, got := range checks {
		if got != want[name] {
			t.Errorf("%s = %v, want %v", name, got, want[name])
		}
	}
	if testutil.ToFloat64(m.gcLastSuccess) == 0 {
		t.Fatal("gc_last_success_timestamp_seconds not set by a successful sweep")
	}
}

func TestGCLastSuccessIgnoresFailures(t *testing.T) {
	m := New()
	start := testutil.ToFloat64(m.gcLastSuccess)
	time.Sleep(1100 * time.Millisecond) // a second passes, so a wrong update would show
	m.GCRun(GCError, time.Second, 0, 0)
	m.GCRun(GCSkippedLocked, time.Second, 0, 0)
	if testutil.ToFloat64(m.gcLastSuccess) != start {
		t.Fatal("a failed or skipped sweep set the last-success timestamp")
	}
}

// A replica that has not swept yet reports its start time, not 0: the
// GC-stalled alert measures time since the last success, and 0 would read as
// decades after every rollout.
func TestGCLastSuccessStartsAtProcessStart(t *testing.T) {
	before := float64(time.Now().Unix())
	m := New()
	if got := testutil.ToFloat64(m.gcLastSuccess); got < before || got > float64(time.Now().Unix())+1 {
		t.Fatalf("gc_last_success_timestamp_seconds = %v at start, want about %v", got, before)
	}
}

func TestCaptureKindsRemainBoundedAndArtifactsAreSeparate(t *testing.T) {
	m := New()
	m.Capture("untrusted-kind", CaptureInvalid)
	m.Capture("inspection", CaptureCreated)
	m.Dismissal("inspection")
	m.CaptureArtifactBytes("inspection", "payload", 0)
	if got := testutil.ToFloat64(m.captures.WithLabelValues("unknown", CaptureInvalid)); got != 1 {
		t.Fatal(got)
	}
	if got := testutil.ToFloat64(m.captures.WithLabelValues("inspection", CaptureCreated)); got != 1 {
		t.Fatal(got)
	}
	if got := testutil.ToFloat64(m.dismissals.WithLabelValues("inspection")); got != 1 {
		t.Fatal(got)
	}
	if got := testutil.CollectAndCount(m.artifactBytes); got != 1 {
		t.Fatalf("zero-byte payload should create one histogram, got %d", got)
	}
}
