package queue

import (
	"context"
	"net/http"
	"time"
)

type Publisher interface {
	Publish(ctx context.Context, queue string, msg Message) error
	Ready(ctx context.Context) error
	Close() error
}

type Message struct {
	SchemaVersion  string         `json:"schema_version"`
	ID             string         `json:"id"`
	IdempotencyKey string         `json:"idempotency_key,omitempty"`
	RequestID      string         `json:"request_id,omitempty"`
	TraceID        string         `json:"trace_id,omitempty"`
	Endpoint       string         `json:"endpoint"`
	Queue          string         `json:"queue"`
	ReceivedAt     time.Time      `json:"received_at"`
	Method         string         `json:"method,omitempty"`
	Path           string         `json:"path,omitempty"`
	Headers        http.Header    `json:"headers,omitempty"`
	Query          map[string]any `json:"query,omitempty"`
	Body           any            `json:"body"`
	RawBodyBase64  *string        `json:"raw_body_base64,omitempty"`
	SourceIP       string         `json:"source_ip,omitempty"`
	Auth           any            `json:"auth,omitempty"`
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
	DLQ         *DeadLetterDeclaration
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
}
