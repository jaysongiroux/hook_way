package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/testcontainers/testcontainers-go"
	tcrabbitmq "github.com/testcontainers/testcontainers-go/modules/rabbitmq"
)

func TestRabbitMQPublisherPublishesConsumableEnvelope(t *testing.T) {
	ctx := context.Background()
	container, err := runRabbitMQ(ctx)
	if err != nil {
		t.Skipf("rabbitmq testcontainer unavailable: %v", err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(container) })
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
	if err := pub.Publish(ctx, "billing-events", msg); err != nil {
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
	ctx := context.Background()
	container, err := runRabbitMQ(ctx)
	if err != nil {
		t.Skipf("rabbitmq testcontainer unavailable: %v", err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(container) })
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
			if err := pub.Publish(ctx, "billing-events", msg); err != nil {
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
	ctx := context.Background()
	container, err := runRabbitMQ(ctx)
	if err != nil {
		t.Skipf("rabbitmq testcontainer unavailable: %v", err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(container) })
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

func TestRabbitMQPublisherConnectsAfterUnavailableStartup(t *testing.T) {
	ctx := context.Background()
	container, err := runRabbitMQ(ctx)
	if err != nil {
		t.Skipf("rabbitmq testcontainer unavailable: %v", err)
	}
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
	if err != nil {
		t.Skipf("rabbitmq restart testcontainer unavailable: %v", err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(container) })
	restartedURL, err := container.AmqpURL(ctx)
	if err != nil {
		t.Fatal(err)
	}
	pub.cfg.URL = restartedURL

	msg := Message{ID: "evt_reconnect_startup", Endpoint: "paddle_billing", Body: map[string]any{"ok": true}, ReceivedAt: time.Now().UTC()}
	if err := pub.Publish(ctx, "billing-events", msg); err != nil {
		t.Fatal(err)
	}
	if metrics.reconnects == 0 || metrics.confirms == 0 {
		t.Fatalf("expected reconnect and confirm metrics, got %#v", metrics)
	}
}

func TestRabbitMQPublisherReconnectsAfterDisconnectBeforePublish(t *testing.T) {
	ctx := context.Background()
	container, err := runRabbitMQ(ctx)
	if err != nil {
		t.Skipf("rabbitmq testcontainer unavailable: %v", err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(container) })
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
	if err := pub.Publish(ctx, "billing-events", msg); err != nil {
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
		URL: "amqp://guest:guest@127.0.0.1:1/", Exchange: "hookway.retry", ExchangeType: "direct", Durable: true,
		PublishTimeout: 50 * time.Millisecond,
		Retry:          RetryPolicy{MaxAttempts: 3, InitialBackoff: time.Millisecond, MaxBackoff: time.Millisecond},
		Metrics:        metrics,
	})
	if err != nil {
		t.Fatalf("constructor should tolerate unavailable startup: %v", err)
	}

	msg := Message{ID: "evt_retry", Endpoint: "paddle_billing", Body: map[string]any{"ok": true}, ReceivedAt: time.Now().UTC()}
	if err := pub.Publish(ctx, "billing-events", msg); err == nil {
		t.Fatal("expected publish error")
	}
	if metrics.retries != 2 {
		t.Fatalf("expected 2 retries, got %d", metrics.retries)
	}
}

type rabbitMQMetricsRecorder struct {
	reconnects int
	confirms   int
	retries    int
	dlqs       int
}

func (r *rabbitMQMetricsRecorder) RabbitMQReconnect()         { r.reconnects++ }
func (r *rabbitMQMetricsRecorder) RabbitMQPublishConfirm()    { r.confirms++ }
func (r *rabbitMQMetricsRecorder) RabbitMQPublishRetry()      { r.retries++ }
func (r *rabbitMQMetricsRecorder) RabbitMQDeadLetterPublish() { r.dlqs++ }

func runRabbitMQ(ctx context.Context) (container *tcrabbitmq.RabbitMQContainer, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("rabbitmq testcontainer unavailable: %v", recovered)
		}
	}()
	return tcrabbitmq.Run(ctx, "rabbitmq:3-management")
}
