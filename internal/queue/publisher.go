package queue

import (
	"context"
	"log/slog"
	"net/http"
	"time"
)

type Publisher interface {
	Publish(ctx context.Context, destinationQueueID, queue string, msg Message) error
	Ready(ctx context.Context) error
	Close() error
}

type Message struct {
	SchemaVersion  string `json:"schema_version"`
	ID             string `json:"id"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
	RequestID      string `json:"request_id,omitempty"`
	TraceID        string `json:"trace_id,omitempty"`
	// Traceparent is the outbound W3C traceparent header value: the inbound
	// header verbatim when syntactically valid, otherwise one synthesized
	// from TraceID. The webhook destination sets it on the outbound request
	// (envelope and proxy modes); other destinations carry it in the JSON
	// envelope but do not otherwise act on it.
	Traceparent        string         `json:"traceparent,omitempty"`
	Endpoint           string         `json:"endpoint"`
	DestinationQueueID string         `json:"destination_queue_id"`
	Queue              string         `json:"queue"`
	ReceivedAt         time.Time      `json:"received_at"`
	Method             string         `json:"method,omitempty"`
	Path               string         `json:"path,omitempty"`
	Headers            http.Header    `json:"headers,omitempty"`
	Query              map[string]any `json:"query,omitempty"`
	Body               any            `json:"body"`
	RawBodyBase64      *string        `json:"raw_body_base64,omitempty"`
	SourceIP           string         `json:"source_ip,omitempty"`
	Auth               any            `json:"auth,omitempty"`
	// In-memory only (never serialized); carries the original request for
	// webhook proxy mode. Other destinations ignore these.
	RawBody     []byte      `json:"-"`
	OrigHeaders http.Header `json:"-"`
	// Hops is the inbound loop-protection hop count. The webhook destination
	// stamps Hops+1 on the outbound request; other destinations ignore it.
	Hops int `json:"-"`
	// DeadLetter is populated only when an async webhook delivery has terminally
	// failed and is being redirected to a configured dead-letter destination. It
	// is additive and omitted otherwise, so schema_version stays 1.0.
	DeadLetter *DeadLetterMeta `json:"dead_letter,omitempty"`
	// ReplayOf is set only on messages produced by the replay API: it carries
	// the original captured event id this delivery is a replay of. It is
	// additive and omitted for normal (first-time) deliveries, so
	// schema_version stays 1.0.
	ReplayOf string `json:"replay_of,omitempty"`
}

// DeadLetterMeta records why and how a webhook delivery was dead-lettered. It
// travels on the copied Message the dead-letter destination receives.
type DeadLetterMeta struct {
	Destination string    `json:"destination"` // the failed webhook destination id
	Target      string    `json:"target"`      // the failed webhook target
	Outcome     string    `json:"outcome"`     // last delivery outcome (4xx|5xx|429|timeout|error)
	Attempts    int       `json:"attempts"`    // delivery attempts made before giving up
	FailedAt    time.Time `json:"failed_at"`
}

type RabbitMQConfig struct {
	URL            string
	Exchange       string
	ExchangeType   string
	Durable        bool
	PublishTimeout time.Duration
	DeclareQueues  bool
	// PublisherChannels bounds how many AMQP channels publish concurrently.
	// Each channel carries one publisher-confirm round-trip at a time, so this
	// is the effective publish concurrency. Defaults to 8 when <= 0.
	PublisherChannels int
	TLS               RabbitMQTLSConfig
	Queues            []string
	// QueueDeclarations is the queue-native shape factory/config can grow into
	// without changing Publisher. Queues remains for existing simple config.
	QueueDeclarations []QueueDeclaration
	Retry             RetryPolicy
	Metrics           RabbitMQMetrics
	DestinationID     string
	Logger            *slog.Logger
}

// RabbitMQTLSConfig mirrors the config-package TLS options for amqps:// dialing.
type RabbitMQTLSConfig struct {
	CACert             string
	ClientCert         string
	ClientKey          string
	ServerName         string
	InsecureSkipVerify bool
}

type QueueDeclaration struct {
	Name        string
	CreateQueue bool
	Durable     *bool
	Options     QueueOptions
	DLQ         *DeadLetterDeclaration
}

type QueueOptions struct {
	MessageTTL           time.Duration
	Expires              time.Duration
	MaxLength            int64
	Overflow             string
	DeadLetterRoutingKey string
}

type DeadLetterDeclaration struct {
	Exchange     string
	ExchangeType string
	Queue        string
	RoutingKey   string
	CreateQueue  bool
	Durable      *bool
}

type RetryPolicy struct {
	MaxAttempts    int
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
}

type RabbitMQMetrics interface {
	RabbitMQReconnect()
	RabbitMQPublishConfirm()
	RabbitMQPublishRetry()
	RabbitMQDeadLetterPublish()
	// RabbitMQUnroutable records a mandatory publish the broker returned
	// (basic.return) as unroutable — a terminal config error (no binding for
	// the routing key), not a transient failure.
	RabbitMQUnroutable(destinationQueueID, queue string)
}

// RedisPubSubMetrics records redis_pubsub delivery observability. Implemented by
// *metrics.Metrics; passed via RedisPubSubConfig. May be nil.
type RedisPubSubMetrics interface {
	RedisPubSubNoSubscriber(destination, target string)
}

// WebhookMetrics records webhook delivery observability (implemented by *metrics.Metrics; may be nil).
type WebhookMetrics interface {
	WebhookDelivery(destination, target, outcome string)
	WebhookRetry(destination, target string)
	// WebhookDropped records an async delivery dropped before it was ever
	// attempted. reason is "queue_full" or "engine_closing".
	WebhookDropped(destination, target, reason string)
	// WebhookDeadLetter records a terminally-failed delivery successfully
	// redirected to its dead-letter destination.
	WebhookDeadLetter(destination, target, outcome string)
	// WebhookDeadLetterFailure records a dead-letter redirect that itself failed.
	WebhookDeadLetterFailure(destination, target string)
	// WebhookQueueDepth reports the current async delivery queue depth.
	WebhookQueueDepth(destination string, depth int)
	// WebhookQueueCapacity reports the async delivery queue capacity (set once).
	WebhookQueueCapacity(destination string, capacity int)
	// Panic records a panic recovered inside the async delivery engine worker.
	// location is always "webhook_worker" for this interface's callers.
	Panic(location string)
}
