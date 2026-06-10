package queue

import (
	"context"
	"fmt"
	"sort"

	"github.com/jasongiroux/hookway/internal/config"
)

type RabbitMQConnector func(ctx context.Context, destinationID string, cfg RabbitMQConfig) (Publisher, error)

type FactoryOptions struct {
	RabbitMQConnector RabbitMQConnector
	Metrics           RabbitMQMetrics
}

type Factory struct {
	rabbitMQConnector RabbitMQConnector
	metrics           RabbitMQMetrics
}

func NewFactory(opts FactoryOptions) *Factory {
	connector := opts.RabbitMQConnector
	if connector == nil {
		connector = func(ctx context.Context, destinationID string, cfg RabbitMQConfig) (Publisher, error) {
			return NewRabbitMQPublisher(ctx, cfg)
		}
	}
	return &Factory{rabbitMQConnector: connector, metrics: opts.Metrics}
}

func (f *Factory) Create(ctx context.Context, cfg config.Config) (Publisher, error) {
	publishers := map[string]Publisher{}
	for id, messageQueue := range cfg.MessageQueues {
		if !destinationUsed(cfg, id) {
			continue
		}
		switch messageQueue.Type {
		case "rabbitmq":
			rabbit := messageQueue.RabbitMQ
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
		default:
			return nil, fmt.Errorf("unsupported message_queues.%s.type %q", id, messageQueue.Type)
		}
	}
	return NewRoutingPublisher(publishers), nil
}

func destinationUsed(cfg config.Config, destinationID string) bool {
	for _, endpoint := range cfg.Endpoints {
		if endpoint.Enabled && endpoint.DestinationQueueID == destinationID {
			return true
		}
	}
	return false
}

func collectQueueDeclarations(cfg config.Config, destinationID string) []QueueDeclaration {
	byQueue := map[string]QueueDeclaration{}
	for _, endpoint := range cfg.Endpoints {
		if !endpoint.Enabled || endpoint.DestinationQueueID != destinationID {
			continue
		}
		declaration, ok := byQueue[endpoint.Queue]
		if !ok {
			declaration = QueueDeclaration{Name: endpoint.Queue}
		}
		declaration.CreateQueue = declaration.CreateQueue || endpoint.CreateQueue
		rabbit := cfg.MessageQueues[destinationID].RabbitMQ
		declaration.Options = mergeQueueOptions(rabbit.QueueDefaults, endpoint.QueueOptions)
		if rabbit.DeadLetter.Enabled {
			dlq := rabbit.DeadLetter
			declaration.DLQ = &DeadLetterDeclaration{
				Exchange: dlq.Exchange, ExchangeType: dlq.ExchangeType, Queue: dlq.Queue,
				RoutingKey: dlq.RoutingKey, CreateQueue: dlq.CreateQueue, Durable: &dlq.Durable,
			}
		}
		byQueue[endpoint.Queue] = declaration
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
		if !endpoint.Enabled || endpoint.DestinationQueueID != destinationID {
			continue
		}
		if _, ok := seen[endpoint.Queue]; !ok {
			seen[endpoint.Queue] = struct{}{}
			queues = append(queues, endpoint.Queue)
		}
	}
	return queues
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
