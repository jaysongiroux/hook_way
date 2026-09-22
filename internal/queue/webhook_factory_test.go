package queue

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jaysongiroux/hook_way/internal/config"
)

// dlqFactoryConfig wires a webhook destination "notify" (endpoint-routed) whose
// terminal failures dead-letter to a rabbitmq destination "dlq"/"dead" that has
// NO endpoint route of its own.
func dlqFactoryConfig(webhookURL string, declareQueues bool) config.Config {
	return config.Config{
		Server: config.ServerConfig{FanoutConcurrency: 2, ShutdownTimeout: time.Second},
		Destinations: map[string]config.DestinationConfig{
			"notify": {Type: "webhook", Webhook: config.WebhookConfig{
				URL: webhookURL, Timeout: time.Second, DeliveryMode: "async",
				Auth:       config.WebhookAuthConfig{Type: "none"},
				DeadLetter: config.WebhookDeadLetterConfig{Destination: "dlq", Target: "dead"},
			}},
			"dlq": {Type: "rabbitmq", RabbitMQ: config.RabbitMQConfig{
				URL: "amqp://x", Exchange: "hookway.dlq", ExchangeType: "direct", DeclareQueues: declareQueues,
			}},
		},
		Endpoints: map[string]config.EndpointConfig{
			"hook": {Enabled: true, Path: "/hook", Destinations: []config.EndpointDestinationConfig{
				{Destination: "notify", Target: "orders"},
			}},
		},
	}
}

func TestDestinationUsedIncludesDeadLetterReferenced(t *testing.T) {
	cfg := dlqFactoryConfig("http://x", true)
	if !destinationUsed(cfg, "dlq") {
		t.Fatal("dlq destination referenced only via dead_letter must be considered used")
	}
	// A dead-letter destination whose referencing webhook has NO enabled endpoint
	// route must NOT be considered used.
	cfg2 := dlqFactoryConfig("http://x", true)
	cfg2.Endpoints["hook"] = config.EndpointConfig{Enabled: false}
	if destinationUsed(cfg2, "dlq") {
		t.Fatal("dlq for an unrouted webhook must not be used")
	}
}

func TestDeadLetterTargetsMergedIntoCollectors(t *testing.T) {
	cfg := dlqFactoryConfig("http://x", true)
	queues := collectQueues(cfg, "dlq")
	found := false
	for _, q := range queues {
		if q == "dead" {
			found = true
		}
	}
	if !found {
		t.Fatalf("collectQueues(dlq) = %v, want it to include the dead-letter target \"dead\"", queues)
	}

	decls := collectQueueDeclarations(cfg, "dlq")
	var got *QueueDeclaration
	for i := range decls {
		if decls[i].Name == "dead" {
			got = &decls[i]
		}
	}
	if got == nil {
		t.Fatalf("collectQueueDeclarations(dlq) missing \"dead\": %+v", decls)
	}
	if !got.CreateQueue {
		t.Fatal("dead-letter queue must be declared (CreateQueue) when declare_queues:true")
	}
}

func TestFactoryBuildsDeadLetterDestinationAndWiresResolver(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404) // force terminal failure => dead-letter
	}))
	defer srv.Close()

	var gotRabbit RabbitMQConfig
	rec := &recordingPublisher{}
	f := NewFactory(FactoryOptions{
		RabbitMQConnector: func(ctx context.Context, id string, cfg RabbitMQConfig) (Publisher, error) {
			gotRabbit = cfg
			return rec, nil
		},
		Logger: testLogger(),
	})
	pub, err := f.Create(context.Background(), dlqFactoryConfig(srv.URL, true))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	rp, ok := pub.(*RoutingPublisher)
	if !ok {
		t.Fatalf("expected *RoutingPublisher, got %T", pub)
	}
	if _, ok := rp.publishers["dlq"]; !ok {
		t.Fatal("dlq destination was not built despite being a dead-letter target")
	}
	// The DLQ target must have been collected into the rabbitmq config.
	foundQueue := false
	for _, q := range gotRabbit.Queues {
		if q == "dead" {
			foundQueue = true
		}
	}
	if !foundQueue {
		t.Fatalf("rabbitmq dlq Queues = %v, want to include \"dead\"", gotRabbit.Queues)
	}

	// Drive a failed delivery through and confirm the resolver routed a
	// dead-lettered message to the rabbitmq publisher.
	if err := rp.Publish(context.Background(), "notify", "orders", Message{ID: "evt", ReceivedAt: time.Now()}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	_ = rp.Close() // drains webhook engine, running the delivery + dead-letter
	if rec.published == 0 {
		t.Fatal("dead-letter resolver did not route the failed delivery to the dlq publisher")
	}
	if rec.msg.DeadLetter == nil || rec.msg.DeadLetter.Destination != "notify" {
		t.Fatalf("dlq publisher received message without dead-letter meta: %#v", rec.msg.DeadLetter)
	}
	if rec.queue != "dead" {
		t.Fatalf("dead-letter routed to target %q, want \"dead\"", rec.queue)
	}
}

// TestRoutingPublisherClosesWebhooksBeforeDeadLetterTargets guards against a
// Close-ordering bug: RoutingPublisher.Close() must close webhook publishers
// (which can dead-letter into another destination while draining) BEFORE it
// closes the publishers those webhooks might dead-letter into. dlqFactoryConfig
// wires "notify" (webhook) -> dead-letters to "dlq" (rabbitmq), and "dlq" sorts
// alphabetically BEFORE "notify" — exactly the ordering that would trip a naive
// single-pass sorted Close(). The fake dlq publisher rejects Publish once its
// own Close() has run, mimicking a real backend (rabbitmq/SQS client) that
// refuses posts after shutdown, so this test fails if the dlq publisher is
// closed before the webhook engine finishes draining.
func TestRoutingPublisherClosesWebhooksBeforeDeadLetterTargets(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404) // force terminal failure => dead-letter
	}))
	defer srv.Close()

	rec := &recordingPublisher{rejectAfterClose: true}
	f := NewFactory(FactoryOptions{
		RabbitMQConnector: func(ctx context.Context, id string, cfg RabbitMQConfig) (Publisher, error) {
			return rec, nil
		},
		Logger: testLogger(),
	})
	pub, err := f.Create(context.Background(), dlqFactoryConfig(srv.URL, true))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	rp, ok := pub.(*RoutingPublisher)
	if !ok {
		t.Fatalf("expected *RoutingPublisher, got %T", pub)
	}

	if err := rp.Publish(context.Background(), "notify", "orders", Message{ID: "evt", ReceivedAt: time.Now()}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	_ = rp.Close() // must drain+dead-letter "notify" before closing "dlq"

	if rec.published == 0 {
		t.Fatal("dead-letter was dropped: the dlq publisher was already closed when the webhook engine's drain dispatched the dead-letter (Close ordering bug — webhook publishers must close before their dead-letter targets)")
	}
	if !rec.closed {
		t.Fatal("dlq publisher was never closed by RoutingPublisher.Close()")
	}
}

func TestFactoryPerDestinationMaxQueueSizing(t *testing.T) {
	cfg := config.Config{
		Server: config.ServerConfig{FanoutConcurrency: 2, ShutdownTimeout: time.Second},
		Destinations: map[string]config.DestinationConfig{
			"small": {Type: "webhook", Webhook: config.WebhookConfig{URL: "http://x", MaxQueue: 5, Auth: config.WebhookAuthConfig{Type: "none"}}},
			"big":   {Type: "webhook", Webhook: config.WebhookConfig{URL: "http://y", MaxQueue: 500, Auth: config.WebhookAuthConfig{Type: "none"}}},
		},
		Endpoints: map[string]config.EndpointConfig{
			"a": {Enabled: true, Path: "/a", Destinations: []config.EndpointDestinationConfig{{Destination: "small", Target: "t"}}},
			"b": {Enabled: true, Path: "/b", Destinations: []config.EndpointDestinationConfig{{Destination: "big", Target: "t"}}},
		},
	}
	pub, err := NewFactory(FactoryOptions{Logger: testLogger()}).Create(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	rp := pub.(*RoutingPublisher)
	defer func() { _ = rp.Close() }()
	small := rp.publishers["small"].(*WebhookPublisher)
	big := rp.publishers["big"].(*WebhookPublisher)
	if small.engine.capacity() != 5 {
		t.Fatalf("small engine capacity = %d, want 5", small.engine.capacity())
	}
	if big.engine.capacity() != 500 {
		t.Fatalf("big engine capacity = %d, want 500", big.engine.capacity())
	}
}

func TestFactoryWebhookConnectorSeam(t *testing.T) {
	var called int
	rec := &recordingPublisher{}
	f := NewFactory(FactoryOptions{
		WebhookConnector: func(ctx context.Context, id string, cfg config.WebhookConfig, targets []WebhookTarget) (Publisher, error) {
			called++
			return rec, nil
		},
		Logger: testLogger(),
	})
	cfg := config.Config{
		Server: config.ServerConfig{FanoutConcurrency: 1, ShutdownTimeout: time.Second},
		Destinations: map[string]config.DestinationConfig{
			"notify": {Type: "webhook", Webhook: config.WebhookConfig{URL: "http://x", Auth: config.WebhookAuthConfig{Type: "none"}}},
		},
		Endpoints: map[string]config.EndpointConfig{
			"e": {Enabled: true, Path: "/e", Destinations: []config.EndpointDestinationConfig{{Destination: "notify", Target: "orders"}}},
		},
	}
	pub, err := f.Create(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if called != 1 {
		t.Fatalf("webhook connector seam called %d times, want 1", called)
	}
	rp := pub.(*RoutingPublisher)
	if rp.publishers["notify"] != rec {
		t.Fatal("factory did not use the connector's returned publisher")
	}
}
