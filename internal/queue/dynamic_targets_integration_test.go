package queue

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
	segkafka "github.com/segmentio/kafka-go"

	"github.com/jaysongiroux/hook_way/internal/awsconf"
	"github.com/jaysongiroux/hook_way/internal/config"
)

// TestAWSSQSDynamicTargetCreatesQueueOnFirstSight publishes to a rendered
// dynamic target against LocalStack: the queue does not exist at startup, is
// created on first publish with the template's attributes, and the message
// lands in it. Named TestAWS* so it runs under make aws-integration-test.
func TestAWSSQSDynamicTargetCreatesQueueOnFirstSight(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	endpoint := startLocalStack(t)
	ctx := context.Background()
	awsCfg, err := awsconf.Load(ctx, "us-east-1", endpoint, "test", "test")
	if err != nil {
		t.Fatal(err)
	}
	client := sqs.NewFromConfig(awsCfg)
	pub, err := NewSQSPublisher(ctx, client, SQSConfig{
		Region: "us-east-1", Endpoint: endpoint, AccessKeyID: "test", SecretAccessKey: "test",
		Targets: []SQSTarget{{Name: "{env}-dyn-jobs", Create: true, VisibilityTimeout: 45 * time.Second, Template: mustMatcher("{env}-dyn-jobs", map[string]string{"env": "^tenant-[a-z0-9]+$"})}},
	})
	if err != nil {
		t.Fatalf("NewSQSPublisher: %v", err)
	}
	if _, err := client.GetQueueUrl(ctx, &sqs.GetQueueUrlInput{QueueName: aws.String("tenant-a-dyn-jobs")}); err == nil {
		t.Fatal("queue must not exist before the first publish")
	}
	if err := pub.Publish(ctx, "jobs", "tenant-a-dyn-jobs", Message{ID: "evt_dyn", Body: map[string]any{"a": 1}}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	urlOut, err := client.GetQueueUrl(ctx, &sqs.GetQueueUrlInput{QueueName: aws.String("tenant-a-dyn-jobs")})
	if err != nil {
		t.Fatalf("queue should exist after first publish: %v", err)
	}
	attrs, err := client.GetQueueAttributes(ctx, &sqs.GetQueueAttributesInput{QueueUrl: urlOut.QueueUrl, AttributeNames: []sqstypes.QueueAttributeName{sqstypes.QueueAttributeNameVisibilityTimeout}})
	if err != nil || attrs.Attributes["VisibilityTimeout"] != "45" {
		t.Fatalf("template attributes should apply to the created queue: %v %v", err, attrs)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		out, err := client.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{QueueUrl: urlOut.QueueUrl, MaxNumberOfMessages: 1, WaitTimeSeconds: 2})
		if err != nil {
			t.Fatal(err)
		}
		if len(out.Messages) > 0 {
			var m Message
			if err := json.Unmarshal([]byte(aws.ToString(out.Messages[0].Body)), &m); err != nil || m.ID != "evt_dyn" {
				t.Fatalf("unexpected message: %v %#v", err, m)
			}
			return
		}
	}
	t.Fatal("did not receive the published message")
}

// TestKafkaDynamicTargetCreatesTopic publishes to a rendered dynamic topic:
// the topic is created on first sight (create: true) and the message can be
// read back.
func TestKafkaDynamicTargetCreatesTopic(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	brokers := startKafka(t)
	ctx := context.Background()
	targets := []KafkaTarget{{Topic: "{env}-dyn-events", Create: true, Template: mustMatcher("{env}-dyn-events", map[string]string{"env": "^tenant-[a-z0-9]+$"})}}
	ops, err := newKafkaOps(ctx, "events", config.KafkaConfig{Brokers: brokers, Acks: "all", PublishTimeout: 15 * time.Second}, targets, slog.Default())
	if err != nil {
		t.Fatalf("newKafkaOps: %v", err)
	}
	pub := NewKafkaPublisher(ops, KafkaPublisherConfig{DestinationID: "events", PublishTimeout: 15 * time.Second, Targets: targets})
	t.Cleanup(func() { _ = pub.Close() })
	// The first write may race leader election of the freshly created topic;
	// the writer retries within its attempts, but allow one more try here.
	var pubErr error
	for attempt := 0; attempt < 3; attempt++ {
		if pubErr = pub.Publish(ctx, "events", "tenant-a-dyn-events", Message{ID: "evt-dyn", ReceivedAt: time.Now().UTC()}); pubErr == nil {
			break
		}
		time.Sleep(time.Second)
	}
	if pubErr != nil {
		t.Fatalf("Publish: %v", pubErr)
	}
	reader := segkafka.NewReader(segkafka.ReaderConfig{Brokers: brokers, Topic: "tenant-a-dyn-events", MinBytes: 1, MaxBytes: 10e6})
	t.Cleanup(func() { _ = reader.Close() })
	readCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	got, err := reader.ReadMessage(readCtx)
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	var decoded Message
	if err := json.Unmarshal(got.Value, &decoded); err != nil || decoded.ID != "evt-dyn" {
		t.Fatalf("round trip: %v %#v", err, decoded)
	}
}

// TestRedisStreamDynamicTargetCreatesGroup publishes to a rendered dynamic
// stream: the consumer group is created on first sight and the entry lands on
// the prefixed stream key.
func TestRedisStreamDynamicTargetCreatesGroup(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	redisURL := startRedis(t, "redis:7")
	ctx := context.Background()
	ops, err := newRedisClient(config.RedisConfig{Mode: "single", URL: redisURL})
	if err != nil {
		t.Fatalf("newRedisClient: %v", err)
	}
	pub, err := NewRedisStreamPublisher(ctx, ops, RedisStreamConfig{
		DestinationID: "streams", KeyPrefix: "hw",
		Targets: []RedisStreamTarget{{Name: "{env}-dyn", ConsumerGroup: "workers", Create: true, Template: mustMatcher("{env}-dyn", map[string]string{"env": "^tenant-[a-z0-9]+$"})}},
	})
	if err != nil {
		t.Fatalf("NewRedisStreamPublisher: %v", err)
	}
	t.Cleanup(func() { _ = pub.Close() })
	if err := pub.Publish(ctx, "streams", "tenant-a-dyn", Message{ID: "evt-dyn", ReceivedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	raw := rawRedisClient(t, redisURL)
	t.Cleanup(func() { _ = raw.Close() })
	if n, err := raw.XLen(ctx, "hw:tenant-a-dyn").Result(); err != nil || n != 1 {
		t.Fatalf("XLEN hw:tenant-a-dyn = %d, %v", n, err)
	}
	groups, err := raw.XInfoGroups(ctx, "hw:tenant-a-dyn").Result()
	if err != nil || len(groups) != 1 || groups[0].Name != "workers" {
		t.Fatalf("consumer group should exist on the rendered stream: %v %v", groups, err)
	}
}
