package metrics

import (
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

type Metrics struct {
	registry             *prometheus.Registry
	httpRequests         *prometheus.CounterVec
	authFailures         *prometheus.CounterVec
	rateLimited          *prometheus.CounterVec
	publishTotal         *prometheus.CounterVec
	publishDuration      *prometheus.HistogramVec
	bodyBytes            *prometheus.HistogramVec
	inflight             prometheus.Gauge
	configLoadFailures   *prometheus.CounterVec
	rabbitMQReconnects   prometheus.Counter
	rabbitMQConfirms     prometheus.Counter
	rabbitMQRetries      prometheus.Counter
	rabbitMQDLQPublishes prometheus.Counter
}

func New() *Metrics {
	m := &Metrics{registry: prometheus.NewRegistry()}
	m.httpRequests = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "hookway_http_requests_total",
		Help: "HTTP requests handled by Hookway.",
	}, []string{"endpoint", "queue", "status"})
	m.authFailures = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "hookway_auth_failures_total",
		Help: "Webhook authentication failures.",
	}, []string{"endpoint", "reason"})
	m.rateLimited = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "hookway_rate_limited_total",
		Help: "Rate limited webhook requests.",
	}, []string{"endpoint", "queue"})
	m.publishTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "hookway_publish_total",
		Help: "RabbitMQ publish attempts.",
	}, []string{"endpoint", "queue", "status"})
	m.publishDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name: "hookway_publish_duration_seconds",
		Help: "RabbitMQ publish duration.",
	}, []string{"endpoint", "queue"})
	m.bodyBytes = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "hookway_request_body_bytes",
		Help:    "Webhook request body bytes.",
		Buckets: []float64{128, 512, 1024, 4096, 16384, 65536, 262144, 1048576},
	}, []string{"endpoint", "queue"})
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
	m.registry.MustRegister(
		m.httpRequests, m.authFailures, m.rateLimited, m.publishTotal, m.publishDuration,
		m.bodyBytes, m.inflight, m.configLoadFailures, m.rabbitMQReconnects, m.rabbitMQConfirms,
		m.rabbitMQRetries, m.rabbitMQDLQPublishes,
	)
	return m
}

func (m *Metrics) Registry() *prometheus.Registry { return m.registry }

func (m *Metrics) Inflight(delta float64) { m.inflight.Add(delta) }

func (m *Metrics) HTTP(endpoint, queue string, status int) {
	m.httpRequests.WithLabelValues(endpoint, queue, strconv.Itoa(status)).Inc()
}

func (m *Metrics) AuthFailure(endpoint, reason string) {
	m.authFailures.WithLabelValues(endpoint, reason).Inc()
}

func (m *Metrics) RateLimited(endpoint, queue string) {
	m.rateLimited.WithLabelValues(endpoint, queue).Inc()
}

func (m *Metrics) Publish(endpoint, queue, status string, duration time.Duration) {
	m.publishTotal.WithLabelValues(endpoint, queue, status).Inc()
	m.publishDuration.WithLabelValues(endpoint, queue).Observe(duration.Seconds())
}

func (m *Metrics) BodyBytes(endpoint, queue string, bytes int) {
	m.bodyBytes.WithLabelValues(endpoint, queue).Observe(float64(bytes))
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
