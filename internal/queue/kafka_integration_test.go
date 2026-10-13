package queue

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/jaysongiroux/hook_way/internal/config"
	"github.com/jaysongiroux/hook_way/internal/testenv"

	segkafka "github.com/segmentio/kafka-go"
	tckafka "github.com/testcontainers/testcontainers-go/modules/kafka"
)

// startKafka boots a Kafka (KRaft) container and returns its broker addresses.
// It self-skips when Docker is unavailable, mirroring startRedis/startLocalStack.
func startKafka(t *testing.T) []string {
	t.Helper()
	testenv.SkipIfShort(t)
	ctx := context.Background()
	container, err := tckafka.Run(ctx, "confluentinc/confluent-local:7.5.0")
	testenv.RequireContainer(t, err, "kafka")
	t.Cleanup(func() { _ = container.Terminate(ctx) })
	brokers, err := container.Brokers(ctx)
	if err != nil {
		t.Fatalf("get kafka brokers: %v", err)
	}
	return brokers
}

// TestKafkaPublishRoundTrip publishes a Message via KafkaPublisher to an
// auto-created topic and reads it back with a raw kafka-go Reader, asserting the
// JSON envelope round-trips (ID) and the partition key was set from the body.
func TestKafkaPublishRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	brokers := startKafka(t)
	ctx := context.Background()

	targets := []KafkaTarget{{
		Topic:        "webhooks",
		Create:       true,
		PartitionKey: &FIFOSettings{GroupIDSource: "body", GroupIDPath: "account.id"},
	}}
	ops, err := newKafkaOps(ctx, "events", config.KafkaConfig{
		Brokers:        brokers,
		Acks:           "all",
		PublishTimeout: 15 * time.Second,
	}, targets, slog.Default())
	if err != nil {
		t.Fatalf("newKafkaOps: %v", err)
	}
	pub := NewKafkaPublisher(ops, KafkaPublisherConfig{
		DestinationID:  "events",
		PublishTimeout: 15 * time.Second,
		Targets:        targets,
	})
	t.Cleanup(func() { _ = pub.Close() })

	msg := Message{ID: "evt-1", Endpoint: "ep", ReceivedAt: time.Now().UTC(), Body: map[string]any{"account": map[string]any{"id": "acct-7"}}}
	// The topic is pre-created by newKafkaOps above (admin CreateTopics call),
	// so the first publish no longer races broker-side auto-creation.
	if err := pub.Publish(ctx, "events", "webhooks", msg); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	reader := segkafka.NewReader(segkafka.ReaderConfig{Brokers: brokers, Topic: "webhooks", MinBytes: 1, MaxBytes: 10e6})
	t.Cleanup(func() { _ = reader.Close() })
	readCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	got, err := reader.ReadMessage(readCtx)
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if string(got.Key) != "acct-7" {
		t.Errorf("partition key: got %q, want acct-7", string(got.Key))
	}
	var decoded Message
	if err := json.Unmarshal(got.Value, &decoded); err != nil {
		t.Fatalf("value is not the JSON envelope: %v", err)
	}
	if decoded.ID != "evt-1" {
		t.Errorf("round-tripped id: got %q, want evt-1", decoded.ID)
	}
}
