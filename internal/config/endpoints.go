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
}

type EndpointDestinationConfig struct {
	Destination  string               `yaml:"destination"`
	Target       string               `yaml:"target"`
	Create       bool                 `yaml:"create"`
	QueueOptions RabbitMQQueueOptions `yaml:"queue_options"`
	SQSOptions   SQSEndpointOptions   `yaml:"sqs_options"`
	SNSOptions   SNSEndpointOptions   `yaml:"sns_options"`
	FIFO         *FIFOConfig          `yaml:"fifo"`
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
	FIFO *rawFIFOConfig `yaml:"fifo"`
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
			FIFO: convertFIFO(destination.FIFO),
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
