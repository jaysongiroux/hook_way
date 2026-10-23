package config

import (
	"fmt"
	"time"
)

type RabbitMQConfig struct {
	URL               string
	Exchange          string
	ExchangeType      string
	Durable           bool
	PublishTimeout    time.Duration
	DeclareQueues     bool
	PublisherChannels int
	TLS               RabbitMQTLSConfig
	DeadLetter        RabbitMQDeadLetterConfig
	QueueDefaults     RabbitMQQueueOptions
	Retry             RetryPolicyConfig
}

// RabbitMQTLSConfig configures TLS for amqps:// connections. All fields are
// optional: with an empty block, amqps:// still works against brokers whose
// certificates chain to the system trust store (e.g. CloudAMQP). Set these only
// for private CAs, mutual TLS, or a non-default server name.
type RabbitMQTLSConfig struct {
	CACert             string
	ClientCert         string
	ClientKey          string
	ServerName         string
	InsecureSkipVerify bool
}

type RabbitMQDeadLetterConfig struct {
	Enabled      bool
	Exchange     string
	ExchangeType string
	Queue        string
	RoutingKey   string
	Durable      bool
	CreateQueue  bool
}

type RabbitMQQueueOptions struct {
	MessageTTL           time.Duration
	Expires              time.Duration
	MaxLength            int64
	Overflow             string
	DeadLetterRoutingKey string
}

// RedisTLSConfig configures TLS for Redis/Valkey connections. Mirror of RabbitMQTLSConfig.
// All fields are optional: set these only for private CAs, mutual TLS, or a non-default
// server name.
type RedisTLSConfig struct {
	Enabled            bool
	CACert             string
	ClientCert         string
	ClientKey          string
	ServerName         string
	InsecureSkipVerify bool
}

// RedisConfig holds the shared connection block for all redis_* destination types.
// Mode selects the deployment topology: "single" (default), "cluster", or "sentinel".
type RedisConfig struct {
	Mode       string
	URL        string
	Addrs      []string
	MasterName string
	Username   string
	Password   string
	DB         int
	KeyPrefix  string
	TLS        RedisTLSConfig
}

type MCPEventsConfig struct {
	Endpoint string `yaml:"endpoint" json:"endpoint" jsonschema:"required"`
}

type DestinationConfig struct {
	MCPEvents MCPEventsConfig
	Type      string
	// Connection is the connections entry this destination referenced, or ""
	// when it was configured inline. It records what was *authored*: the name
	// is kept even when it failed to resolve, so a consumer reading a Config
	// can still see the intent. Whether a merge actually happened is a
	// separate question, answered by SharedConfig.ConnectionOf.
	Connection         string
	OnExistingConflict string
	RabbitMQ           RabbitMQConfig
	SQS                SQSConfig
	SNS                SNSConfig
	S3                 S3Config
	Lambda             LambdaConfig
	Postgres           PostgresConfig
	Mongo              MongoConfig
	Redis              RedisConfig
	Kafka              KafkaConfig
	Webhook            WebhookConfig
	Temporal           TemporalConfig
}

type RetryPolicyConfig struct {
	MaxAttempts    int
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
}

type WebhookTLSConfig struct {
	CACert             string
	ClientCert         string
	ClientKey          string
	ServerName         string
	InsecureSkipVerify bool
}

type WebhookAuthConfig struct {
	Type          string // none | api_key | hmac | proxy
	Header        string // api_key: header name
	Value         string // api_key: header value
	Secret        string // hmac: shared secret
	Scheme        string // hmac: signature scheme (default hmac_sha256_ts_dot)
	EventIDHeader string // hmac: event-id header (default X-Hookway-Event-Id)
}

type WebhookConfig struct {
	URL string
	// AllowPrivateTargets permits deliberately internal request-templated hosts.
	// Literal operator-configured hosts retain their existing trust policy.
	AllowPrivateTargets bool
	Timeout             time.Duration
	MaxQueue            int
	MaxQueueBytes       int64
	DrainTimeout        time.Duration
	DeliveryMode        string // "" (async) | "sync" | "async"
	DeadLetter          WebhookDeadLetterConfig
	Retry               RetryPolicyConfig
	Auth                WebhookAuthConfig
	TLS                 WebhookTLSConfig
}

// WebhookDeadLetterConfig points a failed async webhook delivery at another
// (non-webhook) destination/target. Async only. Empty means log-and-drop.
type WebhookDeadLetterConfig struct {
	Destination string
	Target      string
}

type SQSConfig struct {
	Region          string
	Endpoint        string
	AccessKeyID     string
	SecretAccessKey string
}

type SNSConfig struct {
	Region          string
	Endpoint        string
	AccessKeyID     string
	SecretAccessKey string
}

// LambdaConfig is the AWS Lambda destination connection block. Credentials
// mirror SQS/SNS: both static keys or neither (default credential chain).
// InvocationType is "" (Event) | Event | RequestResponse.
type LambdaConfig struct {
	Region          string
	Endpoint        string
	AccessKeyID     string
	SecretAccessKey string
	InvocationType  string
}

type S3Config struct {
	Region          string
	Endpoint        string
	AccessKeyID     string
	SecretAccessKey string
	Bucket          string
	Create          bool
	SSE             string
	SSEKMSKeyID     string
}

type PostgresTLSConfig struct {
	CACert             string
	ClientCert         string
	ClientKey          string
	ServerName         string
	InsecureSkipVerify bool
}

type PostgresConfig struct {
	URL      string
	Schema   string // defaults to "public" when empty
	Table    string
	MaxConns int32
	TLS      PostgresTLSConfig
}

type MongoTLSConfig struct {
	CACert             string
	ClientCert         string
	ClientKey          string
	ServerName         string
	InsecureSkipVerify bool
}

type MongoConfig struct {
	URI         string
	Database    string
	MaxPoolSize uint64
	TLS         MongoTLSConfig
}

type KafkaConfig struct {
	Brokers        []string
	Acks           string
	Compression    string
	PublishTimeout time.Duration
	SASL           KafkaSASLConfig
	TLS            KafkaTLSConfig
	Retry          KafkaRetryConfig
}

type KafkaSASLConfig struct {
	Mechanism string
	Username  string
	Password  string
}

type KafkaTLSConfig struct {
	Enabled            bool
	CACert             string
	ClientCert         string
	ClientKey          string
	ServerName         string
	InsecureSkipVerify bool
}

type KafkaRetryConfig struct {
	MaxAttempts    int
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
}

// TemporalConfig is the connection block for a temporal destination. The
// route target is the task queue; workflow type and id derivation live in the
// per-route TemporalEndpointOptions. ConnectTimeout bounds the startup Dial
// (fail-fast); StartTimeout bounds each ExecuteWorkflow RPC.
type TemporalConfig struct {
	HostPort       string
	Namespace      string
	APIKey         string
	ConnectTimeout time.Duration
	StartTimeout   time.Duration
	TLS            TemporalTLSConfig
}

type TemporalTLSConfig struct {
	Enabled            bool
	CACert             string
	ClientCert         string
	ClientKey          string
	ServerName         string
	InsecureSkipVerify bool
}

type rawRabbitMQQueueOptions struct {
	MessageTTL           string `yaml:"message_ttl"`
	Expires              string `yaml:"expires"`
	MaxLength            int64  `yaml:"max_length"`
	Overflow             string `yaml:"overflow"`
	DeadLetterRoutingKey string `yaml:"dead_letter_routing_key"`
}

// rawPostgresConfig is the YAML mirror of PostgresConfig, used by postgres
// destinations (rawDestinationConfig.Postgres).
type rawPostgresConfig struct {
	URL      string `yaml:"url"`
	Schema   string `yaml:"schema"`
	Table    string `yaml:"table"`
	MaxConns int32  `yaml:"max_conns"`
	TLS      struct {
		CACert             string `yaml:"ca_cert"`
		ClientCert         string `yaml:"client_cert"`
		ClientKey          string `yaml:"client_key"`
		ServerName         string `yaml:"server_name"`
		InsecureSkipVerify bool   `yaml:"insecure_skip_verify"`
	} `yaml:"tls"`
}

// convertPostgresConfig maps a raw postgres connection block to the typed
// PostgresConfig, defaulting Schema to "public" when omitted.
func convertPostgresConfig(raw rawPostgresConfig) PostgresConfig {
	return PostgresConfig{
		URL:      raw.URL,
		Schema:   postgresSchema(raw.Schema),
		Table:    raw.Table,
		MaxConns: raw.MaxConns,
		TLS: PostgresTLSConfig{
			CACert:             raw.TLS.CACert,
			ClientCert:         raw.TLS.ClientCert,
			ClientKey:          raw.TLS.ClientKey,
			ServerName:         raw.TLS.ServerName,
			InsecureSkipVerify: raw.TLS.InsecureSkipVerify,
		},
	}
}

type rawDestinationConfig struct {
	MCPEvents MCPEventsConfig `yaml:"mcp_events"`
	Type      string          `yaml:"type"`
	// Connection names an entry in the top-level connections block whose type
	// can serve this destination type (see connTypeForDestType). Its fields
	// are merged into the backend block below by applyConnection, before
	// convertDestination runs.
	Connection         string `yaml:"connection"`
	OnExistingConflict string `yaml:"on_existing_conflict"`
	RabbitMQ           struct {
		URL               string                  `yaml:"url"`
		Exchange          string                  `yaml:"exchange"`
		ExchangeType      string                  `yaml:"exchange_type"`
		Durable           bool                    `yaml:"durable"`
		PublishTimeout    string                  `yaml:"publish_timeout"`
		DeclareQueues     bool                    `yaml:"declare_queues"`
		PublisherChannels int                     `yaml:"publisher_channels"`
		QueueDefaults     rawRabbitMQQueueOptions `yaml:"queue_defaults"`
		Retry             struct {
			MaxAttempts    int    `yaml:"max_attempts"`
			InitialBackoff string `yaml:"initial_backoff"`
			MaxBackoff     string `yaml:"max_backoff"`
		} `yaml:"retry"`
		TLS struct {
			CACert             string `yaml:"ca_cert"`
			ClientCert         string `yaml:"client_cert"`
			ClientKey          string `yaml:"client_key"`
			ServerName         string `yaml:"server_name"`
			InsecureSkipVerify bool   `yaml:"insecure_skip_verify"`
		} `yaml:"tls"`
		DeadLetter struct {
			Enabled      bool   `yaml:"enabled"`
			Exchange     string `yaml:"exchange"`
			ExchangeType string `yaml:"exchange_type"`
			Queue        string `yaml:"queue"`
			RoutingKey   string `yaml:"routing_key"`
			Durable      bool   `yaml:"durable"`
			CreateQueue  bool   `yaml:"create_queue"`
		} `yaml:"dead_letter"`
	} `yaml:"rabbitmq"`
	SQS struct {
		Region          string `yaml:"region"`
		Endpoint        string `yaml:"endpoint"`
		AccessKeyID     string `yaml:"access_key_id"`
		SecretAccessKey string `yaml:"secret_access_key"`
	} `yaml:"sqs"`
	SNS struct {
		Region          string `yaml:"region"`
		Endpoint        string `yaml:"endpoint"`
		AccessKeyID     string `yaml:"access_key_id"`
		SecretAccessKey string `yaml:"secret_access_key"`
	} `yaml:"sns"`
	Lambda struct {
		Region          string `yaml:"region"`
		Endpoint        string `yaml:"endpoint"`
		AccessKeyID     string `yaml:"access_key_id"`
		SecretAccessKey string `yaml:"secret_access_key"`
		InvocationType  string `yaml:"invocation_type"`
	} `yaml:"lambda"`
	S3 struct {
		Region          string `yaml:"region"`
		Endpoint        string `yaml:"endpoint"`
		AccessKeyID     string `yaml:"access_key_id"`
		SecretAccessKey string `yaml:"secret_access_key"`
		Bucket          string `yaml:"bucket"`
		Create          bool   `yaml:"create"`
		SSE             string `yaml:"sse"`
		SSEKMSKeyID     string `yaml:"sse_kms_key_id"`
	} `yaml:"s3"`
	Postgres rawPostgresConfig `yaml:"postgres"`
	Mongo    struct {
		URI         string `yaml:"uri"`
		Database    string `yaml:"database"`
		MaxPoolSize uint64 `yaml:"max_pool_size"`
		TLS         struct {
			CACert             string `yaml:"ca_cert"`
			ClientCert         string `yaml:"client_cert"`
			ClientKey          string `yaml:"client_key"`
			ServerName         string `yaml:"server_name"`
			InsecureSkipVerify bool   `yaml:"insecure_skip_verify"`
		} `yaml:"tls"`
	} `yaml:"mongodb"`
	Webhook struct {
		URL                 string `yaml:"url"`
		AllowPrivateTargets bool   `yaml:"allow_private_targets,omitempty"`
		Timeout             string `yaml:"timeout"`
		MaxQueue            int    `yaml:"max_queue"`
		MaxQueueBytes       int64  `yaml:"max_queue_bytes"`
		DrainTimeout        string `yaml:"drain_timeout"`
		DeliveryMode        string `yaml:"delivery_mode"`
		DeadLetter          struct {
			Destination string `yaml:"destination"`
			Target      string `yaml:"target"`
		} `yaml:"dead_letter"`
		Retry struct {
			MaxAttempts    int    `yaml:"max_attempts"`
			InitialBackoff string `yaml:"initial_backoff"`
			MaxBackoff     string `yaml:"max_backoff"`
		} `yaml:"retry"`
		Auth struct {
			Type          string `yaml:"type"`
			Header        string `yaml:"header"`
			Value         string `yaml:"value"`
			Secret        string `yaml:"secret"`
			Scheme        string `yaml:"scheme"`
			EventIDHeader string `yaml:"event_id_header"`
		} `yaml:"auth"`
		TLS struct {
			CACert             string `yaml:"ca_cert"`
			ClientCert         string `yaml:"client_cert"`
			ClientKey          string `yaml:"client_key"`
			ServerName         string `yaml:"server_name"`
			InsecureSkipVerify bool   `yaml:"insecure_skip_verify"`
		} `yaml:"tls"`
	} `yaml:"webhook"`
	Redis struct {
		Mode       string   `yaml:"mode"`
		URL        string   `yaml:"url"`
		Addrs      []string `yaml:"addrs"`
		MasterName string   `yaml:"master_name"`
		Username   string   `yaml:"username"`
		Password   string   `yaml:"password"`
		DB         int      `yaml:"db"`
		KeyPrefix  string   `yaml:"key_prefix"`
		TLS        struct {
			Enabled            bool   `yaml:"enabled"`
			CACert             string `yaml:"ca_cert"`
			ClientCert         string `yaml:"client_cert"`
			ClientKey          string `yaml:"client_key"`
			ServerName         string `yaml:"server_name"`
			InsecureSkipVerify bool   `yaml:"insecure_skip_verify"`
		} `yaml:"tls"`
	} `yaml:"redis"`
	Kafka struct {
		Brokers        []string `yaml:"brokers"`
		Acks           string   `yaml:"acks"`
		Compression    string   `yaml:"compression"`
		PublishTimeout string   `yaml:"publish_timeout"`
		SASL           struct {
			Mechanism string `yaml:"mechanism"`
			Username  string `yaml:"username"`
			Password  string `yaml:"password"`
		} `yaml:"sasl"`
		TLS struct {
			Enabled            bool   `yaml:"enabled"`
			CACert             string `yaml:"ca_cert"`
			ClientCert         string `yaml:"client_cert"`
			ClientKey          string `yaml:"client_key"`
			ServerName         string `yaml:"server_name"`
			InsecureSkipVerify bool   `yaml:"insecure_skip_verify"`
		} `yaml:"tls"`
		Retry struct {
			MaxAttempts    int    `yaml:"max_attempts"`
			InitialBackoff string `yaml:"initial_backoff"`
			MaxBackoff     string `yaml:"max_backoff"`
		} `yaml:"retry"`
	} `yaml:"kafka"`
	Temporal struct {
		HostPort       string `yaml:"host_port"`
		Namespace      string `yaml:"namespace"`
		APIKey         string `yaml:"api_key"`
		ConnectTimeout string `yaml:"connect_timeout"`
		StartTimeout   string `yaml:"start_timeout"`
		TLS            struct {
			Enabled            bool   `yaml:"enabled"`
			CACert             string `yaml:"ca_cert"`
			ClientCert         string `yaml:"client_cert"`
			ClientKey          string `yaml:"client_key"`
			ServerName         string `yaml:"server_name"`
			InsecureSkipVerify bool   `yaml:"insecure_skip_verify"`
		} `yaml:"tls"`
	} `yaml:"temporal"`
}

func convertDestination(fieldPrefix string, raw rawDestinationConfig) (DestinationConfig, []ValidationIssue) {
	var issues []ValidationIssue
	policy := raw.OnExistingConflict
	switch raw.Type {
	case "sqs", "sns", "s3":
		switch policy {
		case "", "error", "warn", "reconcile":
			// ok
		default:
			issues = append(issues, validationError(fieldPrefix+".on_existing_conflict",
				fmt.Sprintf("%q is not supported", policy),
				"use \"error\", \"warn\", or \"reconcile\""))
		}
		if policy == "" {
			policy = "reconcile"
		}
	default: // rabbitmq and anything else
		if policy != "" {
			issues = append(issues, validationError(fieldPrefix+".on_existing_conflict",
				"only valid for sqs/sns destinations",
				"remove on_existing_conflict or point the destination at sqs/sns"))
		}
	}
	publish, issue := parseDuration(fieldPrefix+".rabbitmq.publish_timeout", raw.RabbitMQ.PublishTimeout, 5*time.Second)
	if issue.Field != "" {
		issues = append(issues, issue)
	}
	queueDefaults, queueDefaultIssues := convertQueueOptions(fieldPrefix+".rabbitmq.queue_defaults", raw.RabbitMQ.QueueDefaults)
	issues = append(issues, queueDefaultIssues...)
	// 0 fallback preserves "unset" (mirrors the webhook retry block below);
	// queue-layer defaults (3 attempts / 100ms / 2s) apply when the field is 0.
	rabbitInitBackoff, issue := parseDuration(fieldPrefix+".rabbitmq.retry.initial_backoff", raw.RabbitMQ.Retry.InitialBackoff, 0)
	if issue.Field != "" {
		issues = append(issues, issue)
	}
	rabbitMaxBackoff, issue := parseDuration(fieldPrefix+".rabbitmq.retry.max_backoff", raw.RabbitMQ.Retry.MaxBackoff, 0)
	if issue.Field != "" {
		issues = append(issues, issue)
	}
	kafkaPublishTimeout, issue := parseDuration(fieldPrefix+".kafka.publish_timeout", raw.Kafka.PublishTimeout, 5*time.Second)
	if issue.Field != "" {
		issues = append(issues, issue)
	}
	kafkaInitialBackoff, issue := parsePositiveDuration(fieldPrefix+".kafka.retry.initial_backoff", raw.Kafka.Retry.InitialBackoff)
	if issue.Field != "" {
		issues = append(issues, issue)
	}
	kafkaMaxBackoff, issue := parsePositiveDuration(fieldPrefix+".kafka.retry.max_backoff", raw.Kafka.Retry.MaxBackoff)
	if issue.Field != "" {
		issues = append(issues, issue)
	}
	temporalConnectTimeout, issue := parsePositiveDefaultDuration(fieldPrefix+".temporal.connect_timeout", raw.Temporal.ConnectTimeout, 10*time.Second)
	if issue.Field != "" {
		issues = append(issues, issue)
	}
	temporalStartTimeout, issue := parsePositiveDefaultDuration(fieldPrefix+".temporal.start_timeout", raw.Temporal.StartTimeout, 5*time.Second)
	if issue.Field != "" {
		issues = append(issues, issue)
	}
	webhookTimeout, issue := parseDuration(fieldPrefix+".webhook.timeout", raw.Webhook.Timeout, 10*time.Second)
	if raw.Webhook.MaxQueueBytes < 0 {
		issues = append(issues, validationError(fieldPrefix+".webhook.max_queue_bytes", "must be non-negative", "omit or use 0 for the 64 MiB default; use a positive finite byte budget to admit larger async deliveries"))
	}
	if issue.Field != "" {
		issues = append(issues, issue)
	}
	webhookDrain, issue := parseDuration(fieldPrefix+".webhook.drain_timeout", raw.Webhook.DrainTimeout, 0)
	if issue.Field != "" {
		issues = append(issues, issue)
	}
	webhookInitBackoff, issue := parseDuration(fieldPrefix+".webhook.retry.initial_backoff", raw.Webhook.Retry.InitialBackoff, 0)
	if issue.Field != "" {
		issues = append(issues, issue)
	}
	webhookMaxBackoff, issue := parseDuration(fieldPrefix+".webhook.retry.max_backoff", raw.Webhook.Retry.MaxBackoff, 0)
	if issue.Field != "" {
		issues = append(issues, issue)
	}
	return DestinationConfig{
		MCPEvents:          raw.MCPEvents,
		Type:               raw.Type,
		Connection:         raw.Connection,
		OnExistingConflict: policy,
		RabbitMQ: RabbitMQConfig{
			URL: raw.RabbitMQ.URL, Exchange: raw.RabbitMQ.Exchange,
			ExchangeType: raw.RabbitMQ.ExchangeType, Durable: raw.RabbitMQ.Durable,
			PublishTimeout: publish, DeclareQueues: raw.RabbitMQ.DeclareQueues,
			PublisherChannels: raw.RabbitMQ.PublisherChannels,
			Retry: RetryPolicyConfig{
				MaxAttempts:    raw.RabbitMQ.Retry.MaxAttempts,
				InitialBackoff: rabbitInitBackoff,
				MaxBackoff:     rabbitMaxBackoff,
			},
			TLS: RabbitMQTLSConfig{
				CACert: raw.RabbitMQ.TLS.CACert, ClientCert: raw.RabbitMQ.TLS.ClientCert,
				ClientKey: raw.RabbitMQ.TLS.ClientKey, ServerName: raw.RabbitMQ.TLS.ServerName,
				InsecureSkipVerify: raw.RabbitMQ.TLS.InsecureSkipVerify,
			},
			DeadLetter: RabbitMQDeadLetterConfig{
				Enabled: raw.RabbitMQ.DeadLetter.Enabled, Exchange: raw.RabbitMQ.DeadLetter.Exchange,
				ExchangeType: raw.RabbitMQ.DeadLetter.ExchangeType, Queue: raw.RabbitMQ.DeadLetter.Queue,
				RoutingKey: raw.RabbitMQ.DeadLetter.RoutingKey, Durable: raw.RabbitMQ.DeadLetter.Durable,
				CreateQueue: raw.RabbitMQ.DeadLetter.CreateQueue,
			},
			QueueDefaults: queueDefaults,
		},
		SQS: SQSConfig{
			Region:          raw.SQS.Region,
			Endpoint:        raw.SQS.Endpoint,
			AccessKeyID:     raw.SQS.AccessKeyID,
			SecretAccessKey: raw.SQS.SecretAccessKey,
		},
		SNS: SNSConfig{
			Region:          raw.SNS.Region,
			Endpoint:        raw.SNS.Endpoint,
			AccessKeyID:     raw.SNS.AccessKeyID,
			SecretAccessKey: raw.SNS.SecretAccessKey,
		},
		Lambda: LambdaConfig{
			Region:          raw.Lambda.Region,
			Endpoint:        raw.Lambda.Endpoint,
			AccessKeyID:     raw.Lambda.AccessKeyID,
			SecretAccessKey: raw.Lambda.SecretAccessKey,
			InvocationType:  raw.Lambda.InvocationType,
		},
		S3: S3Config{
			Region:          raw.S3.Region,
			Endpoint:        raw.S3.Endpoint,
			AccessKeyID:     raw.S3.AccessKeyID,
			SecretAccessKey: raw.S3.SecretAccessKey,
			Bucket:          raw.S3.Bucket,
			Create:          raw.S3.Create,
			SSE:             raw.S3.SSE,
			SSEKMSKeyID:     raw.S3.SSEKMSKeyID,
		},
		Postgres: convertPostgresConfig(raw.Postgres),
		Mongo: MongoConfig{
			URI:         raw.Mongo.URI,
			Database:    raw.Mongo.Database,
			MaxPoolSize: raw.Mongo.MaxPoolSize,
			TLS: MongoTLSConfig{
				CACert:             raw.Mongo.TLS.CACert,
				ClientCert:         raw.Mongo.TLS.ClientCert,
				ClientKey:          raw.Mongo.TLS.ClientKey,
				ServerName:         raw.Mongo.TLS.ServerName,
				InsecureSkipVerify: raw.Mongo.TLS.InsecureSkipVerify,
			},
		},
		Redis: RedisConfig{
			Mode:       redisMode(raw.Type, raw.Redis.Mode),
			URL:        raw.Redis.URL,
			Addrs:      raw.Redis.Addrs,
			MasterName: raw.Redis.MasterName,
			Username:   raw.Redis.Username,
			Password:   raw.Redis.Password,
			DB:         raw.Redis.DB,
			KeyPrefix:  raw.Redis.KeyPrefix,
			TLS: RedisTLSConfig{
				Enabled:            raw.Redis.TLS.Enabled,
				CACert:             raw.Redis.TLS.CACert,
				ClientCert:         raw.Redis.TLS.ClientCert,
				ClientKey:          raw.Redis.TLS.ClientKey,
				ServerName:         raw.Redis.TLS.ServerName,
				InsecureSkipVerify: raw.Redis.TLS.InsecureSkipVerify,
			},
		},
		Kafka: KafkaConfig{
			Brokers:        raw.Kafka.Brokers,
			Acks:           kafkaAcks(raw.Kafka.Acks),
			Compression:    kafkaCompression(raw.Kafka.Compression),
			PublishTimeout: kafkaPublishTimeout,
			SASL: KafkaSASLConfig{
				Mechanism: raw.Kafka.SASL.Mechanism,
				Username:  raw.Kafka.SASL.Username,
				Password:  raw.Kafka.SASL.Password,
			},
			TLS: KafkaTLSConfig{
				Enabled:            raw.Kafka.TLS.Enabled,
				CACert:             raw.Kafka.TLS.CACert,
				ClientCert:         raw.Kafka.TLS.ClientCert,
				ClientKey:          raw.Kafka.TLS.ClientKey,
				ServerName:         raw.Kafka.TLS.ServerName,
				InsecureSkipVerify: raw.Kafka.TLS.InsecureSkipVerify,
			},
			Retry: KafkaRetryConfig{
				MaxAttempts:    raw.Kafka.Retry.MaxAttempts,
				InitialBackoff: kafkaInitialBackoff,
				MaxBackoff:     kafkaMaxBackoff,
			},
		},
		Webhook: WebhookConfig{
			URL:                 raw.Webhook.URL,
			AllowPrivateTargets: raw.Webhook.AllowPrivateTargets,
			Timeout:             webhookTimeout,
			MaxQueue:            raw.Webhook.MaxQueue,
			MaxQueueBytes:       raw.Webhook.MaxQueueBytes,
			DrainTimeout:        webhookDrain,
			DeliveryMode:        raw.Webhook.DeliveryMode,
			DeadLetter: WebhookDeadLetterConfig{
				Destination: raw.Webhook.DeadLetter.Destination,
				Target:      raw.Webhook.DeadLetter.Target,
			},
			Retry: RetryPolicyConfig{
				MaxAttempts:    raw.Webhook.Retry.MaxAttempts,
				InitialBackoff: webhookInitBackoff,
				MaxBackoff:     webhookMaxBackoff,
			},
			Auth: WebhookAuthConfig{
				Type: raw.Webhook.Auth.Type, Header: raw.Webhook.Auth.Header, Value: raw.Webhook.Auth.Value,
				Secret: raw.Webhook.Auth.Secret, Scheme: raw.Webhook.Auth.Scheme, EventIDHeader: raw.Webhook.Auth.EventIDHeader,
			},
			TLS: WebhookTLSConfig{
				CACert: raw.Webhook.TLS.CACert, ClientCert: raw.Webhook.TLS.ClientCert, ClientKey: raw.Webhook.TLS.ClientKey,
				ServerName: raw.Webhook.TLS.ServerName, InsecureSkipVerify: raw.Webhook.TLS.InsecureSkipVerify,
			},
		},
		Temporal: TemporalConfig{
			HostPort:       raw.Temporal.HostPort,
			Namespace:      raw.Temporal.Namespace,
			APIKey:         raw.Temporal.APIKey,
			ConnectTimeout: temporalConnectTimeout,
			StartTimeout:   temporalStartTimeout,
			TLS: TemporalTLSConfig{
				Enabled:            raw.Temporal.TLS.Enabled,
				CACert:             raw.Temporal.TLS.CACert,
				ClientCert:         raw.Temporal.TLS.ClientCert,
				ClientKey:          raw.Temporal.TLS.ClientKey,
				ServerName:         raw.Temporal.TLS.ServerName,
				InsecureSkipVerify: raw.Temporal.TLS.InsecureSkipVerify,
			},
		},
	}, issues
}

// postgresSchema returns the configured schema, defaulting to "public".
func postgresSchema(schema string) string {
	if schema == "" {
		return "public"
	}
	return schema
}

// kafkaAcks defaults the producer acknowledgement level to "all" (RequireAll)
// when omitted, so durability is the safe default.
func kafkaAcks(a string) string {
	if a == "" {
		return "all"
	}
	return a
}

// kafkaCompression defaults to "none" when omitted.
func kafkaCompression(c string) string {
	if c == "" {
		return "none"
	}
	return c
}

// redisMode returns the configured mode for redis_* destinations, defaulting to
// "single" when empty. For non-redis destination types it leaves the raw value
// untouched so a non-redis destination keeps a zero RedisConfig{}.
func redisMode(destType, mode string) string {
	switch destType {
	case "redis_stream", "redis_list", "redis_pubsub", "redis_kv":
		if mode == "" {
			return "single"
		}
	}
	return mode
}

func convertQueueOptions(fieldPrefix string, raw rawRabbitMQQueueOptions) (RabbitMQQueueOptions, []ValidationIssue) {
	var issues []ValidationIssue
	messageTTL, issue := parsePositiveDuration(fieldPrefix+".message_ttl", raw.MessageTTL)
	if issue.Field != "" {
		issues = append(issues, issue)
	}
	expires, issue := parsePositiveDuration(fieldPrefix+".expires", raw.Expires)
	if issue.Field != "" {
		issues = append(issues, issue)
	}
	return RabbitMQQueueOptions{
		MessageTTL:           messageTTL,
		Expires:              expires,
		MaxLength:            raw.MaxLength,
		Overflow:             raw.Overflow,
		DeadLetterRoutingKey: raw.DeadLetterRoutingKey,
	}, issues
}

func queueOptionValidationIssues(fieldPrefix string, opts RabbitMQQueueOptions) []ValidationIssue {
	var issues []ValidationIssue
	if opts.MaxLength < 0 {
		issues = append(issues, validationError(fieldPrefix+".max_length", "must be zero (omitted) or a positive integer", "set a queue cap such as 100000"))
	}
	switch opts.Overflow {
	case "", "reject-publish", "reject-publish-dlx":
	default:
		issues = append(issues, validationError(fieldPrefix+".overflow", fmt.Sprintf("%q is not supported", opts.Overflow), "use \"reject-publish\" or \"reject-publish-dlx\" for webhook queues"))
	}
	if opts.DeadLetterRoutingKey != "" && !safeName.MatchString(opts.DeadLetterRoutingKey) {
		issues = append(issues, validationError(fieldPrefix+".dead_letter_routing_key", "routing key contains unsupported characters", "use 1-128 letters, numbers, dot, underscore, or hyphen"))
	}
	return issues
}
