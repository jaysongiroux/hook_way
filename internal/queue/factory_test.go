package queue

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jasongiroux/hookway/internal/config"
)

func TestFactoryCreatesRabbitMQPublishersForMessageQueues(t *testing.T) {
	called := map[string]RabbitMQConfig{}
	factory := NewFactory(FactoryOptions{
		RabbitMQConnector: func(ctx context.Context, destinationID string, cfg RabbitMQConfig) (Publisher, error) {
			called[destinationID] = cfg
			return &recordingPublisher{}, nil
		},
	})

	pub, err := factory.Create(context.Background(), config.Config{
		MessageQueues: map[string]config.MessageQueueConfig{
			"billing": {
				Type: "rabbitmq",
				RabbitMQ: config.RabbitMQConfig{
					URL: "amqp://billing", Exchange: "hookway.billing", ExchangeType: "direct",
					Durable: true, PublishTimeout: time.Second, DeclareQueues: true,
				},
			},
			"activity": {
				Type: "rabbitmq",
				RabbitMQ: config.RabbitMQConfig{
					URL: "amqp://activity", Exchange: "hookway.activity", ExchangeType: "direct",
					Durable: true, PublishTimeout: time.Second, DeclareQueues: true,
					QueueDefaults: config.RabbitMQQueueOptions{MessageTTL: 24 * time.Hour},
				},
			},
		},
		Endpoints: map[string]config.EndpointConfig{
			"paddle_billing": {
				Enabled: true, DestinationQueueID: "billing", Queue: "billing-events", CreateQueue: true,
				QueueOptions: config.RabbitMQQueueOptions{
					MessageTTL: 72 * time.Hour, MaxLength: 500000, Overflow: "reject-publish",
				},
			},
			"github_activity": {Enabled: true, DestinationQueueID: "activity", Queue: "activity-events", CreateQueue: true},
		},
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if pub == nil || len(called) != 2 {
		t.Fatalf("expected two rabbitmq connectors, got %d", len(called))
	}
	billing := called["billing"]
	if billing.URL != "amqp://billing" || billing.Exchange != "hookway.billing" || len(billing.Queues) != 1 || billing.Queues[0] != "billing-events" {
		t.Fatalf("bad billing rabbitmq config: %#v", billing)
	}
	if len(billing.QueueDeclarations) != 1 || billing.QueueDeclarations[0].Name != "billing-events" || !billing.QueueDeclarations[0].CreateQueue {
		t.Fatalf("bad billing queue declarations: %#v", billing.QueueDeclarations)
	}
	if billing.QueueDeclarations[0].Options.MessageTTL != 72*time.Hour {
		t.Fatalf("queue options not mapped: %#v", billing.QueueDeclarations[0].Options)
	}
	activity := called["activity"]
	if activity.URL != "amqp://activity" || activity.QueueDeclarations[0].Options.MessageTTL != 24*time.Hour {
		t.Fatalf("bad activity rabbitmq config: %#v", activity)
	}
}

func TestFactoryHonorsEndpointCreateQueueAndDeadLetterConfigPerDestination(t *testing.T) {
	called := false
	factory := NewFactory(FactoryOptions{
		RabbitMQConnector: func(ctx context.Context, destinationID string, cfg RabbitMQConfig) (Publisher, error) {
			called = true
			if destinationID != "billing" {
				t.Fatalf("destination = %q", destinationID)
			}
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
			if byName["created-events"].Options.MessageTTL != 24*time.Hour {
				t.Fatalf("created-events should inherit queue defaults: %#v", byName["created-events"])
			}
			if byName["external-events"].Options.MessageTTL != 6*time.Hour {
				t.Fatalf("external-events should override queue defaults: %#v", byName["external-events"])
			}
			if byName["external-events"].Options.DeadLetterRoutingKey != "external.dead" {
				t.Fatalf("external-events should override dead letter routing key: %#v", byName["external-events"])
			}
			return &recordingPublisher{}, nil
		},
	})

	_, err := factory.Create(context.Background(), config.Config{
		MessageQueues: map[string]config.MessageQueueConfig{
			"billing": {
				Type: "rabbitmq",
				RabbitMQ: config.RabbitMQConfig{
					URL: "amqp://example", Exchange: "hookway.webhooks", ExchangeType: "direct",
					Durable: true, PublishTimeout: time.Second,
					DeadLetter: config.RabbitMQDeadLetterConfig{
						Enabled: true, Exchange: "hookway.webhooks.dlx", ExchangeType: "direct",
						Queue: "hookway.webhooks.dlq", RoutingKey: "webhooks.dead", Durable: true, CreateQueue: true,
					},
					QueueDefaults: config.RabbitMQQueueOptions{MessageTTL: 24 * time.Hour},
				},
			},
		},
		Endpoints: map[string]config.EndpointConfig{
			"created": {Enabled: true, DestinationQueueID: "billing", Queue: "created-events", CreateQueue: true},
			"external": {
				Enabled: true, DestinationQueueID: "billing", Queue: "external-events", CreateQueue: false,
				QueueOptions: config.RabbitMQQueueOptions{MessageTTL: 6 * time.Hour, DeadLetterRoutingKey: "external.dead"},
			},
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
		MessageQueues: map[string]config.MessageQueueConfig{"billing": {Type: "sqs"}},
		Endpoints:     map[string]config.EndpointConfig{"paddle": {Enabled: true, DestinationQueueID: "billing", Queue: "events"}},
	})
	if err == nil {
		t.Fatal("expected unsupported type error")
	}
}

func TestRoutingPublisherRoutesReadinessAndCloseByDestination(t *testing.T) {
	billing := &recordingPublisher{}
	activity := &recordingPublisher{readyErr: errors.New("down")}
	pub := NewRoutingPublisher(map[string]Publisher{
		"billing":  billing,
		"activity": activity,
	})

	if err := pub.Publish(context.Background(), "billing", "billing-events", Message{ID: "evt_1"}); err != nil {
		t.Fatalf("Publish returned error: %v", err)
	}
	if billing.destinationID != "billing" || billing.queue != "billing-events" || activity.published != 0 {
		t.Fatalf("bad route: billing=%#v activity=%#v", billing, activity)
	}
	if err := pub.Ready(context.Background()); err == nil {
		t.Fatal("expected readiness failure when one destination is down")
	}
	if err := pub.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	if !billing.closed || !activity.closed {
		t.Fatalf("expected all publishers closed: billing=%v activity=%v", billing.closed, activity.closed)
	}
}

type recordingPublisher struct {
	err           error
	readyErr      error
	closed        bool
	published     int
	destinationID string
	queue         string
	msg           Message
}

func (r *recordingPublisher) Publish(ctx context.Context, destinationID, queue string, msg Message) error {
	r.published++
	r.destinationID = destinationID
	r.queue = queue
	r.msg = msg
	return r.err
}

func (r *recordingPublisher) Ready(context.Context) error { return r.readyErr }

func (r *recordingPublisher) Close() error {
	r.closed = true
	return nil
}
