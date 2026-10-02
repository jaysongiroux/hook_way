package metrics

import (
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"

	"github.com/jaysongiroux/hook_way/internal/config"
)

type Metrics struct {
	admissionWaiting         prometheus.Gauge
	admissionWaits           *prometheus.CounterVec
	admissionWaitDuration    *prometheus.HistogramVec
	batch                    *prometheus.CounterVec
	credentials              *prometheus.CounterVec
	routing                  *prometheus.CounterVec
	registry                 *prometheus.Registry
	httpRequests             *prometheus.CounterVec
	authFailures             *prometheus.CounterVec
	rateLimited              *prometheus.CounterVec
	saturated                *prometheus.CounterVec
	forbidden                *prometheus.CounterVec
	duplicateRejected        *prometheus.CounterVec
	filtered                 *prometheus.CounterVec
	variableUnresolved       *prometheus.CounterVec
	variableLookup           *prometheus.CounterVec
	variableLookupDuration   *prometheus.HistogramVec
	loopRejected             *prometheus.CounterVec
	publishTotal             *prometheus.CounterVec
	publishDuration          *prometheus.HistogramVec
	messagesPublished        *prometheus.CounterVec
	requestDuration          *prometheus.HistogramVec
	bodyBytes                *prometheus.HistogramVec
	endpointInfo             *prometheus.GaugeVec
	destinationInfo          *prometheus.GaugeVec
	inflight                 prometheus.Gauge
	admitted                 *prometheus.GaugeVec
	admissionCapacity        prometheus.Gauge
	admissionServers         *prometheus.GaugeVec
	configLoadFailures       *prometheus.CounterVec
	rabbitMQReconnects       prometheus.Counter
	rabbitMQConfirms         prometheus.Counter
	rabbitMQRetries          prometheus.Counter
	rabbitMQDLQPublishes     prometheus.Counter
	rabbitMQUnroutable       *prometheus.CounterVec
	redisPubSubNoSubscribers *prometheus.CounterVec
	webhookDelivery          *prometheus.CounterVec
	webhookRetries           *prometheus.CounterVec
	webhookDropped           *prometheus.CounterVec
	webhookDeadLetter        *prometheus.CounterVec
	webhookDeadLetterFailure *prometheus.CounterVec
	webhookQueueDepth        *prometheus.GaugeVec
	webhookQueueCapacity     *prometheus.GaugeVec
	destinationReady         *prometheus.GaugeVec
	panics                   *prometheus.CounterVec
	buildInfo                *prometheus.GaugeVec
	compensationFailed       *prometheus.CounterVec
	flowOutcomes             *prometheus.CounterVec
	nodeTotal                *prometheus.CounterVec
	nodeDuration             *prometheus.HistogramVec
	secretRefreshFailures    *prometheus.CounterVec
	captureTotal             *prometheus.CounterVec
	captureDropped           *prometheus.CounterVec
	captureBufferBytes       prometheus.Gauge
	controlRuns              *prometheus.CounterVec
	controlInflight          prometheus.Gauge
	runtimeRegistered        bool
}

func New() *Metrics {
	m := &Metrics{registry: prometheus.NewRegistry()}
	m.admissionWaiting = prometheus.NewGauge(prometheus.GaugeOpts{Name: "hookway_admission_waiting_requests", Help: "HTTP handlers waiting for an execution slot; bodies have not been parsed."})
	m.admissionWaits = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "hookway_admission_wait_total", Help: "Contended admission outcomes for configured bounded waiting queues."}, []string{"flow", "result"})
	m.admissionWaitDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "hookway_admission_wait_duration_seconds", Help: "Time spent waiting for execution admission.", Buckets: []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5}}, []string{"flow", "result"})
	m.httpRequests = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "hookway_http_requests_total",
		Help: "HTTP requests handled by Hookway.",
	}, []string{"endpoint", "destination_queue_id", "queue", "status"})
	m.authFailures = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "hookway_auth_failures_total",
		Help: "Webhook authentication failures.",
	}, []string{"endpoint", "reason"})
	m.rateLimited = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "hookway_rate_limited_total",
		Help: "Rate limited webhook requests.",
	}, []string{"endpoint", "destination_queue_id", "queue"})
	m.saturated = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "hookway_saturated_total",
		Help: "Webhook requests rejected because the in-flight limit was reached.",
	}, []string{"endpoint", "destination_queue_id", "queue"})
	m.forbidden = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "hookway_forbidden_total",
		Help: "Webhook requests rejected by the endpoint IP allowlist.",
	}, []string{"endpoint", "destination_queue_id", "queue"})
	m.duplicateRejected = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "hookway_duplicate_rejected_total",
		Help: "Webhook requests rejected by a replay_guard node as duplicates.",
	}, []string{"endpoint", "destination_queue_id", "queue"})
	m.filtered = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "hookway_filtered_total",
		Help: "Webhook deliveries dropped by endpoint or route filters.",
	}, []string{"endpoint", "destination_queue_id", "queue", "level"})
	m.variableUnresolved = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "hookway_variable_unresolved_total",
		Help: "Webhook requests whose routing variables could not be resolved, by action taken (rejected or fallback).",
	}, []string{"endpoint", "variable", "action"})
	m.credentials = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "hookway_credentials_total", Help: "Native outbound credential operations by configured profile and bounded result."}, []string{"profile", "result"})
	m.batch = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "hookway_batch_children_total", Help: "Batch children by configured node and finite outcome."}, []string{"node", "result"})
	m.routing = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "hookway_routing_total",
		Help: "Typed routing and environment admission by bounded outcome.",
	}, []string{"node", "result"})
	m.variableLookup = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "hookway_variable_lookup_total",
		Help: "Lookup-source variable resolutions by result: hit, miss, cached, or error.",
	}, []string{"endpoint", "variable", "provider", "result"})
	m.variableLookupDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name: "hookway_variable_lookup_duration_seconds",
		Help: "Duration of lookup-source variable resolutions, including cache hits.",
	}, []string{"endpoint", "variable", "provider"})
	m.loopRejected = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "hookway_loop_rejected_total",
		Help: "Webhook requests rejected because the delivery hop count reached the loop-protection limit.",
	}, []string{"endpoint", "destination_queue_id", "queue"})
	m.publishTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "hookway_publish_total",
		Help: "Publish attempts to a configured destination.",
	}, []string{"endpoint", "destination_queue_id", "queue", "status"})
	m.publishDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name: "hookway_publish_duration_seconds",
		Help: "Publish duration to a configured destination.",
	}, []string{"endpoint", "destination_queue_id", "queue"})
	m.messagesPublished = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "hookway_messages_published_total",
		Help: "Webhook messages successfully published to a destination.",
	}, []string{"endpoint", "destination_queue_id", "queue"})
	m.requestDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "hookway_request_duration_seconds",
		Help:    "End-to-end webhook request handling duration.",
		Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
	}, []string{"endpoint", "destination_queue_id", "queue", "status"})
	m.bodyBytes = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "hookway_request_body_bytes",
		Help:    "Webhook request body bytes.",
		Buckets: []float64{128, 512, 1024, 4096, 16384, 65536, 262144, 1048576},
	}, []string{"endpoint", "destination_queue_id", "queue"})
	m.endpointInfo = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "hookway_endpoint_info",
		Help: "Enabled webhook endpoint (value is always 1).",
	}, []string{"endpoint", "destination_queue_id", "queue", "path"})
	m.destinationInfo = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "hookway_destination_info",
		Help: "Configured message queue destination (value is always 1).",
	}, []string{"destination_queue_id", "type"})
	m.inflight = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "hookway_inflight_requests",
		Help: "In-flight Hookway requests.",
	})
	m.admitted = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "hookway_admitted_requests",
		Help: "Active flow requests past admission, by bounded or unlimited mode; excludes health, metrics and admission rejections.",
	}, []string{"mode"})
	m.admissionCapacity = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "hookway_admission_capacity",
		Help: "Sum of finite admission slots for flow servers sharing this registry, including servers still draining admitted work.",
	})
	m.admissionServers = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "hookway_admission_servers",
		Help: "Flow servers owning admission telemetry by bounded or unlimited mode, including servers still draining admitted work.",
	}, []string{"mode"})
	for _, mode := range []string{"bounded", "unlimited"} {
		m.admitted.WithLabelValues(mode).Set(0)
		m.admissionServers.WithLabelValues(mode).Set(0)
	}
	m.configLoadFailures = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "hookway_config_load_failures_total",
		Help: "Config load failures before serving traffic.",
	}, []string{"reason"})
	m.rabbitMQReconnects = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "hookway_rabbitmq_reconnects_total",
		Help: "RabbitMQ reconnects.",
	})
	m.rabbitMQConfirms = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "hookway_rabbitmq_confirms_total",
		Help: "RabbitMQ publish confirms.",
	})
	m.rabbitMQRetries = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "hookway_rabbitmq_retries_total",
		Help: "RabbitMQ publish retries.",
	})
	m.rabbitMQDLQPublishes = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "hookway_rabbitmq_dead_letter_publish_total",
		Help: "RabbitMQ dead-letter publish attempts.",
	})
	m.rabbitMQUnroutable = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "hookway_rabbitmq_unroutable_total",
		Help: "RabbitMQ mandatory publishes returned as unroutable.",
	}, []string{"destination_queue_id", "queue"})
	m.redisPubSubNoSubscribers = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "hookway_redis_pubsub_dropped_no_subscriber_total",
		Help: "Redis pub/sub messages dropped because no subscriber was listening.",
	}, []string{"destination_queue_id", "queue"})
	m.webhookDelivery = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "hookway_webhook_delivery_total", Help: "Webhook delivery attempts by outcome.",
	}, []string{"destination_queue_id", "queue", "outcome"})
	m.webhookRetries = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "hookway_webhook_retries_total", Help: "Webhook delivery retries.",
	}, []string{"destination_queue_id", "queue"})
	m.webhookDropped = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "hookway_webhook_dropped_total", Help: "Webhook deliveries dropped before delivery, by reason (queue_full|engine_closing).",
	}, []string{"destination_queue_id", "queue", "reason"})
	m.webhookDeadLetter = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "hookway_webhook_dead_letter_total", Help: "Terminally-failed webhook deliveries redirected to a dead-letter destination.",
	}, []string{"destination_queue_id", "queue", "outcome"})
	m.webhookDeadLetterFailure = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "hookway_webhook_dead_letter_failures_total", Help: "Dead-letter redirects that themselves failed.",
	}, []string{"destination_queue_id", "queue"})
	m.webhookQueueDepth = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "hookway_webhook_queue_depth", Help: "Current webhook delivery queue depth.",
	}, []string{"destination_queue_id"})
	m.webhookQueueCapacity = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "hookway_webhook_queue_capacity", Help: "Configured webhook delivery queue capacity.",
	}, []string{"destination_queue_id"})
	m.destinationReady = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "hookway_destination_ready", Help: "Readiness of each configured message queue destination as observed by the background readiness prober (1 ready, 0 not ready).",
	}, []string{"destination_queue_id"})
	m.panics = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "hookway_panics_total", Help: "Panics recovered by Hookway, by the location they were recovered in.",
	}, []string{"location"})
	m.buildInfo = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "hookway_build_info", Help: "Build version and commit Hookway was compiled from (value is always 1).",
	}, []string{"version", "commit"})
	m.compensationFailed = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "hookway_compensation_failed_total",
		Help: "Replay-reservation release failures. A silent failure leaves the reservation until TTL, so a legitimate retry is rejected 409.",
	}, []string{"flow"})
	m.flowOutcomes = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "hookway_flow_outcomes_total", Help: "Execution outcomes independent of HTTP response status."}, []string{"flow", "outcome"})
	m.nodeTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "hookway_node_total",
		Help: "Graph node executions by outcome. Labels are config-bounded (flow, node id, node type); never rendered lookup targets.",
	}, []string{"flow", "node_id", "node_type", "outcome"})
	m.nodeDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "hookway_node_duration_seconds",
		Help:    "Graph node execution duration. Labels are config-bounded (flow, node id, node type).",
		Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
	}, []string{"flow", "node_id", "node_type"})
	m.secretRefreshFailures = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "hookway_secret_refresh_failures_total",
		Help: "Background secret_ref refresh failures. The last known-good secret continues to be served.",
	}, []string{"provider", "name"})
	m.captureTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "hookway_capture_total",
		Help: "Captured failures by flow and outcome (uploaded or dropped).",
	}, []string{"flow", "outcome"})
	m.captureDropped = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "hookway_capture_dropped_total",
		Help: "Captured failures dropped before reaching the replay service, by reason.",
	}, []string{"reason"})
	m.captureBufferBytes = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "hookway_capture_buffer_bytes",
		Help: "Bytes of captured failures waiting for upload, including in-flight uploads.",
	})
	for _, reason := range CaptureDropReasons {
		m.captureDropped.WithLabelValues(reason)
	}
	m.controlRuns = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "hookway_control_runs_total",
		Help: "Deliver requests the control listener answered, by flow and outcome.",
	}, []string{"flow", "outcome"})
	m.controlInflight = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "hookway_control_inflight",
		Help: "Redrives the control listener is running now.",
	})
	m.registry.MustRegister(
		m.admissionWaiting, m.admissionWaits, m.admissionWaitDuration,
		m.httpRequests, m.authFailures, m.rateLimited, m.saturated, m.forbidden, m.duplicateRejected, m.filtered, m.variableUnresolved, m.variableLookup, m.variableLookupDuration, m.loopRejected, m.routing, m.credentials, m.batch,
		m.publishTotal, m.publishDuration, m.messagesPublished, m.requestDuration, m.bodyBytes,
		m.endpointInfo, m.destinationInfo, m.inflight, m.admitted, m.admissionCapacity, m.admissionServers, m.configLoadFailures, m.rabbitMQReconnects,
		m.rabbitMQConfirms, m.rabbitMQRetries, m.rabbitMQDLQPublishes, m.rabbitMQUnroutable, m.redisPubSubNoSubscribers,
		m.webhookDelivery, m.webhookRetries, m.webhookDropped,
		m.webhookDeadLetter, m.webhookDeadLetterFailure, m.webhookQueueDepth, m.webhookQueueCapacity,
		m.destinationReady, m.panics, m.buildInfo, m.compensationFailed, m.flowOutcomes, m.nodeTotal, m.nodeDuration, m.secretRefreshFailures,
		m.captureTotal, m.captureDropped, m.captureBufferBytes, m.controlRuns, m.controlInflight,
	)
	return m
}

// EnableRuntimeCollectors registers the standard Go runtime and process
// collectors (goroutines, heap/GC, CPU seconds, resident memory, open FDs) on
// the metrics registry. It is idempotent and safe to call once after config is
// loaded. Go runtime metrics in particular are the cheapest way to catch a
// goroutine or memory leak in the publisher pool / reconnect watcher.
func (m *Metrics) EnableRuntimeCollectors() {
	if m.runtimeRegistered {
		return
	}
	m.registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	m.runtimeRegistered = true
}

func (m *Metrics) Registry() *prometheus.Registry { return m.registry }

func (m *Metrics) Inflight(delta float64) { m.inflight.Add(delta) }

// CaptureDropReasons are hookway_capture_dropped_total's reason values
// (capture's Drop* constants). Each series is created at 0, so an alert on
// increase() sees the very first drop.
var CaptureDropReasons = []string{"buffer_full", "upload_failed", "capacity", "rejected", "shutdown"}

// CaptureUploaded records a failure the replay service stored.
func (m *Metrics) CaptureUploaded(flow string) {
	m.captureTotal.WithLabelValues(flow, "uploaded").Inc()
}

// CaptureDropped records a captured failure that never reached the replay
// service. reason is one of capture's Drop* constants.
func (m *Metrics) CaptureDropped(flow, reason string) {
	m.captureTotal.WithLabelValues(flow, "dropped").Inc()
	m.captureDropped.WithLabelValues(reason).Inc()
}

// SetCaptureBufferBytes records the uploader's buffered bytes.
func (m *Metrics) SetCaptureBufferBytes(n int64) { m.captureBufferBytes.Set(float64(n)) }

// ControlRun records one deliver request. flow is a configured flow name or
// "unknown"; outcome is one of control's Outcome* constants.
func (m *Metrics) ControlRun(flow, outcome string) {
	m.controlRuns.WithLabelValues(flow, outcome).Inc()
}

// SetControlInflight records the redrives running now.
func (m *Metrics) SetControlInflight(n int64) { m.controlInflight.Set(float64(n)) }

func (m *Metrics) HTTP(endpoint, destinationQueueID, queue string, status int) {
	m.httpRequests.WithLabelValues(endpoint, destinationQueueID, queue, strconv.Itoa(status)).Inc()
}

func (m *Metrics) AuthFailure(endpoint, reason string) {
	m.authFailures.WithLabelValues(endpoint, reason).Inc()
}

func (m *Metrics) RateLimited(endpoint, destinationQueueID, queue string) {
	m.rateLimited.WithLabelValues(endpoint, destinationQueueID, queue).Inc()
}

func (m *Metrics) Saturated(endpoint, destinationQueueID, queue string) {
	m.saturated.WithLabelValues(endpoint, destinationQueueID, queue).Inc()
}

func (m *Metrics) Forbidden(endpoint, destinationQueueID, queue string) {
	m.forbidden.WithLabelValues(endpoint, destinationQueueID, queue).Inc()
}

// DuplicateRejected records a request a replay_guard node rejected as a
// duplicate (HTTP 409).
func (m *Metrics) DuplicateRejected(endpoint, destinationQueueID, queue string) {
	m.duplicateRejected.WithLabelValues(endpoint, destinationQueueID, queue).Inc()
}

// Filtered counts a delivery dropped by a filter chain. level is "endpoint" or
// "route".
func (m *Metrics) Filtered(endpoint, destinationQueueID, queue, level string) {
	m.filtered.WithLabelValues(endpoint, destinationQueueID, queue, level).Inc()
}

// VariableUnresolved records a request whose routing variable could not be
// resolved. action is "rejected" (400 returned) or "fallback" (routed to the
// endpoint's on_unresolved_variable route).
func (m *Metrics) VariableUnresolved(endpoint, variable, action string) {
	m.variableUnresolved.WithLabelValues(endpoint, variable, action).Inc()
}

// VariableLookup records one lookup-source resolution. result is "hit",
// "miss", "cached", or "error".
func (m *Metrics) VariableLookup(endpoint, variable, provider, result string, duration time.Duration) {
	m.variableLookup.WithLabelValues(endpoint, variable, provider, result).Inc()
	m.variableLookupDuration.WithLabelValues(endpoint, variable, provider).Observe(duration.Seconds())
}

// LoopRejected counts an inbound request rejected by loop protection.
func (m *Metrics) LoopRejected(endpoint, destinationQueueID, queue string) {
	m.loopRejected.WithLabelValues(endpoint, destinationQueueID, queue).Inc()
}

func (m *Metrics) Publish(endpoint, destinationQueueID, queue, status string, duration time.Duration) {
	m.publishTotal.WithLabelValues(endpoint, destinationQueueID, queue, status).Inc()
	m.publishDuration.WithLabelValues(endpoint, destinationQueueID, queue).Observe(duration.Seconds())
}

func (m *Metrics) BodyBytes(endpoint, destinationQueueID, queue string, bytes int) {
	m.bodyBytes.WithLabelValues(endpoint, destinationQueueID, queue).Observe(float64(bytes))
}

func (m *Metrics) MessagePublished(endpoint, destinationQueueID, queue string) {
	m.messagesPublished.WithLabelValues(endpoint, destinationQueueID, queue).Inc()
}

func (m *Metrics) RequestDuration(endpoint, destinationQueueID, queue string, status int, duration time.Duration) {
	m.requestDuration.WithLabelValues(endpoint, destinationQueueID, queue, strconv.Itoa(status)).Observe(duration.Seconds())
}

// RegisterConfig exposes enabled endpoints and configured destinations as info
// gauges so dashboards can list routing topology without scraping YAML.
func (m *Metrics) RegisterConfig(cfg config.Config) {
	for name, endpoint := range cfg.Endpoints {
		if !endpoint.Enabled {
			continue
		}
		for _, route := range endpoint.DestinationRoutes() {
			m.endpointInfo.WithLabelValues(name, route.Destination, route.Template, endpoint.Path).Set(1)
		}
	}
	for id, destination := range cfg.Destinations {
		queueType := destination.Type
		if queueType == "" {
			queueType = "rabbitmq"
		}
		m.destinationInfo.WithLabelValues(id, queueType).Set(1)
	}
}

func (m *Metrics) ConfigLoadFailure(reason string) {
	m.configLoadFailures.WithLabelValues(reason).Inc()
}

func (m *Metrics) RabbitMQReconnect() {
	m.rabbitMQReconnects.Inc()
}

func (m *Metrics) RabbitMQPublishConfirm() {
	m.rabbitMQConfirms.Inc()
}

func (m *Metrics) RabbitMQPublishRetry() {
	m.rabbitMQRetries.Inc()
}

func (m *Metrics) RabbitMQDeadLetterPublish() {
	m.rabbitMQDLQPublishes.Inc()
}

// RabbitMQUnroutable records a mandatory publish that the broker returned as
// unroutable (basic.return): the exchange had no matching binding for the
// routing key, so the message was never enqueued despite Hookway getting a
// broker confirm.
func (m *Metrics) RabbitMQUnroutable(destinationQueueID, queue string) {
	m.rabbitMQUnroutable.WithLabelValues(destinationQueueID, queue).Inc()
}

func (m *Metrics) RedisPubSubNoSubscriber(destination, target string) {
	m.redisPubSubNoSubscribers.WithLabelValues(destination, target).Inc()
}

func (m *Metrics) WebhookDelivery(destination, target, outcome string) {
	m.webhookDelivery.WithLabelValues(destination, target, outcome).Inc()
}

func (m *Metrics) WebhookRetry(destination, target string) {
	m.webhookRetries.WithLabelValues(destination, target).Inc()
}

func (m *Metrics) WebhookDropped(destination, target, reason string) {
	m.webhookDropped.WithLabelValues(destination, target, reason).Inc()
}

func (m *Metrics) WebhookDeadLetter(destination, target, outcome string) {
	m.webhookDeadLetter.WithLabelValues(destination, target, outcome).Inc()
}

func (m *Metrics) WebhookDeadLetterFailure(destination, target string) {
	m.webhookDeadLetterFailure.WithLabelValues(destination, target).Inc()
}

func (m *Metrics) WebhookQueueDepth(destination string, depth int) {
	m.webhookQueueDepth.WithLabelValues(destination).Set(float64(depth))
}

func (m *Metrics) WebhookQueueCapacity(destination string, capacity int) {
	m.webhookQueueCapacity.WithLabelValues(destination).Set(float64(capacity))
}

// DestinationReady sets the readiness gauge for a single destination: 1 when
// the background readiness prober's last probe of it succeeded, 0 otherwise.
func (m *Metrics) DestinationReady(destinationQueueID string, ready bool) {
	value := 0.0
	if ready {
		value = 1
	}
	m.destinationReady.WithLabelValues(destinationQueueID).Set(value)
}

// Panic records a recovered panic. location identifies where it was recovered:
// "handler" (ServeHTTP), "node" (graph executor), "fanout" (skip_failed
// fan-out publish worker), or "webhook_worker" (async webhook delivery engine worker).
func (m *Metrics) Panic(location string) {
	m.panics.WithLabelValues(location).Inc()
}

// CompensationFailed records a failed replay-reservation release. The HTTP
// response has already been decided; this is the only signal that a retry
// may be rejected 409 until the cache TTL expires.
func (m *Metrics) CompensationFailed(flow string) {
	m.compensationFailed.WithLabelValues(flow).Inc()
}

// Node records one graph node execution. outcome is "ok", "dropped", "error", or "panic".
// Labels must stay config-bounded: never pass a rendered lookup target or
// request-derived value.
func (m *Metrics) Node(flow, nodeID, nodeType, outcome string, duration time.Duration) {
	m.nodeTotal.WithLabelValues(flow, nodeID, nodeType, outcome).Inc()
	m.nodeDuration.WithLabelValues(flow, nodeID, nodeType).Observe(duration.Seconds())
}

// SecretRefreshFailure records a failed background secret_ref refresh. The
// resolver keeps serving the last known-good value.
func (m *Metrics) SecretRefreshFailure(provider, name string) {
	m.secretRefreshFailures.WithLabelValues(provider, name).Inc()
}

// SetBuildInfo records the running binary's version and commit as a gauge
// fixed at 1, so dashboards can join other metrics against the build that
// produced them. version and commit are compiled-in constants (see
// cmd/hookway's -ldflags-injected package vars), not user or request input,
// so this carries no unbounded-cardinality risk.
func (m *Metrics) SetBuildInfo(version, commit string) {
	m.buildInfo.WithLabelValues(version, commit).Set(1)
}

func (m *Metrics) FlowOutcome(flow, outcome string) {
	m.flowOutcomes.WithLabelValues(flow, outcome).Inc()
}

func (m *Metrics) Routing(node, result string) {
	switch result {
	case "hit", "miss", "key_invalid", "store_error", "environment_denied", "registration_registered", "registration_pending", "registration_key_invalid", "registration_environment_denied":
	default:
		result = "store_error"
	}
	m.routing.WithLabelValues(node, result).Inc()
}

func (m *Metrics) Credential(profile, result string) {
	switch result {
	case "acquired", "cache_hit", "acquisition_failed", "secret_refresh_failed", "session_rejected":
	default:
		result = "acquisition_failed"
	}
	m.credentials.WithLabelValues(profile, result).Inc()
}

func (m *Metrics) Batch(node, result string) {
	switch result {
	case "published", "environment_missing", "environment_denied", "environment_skipped", "id_invalid", "child_invalid", "filtered", "child_failed", "publish_failed", "unattempted":
	default:
		result = "child_failed"
	}
	m.batch.WithLabelValues(node, result).Inc()
}

func (m *Metrics) AdmissionWaiting(delta float64) { m.admissionWaiting.Add(delta) }
func (m *Metrics) AdmissionWait(flow, result string, duration time.Duration) {
	m.admissionWaits.WithLabelValues(flow, result).Inc()
	m.admissionWaitDuration.WithLabelValues(flow, result).Observe(duration.Seconds())
}
