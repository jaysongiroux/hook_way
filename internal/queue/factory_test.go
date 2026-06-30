package queue

import (
	"context"
	"errors"
	"fmt"
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
		Destinations: map[string]config.DestinationConfig{
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
				Enabled: true,
				Destinations: []config.EndpointDestinationConfig{{
					Destination: "billing", Target: "billing-events", Create: true,
					QueueOptions: config.RabbitMQQueueOptions{
						MessageTTL: 72 * time.Hour, MaxLength: 500000, Overflow: "reject-publish",
					},
				}},
			},
			"github_activity": {
				Enabled: true,
				Destinations: []config.EndpointDestinationConfig{{
					Destination: "activity", Target: "activity-events", Create: true,
				}},
			},
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

func TestFactoryCreatesPublishersAndDeclarationsForFanoutDestinations(t *testing.T) {
	called := map[string]RabbitMQConfig{}
	factory := NewFactory(FactoryOptions{
		RabbitMQConnector: func(ctx context.Context, destinationID string, cfg RabbitMQConfig) (Publisher, error) {
			called[destinationID] = cfg
			return &recordingPublisher{}, nil
		},
	})

	_, err := factory.Create(context.Background(), config.Config{
		Destinations: map[string]config.DestinationConfig{
			"primary": {Type: "rabbitmq", RabbitMQ: config.RabbitMQConfig{URL: "amqp://primary", Exchange: "hookway.primary", ExchangeType: "direct"}},
			"audit":   {Type: "rabbitmq", RabbitMQ: config.RabbitMQConfig{URL: "amqp://audit", Exchange: "hookway.audit", ExchangeType: "direct"}},
		},
		Endpoints: map[string]config.EndpointConfig{
			"example": {
				Enabled: true,
				Destinations: []config.EndpointDestinationConfig{
					{Destination: "primary", Target: "example-webhooks", Create: true, QueueOptions: config.RabbitMQQueueOptions{MessageTTL: time.Hour}},
					{Destination: "audit", Target: "example-audit-webhooks", Create: false, QueueOptions: config.RabbitMQQueueOptions{MessageTTL: 2 * time.Hour}},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	primary := called["primary"]
	if len(primary.Queues) != 1 || primary.Queues[0] != "example-webhooks" {
		t.Fatalf("primary queues = %#v", primary.Queues)
	}
	if len(primary.QueueDeclarations) != 1 || primary.QueueDeclarations[0].Name != "example-webhooks" {
		t.Fatalf("primary declarations = %#v", primary.QueueDeclarations)
	}
	if !primary.QueueDeclarations[0].CreateQueue || primary.QueueDeclarations[0].Options.MessageTTL != time.Hour {
		t.Fatalf("primary declaration options = %#v", primary.QueueDeclarations[0])
	}
	audit := called["audit"]
	if len(audit.Queues) != 1 || audit.Queues[0] != "example-audit-webhooks" {
		t.Fatalf("audit queues = %#v", audit.Queues)
	}
	if len(audit.QueueDeclarations) != 1 || audit.QueueDeclarations[0].Name != "example-audit-webhooks" {
		t.Fatalf("audit declarations = %#v", audit.QueueDeclarations)
	}
	if audit.QueueDeclarations[0].CreateQueue || audit.QueueDeclarations[0].Options.MessageTTL != 2*time.Hour {
		t.Fatalf("audit declaration options = %#v", audit.QueueDeclarations[0])
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
		Destinations: map[string]config.DestinationConfig{
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
			"created": {
				Enabled: true,
				Destinations: []config.EndpointDestinationConfig{{
					Destination: "billing", Target: "created-events", Create: true,
				}},
			},
			"external": {
				Enabled: true,
				Destinations: []config.EndpointDestinationConfig{{
					Destination: "billing", Target: "external-events", Create: false,
					QueueOptions: config.RabbitMQQueueOptions{MessageTTL: 6 * time.Hour, DeadLetterRoutingKey: "external.dead"},
				}},
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
		Destinations: map[string]config.DestinationConfig{"billing": {Type: "kafka"}},
		Endpoints: map[string]config.EndpointConfig{"paddle": {
			Enabled: true,
			Destinations: []config.EndpointDestinationConfig{{
				Destination: "billing", Target: "events",
			}},
		}},
	})
	if err == nil {
		t.Fatal("expected unsupported type error")
	}
}

func TestFactoryCreatesSQSPublisherWithTargets(t *testing.T) {
	var got SQSConfig
	factory := NewFactory(FactoryOptions{
		SQSConnector: func(ctx context.Context, destinationID string, cfg SQSConfig) (Publisher, error) {
			got = cfg
			return &recordingPublisher{}, nil
		},
	})
	_, err := factory.Create(context.Background(), config.Config{
		Destinations: map[string]config.DestinationConfig{
			"jobs": {Type: "sqs", SQS: config.SQSConfig{Region: "us-east-1", Endpoint: "http://localhost:4566"}},
		},
		Endpoints: map[string]config.EndpointConfig{
			"billing": {Enabled: true, Destinations: []config.EndpointDestinationConfig{{
				Destination: "jobs", Target: "billing-jobs.fifo", Create: true,
				SQSOptions:  config.SQSEndpointOptions{VisibilityTimeout: 30 * time.Second},
				FIFO:        &config.FIFOConfig{GroupID: config.FIFOGroupIDConfig{Source: "body", Path: "customer.id", Fallback: "endpoint"}, Dedup: config.FIFODedupConfig{UseIdempotencyKey: true}},
			}}},
		},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got.Region != "us-east-1" || len(got.Targets) != 1 {
		t.Fatalf("bad sqs config: %#v", got)
	}
	target := got.Targets[0]
	if target.Name != "billing-jobs.fifo" || !target.Create || target.VisibilityTimeout != 30*time.Second {
		t.Fatalf("bad target: %#v", target)
	}
	if target.FIFO == nil || target.FIFO.GroupIDSource != "body" || target.FIFO.GroupIDPath != "customer.id" || !target.FIFO.DedupUseIdempotencyKey {
		t.Fatalf("bad fifo settings: %#v", target.FIFO)
	}
}

func TestFactoryCreatesSNSPublisherWithTargets(t *testing.T) {
	var got SNSConfig
	factory := NewFactory(FactoryOptions{
		SNSConnector: func(ctx context.Context, destinationID string, cfg SNSConfig) (Publisher, error) {
			got = cfg
			return &recordingPublisher{}, nil
		},
	})
	_, err := factory.Create(context.Background(), config.Config{
		Destinations: map[string]config.DestinationConfig{
			"alerts": {Type: "sns", SNS: config.SNSConfig{Region: "us-east-1"}},
		},
		Endpoints: map[string]config.EndpointConfig{
			"billing": {Enabled: true, Destinations: []config.EndpointDestinationConfig{{
				Destination: "alerts", Target: "billing-alerts", Create: true,
				SNSOptions:  config.SNSEndpointOptions{Subject: "billing event"},
			}}},
		},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got.Region != "us-east-1" || len(got.Targets) != 1 || got.Targets[0].Subject != "billing event" {
		t.Fatalf("bad sns config: %#v", got)
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

func TestFactoryPassesConflictPolicyAndDestIDToSQS(t *testing.T) {
	var got SQSConfig
	factory := NewFactory(FactoryOptions{
		SQSConnector: func(ctx context.Context, destinationID string, cfg SQSConfig) (Publisher, error) {
			got = cfg
			return &recordingPublisher{}, nil
		},
	})
	_, err := factory.Create(context.Background(), config.Config{
		Destinations: map[string]config.DestinationConfig{
			"jobs": {Type: "sqs", OnExistingConflict: "warn", SQS: config.SQSConfig{Region: "us-east-1"}},
		},
		Endpoints: map[string]config.EndpointConfig{
			"e": {Enabled: true, Destinations: []config.EndpointDestinationConfig{{Destination: "jobs", Target: "q"}}},
		},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got.Policy != ConflictWarn {
		t.Fatalf("policy = %q, want warn", got.Policy)
	}
	if got.DestinationID != "jobs" {
		t.Fatalf("destID = %q, want jobs", got.DestinationID)
	}
}

func TestFactoryCreatesRedisStreamPublisherWithTargets(t *testing.T) {
	type capturedCall struct {
		destType  string
		keyPrefix string
		targets   []RedisTarget
	}
	var got capturedCall

	factory := NewFactory(FactoryOptions{
		RedisConnector: func(ctx context.Context, destinationID, destType string, cfg config.RedisConfig, targets []RedisTarget) (Publisher, error) {
			got = capturedCall{
				destType:  destType,
				keyPrefix: cfg.KeyPrefix,
				targets:   targets,
			}
			return &recordingPublisher{}, nil
		},
	})

	_, err := factory.Create(context.Background(), config.Config{
		Destinations: map[string]config.DestinationConfig{
			"events": {
				Type: "redis_stream",
				Redis: config.RedisConfig{
					Mode:      "single",
					URL:       "redis://localhost:6379",
					KeyPrefix: "hookway",
				},
			},
		},
		Endpoints: map[string]config.EndpointConfig{
			"billing": {
				Enabled: true,
				Destinations: []config.EndpointDestinationConfig{{
					Destination: "events",
					Target:      "webhooks.stream",
					Create:      true,
					RedisOptions: config.RedisEndpointOptions{
						MaxLen:        100000,
						ConsumerGroup: "workers",
					},
				}},
			},
		},
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if got.destType != "redis_stream" {
		t.Fatalf("destType = %q, want redis_stream", got.destType)
	}
	if got.keyPrefix != "hookway" {
		t.Fatalf("keyPrefix = %q, want hookway", got.keyPrefix)
	}
	if len(got.targets) != 1 {
		t.Fatalf("targets len = %d, want 1", len(got.targets))
	}
	target := got.targets[0]
	if target.Name != "webhooks.stream" {
		t.Fatalf("target.Name = %q, want webhooks.stream", target.Name)
	}
	if target.MaxLen != 100000 {
		t.Fatalf("target.MaxLen = %d, want 100000", target.MaxLen)
	}
	if target.ConsumerGroup != "workers" {
		t.Fatalf("target.ConsumerGroup = %q, want workers", target.ConsumerGroup)
	}
	if !target.Create {
		t.Fatalf("target.Create = false, want true")
	}
}

func TestFactoryDefaultRedisConnectorRedisListIsImplemented(t *testing.T) {
	// redis_list is now fully implemented; the factory must succeed (not return
	// "not yet implemented"). We use a custom RedisConnector that never dials a
	// real server so this test stays Docker-free.
	called := false
	factory := NewFactory(FactoryOptions{
		RedisConnector: func(_ context.Context, _, destType string, _ config.RedisConfig, targets []RedisTarget) (Publisher, error) {
			called = true
			if destType != "redis_list" {
				return nil, fmt.Errorf("unexpected destType %q", destType)
			}
			// Build a real RedisListPublisher against a fake ops — no network needed.
			listTargets := make([]RedisListTarget, 0, len(targets))
			for _, tgt := range targets {
				listTargets = append(listTargets, RedisListTarget{Name: tgt.Name, MaxLen: tgt.MaxLen})
			}
			return NewRedisListPublisher(context.Background(), &fakeRedisOps{}, RedisListConfig{
				DestinationID: "lists",
				Targets:       listTargets,
			})
		},
	})

	pub, err := factory.Create(context.Background(), config.Config{
		Destinations: map[string]config.DestinationConfig{
			"lists": {
				Type: "redis_list",
				Redis: config.RedisConfig{
					Mode: "single",
					URL:  "redis://localhost:6379",
				},
			},
		},
		Endpoints: map[string]config.EndpointConfig{
			"billing": {
				Enabled: true,
				Destinations: []config.EndpointDestinationConfig{{
					Destination: "lists",
					Target:      "billing-list",
				}},
			},
		},
	})
	if err != nil {
		t.Fatalf("expected redis_list to be implemented, got error: %v", err)
	}
	if !called {
		t.Fatal("RedisConnector was not called")
	}
	if pub == nil {
		t.Fatal("expected non-nil publisher")
	}
}

func TestFactoryCreatesS3Publisher(t *testing.T) {
	var gotCfg S3Config
	cfg := config.Config{
		Destinations: map[string]config.DestinationConfig{
			"archive": {
				Type: "s3",
				S3:   config.S3Config{Region: "us-east-1", Bucket: "events", Create: true},
			},
		},
		Endpoints: map[string]config.EndpointConfig{
			"orders": {
				Enabled: true,
				Path:    "/orders",
				Destinations: []config.EndpointDestinationConfig{
					{Destination: "archive", Target: "orders"},
					{Destination: "archive", Target: "orders"}, // duplicate -> deduped
					{Destination: "archive", Target: "refunds"},
				},
			},
		},
	}
	f := NewFactory(FactoryOptions{
		S3Connector: func(ctx context.Context, destinationID string, c S3Config) (Publisher, error) {
			gotCfg = c
			return &recordingPublisher{}, nil
		},
	})
	if _, err := f.Create(context.Background(), cfg); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if gotCfg.Bucket != "events" || gotCfg.Region != "us-east-1" || !gotCfg.Create {
		t.Fatalf("s3 cfg not passed through: %#v", gotCfg)
	}
	if len(gotCfg.Targets) != 2 {
		t.Fatalf("expected 2 deduped targets, got %#v", gotCfg.Targets)
	}
	// collectS3Targets sorts by name: orders, refunds.
	if gotCfg.Targets[0].Name != "orders" || gotCfg.Targets[1].Name != "refunds" {
		t.Fatalf("unexpected targets: %#v", gotCfg.Targets)
	}
}

func TestFactoryCreatesPostgresPublisher(t *testing.T) {
	var gotCfg config.PostgresConfig
	var gotTargets []PostgresTarget
	cfg := config.Config{
		Destinations: map[string]config.DestinationConfig{
			"warehouse": {Type: "postgres", Postgres: config.PostgresConfig{
				URL: "postgres://localhost/db", Schema: "public", Table: "webhook_events",
			}},
		},
		Endpoints: map[string]config.EndpointConfig{
			"orders": {Enabled: true, Path: "/orders", Destinations: []config.EndpointDestinationConfig{
				{Destination: "warehouse", Target: "orders"},
				{Destination: "warehouse", Target: "orders"}, // dup -> deduped
				{Destination: "warehouse", Target: "refunds"},
			}},
		},
	}
	f := NewFactory(FactoryOptions{
		PostgresConnector: func(ctx context.Context, id string, c config.PostgresConfig, targets []PostgresTarget) (Publisher, error) {
			gotCfg = c
			gotTargets = targets
			return &recordingPublisher{}, nil
		},
	})
	if _, err := f.Create(context.Background(), cfg); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if gotCfg.Table != "webhook_events" || gotCfg.URL != "postgres://localhost/db" {
		t.Fatalf("postgres cfg not passed through: %#v", gotCfg)
	}
	if gotCfg.Schema != "public" {
		t.Fatalf("schema not passed through: %q", gotCfg.Schema)
	}
	if len(gotTargets) != 2 {
		t.Fatalf("expected 2 deduped targets, got %#v", gotTargets)
	}
	// collectPostgresTargets sorts ascending: "orders" < "refunds".
	if gotTargets[0].Name != "orders" || gotTargets[1].Name != "refunds" {
		t.Fatalf("unexpected targets/order: %#v", gotTargets)
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
