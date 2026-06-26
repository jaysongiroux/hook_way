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

type DestinationConfig struct {
	Type               string
	OnExistingConflict string
	RabbitMQ           RabbitMQConfig
	SQS                SQSConfig
	SNS                SNSConfig
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
}

func convertDestination(fieldPrefix string, raw rawDestinationConfig) (DestinationConfig, []ValidationIssue) {
	var issues []ValidationIssue
	policy := raw.OnExistingConflict
	switch raw.Type {
	case "sqs", "sns":
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
	}, issues
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
