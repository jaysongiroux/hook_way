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

type DestinationConfig struct {
	Type               string
	OnExistingConflict string
	RabbitMQ           RabbitMQConfig
	SQS                SQSConfig
	SNS                SNSConfig
	S3                 S3Config
	Postgres           PostgresConfig
	Mongo              MongoConfig
	Redis              RedisConfig
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

type rawRabbitMQQueueOptions struct {
	MessageTTL           string `yaml:"message_ttl"`
	Expires              string `yaml:"expires"`
	MaxLength            int64  `yaml:"max_length"`
	Overflow             string `yaml:"overflow"`
	DeadLetterRoutingKey string `yaml:"dead_letter_routing_key"`
}

type rawDestinationConfig struct {
	Type               string `yaml:"type"`
	OnExistingConflict string `yaml:"on_existing_conflict"`
	RabbitMQ struct {
		URL               string                  `yaml:"url"`
		Exchange          string                  `yaml:"exchange"`
		ExchangeType      string                  `yaml:"exchange_type"`
		Durable           bool                    `yaml:"durable"`
		PublishTimeout    string                  `yaml:"publish_timeout"`
		DeclareQueues     bool                    `yaml:"declare_queues"`
		PublisherChannels int                     `yaml:"publisher_channels"`
		QueueDefaults     rawRabbitMQQueueOptions `yaml:"queue_defaults"`
		TLS               struct {
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
	Postgres struct {
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
	} `yaml:"postgres"`
	Mongo struct {
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
	return DestinationConfig{
		Type:               raw.Type,
		OnExistingConflict: policy,
		RabbitMQ: RabbitMQConfig{
			URL: raw.RabbitMQ.URL, Exchange: raw.RabbitMQ.Exchange,
			ExchangeType: raw.RabbitMQ.ExchangeType, Durable: raw.RabbitMQ.Durable,
			PublishTimeout: publish, DeclareQueues: raw.RabbitMQ.DeclareQueues,
			PublisherChannels: raw.RabbitMQ.PublisherChannels,
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
		Postgres: PostgresConfig{
			URL:      raw.Postgres.URL,
			Schema:   postgresSchema(raw.Postgres.Schema),
			Table:    raw.Postgres.Table,
			MaxConns: raw.Postgres.MaxConns,
			TLS: PostgresTLSConfig{
				CACert:             raw.Postgres.TLS.CACert,
				ClientCert:         raw.Postgres.TLS.ClientCert,
				ClientKey:          raw.Postgres.TLS.ClientKey,
				ServerName:         raw.Postgres.TLS.ServerName,
				InsecureSkipVerify: raw.Postgres.TLS.InsecureSkipVerify,
			},
		},
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
	}, issues
}

// postgresSchema returns the configured schema, defaulting to "public".
func postgresSchema(schema string) string {
	if schema == "" {
		return "public"
	}
	return schema
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
