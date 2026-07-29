package queue

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/jasongiroux/hookway/internal/config"
)

type RabbitMQConnector func(ctx context.Context, destinationID string, cfg RabbitMQConfig) (Publisher, error)
type SQSConnector func(ctx context.Context, destinationID string, cfg SQSConfig) (Publisher, error)
type SNSConnector func(ctx context.Context, destinationID string, cfg SNSConfig) (Publisher, error)
type S3Connector func(ctx context.Context, destinationID string, cfg S3Config) (Publisher, error)

// WebhookConnector is an optional test seam: when set on FactoryOptions, Create
// calls it instead of NewWebhookPublisher for each webhook destination.
type WebhookConnector func(ctx context.Context, destinationID string, cfg config.WebhookConfig, targets []WebhookTarget) (Publisher, error)

// RedisConnector creates a Publisher for the given Redis destination. It is
// called once per redis_* destination in Create. The default connector builds
// the connection via newRedisClient and dispatches on destType to the correct
// publisher constructor. Callers can inject a fake in tests.
type RedisConnector func(ctx context.Context, destinationID, destType string, cfg config.RedisConfig, targets []RedisTarget) (Publisher, error)

// PostgresConnector creates a Publisher for the given Postgres destination. It
// is called once per postgres destination in Create. The default connector
// builds the connection via newPostgresClient then calls NewPostgresPublisher.
// Callers can inject a fake in tests.
type PostgresConnector func(ctx context.Context, destinationID string, cfg config.PostgresConfig, targets []PostgresTarget) (Publisher, error)

// MongoConnector creates a Publisher for the given MongoDB destination. It is
// called once per mongodb destination in Create. The default connector builds
// the connection via newMongoClient then calls NewMongoPublisher. Callers can
// inject a fake in tests.
type MongoConnector func(ctx context.Context, destinationID string, cfg config.MongoConfig, targets []MongoTarget) (Publisher, error)

// KafkaConnector creates a Publisher for the given Kafka destination. The
// default connector builds a kafka-go Writer/Client via newKafkaOps then calls
// NewKafkaPublisher. Callers can inject a fake in tests.
type KafkaConnector func(ctx context.Context, destinationID string, cfg config.KafkaConfig, targets []KafkaTarget) (Publisher, error)

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
	S3Connector       S3Connector
	PostgresConnector PostgresConnector
	MongoConnector    MongoConnector
	KafkaConnector    KafkaConnector
	WebhookConnector  WebhookConnector
	Metrics           RabbitMQMetrics
	Logger            *slog.Logger
}

type Factory struct {
	rabbitMQConnector RabbitMQConnector
	sqsConnector      SQSConnector
	snsConnector      SNSConnector
	redisConnector    RedisConnector
	s3Connector       S3Connector
	postgresConnector PostgresConnector
	mongoConnector    MongoConnector
	kafkaConnector    KafkaConnector
	webhookConnector  WebhookConnector
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
	s3Connector := opts.S3Connector
	if s3Connector == nil {
		s3Connector = func(ctx context.Context, destinationID string, cfg S3Config) (Publisher, error) {
			awsCfg, err := newAWSConfig(ctx, cfg.Region, cfg.Endpoint, cfg.AccessKeyID, cfg.SecretAccessKey)
			if err != nil {
				return nil, err
			}
			client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
				// LocalStack/dev custom endpoints require path-style addressing.
				if cfg.Endpoint != "" {
					o.UsePathStyle = true
				}
			})
			return NewS3Publisher(ctx, client, cfg)
		}
	}
	redisConnector := opts.RedisConnector
	if redisConnector == nil {
		logger := opts.Logger
		var redisPubSubMetrics RedisPubSubMetrics
		if m, ok := opts.Metrics.(RedisPubSubMetrics); ok {
			redisPubSubMetrics = m
		}
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
					Metrics:       redisPubSubMetrics,
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
	postgresConnector := opts.PostgresConnector
	if postgresConnector == nil {
		logger := opts.Logger
		postgresConnector = func(ctx context.Context, destinationID string, cfg config.PostgresConfig, targets []PostgresTarget) (Publisher, error) {
			ops, err := newPostgresClient(ctx, cfg)
			if err != nil {
				return nil, fmt.Errorf("postgres destination %q: connect: %w", destinationID, err)
			}
			return NewPostgresPublisher(ctx, ops, PostgresConfig{
				Schema:        cfg.Schema,
				Table:         cfg.Table,
				DestinationID: destinationID,
				Logger:        logger,
				Targets:       targets,
			})
		}
	}
	mongoConnector := opts.MongoConnector
	if mongoConnector == nil {
		logger := opts.Logger
		mongoConnector = func(ctx context.Context, destinationID string, cfg config.MongoConfig, targets []MongoTarget) (Publisher, error) {
			ops, err := newMongoClient(ctx, cfg)
			if err != nil {
				return nil, fmt.Errorf("mongodb destination %q: connect: %w", destinationID, err)
			}
			return NewMongoPublisher(ctx, ops, MongoConfig{
				DestinationID: destinationID,
				Logger:        logger,
				Targets:       targets,
			})
		}
	}
	kafkaConnector := opts.KafkaConnector
	if kafkaConnector == nil {
		logger := opts.Logger
		kafkaConnector = func(ctx context.Context, destinationID string, cfg config.KafkaConfig, targets []KafkaTarget) (Publisher, error) {
			ops, err := newKafkaOps(ctx, destinationID, cfg, targets, logger)
			if err != nil {
				return nil, fmt.Errorf("kafka destination %q: %w", destinationID, err)
			}
			return NewKafkaPublisher(ops, KafkaPublisherConfig{
				DestinationID:  destinationID,
				PublishTimeout: cfg.PublishTimeout,
				Logger:         logger,
				Targets:        targets,
			}), nil
		}
	}
	return &Factory{
		rabbitMQConnector: connector,
		sqsConnector:      sqsConnector,
		snsConnector:      snsConnector,
		redisConnector:    redisConnector,
		s3Connector:       s3Connector,
		postgresConnector: postgresConnector,
		mongoConnector:    mongoConnector,
		kafkaConnector:    kafkaConnector,
		webhookConnector:  opts.WebhookConnector,
		metrics:           opts.Metrics,
		logger:            opts.Logger,
	}
}

func (f *Factory) Create(ctx context.Context, cfg config.Config) (Publisher, error) {
	publishers := map[string]Publisher{}
	var webhookPubs []*WebhookPublisher

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
				Retry: RetryPolicy{
					MaxAttempts:    rabbit.Retry.MaxAttempts,
					InitialBackoff: rabbit.Retry.InitialBackoff,
					MaxBackoff:     rabbit.Retry.MaxBackoff,
				},
				TLS: RabbitMQTLSConfig{
					CACert: rabbit.TLS.CACert, ClientCert: rabbit.TLS.ClientCert, ClientKey: rabbit.TLS.ClientKey,
					ServerName: rabbit.TLS.ServerName, InsecureSkipVerify: rabbit.TLS.InsecureSkipVerify,
				},
				Queues:            collectQueues(cfg, id),
				QueueDeclarations: collectQueueDeclarations(cfg, id),
				Metrics:           f.metrics,
				DestinationID:     id,
				Logger:            f.logger,
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
		case "s3":
			publisher, err := f.s3Connector(ctx, id, S3Config{
				Region:          destination.S3.Region,
				Endpoint:        destination.S3.Endpoint,
				AccessKeyID:     destination.S3.AccessKeyID,
				SecretAccessKey: destination.S3.SecretAccessKey,
				Bucket:          destination.S3.Bucket,
				Create:          destination.S3.Create,
				SSE:             destination.S3.SSE,
				SSEKMSKeyID:     destination.S3.SSEKMSKeyID,
				DestinationID:   id,
				Logger:          f.logger,
				Targets:         collectS3Targets(cfg, id),
			})
			if err != nil {
				return nil, err
			}
			publishers[id] = publisher
		case "postgres":
			publisher, err := f.postgresConnector(ctx, id, destination.Postgres, collectPostgresTargets(cfg, id))
			if err != nil {
				return nil, err
			}
			publishers[id] = publisher
		case "mongodb":
			publisher, err := f.mongoConnector(ctx, id, destination.Mongo, collectMongoTargets(cfg, id))
			if err != nil {
				return nil, err
			}
			publishers[id] = publisher
		case "kafka":
			publisher, err := f.kafkaConnector(ctx, id, destination.Kafka, collectKafkaTargets(cfg, id))
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
		case "webhook":
			var publisher Publisher
			var err error
			if f.webhookConnector != nil {
				publisher, err = f.webhookConnector(ctx, id, destination.Webhook, collectWebhookTargets(cfg, id))
			} else {
				var wm WebhookMetrics
				if m, ok := f.metrics.(WebhookMetrics); ok {
					wm = m
				}
				publisher, err = NewWebhookPublisher(destination.Webhook, id, collectWebhookTargets(cfg, id), WebhookPublisherOptions{
					Workers:      cfg.Server.FanoutConcurrency,
					DefaultDrain: cfg.Server.ShutdownTimeout,
					Metrics:      wm,
					Logger:       f.logger,
				})
			}
			if err != nil {
				return nil, err
			}
			// Only real webhook publishers carry a dead-letter engine to wire in
			// phase two; a test-seam connector may return any Publisher.
			if wp, ok := publisher.(*WebhookPublisher); ok {
				webhookPubs = append(webhookPubs, wp)
			}
			publishers[id] = publisher
		default:
			return nil, fmt.Errorf("unsupported destinations.%s.type %q", id, destination.Type)
		}
	}

	// Phase two: wire dead-letter routing. A failed async webhook delivery calls
	// this resolver, which routes the copied message to the (already-built)
	// dead-letter destination's publisher. Validation guarantees the target is a
	// non-webhook destination, so no delivery cycle is possible.
	dlqPublish := func(ctx context.Context, destID, target string, msg Message) error {
		p, ok := publishers[destID]
		if !ok {
			return fmt.Errorf("dead-letter destination %q is not configured", destID)
		}
		return p.Publish(ctx, destID, target, msg)
	}
	for _, wp := range webhookPubs {
		wp.setDeadLetterPublisher(dlqPublish)
	}

	return NewRoutingPublisher(publishers), nil
}

// destinationUsed reports whether a destination must be built: either it is the
// target of an enabled endpoint route, or it is referenced as the dead-letter
// destination of some endpoint-used webhook destination. The dead-letter level
// is one deep only — DLQ targets are never webhook destinations.
func destinationUsed(cfg config.Config, destinationID string) bool {
	for _, endpoint := range cfg.Endpoints {
		for _, route := range endpoint.DestinationRoutes() {
			if endpoint.Enabled && route.Destination == destinationID {
				return true
			}
		}
	}
	for id, dest := range cfg.Destinations {
		if dest.Type != "webhook" || dest.Webhook.DeadLetter.Destination != destinationID {
			continue
		}
		if endpointRoutesToDestination(cfg, id) {
			return true
		}
	}
	return false
}

// endpointRoutesToDestination reports whether any enabled endpoint routes to the
// given destination id (the direct-use test, without the dead-letter expansion).
func endpointRoutesToDestination(cfg config.Config, destinationID string) bool {
	for _, endpoint := range cfg.Endpoints {
		if !endpoint.Enabled {
			continue
		}
		for _, route := range endpoint.DestinationRoutes() {
			if route.Destination == destinationID {
				return true
			}
		}
	}
	return false
}

// deadLetterTargets returns the dead-letter target names that route to
// destinationID from endpoint-used webhook destinations. These are merged into
// the backend target collectors so a DLQ target with no direct endpoint route is
// still configured (and declared, for rabbitmq declare_queues). Deduped.
func deadLetterTargets(cfg config.Config, destinationID string) []string {
	seen := map[string]struct{}{}
	var targets []string
	for id, dest := range cfg.Destinations {
		if dest.Type != "webhook" {
			continue
		}
		dl := dest.Webhook.DeadLetter
		if dl.Destination != destinationID || dl.Target == "" {
			continue
		}
		if !endpointRoutesToDestination(cfg, id) {
			continue
		}
		if _, ok := seen[dl.Target]; ok {
			continue
		}
		seen[dl.Target] = struct{}{}
		targets = append(targets, dl.Target)
	}
	return targets
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
	// Merge dead-letter targets routed here from webhook destinations. A DLQ
	// queue has no endpoint route, so it is declared based on the rabbitmq
	// destination's declare_queues flag (mirroring the auto-declare default).
	rabbit := cfg.Destinations[destinationID].RabbitMQ
	for _, t := range deadLetterTargets(cfg, destinationID) {
		if _, ok := byQueue[t]; ok {
			continue
		}
		declaration := QueueDeclaration{Name: t, CreateQueue: rabbit.DeclareQueues}
		declaration.Options = mergeQueueOptions(rabbit.QueueDefaults, config.RabbitMQQueueOptions{})
		if rabbit.DeadLetter.Enabled {
			dlq := rabbit.DeadLetter
			declaration.DLQ = &DeadLetterDeclaration{
				Exchange: dlq.Exchange, ExchangeType: dlq.ExchangeType, Queue: dlq.Queue,
				RoutingKey: dlq.RoutingKey, CreateQueue: dlq.CreateQueue, Durable: &dlq.Durable,
			}
		}
		byQueue[t] = declaration
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
	for _, t := range deadLetterTargets(cfg, destinationID) {
		if _, ok := seen[t]; !ok {
			seen[t] = struct{}{}
			queues = append(queues, t)
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
	for _, t := range deadLetterTargets(cfg, destinationID) {
		if _, ok := seen[t]; !ok {
			seen[t] = struct{}{}
			targets = append(targets, SQSTarget{Name: t})
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
	for _, t := range deadLetterTargets(cfg, destinationID) {
		if _, ok := seen[t]; !ok {
			seen[t] = struct{}{}
			targets = append(targets, SNSTarget{Name: t})
		}
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].Name < targets[j].Name })
	return targets
}

func collectS3Targets(cfg config.Config, destinationID string) []S3Target {
	seen := map[string]struct{}{}
	var targets []S3Target
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
			targets = append(targets, S3Target{Name: route.Target})
		}
	}
	for _, t := range deadLetterTargets(cfg, destinationID) {
		if _, ok := seen[t]; !ok {
			seen[t] = struct{}{}
			targets = append(targets, S3Target{Name: t})
		}
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].Name < targets[j].Name })
	return targets
}

func collectPostgresTargets(cfg config.Config, destinationID string) []PostgresTarget {
	seen := map[string]struct{}{}
	var targets []PostgresTarget
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
			targets = append(targets, PostgresTarget{Name: route.Target})
		}
	}
	for _, t := range deadLetterTargets(cfg, destinationID) {
		if _, ok := seen[t]; !ok {
			seen[t] = struct{}{}
			targets = append(targets, PostgresTarget{Name: t})
		}
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].Name < targets[j].Name })
	return targets
}

func collectMongoTargets(cfg config.Config, destinationID string) []MongoTarget {
	seen := map[string]struct{}{}
	var targets []MongoTarget
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
			targets = append(targets, MongoTarget{Name: route.Target})
		}
	}
	for _, t := range deadLetterTargets(cfg, destinationID) {
		if _, ok := seen[t]; !ok {
			seen[t] = struct{}{}
			targets = append(targets, MongoTarget{Name: t})
		}
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].Name < targets[j].Name })
	return targets
}

func collectKafkaTargets(cfg config.Config, destinationID string) []KafkaTarget {
	seen := map[string]struct{}{}
	var targets []KafkaTarget
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
			var partitionKey *FIFOSettings
			if pk := route.KafkaOptions.PartitionKey; pk.Source != "" {
				partitionKey = &FIFOSettings{
					GroupIDSource:   pk.Source,
					GroupIDValue:    pk.Value,
					GroupIDPath:     pk.Path,
					GroupIDFallback: pk.Fallback,
				}
			}
			targets = append(targets, KafkaTarget{
				Topic:             route.Target,
				Create:            route.Create,
				PartitionKey:      partitionKey,
				Partitions:        route.KafkaOptions.Partitions,
				ReplicationFactor: route.KafkaOptions.ReplicationFactor,
			})
		}
	}
	for _, t := range deadLetterTargets(cfg, destinationID) {
		if _, ok := seen[t]; !ok {
			seen[t] = struct{}{}
			targets = append(targets, KafkaTarget{Topic: t})
		}
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].Topic < targets[j].Topic })
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
	for _, t := range deadLetterTargets(cfg, destinationID) {
		if _, ok := seen[t]; !ok {
			seen[t] = struct{}{}
			targets = append(targets, RedisTarget{Name: t})
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

func collectWebhookTargets(cfg config.Config, destinationID string) []WebhookTarget {
	seen := map[string]struct{}{}
	var targets []WebhookTarget
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
			targets = append(targets, WebhookTarget{Name: route.Target})
		}
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].Name < targets[j].Name })
	return targets
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
