package queue

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/jaysongiroux/hook_way/internal/config"
	"github.com/jaysongiroux/hook_way/internal/testenv"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/testcontainers/testcontainers-go"
	tcrabbitmq "github.com/testcontainers/testcontainers-go/modules/rabbitmq"
)

func TestRabbitMQPublisherPublishesConsumableEnvelope(t *testing.T) {
	ctx, container := startRabbitMQ(t)
	url, err := container.AmqpURL(ctx)
	if err != nil {
		t.Fatal(err)
	}

	pub, err := NewRabbitMQPublisher(ctx, RabbitMQConfig{
		URL: url, Exchange: "hookway.test", ExchangeType: "direct", Durable: true,
		PublishTimeout: 5 * time.Second, DeclareQueues: true, Queues: []string{"billing-events"},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pub.Close() })

	msg := Message{ID: "evt_1", Endpoint: "paddle_billing", Body: map[string]any{"ok": true}, ReceivedAt: time.Now().UTC()}
	if err := pub.Publish(ctx, "billing", "billing-events", msg); err != nil {
		t.Fatal(err)
	}

	conn, err := amqp.Dial(url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	ch, err := conn.Channel()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ch.Close() })
	delivery, ok, err := ch.Get("billing-events", true)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected message")
	}
	var got Message
	if err := json.Unmarshal(delivery.Body, &got); err != nil {
		t.Fatal(err)
	}
	if got.ID != "evt_1" || got.Endpoint != "paddle_billing" {
		t.Fatalf("message = %#v", got)
	}
}

func TestRabbitMQPublisherPublishesConcurrentlyAcrossChannelPool(t *testing.T) {
	ctx, container := startRabbitMQ(t)
	url, err := container.AmqpURL(ctx)
	if err != nil {
		t.Fatal(err)
	}

	pub, err := NewRabbitMQPublisher(ctx, RabbitMQConfig{
		URL: url, Exchange: "hookway.concurrent", ExchangeType: "direct", Durable: true,
		PublishTimeout: 5 * time.Second, DeclareQueues: true, Queues: []string{"billing-events"},
		PublisherChannels: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pub.Close() })

	const total = 50
	var wg sync.WaitGroup
	errs := make(chan error, total)
	for i := 0; i < total; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			msg := Message{ID: fmt.Sprintf("evt_%d", n), Endpoint: "paddle_billing", Body: map[string]any{"n": n}, ReceivedAt: time.Now().UTC()}
			if err := pub.Publish(ctx, "billing", "billing-events", msg); err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent publish failed: %v", err)
	}

	conn, err := amqp.Dial(url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	ch, err := conn.Channel()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ch.Close() })
	q, err := ch.QueueDeclarePassive("billing-events", true, false, false, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if q.Messages != total {
		t.Fatalf("queued messages = %d, want %d", q.Messages, total)
	}
}

func TestRabbitMQPublisherDeclaresDeadLetterQueue(t *testing.T) {
	ctx, container := startRabbitMQ(t)
	url, err := container.AmqpURL(ctx)
	if err != nil {
		t.Fatal(err)
	}

	pub, err := NewRabbitMQPublisher(ctx, RabbitMQConfig{
		URL: url, Exchange: "hookway.dlx.test", ExchangeType: "direct", Durable: true,
		PublishTimeout: 5 * time.Second,
		QueueDeclarations: []QueueDeclaration{{
			Name: "billing-events", CreateQueue: true,
			DLQ: &DeadLetterDeclaration{Exchange: "hookway.dlx", Queue: "billing-events.dlq", RoutingKey: "billing-events.dlq", CreateQueue: true},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pub.Close() })

	conn, err := amqp.Dial(url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	ch, err := conn.Channel()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ch.Close() })
	if _, err := ch.QueueDeclarePassive("billing-events.dlq", true, false, false, false, nil); err != nil {
		t.Fatalf("expected declared dlq: %v", err)
	}
}

func TestRabbitMQPublisherDeclaresQueueOptions(t *testing.T) {
	ctx, container := startRabbitMQ(t)
	url, err := container.AmqpURL(ctx)
	if err != nil {
		t.Fatal(err)
	}

	pub, err := NewRabbitMQPublisher(ctx, RabbitMQConfig{
		URL: url, Exchange: "hookway.options.test", ExchangeType: "direct", Durable: true,
		PublishTimeout: 5 * time.Second,
		QueueDeclarations: []QueueDeclaration{{
			Name: "billing-events", CreateQueue: true,
			Options: QueueOptions{
				MessageTTL: 72 * time.Hour,
				MaxLength:  500000,
				Overflow:   "reject-publish",
			},
			DLQ: &DeadLetterDeclaration{
				Exchange: "hookway.options.dlx", Queue: "billing-events.dlq",
				RoutingKey: "billing.dead", CreateQueue: true,
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pub.Close() })

	conn, err := amqp.Dial(url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	ch, err := conn.Channel()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ch.Close() })
	// Re-declaring with mismatched args fails. Success proves Hookway declared
	// RabbitMQ queue args in milliseconds and integers as expected.
	_, err = ch.QueueDeclare("billing-events", true, false, false, false, amqp.Table{
		"x-message-ttl":             int32(259200000), // 72h in milliseconds
		"x-max-length":              int32(500000),
		"x-overflow":                "reject-publish",
		"x-dead-letter-exchange":    "hookway.options.dlx",
		"x-dead-letter-routing-key": "billing.dead",
	})
	if err != nil {
		t.Fatalf("queue options not declared: %v", err)
	}
}

func TestRabbitMQFactoryRoutesToMultipleBrokers(t *testing.T) {
	ctx, billingContainer := startRabbitMQ(t)
	_, activityContainer := startRabbitMQ(t)
	billingURL, err := billingContainer.AmqpURL(ctx)
	if err != nil {
		t.Fatal(err)
	}
	activityURL, err := activityContainer.AmqpURL(ctx)
	if err != nil {
		t.Fatal(err)
	}

	var pub Publisher
	pub, err = NewFactory(FactoryOptions{}).Create(ctx, configForRabbitMQIntegration(billingURL, activityURL))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pub.Close() })

	if err := pub.Publish(ctx, "billing", "billing-events", Message{ID: "evt_billing", Endpoint: "paddle_billing", DestinationQueueID: "billing", Queue: "billing-events", Body: map[string]any{"ok": true}, ReceivedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := pub.Publish(ctx, "activity", "activity-events", Message{ID: "evt_activity", Endpoint: "github_events", DestinationQueueID: "activity", Queue: "activity-events", Body: map[string]any{"ok": true}, ReceivedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}

	assertRabbitMQMessage(t, ctx, billingURL, "billing-events", "evt_billing")
	assertRabbitMQMessage(t, ctx, activityURL, "activity-events", "evt_activity")
}

func configForRabbitMQIntegration(billingURL, activityURL string) config.Config {
	return config.Config{
		Destinations: map[string]config.DestinationConfig{
			"billing": {
				Type: "rabbitmq",
				RabbitMQ: config.RabbitMQConfig{
					URL: billingURL, Exchange: "hookway.billing.integration", ExchangeType: "direct",
					Durable: true, PublishTimeout: 5 * time.Second, DeclareQueues: true,
				},
			},
			"activity": {
				Type: "rabbitmq",
				RabbitMQ: config.RabbitMQConfig{
					URL: activityURL, Exchange: "hookway.activity.integration", ExchangeType: "direct",
					Durable: true, PublishTimeout: 5 * time.Second, DeclareQueues: true,
				},
			},
		},
		Endpoints: map[string]config.EndpointConfig{
			"paddle_billing": {
				Enabled: true,
				Destinations: []config.EndpointDestinationConfig{{
					Destination: "billing", Target: "billing-events", Create: true,
				}},
			},
			"github_events": {
				Enabled: true,
				Destinations: []config.EndpointDestinationConfig{{
					Destination: "activity", Target: "activity-events", Create: true,
				}},
			},
		},
	}
}

func assertRabbitMQMessage(t *testing.T, ctx context.Context, url, queueName, wantID string) {
	t.Helper()
	conn, err := amqp.Dial(url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	ch, err := conn.Channel()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ch.Close() })
	delivery, ok, err := ch.Get(queueName, true)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatalf("expected message in %s", queueName)
	}
	var got Message
	if err := json.Unmarshal(delivery.Body, &got); err != nil {
		t.Fatal(err)
	}
	if got.ID != wantID {
		t.Fatalf("message id = %q, want %q", got.ID, wantID)
	}
}

func TestRabbitMQPublisherConnectsAfterUnavailableStartup(t *testing.T) {
	testenv.SkipIfShort(t)
	ctx := context.Background()
	container, err := runRabbitMQ(ctx)
	testenv.RequireContainer(t, err, "rabbitmq")
	url, err := container.AmqpURL(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := testcontainers.TerminateContainer(container); err != nil {
		t.Fatal(err)
	}

	metrics := &rabbitMQMetricsRecorder{}
	startCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	pub, err := NewRabbitMQPublisher(startCtx, RabbitMQConfig{
		URL: url, Exchange: "hookway.reconnect.startup", ExchangeType: "direct", Durable: true,
		PublishTimeout: time.Second, DeclareQueues: true, Queues: []string{"billing-events"},
		Retry:   RetryPolicy{MaxAttempts: 2, InitialBackoff: 10 * time.Millisecond, MaxBackoff: 20 * time.Millisecond},
		Metrics: metrics,
	})
	cancel()
	if err != nil {
		t.Fatalf("constructor should tolerate unavailable startup: %v", err)
	}
	t.Cleanup(func() { _ = pub.Close() })

	container, err = tcrabbitmq.Run(ctx, "rabbitmq:3-management")
	testenv.RequireContainer(t, err, "rabbitmq")
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(container) })
	restartedURL, err := container.AmqpURL(ctx)
	if err != nil {
		t.Fatal(err)
	}
	pub.cfg.URL = restartedURL

	msg := Message{ID: "evt_reconnect_startup", Endpoint: "paddle_billing", Body: map[string]any{"ok": true}, ReceivedAt: time.Now().UTC()}
	if err := pub.Publish(ctx, "billing", "billing-events", msg); err != nil {
		t.Fatal(err)
	}
	if metrics.reconnects == 0 || metrics.confirms == 0 {
		t.Fatalf("expected reconnect and confirm metrics, got %#v", metrics)
	}
}

func TestRabbitMQPublisherReconnectsAfterDisconnectBeforePublish(t *testing.T) {
	ctx, container := startRabbitMQ(t)
	url, err := container.AmqpURL(ctx)
	if err != nil {
		t.Fatal(err)
	}

	metrics := &rabbitMQMetricsRecorder{}
	pub, err := NewRabbitMQPublisher(ctx, RabbitMQConfig{
		URL: url, Exchange: "hookway.reconnect.disconnect", ExchangeType: "direct", Durable: true,
		PublishTimeout: time.Second, DeclareQueues: true, Queues: []string{"billing-events"},
		Retry:   RetryPolicy{MaxAttempts: 3, InitialBackoff: 10 * time.Millisecond, MaxBackoff: 20 * time.Millisecond},
		Metrics: metrics,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pub.Close() })

	// Simulate a broker-side drop by closing the live connection out from under
	// the publisher; the next publish (and the reconnect watcher) must recover.
	pub.mu.Lock()
	conn := pub.conn
	pub.mu.Unlock()
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	msg := Message{ID: "evt_reconnect_disconnect", Endpoint: "paddle_billing", Body: map[string]any{"ok": true}, ReceivedAt: time.Now().UTC()}
	if err := pub.Publish(ctx, "billing", "billing-events", msg); err != nil {
		t.Fatal(err)
	}
	if metrics.reconnects == 0 || metrics.confirms == 0 {
		t.Fatalf("expected reconnect and confirm metrics, got %#v", metrics)
	}
}

func TestRabbitMQPublisherRetriesPublishFailures(t *testing.T) {
	ctx := context.Background()
	metrics := &rabbitMQMetricsRecorder{}
	pub, err := NewRabbitMQPublisher(ctx, RabbitMQConfig{
		URL:            "amqp://guest:guest@127.0.0.1:1/", // #nosec G101 -- unreachable test broker
		Exchange:       "hookway.retry",
		ExchangeType:   "direct",
		Durable:        true,
		PublishTimeout: 2 * time.Second,
		Retry:          RetryPolicy{MaxAttempts: 3, InitialBackoff: time.Millisecond, MaxBackoff: time.Millisecond},
		Metrics:        metrics,
	})
	if err != nil {
		t.Fatalf("constructor should tolerate unavailable startup: %v", err)
	}

	msg := Message{ID: "evt_retry", Endpoint: "paddle_billing", Body: map[string]any{"ok": true}, ReceivedAt: time.Now().UTC()}
	if err := pub.Publish(ctx, "billing", "billing-events", msg); err == nil {
		t.Fatal("expected publish error")
	}
	if metrics.retries != 2 {
		t.Fatalf("expected 2 retries, got %d", metrics.retries)
	}
}

// TestRabbitMQPublisherDefaultsToThreeRetryAttempts guards the B2 fix: an
// unset Retry.MaxAttempts must drive 3 attempts (2 retries) rather than the
// old default of 1 (no retry at all), so transient confirm/connect failures
// are retried out of the box.
func TestRabbitMQPublisherDefaultsToThreeRetryAttempts(t *testing.T) {
	ctx := context.Background()
	metrics := &rabbitMQMetricsRecorder{}
	pub, err := NewRabbitMQPublisher(ctx, RabbitMQConfig{
		URL:            "amqp://guest:guest@127.0.0.1:1/", // #nosec G101 -- unreachable test broker
		Exchange:       "hookway.retry.default",
		ExchangeType:   "direct",
		Durable:        true,
		PublishTimeout: 2 * time.Second,
		Metrics:        metrics,
	})
	if err != nil {
		t.Fatalf("constructor should tolerate unavailable startup: %v", err)
	}

	msg := Message{ID: "evt_retry_default", Endpoint: "paddle_billing", Body: map[string]any{"ok": true}, ReceivedAt: time.Now().UTC()}
	if err := pub.Publish(ctx, "billing", "billing-events", msg); err == nil {
		t.Fatal("expected publish error")
	}
	if metrics.retries != 2 {
		t.Fatalf("expected 2 retries (3 attempts total) by default, got %d", metrics.retries)
	}
}

// TestRabbitMQPublisherHonorsConfiguredMaxAttempts uses a value that differs
// from both the old (1) and new (3) hardcoded defaults, proving max_attempts
// is actually threaded through from config rather than hardcoded.
func TestRabbitMQPublisherHonorsConfiguredMaxAttempts(t *testing.T) {
	ctx := context.Background()
	metrics := &rabbitMQMetricsRecorder{}
	pub, err := NewRabbitMQPublisher(ctx, RabbitMQConfig{
		URL:            "amqp://guest:guest@127.0.0.1:1/", // #nosec G101 -- unreachable test broker
		Exchange:       "hookway.retry.configured",
		ExchangeType:   "direct",
		Durable:        true,
		PublishTimeout: 2 * time.Second,
		Retry:          RetryPolicy{MaxAttempts: 5, InitialBackoff: time.Millisecond, MaxBackoff: time.Millisecond},
		Metrics:        metrics,
	})
	if err != nil {
		t.Fatalf("constructor should tolerate unavailable startup: %v", err)
	}

	msg := Message{ID: "evt_retry_configured", Endpoint: "paddle_billing", Body: map[string]any{"ok": true}, ReceivedAt: time.Now().UTC()}
	if err := pub.Publish(ctx, "billing", "billing-events", msg); err == nil {
		t.Fatal("expected publish error")
	}
	if metrics.retries != 4 {
		t.Fatalf("expected 4 retries (5 attempts total), got %d", metrics.retries)
	}
}

// TestRabbitMQPublisherMandatoryUnroutableFailsWithoutRetryStorm publishes to
// an exchange with no bound queue. A mandatory publish to an unroutable
// routing key is broker-acked (confirm) but also returned (basic.return); by
// spec the broker sends the return before the ack, so draining the return
// channel right after receiving the ack reliably observes it. This must
// surface as a Publish error and fire the unroutable metric exactly once —
// not burn the configured retry budget, since "no binding for this routing
// key" is a terminal config error, not a transient one.
func TestRabbitMQPublisherMandatoryUnroutableFailsWithoutRetryStorm(t *testing.T) {
	ctx, container := startRabbitMQ(t)
	url, err := container.AmqpURL(ctx)
	if err != nil {
		t.Fatal(err)
	}

	metrics := &rabbitMQMetricsRecorder{}
	pub, err := NewRabbitMQPublisher(ctx, RabbitMQConfig{
		URL: url, Exchange: "hookway.unroutable", ExchangeType: "direct", Durable: true,
		PublishTimeout: 5 * time.Second,
		// No DeclareQueues/Queues: the exchange exists but nothing is bound to
		// "no-such-route", so a mandatory publish to it is unroutable.
		Retry:   RetryPolicy{MaxAttempts: 3, InitialBackoff: time.Millisecond, MaxBackoff: time.Millisecond},
		Metrics: metrics,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pub.Close() })

	msg := Message{ID: "evt_unroutable", Endpoint: "paddle_billing", Body: map[string]any{"ok": true}, ReceivedAt: time.Now().UTC()}
	if err := pub.Publish(ctx, "billing", "no-such-route", msg); err == nil {
		t.Fatal("expected publish error for unroutable message")
	}
	if metrics.unroutable != 1 {
		t.Fatalf("expected unroutable metric to fire once, got %d", metrics.unroutable)
	}
	if metrics.retries != 0 {
		t.Fatalf("unroutable is a terminal config error and must not retry; got %d retries", metrics.retries)
	}
	if metrics.confirms != 0 {
		t.Fatalf("an unroutable publish must not count as a successful confirm; got %d", metrics.confirms)
	}
}

type rabbitMQMetricsRecorder struct {
	reconnects int
	confirms   int
	retries    int
	dlqs       int
	unroutable int
}

func (r *rabbitMQMetricsRecorder) RabbitMQReconnect()         { r.reconnects++ }
func (r *rabbitMQMetricsRecorder) RabbitMQPublishConfirm()    { r.confirms++ }
func (r *rabbitMQMetricsRecorder) RabbitMQPublishRetry()      { r.retries++ }
func (r *rabbitMQMetricsRecorder) RabbitMQDeadLetterPublish() { r.dlqs++ }
func (r *rabbitMQMetricsRecorder) RabbitMQUnroutable(destinationQueueID, queue string) {
	r.unroutable++
}

func startRabbitMQ(t *testing.T) (context.Context, *tcrabbitmq.RabbitMQContainer) {
	t.Helper()
	testenv.SkipIfShort(t)
	ctx := context.Background()
	container, err := runRabbitMQ(ctx)
	testenv.RequireContainer(t, err, "rabbitmq")
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(container) })
	return ctx, container
}

func runRabbitMQ(ctx context.Context) (container *tcrabbitmq.RabbitMQContainer, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("rabbitmq testcontainer unavailable: %v", recovered)
		}
	}()
	return tcrabbitmq.Run(ctx, "rabbitmq:3-management")
}

func TestRabbitMQEagerConnectFailureLogs(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	// Port 1 is not a valid AMQP broker — dial will fail immediately.
	pub, err := NewRabbitMQPublisher(ctx, RabbitMQConfig{
		URL:           "amqp://guest:guest@127.0.0.1:1/", // #nosec G101 -- unreachable test broker
		Exchange:      "hookway.test",
		ExchangeType:  "direct",
		DestinationID: "test-dest",
		Logger:        logger,
	})
	if err != nil {
		t.Fatalf("constructor should tolerate unavailable startup: %v", err)
	}
	t.Cleanup(func() { _ = pub.Close() })

	line := lastQueueLogLine(t, &buf, "rabbitmq initial connect failed; will retry on publish")
	if line == nil || line["level"] != "WARN" || line["destination"] == nil {
		t.Fatalf("missing eager-connect warn: %v", line)
	}
}
