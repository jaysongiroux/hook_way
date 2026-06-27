package queue

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/jasongiroux/hookway/internal/config"
)

type RabbitMQConnector func(ctx context.Context, destinationID string, cfg RabbitMQConfig) (Publisher, error)
type SQSConnector func(ctx context.Context, destinationID string, cfg SQSConfig) (Publisher, error)
type SNSConnector func(ctx context.Context, destinationID string, cfg SNSConfig) (Publisher, error)

// RedisConnector creates a Publisher for the given Redis destination. It is
// called once per redis_* destination in Create. The default connector builds
// the connection via newRedisClient and dispatches on destType to the correct
// publisher constructor. Callers can inject a fake in tests.
type RedisConnector func(ctx context.Context, destinationID, destType string, cfg config.RedisConfig, targets []RedisTarget) (Publisher, error)

// RedisTarget describes a single Redis route target. The factory builds one
// RedisTarget per unique route.Target in collectRedisTargets and passes the
// slice to the RedisConnector.
type RedisTarget struct {
	Name          string
	MaxLen        int64
	ConsumerGroup string
	TTL           time.Duration
	KeyTemplate   string
	Create        bool
}

type FactoryOptions struct {
	RabbitMQConnector RabbitMQConnector
	SQSConnector      SQSConnector
	SNSConnector      SNSConnector
	RedisConnector    RedisConnector
	Metrics           RabbitMQMetrics
	Logger            *slog.Logger
}

type Factory struct {
	rabbitMQConnector RabbitMQConnector
	sqsConnector      SQSConnector
	snsConnector      SNSConnector
	redisConnector    RedisConnector
	metrics           RabbitMQMetrics
	logger            *slog.Logger
}

func NewFactory(opts FactoryOptions) *Factory {
	connector := opts.RabbitMQConnector
	if connector == nil {
		connector = func(ctx context.Context, destinationID string, cfg RabbitMQConfig) (Publisher, error) {
			return NewRabbitMQPublisher(ctx, cfg)
		}
	}
	sqsConnector := opts.SQSConnector
	if sqsConnector == nil {
		sqsConnector = func(ctx context.Context, destinationID string, cfg SQSConfig) (Publisher, error) {
			awsCfg, err := newAWSConfig(ctx, cfg.Region, cfg.Endpoint, cfg.AccessKeyID, cfg.SecretAccessKey)
			if err != nil {
				return nil, err
			}
			return NewSQSPublisher(ctx, sqs.NewFromConfig(awsCfg), cfg)
		}
	}
	snsConnector := opts.SNSConnector
	if snsConnector == nil {
		snsConnector = func(ctx context.Context, destinationID string, cfg SNSConfig) (Publisher, error) {
			awsCfg, err := newAWSConfig(ctx, cfg.Region, cfg.Endpoint, cfg.AccessKeyID, cfg.SecretAccessKey)
			if err != nil {
				return nil, err
			}
			return NewSNSPublisher(ctx, sns.NewFromConfig(awsCfg), cfg)
		}
	}
	redisConnector := opts.RedisConnector
	if redisConnector == nil {
		logger := opts.Logger
		redisConnector = func(ctx context.Context, destinationID, destType string, cfg config.RedisConfig, targets []RedisTarget) (Publisher, error) {
			ops, err := newRedisClient(cfg)
			if err != nil {
				return nil, fmt.Errorf("redis destination %q: connect: %w", destinationID, err)
			}
			switch destType {
			case "redis_stream":
				streamTargets := make([]RedisStreamTarget, 0, len(targets))
				for _, t := range targets {
					streamTargets = append(streamTargets, RedisStreamTarget{
						Name:          t.Name,
						MaxLen:        t.MaxLen,
						ConsumerGroup: t.ConsumerGroup,
						Create:        t.Create,
					})
				}
				return NewRedisStreamPublisher(ctx, ops, RedisStreamConfig{
					KeyPrefix:     cfg.KeyPrefix,
					DestinationID: destinationID,
					Logger:        logger,
					Targets:       streamTargets,
				})
			case "redis_list":
				listTargets := make([]RedisListTarget, 0, len(targets))
				for _, t := range targets {
					listTargets = append(listTargets, RedisListTarget{
						Name:   t.Name,
						MaxLen: t.MaxLen,
					})
				}
				return NewRedisListPublisher(ctx, ops, RedisListConfig{
					KeyPrefix:     cfg.KeyPrefix,
					DestinationID: destinationID,
					Logger:        logger,
					Targets:       listTargets,
				})
			case "redis_pubsub":
				pubsubTargets := make([]RedisPubSubTarget, 0, len(targets))
				for _, t := range targets {
					// Only Name is relevant for pub/sub — channels have no
					// max_len, TTL, consumer groups, or other options.
					pubsubTargets = append(pubsubTargets, RedisPubSubTarget{
						Name: t.Name,
					})
				}
				return NewRedisPubSubPublisher(ctx, ops, RedisPubSubConfig{
					KeyPrefix:     cfg.KeyPrefix,
					DestinationID: destinationID,
					Logger:        logger,
					Targets:       pubsubTargets,
				})
			case "redis_kv":
				kvTargets := make([]RedisKVTarget, 0, len(targets))
				for _, t := range targets {
					kvTargets = append(kvTargets, RedisKVTarget{
						Name:        t.Name,
						TTL:         t.TTL,
						KeyTemplate: t.KeyTemplate,
					})
				}
				return NewRedisKVPublisher(ctx, ops, RedisKVConfig{
					KeyPrefix:     cfg.KeyPrefix,
					DestinationID: destinationID,
					Logger:        logger,
					Targets:       kvTargets,
				})
			default:
				return nil, fmt.Errorf("unsupported redis destination type %q", destType)
			}
		}
	}
	return &Factory{
		rabbitMQConnector: connector,
		sqsConnector:      sqsConnector,
		snsConnector:      snsConnector,
		redisConnector:    redisConnector,
		metrics:           opts.Metrics,
		logger:            opts.Logger,
	}
}

func (f *Factory) Create(ctx context.Context, cfg config.Config) (Publisher, error) {
	publishers := map[string]Publisher{}
	for id, destination := range cfg.Destinations {
		if !destinationUsed(cfg, id) {
			continue
		}
		switch destination.Type {
		case "rabbitmq":
			rabbit := destination.RabbitMQ
			publisher, err := f.rabbitMQConnector(ctx, id, RabbitMQConfig{
				URL: rabbit.URL, Exchange: rabbit.Exchange, ExchangeType: rabbit.ExchangeType,
				Durable: rabbit.Durable, PublishTimeout: rabbit.PublishTimeout,
				DeclareQueues: rabbit.DeclareQueues, PublisherChannels: rabbit.PublisherChannels,
				TLS: RabbitMQTLSConfig{
					CACert: rabbit.TLS.CACert, ClientCert: rabbit.TLS.ClientCert, ClientKey: rabbit.TLS.ClientKey,
					ServerName: rabbit.TLS.ServerName, InsecureSkipVerify: rabbit.TLS.InsecureSkipVerify,
				},
				Queues:            collectQueues(cfg, id),
				QueueDeclarations: collectQueueDeclarations(cfg, id),
				Metrics:           f.metrics,
			})
			if err != nil {
				return nil, err
			}
			publishers[id] = publisher
		case "sqs":
			publisher, err := f.sqsConnector(ctx, id, SQSConfig{
				Region:          destination.SQS.Region,
				Endpoint:        destination.SQS.Endpoint,
				AccessKeyID:     destination.SQS.AccessKeyID,
				SecretAccessKey: destination.SQS.SecretAccessKey,
				DestinationID:   id,
				Policy:          toConflictPolicy(destination.OnExistingConflict),
				Logger:          f.logger,
				Targets:         collectSQSTargets(cfg, id),
			})
			if err != nil {
				return nil, err
			}
			publishers[id] = publisher
		case "sns":
			publisher, err := f.snsConnector(ctx, id, SNSConfig{
				Region:          destination.SNS.Region,
				Endpoint:        destination.SNS.Endpoint,
				AccessKeyID:     destination.SNS.AccessKeyID,
				SecretAccessKey: destination.SNS.SecretAccessKey,
				DestinationID:   id,
				Policy:          toConflictPolicy(destination.OnExistingConflict),
				Logger:          f.logger,
				Targets:         collectSNSTargets(cfg, id),
			})
			if err != nil {
				return nil, err
			}
			publishers[id] = publisher
		case "redis_stream", "redis_list", "redis_pubsub", "redis_kv":
			publisher, err := f.redisConnector(ctx, id, destination.Type, destination.Redis, collectRedisTargets(cfg, id))
			if err != nil {
				return nil, err
			}
			publishers[id] = publisher
		default:
			return nil, fmt.Errorf("unsupported destinations.%s.type %q", id, destination.Type)
		}
	}
	return NewRoutingPublisher(publishers), nil
}

func destinationUsed(cfg config.Config, destinationID string) bool {
	for _, endpoint := range cfg.Endpoints {
		for _, route := range endpoint.DestinationRoutes() {
			if endpoint.Enabled && route.Destination == destinationID {
				return true
			}
		}
	}
	return false
}

func collectQueueDeclarations(cfg config.Config, destinationID string) []QueueDeclaration {
	byQueue := map[string]QueueDeclaration{}
	for _, endpoint := range cfg.Endpoints {
		for _, route := range endpoint.DestinationRoutes() {
			if !endpoint.Enabled || route.Destination != destinationID {
				continue
			}
			declaration, ok := byQueue[route.Target]
			if !ok {
				declaration = QueueDeclaration{Name: route.Target}
			}
			declaration.CreateQueue = declaration.CreateQueue || route.Create
			rabbit := cfg.Destinations[destinationID].RabbitMQ
			declaration.Options = mergeQueueOptions(rabbit.QueueDefaults, route.QueueOptions)
			if rabbit.DeadLetter.Enabled {
				dlq := rabbit.DeadLetter
				declaration.DLQ = &DeadLetterDeclaration{
					Exchange: dlq.Exchange, ExchangeType: dlq.ExchangeType, Queue: dlq.Queue,
					RoutingKey: dlq.RoutingKey, CreateQueue: dlq.CreateQueue, Durable: &dlq.Durable,
				}
			}
			byQueue[route.Target] = declaration
		}
	}
	declarations := make([]QueueDeclaration, 0, len(byQueue))
	for _, declaration := range byQueue {
		declarations = append(declarations, declaration)
	}
	sort.Slice(declarations, func(i, j int) bool {
		return declarations[i].Name < declarations[j].Name
	})
	return declarations
}

func collectQueues(cfg config.Config, destinationID string) []string {
	seen := map[string]struct{}{}
	var queues []string
	for _, endpoint := range cfg.Endpoints {
		for _, route := range endpoint.DestinationRoutes() {
			if !endpoint.Enabled || route.Destination != destinationID {
				continue
			}
			if _, ok := seen[route.Target]; !ok {
				seen[route.Target] = struct{}{}
				queues = append(queues, route.Target)
			}
		}
	}
	return queues
}

func collectSQSTargets(cfg config.Config, destinationID string) []SQSTarget {
	seen := map[string]struct{}{}
	var targets []SQSTarget
	for _, endpoint := range cfg.Endpoints {
		if !endpoint.Enabled {
			continue
		}
		for _, route := range endpoint.DestinationRoutes() {
			if route.Destination != destinationID {
				continue
			}
			if _, ok := seen[route.Target]; ok {
				continue
			}
			seen[route.Target] = struct{}{}
			targets = append(targets, SQSTarget{
				Name:              route.Target,
				Create:            route.Create,
				VisibilityTimeout: route.SQSOptions.VisibilityTimeout,
				MessageRetention:  route.SQSOptions.MessageRetention,
				FIFO:              mapFIFO(route.FIFO),
			})
		}
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].Name < targets[j].Name })
	return targets
}

func collectSNSTargets(cfg config.Config, destinationID string) []SNSTarget {
	seen := map[string]struct{}{}
	var targets []SNSTarget
	for _, endpoint := range cfg.Endpoints {
		if !endpoint.Enabled {
			continue
		}
		for _, route := range endpoint.DestinationRoutes() {
			if route.Destination != destinationID {
				continue
			}
			if _, ok := seen[route.Target]; ok {
				continue
			}
			seen[route.Target] = struct{}{}
			targets = append(targets, SNSTarget{
				Name:    route.Target,
				Create:  route.Create,
				Subject: route.SNSOptions.Subject,
				FIFO:    mapFIFO(route.FIFO),
			})
		}
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].Name < targets[j].Name })
	return targets
}

func collectRedisTargets(cfg config.Config, destinationID string) []RedisTarget {
	seen := map[string]struct{}{}
	var targets []RedisTarget
	for _, endpoint := range cfg.Endpoints {
		if !endpoint.Enabled {
			continue
		}
		for _, route := range endpoint.DestinationRoutes() {
			if route.Destination != destinationID {
				continue
			}
			if _, ok := seen[route.Target]; ok {
				continue
			}
			seen[route.Target] = struct{}{}
			targets = append(targets, RedisTarget{
				Name:          route.Target,
				MaxLen:        route.RedisOptions.MaxLen,
				ConsumerGroup: route.RedisOptions.ConsumerGroup,
				TTL:           route.RedisOptions.TTL,
				KeyTemplate:   route.RedisOptions.KeyTemplate,
				Create:        route.Create,
			})
		}
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].Name < targets[j].Name })
	return targets
}

func mapFIFO(c *config.FIFOConfig) *FIFOSettings {
	if c == nil {
		return nil
	}
	return &FIFOSettings{
		GroupIDSource:             c.GroupID.Source,
		GroupIDValue:              c.GroupID.Value,
		GroupIDPath:               c.GroupID.Path,
		GroupIDFallback:           c.GroupID.Fallback,
		DedupUseIdempotencyKey:    c.Dedup.UseIdempotencyKey,
		DedupContentBasedFallback: c.Dedup.ContentBasedFallback,
	}
}

func mergeQueueOptions(defaults, override config.RabbitMQQueueOptions) QueueOptions {
	opts := QueueOptions{
		MessageTTL:           defaults.MessageTTL,
		Expires:              defaults.Expires,
		MaxLength:            defaults.MaxLength,
		Overflow:             defaults.Overflow,
		DeadLetterRoutingKey: defaults.DeadLetterRoutingKey,
	}
	if override.MessageTTL > 0 {
		opts.MessageTTL = override.MessageTTL
	}
	if override.Expires > 0 {
		opts.Expires = override.Expires
	}
	if override.MaxLength > 0 {
		opts.MaxLength = override.MaxLength
	}
	if override.Overflow != "" {
		opts.Overflow = override.Overflow
	}
	if override.DeadLetterRoutingKey != "" {
		opts.DeadLetterRoutingKey = override.DeadLetterRoutingKey
	}
	return opts
}
