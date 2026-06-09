package queue

import (
	"context"
	"fmt"
	"sort"

	"github.com/jasongiroux/hookway/internal/config"
)

type RabbitMQConnector func(ctx context.Context, cfg RabbitMQConfig) (Publisher, error)

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
		connector = func(ctx context.Context, cfg RabbitMQConfig) (Publisher, error) {
			return NewRabbitMQPublisher(ctx, cfg)
		}
	}
	return &Factory{rabbitMQConnector: connector, metrics: opts.Metrics}
}

func (f *Factory) Create(ctx context.Context, cfg config.Config) (Publisher, error) {
	switch cfg.MessageQueue.Type {
	case "rabbitmq":
		rabbit := cfg.MessageQueue.RabbitMQ
		return f.rabbitMQConnector(ctx, RabbitMQConfig{
			URL: rabbit.URL, Exchange: rabbit.Exchange, ExchangeType: rabbit.ExchangeType,
			Durable: rabbit.Durable, PublishTimeout: rabbit.PublishTimeout,
			DeclareQueues: rabbit.DeclareQueues, PublisherChannels: rabbit.PublisherChannels,
			Queues:            collectQueues(cfg),
			QueueDeclarations: collectQueueDeclarations(cfg),
			Metrics:           f.metrics,
		})
	default:
		return nil, fmt.Errorf("unsupported message_queue.type %q", cfg.MessageQueue.Type)
	}
}

func collectQueueDeclarations(cfg config.Config) []QueueDeclaration {
	byQueue := map[string]QueueDeclaration{}
	for _, endpoint := range cfg.Endpoints {
		if !endpoint.Enabled {
			continue
		}
		declaration, ok := byQueue[endpoint.Queue]
		if !ok {
			declaration = QueueDeclaration{Name: endpoint.Queue}
		}
		declaration.CreateQueue = declaration.CreateQueue || endpoint.CreateQueue
		if cfg.MessageQueue.RabbitMQ.DeadLetter.Enabled {
			dlq := cfg.MessageQueue.RabbitMQ.DeadLetter
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

func collectQueues(cfg config.Config) []string {
	seen := map[string]struct{}{}
	var queues []string
	for _, endpoint := range cfg.Endpoints {
		if !endpoint.Enabled {
			continue
		}
		if _, ok := seen[endpoint.Queue]; !ok {
			seen[endpoint.Queue] = struct{}{}
			queues = append(queues, endpoint.Queue)
		}
	}
	return queues
}
