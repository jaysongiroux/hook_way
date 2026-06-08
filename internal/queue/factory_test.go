package queue

import (
	"context"
	"testing"
	"time"

	"github.com/jasongiroux/hookway/internal/config"
)

func TestFactoryCreatesRabbitMQPublisher(t *testing.T) {
	called := false
	factory := NewFactory(FactoryOptions{
		RabbitMQConnector: func(ctx context.Context, cfg RabbitMQConfig) (Publisher, error) {
			called = true
			if cfg.URL != "amqp://example" || cfg.Exchange != "hookway.webhooks" || len(cfg.Queues) != 1 || cfg.Queues[0] != "billing-events" {
				t.Fatalf("bad rabbitmq config: %#v", cfg)
			}
			if len(cfg.QueueDeclarations) != 1 || cfg.QueueDeclarations[0].Name != "billing-events" || !cfg.QueueDeclarations[0].CreateQueue {
				t.Fatalf("bad queue declarations: %#v", cfg.QueueDeclarations)
			}
			return &noopPublisher{}, nil
		},
	})

	pub, err := factory.Create(context.Background(), config.Config{
		MessageQueue: config.MessageQueueConfig{
			Type: "rabbitmq",
			RabbitMQ: config.RabbitMQConfig{
				URL: "amqp://example", Exchange: "hookway.webhooks", ExchangeType: "direct",
				Durable: true, PublishTimeout: time.Second, DeclareQueues: true,
			},
		},
		Endpoints: map[string]config.EndpointConfig{
			"paddle_billing": {Enabled: true, Queue: "billing-events", CreateQueue: true},
		},
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if pub == nil || !called {
		t.Fatal("expected rabbitmq connector to be called")
	}
}

func TestFactoryHonorsEndpointCreateQueueAndDeadLetterConfig(t *testing.T) {
	called := false
	factory := NewFactory(FactoryOptions{
		RabbitMQConnector: func(ctx context.Context, cfg RabbitMQConfig) (Publisher, error) {
			called = true
			if len(cfg.QueueDeclarations) != 2 {
				t.Fatalf("declarations = %#v", cfg.QueueDeclarations)
			}
			byName := map[string]QueueDeclaration{}
			for _, declaration := range cfg.QueueDeclarations {
				byName[declaration.Name] = declaration
			}
			if byName["created-events"].CreateQueue != true {
				t.Fatalf("created-events should create queue: %#v", byName["created-events"])
			}
			if byName["external-events"].CreateQueue != false {
				t.Fatalf("external-events should not create queue: %#v", byName["external-events"])
			}
			if byName["created-events"].DLQ == nil || byName["created-events"].DLQ.Queue != "hookway.webhooks.dlq" || !byName["created-events"].DLQ.CreateQueue {
				t.Fatalf("created-events missing dlq: %#v", byName["created-events"])
			}
			return &noopPublisher{}, nil
		},
	})

	_, err := factory.Create(context.Background(), config.Config{
		MessageQueue: config.MessageQueueConfig{
			Type: "rabbitmq",
			RabbitMQ: config.RabbitMQConfig{
				URL: "amqp://example", Exchange: "hookway.webhooks", ExchangeType: "direct",
				Durable: true, PublishTimeout: time.Second,
				DeadLetter: config.RabbitMQDeadLetterConfig{
					Enabled: true, Exchange: "hookway.webhooks.dlx", ExchangeType: "direct",
					Queue: "hookway.webhooks.dlq", RoutingKey: "webhooks.dead", Durable: true, CreateQueue: true,
				},
			},
		},
		Endpoints: map[string]config.EndpointConfig{
			"created":  {Enabled: true, Queue: "created-events", CreateQueue: true},
			"external": {Enabled: true, Queue: "external-events", CreateQueue: false},
		},
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if !called {
		t.Fatal("expected connector call")
	}
}

func TestFactoryRejectsUnsupportedQueueType(t *testing.T) {
	factory := NewFactory(FactoryOptions{})
	_, err := factory.Create(context.Background(), config.Config{
		MessageQueue: config.MessageQueueConfig{Type: "sqs"},
	})
	if err == nil {
		t.Fatal("expected unsupported type error")
	}
}

type noopPublisher struct{}

func (n *noopPublisher) Publish(context.Context, string, Message) error { return nil }
func (n *noopPublisher) Ready(context.Context) error                    { return nil }
func (n *noopPublisher) Close() error                                   { return nil }
