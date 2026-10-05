// Package metrics is the replay service's Prometheus instrumentation. It
// mirrors core's internal/metrics: its own registry, typed recorder methods,
// and label values bounded by configuration, never by stored data.
package metrics

import (
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

// Capture outcomes (captures_total).
const (
	CaptureCreated   = "created"
	CaptureDuplicate = "duplicate"
	CaptureConflict  = "conflict"
	CaptureCapacity  = "capacity"
	CaptureTooLarge  = "too_large"
	CaptureInvalid   = "invalid"
)

// Payload download outcomes (payload_downloads_total).
const (
	DownloadOK       = "ok"
	DownloadCorrupt  = "corrupt"
	DownloadNotFound = "not_found"
	DownloadGone     = "gone"
)

// Retry request modes (runs_created_total).
const (
	ModeSingle = "single"
	ModeBatch  = "batch"
)

// Executor call outcomes (executor_calls_total), one per attempt.
const (
	CallOK          = "ok"
	CallUnavailable = "unavailable" // core provably did not start the delivery
	CallAuth        = "auth"
	CallBlocked     = "blocked"
	CallAmbiguous   = "ambiguous"
	CallRejected    = "rejected" // core refused the request before executing it
)

// GC sweep outcomes (gc_runs_total).
const (
	GCOK            = "ok"
	GCSkippedLocked = "skipped_locked"
	GCError         = "error"
)

// RunState is one sample of the retry queue.
type RunState struct {
	Queued       int64
	Running      int64
	OldestQueued time.Duration
}

// recordStatuses are the records gauge's status values; the sampler zeroes
// any status absent from a sample.
var recordStatuses = []string{"open", "retrying", "resolved", "unknown", "dismissed"}

// State is one sample of the shared database. Every replica reports it, so
// dashboards aggregate these gauges with max, not sum.
type State struct {
	RecordsByStatus map[string]int64
	UsageRecords    int64
	UsageBytes      int64
	SchemaVersion   int64
}

// PoolStats is a snapshot of the database connection pool.
type PoolStats struct {
	Acquired, Idle, Total, Max int32
	EmptyAcquires              int64
}

type Metrics struct {
	registry          *prometheus.Registry
	buildInfo         *prometheus.GaugeVec
	panics            *prometheus.CounterVec
	httpRequests      *prometheus.CounterVec
	httpDuration      *prometheus.HistogramVec
	authFailures      *prometheus.CounterVec
	captures          *prometheus.CounterVec
	artifactBytes     *prometheus.HistogramVec
	payloadDownloads  *prometheus.CounterVec
	runsCreated       *prometheus.CounterVec
	dismissals        prometheus.Counter
	records           *prometheus.GaugeVec
	usageRecords      prometheus.Gauge
	usageBytes        prometheus.Gauge
	capacityRecords   prometheus.Gauge
	capacityBytes     prometheus.Gauge
	dbUp              prometheus.Gauge
	schemaVersion     prometheus.Gauge
	runs              *prometheus.GaugeVec
	oldestQueued      prometheus.Gauge
	runsTotal         *prometheus.CounterVec
	runDuration       *prometheus.HistogramVec
	executorCalls     *prometheus.CounterVec
	workersBusy       prometheus.Gauge
	leasesExpired     prometheus.Counter
	lateResults       prometheus.Counter
	childrenCreated   prometheus.Counter
	gcRuns            *prometheus.CounterVec
	gcDeletedRecords  prometheus.Counter
	gcDeletedBytes    prometheus.Counter
	gcDuration        prometheus.Histogram
	gcLastSuccess     prometheus.Gauge
	runtimeRegistered bool
	poolRegistered    bool
}

func counterVec(name, help string, labels ...string) *prometheus.CounterVec {
	return prometheus.NewCounterVec(prometheus.CounterOpts{Name: name, Help: help}, labels)
}

func gauge(name, help string) prometheus.Gauge {
	return prometheus.NewGauge(prometheus.GaugeOpts{Name: name, Help: help})
}

func New() *Metrics {
	m := &Metrics{registry: prometheus.NewRegistry()}
	m.buildInfo = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "hookway_replay_build_info", Help: "Build of the running replay service; always 1.",
	}, []string{"version", "commit"})
	m.panics = counterVec("hookway_replay_panics_total", "Recovered panics, by where they were recovered.", "location")
	m.httpRequests = counterVec("hookway_replay_http_requests_total", "HTTP requests served, by route pattern, method, and status.", "route", "method", "status")
	m.httpDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name: "hookway_replay_http_request_duration_seconds", Help: "HTTP request latency, by route pattern and method.",
		Buckets: prometheus.DefBuckets,
	}, []string{"route", "method"})
	m.authFailures = counterVec("hookway_replay_auth_failures_total", "Rejected API requests, by reason (missing, invalid, scope, throttled).", "reason")
	m.captures = counterVec("hookway_replay_captures_total", "Ingest requests, by outcome.", "outcome")
	m.artifactBytes = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name: "hookway_replay_capture_artifact_bytes", Help: "Size of stored artifacts, by artifact (snapshot, payload).",
		Buckets: prometheus.ExponentialBuckets(1024, 4, 10),
	}, []string{"artifact"})
	m.payloadDownloads = counterVec("hookway_replay_payload_downloads_total", "Payload download requests, by outcome.", "outcome")
	m.runsCreated = counterVec("hookway_replay_runs_created_total", "Retry runs created, by request mode (single, batch).", "mode")
	m.dismissals = prometheus.NewCounter(prometheus.CounterOpts{Name: "hookway_replay_dismissals_total", Help: "Records dismissed."})
	m.records = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "hookway_replay_records", Help: "Records in the shared database, by status (sampled; aggregate replicas with max).",
	}, []string{"status"})
	m.usageRecords = gauge("hookway_replay_usage_records", "Records counted against retention.max_records (sampled).")
	m.usageBytes = gauge("hookway_replay_usage_bytes", "Artifact bytes counted against retention.max_bytes (sampled).")
	m.capacityRecords = gauge("hookway_replay_capacity_records", "Configured retention.max_records.")
	m.capacityBytes = gauge("hookway_replay_capacity_bytes", "Configured retention.max_bytes.")
	m.dbUp = gauge("hookway_replay_db_up", "1 when the last state sample reached the database.")
	m.schemaVersion = gauge("hookway_replay_schema_version", "Applied database schema version (sampled).")
	m.runs = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "hookway_replay_runs", Help: "Retry runs queued or running (sampled; aggregate replicas with max).",
	}, []string{"status"})
	m.oldestQueued = gauge("hookway_replay_oldest_queued_run_seconds", "Age of the oldest queued run (sampled).")
	m.runsTotal = counterVec("hookway_replay_runs_total", "Retry runs finished by this replica, by status.", "status")
	m.runDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name: "hookway_replay_run_duration_seconds", Help: "Time from claiming a run to finalizing it, by status.",
		Buckets: prometheus.DefBuckets,
	}, []string{"status"})
	m.executorCalls = counterVec("hookway_replay_executor_calls_total", "Calls to core's control listener, by outcome.", "outcome")
	m.workersBusy = gauge("hookway_replay_workers_busy", "Runs this replica is executing now.")
	m.leasesExpired = prometheus.NewCounter(prometheus.CounterOpts{Name: "hookway_replay_leases_expired_total", Help: "Running runs whose lease expired; each became unknown."})
	m.lateResults = prometheus.NewCounter(prometheus.CounterOpts{Name: "hookway_replay_late_results_total", Help: "Results that arrived after their run's lease expired."})
	m.childrenCreated = prometheus.NewCounter(prometheus.CounterOpts{Name: "hookway_replay_children_created_total", Help: "Child records created from downstream failures of redrives."})
	m.gcRuns = counterVec("hookway_replay_gc_runs_total", "GC sweeps, by outcome.", "outcome")
	m.gcDeletedRecords = prometheus.NewCounter(prometheus.CounterOpts{Name: "hookway_replay_gc_deleted_records_total", Help: "Records deleted by GC."})
	m.gcDeletedBytes = prometheus.NewCounter(prometheus.CounterOpts{Name: "hookway_replay_gc_deleted_bytes_total", Help: "Artifact bytes freed by GC."})
	m.gcDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name: "hookway_replay_gc_duration_seconds", Help: "GC sweep duration.", Buckets: prometheus.DefBuckets,
	})
	m.gcLastSuccess = gauge("hookway_replay_gc_last_success_timestamp_seconds", "Unix time of this replica's last successful GC sweep (its start time until the first).")
	// Until the first sweep, report the start time: "time since the last
	// success" then means "since start", not decades, after every rollout.
	m.gcLastSuccess.SetToCurrentTime()
	m.registry.MustRegister(m.buildInfo, m.panics, m.httpRequests, m.httpDuration, m.authFailures, m.captures,
		m.artifactBytes, m.payloadDownloads, m.runsCreated, m.dismissals, m.records, m.usageRecords, m.usageBytes,
		m.capacityRecords, m.capacityBytes, m.dbUp, m.schemaVersion,
		m.runs, m.oldestQueued, m.runsTotal, m.runDuration, m.executorCalls, m.workersBusy, m.leasesExpired,
		m.lateResults, m.childrenCreated, m.gcRuns, m.gcDeletedRecords, m.gcDeletedBytes, m.gcDuration, m.gcLastSuccess)
	// Pre-create every bounded series so rate() and absent() queries see a
	// zero before the first event.
	for _, s := range recordStatuses {
		m.records.WithLabelValues(s)
	}
	for _, r := range []string{"missing", "invalid", "scope", "throttled"} {
		m.authFailures.WithLabelValues(r)
	}
	for _, o := range []string{CaptureCreated, CaptureDuplicate, CaptureConflict, CaptureCapacity, CaptureTooLarge, CaptureInvalid} {
		m.captures.WithLabelValues(o)
	}
	for _, o := range []string{DownloadOK, DownloadCorrupt, DownloadNotFound, DownloadGone} {
		m.payloadDownloads.WithLabelValues(o)
	}
	for _, mode := range []string{ModeSingle, ModeBatch} {
		m.runsCreated.WithLabelValues(mode)
	}
	for _, s := range []string{"queued", "running"} {
		m.runs.WithLabelValues(s)
	}
	for _, s := range []string{"succeeded", "failed", "blocked", "unknown"} {
		m.runsTotal.WithLabelValues(s)
	}
	for _, o := range []string{CallOK, CallUnavailable, CallAuth, CallBlocked, CallAmbiguous, CallRejected} {
		m.executorCalls.WithLabelValues(o)
	}
	for _, o := range []string{GCOK, GCSkippedLocked, GCError} {
		m.gcRuns.WithLabelValues(o)
	}
	return m
}

// EnableRuntimeCollectors registers the Go runtime and process collectors.
// It is idempotent.
func (m *Metrics) EnableRuntimeCollectors() {
	if m.runtimeRegistered {
		return
	}
	m.registry.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	m.runtimeRegistered = true
}

func (m *Metrics) Registry() *prometheus.Registry { return m.registry }

// SetBuildInfo records the compiled-in version and commit.
func (m *Metrics) SetBuildInfo(version, commit string) {
	m.buildInfo.WithLabelValues(version, commit).Set(1)
}

// Panic records a recovered panic; location is a fixed code site ("handler").
func (m *Metrics) Panic(location string) { m.panics.WithLabelValues(location).Inc() }

// HTTPRequest records one served request. route is the mux pattern's path
// (or "unmatched") and method is one of the known methods (or "other"), so
// neither can grow without bound.
func (m *Metrics) HTTPRequest(route, method string, status int, d time.Duration) {
	m.httpRequests.WithLabelValues(route, method, strconv.Itoa(status)).Inc()
	m.httpDuration.WithLabelValues(route, method).Observe(d.Seconds())
}

func (m *Metrics) AuthFailure(reason string) { m.authFailures.WithLabelValues(reason).Inc() }

func (m *Metrics) Capture(outcome string) { m.captures.WithLabelValues(outcome).Inc() }

func (m *Metrics) CaptureArtifactBytes(artifact string, n int) {
	m.artifactBytes.WithLabelValues(artifact).Observe(float64(n))
}

func (m *Metrics) PayloadDownload(outcome string) { m.payloadDownloads.WithLabelValues(outcome).Inc() }

func (m *Metrics) RunsCreated(mode string, n int) {
	m.runsCreated.WithLabelValues(mode).Add(float64(n))
}

func (m *Metrics) Dismissal() { m.dismissals.Inc() }

// SetCapacity records the configured retention limits.
func (m *Metrics) SetCapacity(records, bytes int64) {
	m.capacityRecords.Set(float64(records))
	m.capacityBytes.Set(float64(bytes))
}

// SetDBUp records whether the last state sample reached the database.
func (m *Metrics) SetDBUp(up bool) {
	if up {
		m.dbUp.Set(1)
	} else {
		m.dbUp.Set(0)
	}
}

// SetState records a successful sample. A status missing from the sample is
// set to zero, so a status that empties out does not keep its last value.
func (m *Metrics) SetState(s State) {
	for _, status := range recordStatuses {
		m.records.WithLabelValues(status).Set(float64(s.RecordsByStatus[status]))
	}
	m.usageRecords.Set(float64(s.UsageRecords))
	m.usageBytes.Set(float64(s.UsageBytes))
	m.schemaVersion.Set(float64(s.SchemaVersion))
}

// RegisterPool exposes connection-pool statistics, read at scrape time (an
// in-memory read, not a database query). Only the first call registers.
func (m *Metrics) RegisterPool(stats func() PoolStats) {
	if m.poolRegistered {
		return
	}
	conns := func(state string, read func(PoolStats) int32) prometheus.Collector {
		return prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: "hookway_replay_db_pool_connections", Help: "Database pool connections, by state.",
			ConstLabels: prometheus.Labels{"state": state},
		}, func() float64 { return float64(read(stats())) })
	}
	m.registry.MustRegister(
		conns("acquired", func(s PoolStats) int32 { return s.Acquired }),
		conns("idle", func(s PoolStats) int32 { return s.Idle }),
		conns("total", func(s PoolStats) int32 { return s.Total }),
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: "hookway_replay_db_pool_max_connections", Help: "Configured database pool size.",
		}, func() float64 { return float64(stats().Max) }),
		prometheus.NewCounterFunc(prometheus.CounterOpts{
			Name: "hookway_replay_db_pool_empty_acquires_total", Help: "Pool acquires that waited because no connection was idle.",
		}, func() float64 { return float64(stats().EmptyAcquires) }),
	)
	m.poolRegistered = true
}

// SetRunState records a sample of the retry queue.
func (m *Metrics) SetRunState(s RunState) {
	m.runs.WithLabelValues("queued").Set(float64(s.Queued))
	m.runs.WithLabelValues("running").Set(float64(s.Running))
	m.oldestQueued.Set(s.OldestQueued.Seconds())
}

// RunFinished records a finalized run.
func (m *Metrics) RunFinished(status string, d time.Duration) {
	m.runsTotal.WithLabelValues(status).Inc()
	m.runDuration.WithLabelValues(status).Observe(d.Seconds())
}

// ExecutorCall records one attempt to reach core's control listener.
func (m *Metrics) ExecutorCall(outcome string) { m.executorCalls.WithLabelValues(outcome).Inc() }

func (m *Metrics) SetWorkersBusy(n int64) { m.workersBusy.Set(float64(n)) }

func (m *Metrics) LeasesExpired(n int) { m.leasesExpired.Add(float64(n)) }

func (m *Metrics) LateResult() { m.lateResults.Inc() }

func (m *Metrics) ChildrenCreated(n int) { m.childrenCreated.Add(float64(n)) }

// GCRun records a sweep. Only a successful sweep moves the last-success
// timestamp, which the "no successful sweep" alert watches.
func (m *Metrics) GCRun(outcome string, d time.Duration, records, bytes int64) {
	m.gcRuns.WithLabelValues(outcome).Inc()
	m.gcDuration.Observe(d.Seconds())
	m.gcDeletedRecords.Add(float64(records))
	m.gcDeletedBytes.Add(float64(bytes))
	if outcome == GCOK {
		m.gcLastSuccess.SetToCurrentTime()
	}
}
