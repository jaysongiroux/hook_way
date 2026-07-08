package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/jasongiroux/hookway/internal/auth"
)

const (
	DeliveryFailurePolicyStopOnFailure = "stop_on_failure"
	DeliveryFailurePolicySkipFailed    = "skip_failed"
)

// authScheme defaults the per-endpoint signature scheme to the timestamped
// HMAC-SHA256 colon scheme when omitted.
func authScheme(scheme string) string {
	if scheme == "" {
		return auth.SchemeHMACSHA256TSColon
	}
	return scheme
}

func deliveryFailurePolicy(policy string) string {
	if policy == "" {
		return DeliveryFailurePolicyStopOnFailure
	}
	return policy
}

type SQSEndpointOptions struct {
	VisibilityTimeout time.Duration
	MessageRetention  time.Duration
}

type SNSEndpointOptions struct {
	Subject string
}

// RedisEndpointOptions are per-route options for redis_* destinations.
// Not all fields apply to all redis types — validation enforces the coupling.
type RedisEndpointOptions struct {
	MaxLen        int64
	ConsumerGroup string
	TTL           time.Duration
	KeyTemplate   string
}

// KafkaEndpointOptions are per-route options for kafka destinations.
type KafkaEndpointOptions struct {
	PartitionKey      KafkaPartitionKeyConfig
	Partitions        int
	ReplicationFactor int
}

// KafkaPartitionKeyConfig derives the Kafka message key (which selects the
// partition). Mirrors the FIFO group_id shape: source is static | endpoint |
// header | body, with an optional fallback ("endpoint" or a literal).
type KafkaPartitionKeyConfig struct {
	Source   string
	Value    string
	Path     string
	Fallback string
}

type FIFOConfig struct {
	GroupID FIFOGroupIDConfig
	Dedup   FIFODedupConfig
}

type FIFOGroupIDConfig struct {
	Source   string // static | endpoint | header | body
	Value    string // used when Source == "static"
	Path     string // used when Source == "header" | "body"
	Fallback string // literal value, or "endpoint"
}

type FIFODedupConfig struct {
	UseIdempotencyKey    bool
	ContentBasedFallback bool
}

type EndpointConfig struct {
	Enabled               bool                        `yaml:"enabled"`
	Path                  string                      `yaml:"path"`
	DeliveryFailurePolicy string                      `yaml:"delivery_failure_policy"`
	Destinations          []EndpointDestinationConfig `yaml:"destinations"`
	MaxBodyBytes          int64                       `yaml:"max_body_bytes"`
	RateLimit             EndpointRateLimitConfig     `yaml:"rate_limit"`
	ReplayProtection      ReplayProtectionConfig      `yaml:"replay_protection"`
	IPAllowlist           []string                    `yaml:"ip_allowlist"`
	Auth                  AuthConfig                  `yaml:"auth"`
	Filters               []FilterConditionConfig     `yaml:"filters"`
	FilterResponse        FilterResponseConfig        `yaml:"filter_response"`
}

type EndpointDestinationConfig struct {
	Destination  string                  `yaml:"destination"`
	Target       string                  `yaml:"target"`
	Create       bool                    `yaml:"create"`
	QueueOptions RabbitMQQueueOptions    `yaml:"queue_options"`
	SQSOptions   SQSEndpointOptions      `yaml:"sqs_options"`
	SNSOptions   SNSEndpointOptions      `yaml:"sns_options"`
	RedisOptions RedisEndpointOptions    `yaml:"redis_options"`
	KafkaOptions KafkaEndpointOptions    `yaml:"kafka_options"`
	FIFO         *FIFOConfig             `yaml:"fifo"`
	Filters      []FilterConditionConfig `yaml:"filters"`
}

// FilterConditionConfig is one condition in an endpoint or route filter chain.
// All conditions in a chain must pass (AND) for the payload to proceed. Value
// carries the raw YAML scalar (string/int/float/bool/nil); the filter package
// decides the comparison type from the operator and value.
type FilterConditionConfig struct {
	Source   string `yaml:"source"`
	Path     string `yaml:"path"`
	Operator string `yaml:"operator"`
	Value    any    `yaml:"value"`
}

// FilterResponseConfig controls how an endpoint responds when its filter chain
// rejects a payload. Status 0 (the default) means a silent 202 ack with no
// destinations; a non-2xx status (e.g. 422) is returned to the sender instead.
type FilterResponseConfig struct {
	Status int `yaml:"status"`
}

// defaultFilterResponseStatus is returned when no filter_response.status is set.
const defaultFilterResponseStatus = 202

// FilterResponseStatus returns the configured non-match status, or 202 default.
func (e EndpointConfig) FilterResponseStatus() int {
	if e.FilterResponse.Status == 0 {
		return defaultFilterResponseStatus
	}
	return e.FilterResponse.Status
}

type EndpointRateLimitConfig struct {
	Enabled           *bool  `yaml:"enabled"`
	RequestsPerMinute int    `yaml:"requests_per_minute"`
	Burst             int    `yaml:"burst"`
	KeyBy             string `yaml:"key_by"`
}

type ReplayProtectionConfig struct {
	Enabled    bool
	TTL        time.Duration
	MaxEntries int
}

type AuthConfig struct {
	Type               string
	Scheme             string
	Secret             string
	SignatureHeader    string
	TimestampTolerance time.Duration
	IdempotencyHeader  string
	RemoveBearer       bool
	ExpectedIssuer     string
	ExpectedAudience   string
	BodyHashClaim      string
}

type rawFIFOConfig struct {
	GroupID struct {
		Source   string `yaml:"source"`
		Value    string `yaml:"value"`
		Path     string `yaml:"path"`
		Fallback string `yaml:"fallback"`
	} `yaml:"group_id"`
	Dedup struct {
		UseIdempotencyKey    bool `yaml:"use_idempotency_key"`
		ContentBasedFallback bool `yaml:"content_based_fallback"`
	} `yaml:"dedup"`
}

type rawEndpointDestinationConfig struct {
	Destination  string                  `yaml:"destination"`
	Target       string                  `yaml:"target"`
	Create       bool                    `yaml:"create"`
	QueueOptions rawRabbitMQQueueOptions `yaml:"queue_options"`
	SQSOptions   struct {
		VisibilityTimeout string `yaml:"visibility_timeout"`
		MessageRetention  string `yaml:"message_retention"`
	} `yaml:"sqs_options"`
	SNSOptions struct {
		Subject string `yaml:"subject"`
	} `yaml:"sns_options"`
	RedisOptions struct {
		MaxLen        int64  `yaml:"max_len"`
		ConsumerGroup string `yaml:"consumer_group"`
		TTL           string `yaml:"ttl"`
		KeyTemplate   string `yaml:"key_template"`
	} `yaml:"redis_options"`
	KafkaOptions struct {
		PartitionKey struct {
			Source   string `yaml:"source"`
			Value    string `yaml:"value"`
			Path     string `yaml:"path"`
			Fallback string `yaml:"fallback"`
		} `yaml:"partition_key"`
		Partitions        int `yaml:"partitions"`
		ReplicationFactor int `yaml:"replication_factor"`
	} `yaml:"kafka_options"`
	FIFO    *rawFIFOConfig             `yaml:"fifo"`
	Filters []rawFilterConditionConfig `yaml:"filters"`
}

type rawFilterConditionConfig struct {
	Source   string `yaml:"source"`
	Path     string `yaml:"path"`
	Operator string `yaml:"operator"`
	Value    any    `yaml:"value"`
}

func convertFilters(raw []rawFilterConditionConfig) []FilterConditionConfig {
	if len(raw) == 0 {
		return nil
	}
	filters := make([]FilterConditionConfig, 0, len(raw))
	for _, f := range raw {
		filters = append(filters, FilterConditionConfig{
			Source: f.Source, Path: f.Path, Operator: f.Operator, Value: f.Value,
		})
	}
	return filters
}

func convertEndpointDestinations(fieldPrefix string, raw []rawEndpointDestinationConfig) ([]EndpointDestinationConfig, []ValidationIssue) {
	var issues []ValidationIssue
	destinations := make([]EndpointDestinationConfig, 0, len(raw))
	for i, destination := range raw {
		queueOptions, queueOptionIssues := convertQueueOptions(fmt.Sprintf("%s[%d].queue_options", fieldPrefix, i), destination.QueueOptions)
		issues = append(issues, queueOptionIssues...)

		visibilityTimeout, issue := parsePositiveDuration(fmt.Sprintf("%s[%d].sqs_options.visibility_timeout", fieldPrefix, i), destination.SQSOptions.VisibilityTimeout)
		if issue.Field != "" {
			issues = append(issues, issue)
		}
		messageRetention, issue := parsePositiveDuration(fmt.Sprintf("%s[%d].sqs_options.message_retention", fieldPrefix, i), destination.SQSOptions.MessageRetention)
		if issue.Field != "" {
			issues = append(issues, issue)
		}

		redisTTL, issue := parsePositiveDuration(fmt.Sprintf("%s[%d].redis_options.ttl", fieldPrefix, i), destination.RedisOptions.TTL)
		if issue.Field != "" {
			issues = append(issues, issue)
		}

		destinations = append(destinations, EndpointDestinationConfig{
			Destination:  destination.Destination,
			Target:       destination.Target,
			Create:       destination.Create,
			QueueOptions: queueOptions,
			SQSOptions: SQSEndpointOptions{
				VisibilityTimeout: visibilityTimeout,
				MessageRetention:  messageRetention,
			},
			SNSOptions: SNSEndpointOptions{
				Subject: destination.SNSOptions.Subject,
			},
			RedisOptions: RedisEndpointOptions{
				MaxLen:        destination.RedisOptions.MaxLen,
				ConsumerGroup: destination.RedisOptions.ConsumerGroup,
				TTL:           redisTTL,
				KeyTemplate:   destination.RedisOptions.KeyTemplate,
			},
			KafkaOptions: KafkaEndpointOptions{
				PartitionKey: KafkaPartitionKeyConfig{
					Source:   destination.KafkaOptions.PartitionKey.Source,
					Value:    destination.KafkaOptions.PartitionKey.Value,
					Path:     destination.KafkaOptions.PartitionKey.Path,
					Fallback: destination.KafkaOptions.PartitionKey.Fallback,
				},
				Partitions:        destination.KafkaOptions.Partitions,
				ReplicationFactor: destination.KafkaOptions.ReplicationFactor,
			},
			FIFO:    convertFIFO(destination.FIFO),
			Filters: convertFilters(destination.Filters),
		})
	}
	return destinations, issues
}

func convertFIFO(raw *rawFIFOConfig) *FIFOConfig {
	if raw == nil {
		return nil
	}
	return &FIFOConfig{
		GroupID: FIFOGroupIDConfig{
			Source: raw.GroupID.Source, Value: raw.GroupID.Value,
			Path: raw.GroupID.Path, Fallback: raw.GroupID.Fallback,
		},
		Dedup: FIFODedupConfig{
			UseIdempotencyKey:    raw.Dedup.UseIdempotencyKey,
			ContentBasedFallback: raw.Dedup.ContentBasedFallback,
		},
	}
}

func (c Config) EndpointForPath(path string) (string, EndpointConfig, bool) {
	for name, endpoint := range c.Endpoints {
		if endpoint.Enabled && endpoint.Path == path {
			return name, endpoint, true
		}
	}
	return "", EndpointConfig{}, false
}

func (e EndpointConfig) DestinationRoutes() []EndpointDestinationConfig {
	return append([]EndpointDestinationConfig(nil), e.Destinations...)
}

func (e EndpointConfig) EffectiveDeliveryFailurePolicy() string {
	if e.DeliveryFailurePolicy == "" {
		return DeliveryFailurePolicyStopOnFailure
	}
	return e.DeliveryFailurePolicy
}

func (e EndpointConfig) DestinationIDs() []string {
	ids := make([]string, 0, len(e.Destinations))
	for _, destination := range e.Destinations {
		ids = append(ids, destination.Destination)
	}
	return ids
}

func (e EndpointConfig) DestinationLabel() string {
	return strings.Join(e.DestinationIDs(), ",")
}

func (e EndpointConfig) TargetLabel() string {
	targets := make([]string, 0, len(e.Destinations))
	for _, destination := range e.Destinations {
		targets = append(targets, destination.Target)
	}
	return strings.Join(targets, ",")
}

func (s ServerConfig) FanoutConcurrencyLimit() int {
	if s.FanoutConcurrency <= 0 {
		return 8
	}
	return s.FanoutConcurrency
}
