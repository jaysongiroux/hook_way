package metrics

import (
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"

	"github.com/jasongiroux/hookway/internal/config"
)

type Metrics struct {
	registry                 *prometheus.Registry
	httpRequests             *prometheus.CounterVec
	authFailures             *prometheus.CounterVec
	rateLimited              *prometheus.CounterVec
	saturated                *prometheus.CounterVec
	forbidden                *prometheus.CounterVec
	replayRejected           *prometheus.CounterVec
	publishTotal             *prometheus.CounterVec
	publishDuration          *prometheus.HistogramVec
	messagesPublished        *prometheus.CounterVec
	requestDuration          *prometheus.HistogramVec
	bodyBytes                *prometheus.HistogramVec
	endpointInfo             *prometheus.GaugeVec
	destinationInfo          *prometheus.GaugeVec
	inflight                 prometheus.Gauge
	configLoadFailures       *prometheus.CounterVec
	rabbitMQReconnects       prometheus.Counter
	rabbitMQConfirms         prometheus.Counter
	rabbitMQRetries          prometheus.Counter
	rabbitMQDLQPublishes     prometheus.Counter
	redisPubSubNoSubscribers *prometheus.CounterVec
	webhookDelivery          *prometheus.CounterVec
	webhookRetries           *prometheus.CounterVec
	webhookDropped           *prometheus.CounterVec
	runtimeRegistered        bool
}

func New() *Metrics {
	m := &Metrics{registry: prometheus.NewRegistry()}
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
	m.replayRejected = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "hookway_replay_rejected_total",
		Help: "Webhook requests rejected as signature replays.",
	}, []string{"endpoint", "destination_queue_id", "queue"})
	m.publishTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "hookway_publish_total",
		Help: "RabbitMQ publish attempts.",
	}, []string{"endpoint", "destination_queue_id", "queue", "status"})
	m.publishDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name: "hookway_publish_duration_seconds",
		Help: "RabbitMQ publish duration.",
	}, []string{"endpoint", "destination_queue_id", "queue"})
	m.messagesPublished = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "hookway_messages_published_total",
		Help: "Webhook messages successfully published to RabbitMQ.",
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
		Name: "hookway_webhook_dropped_total", Help: "Webhook deliveries dropped because the queue was full.",
	}, []string{"destination_queue_id", "queue"})
	m.registry.MustRegister(
		m.httpRequests, m.authFailures, m.rateLimited, m.saturated, m.forbidden, m.replayRejected,
		m.publishTotal, m.publishDuration, m.messagesPublished, m.requestDuration, m.bodyBytes,
		m.endpointInfo, m.destinationInfo, m.inflight, m.configLoadFailures, m.rabbitMQReconnects,
		m.rabbitMQConfirms, m.rabbitMQRetries, m.rabbitMQDLQPublishes, m.redisPubSubNoSubscribers,
		m.webhookDelivery, m.webhookRetries, m.webhookDropped,
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

func (m *Metrics) ReplayRejected(endpoint, destinationQueueID, queue string) {
	m.replayRejected.WithLabelValues(endpoint, destinationQueueID, queue).Inc()
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
			m.endpointInfo.WithLabelValues(name, route.Destination, route.Target, endpoint.Path).Set(1)
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

func (m *Metrics) RedisPubSubNoSubscriber(destination, target string) {
	m.redisPubSubNoSubscribers.WithLabelValues(destination, target).Inc()
}

func (m *Metrics) WebhookDelivery(destination, target, outcome string) {
	m.webhookDelivery.WithLabelValues(destination, target, outcome).Inc()
}

func (m *Metrics) WebhookRetry(destination, target string) {
	m.webhookRetries.WithLabelValues(destination, target).Inc()
}

func (m *Metrics) WebhookDropped(destination, target string) {
	m.webhookDropped.WithLabelValues(destination, target).Inc()
}
