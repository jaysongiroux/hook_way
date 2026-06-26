package queue

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
	snsservice "github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/localstack"
)

// startLocalStack boots a LocalStack container with SQS+SNS and returns its
// endpoint. Skips the test when Docker is unavailable.
func startLocalStack(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	container, err := localstack.Run(ctx, "localstack/localstack:3.4",
		testcontainers.WithEnv(map[string]string{"SERVICES": "sqs,sns"}),
	)
	if err != nil {
		t.Skipf("localstack unavailable: %v", err)
	}
	t.Cleanup(func() { _ = container.Terminate(ctx) })
	host, err := container.Host(ctx)
	if err != nil {
		t.Fatal(err)
	}
	port, err := container.MappedPort(ctx, "4566/tcp")
	if err != nil {
		t.Fatal(err)
	}
	return "http://" + host + ":" + port.Port()
}

func TestAWSSQSPublishRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	endpoint := startLocalStack(t)
	ctx := context.Background()

	awsCfg, err := newAWSConfig(ctx, "us-east-1", endpoint, "test", "test")
	if err != nil {
		t.Fatal(err)
	}
	client := sqs.NewFromConfig(awsCfg)

	pub, err := NewSQSPublisher(ctx, client, SQSConfig{
		Region: "us-east-1", Endpoint: endpoint, AccessKeyID: "test", SecretAccessKey: "test",
		Targets: []SQSTarget{{Name: "it-queue", Create: true}},
	})
	if err != nil {
		t.Fatalf("NewSQSPublisher: %v", err)
	}
	if err := pub.Ready(ctx); err != nil {
		t.Fatalf("Ready: %v", err)
	}
	if err := pub.Publish(ctx, "jobs", "it-queue", Message{ID: "evt_it", Body: map[string]any{"hello": "world"}}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	urlOut, err := client.GetQueueUrl(ctx, &sqs.GetQueueUrlInput{QueueName: aws.String("it-queue")})
	if err != nil {
		t.Fatal(err)
	}
	var received *Message
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		out, err := client.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
			QueueUrl: urlOut.QueueUrl, MaxNumberOfMessages: 1, WaitTimeSeconds: 2,
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(out.Messages) > 0 {
			var m Message
			if err := json.Unmarshal([]byte(aws.ToString(out.Messages[0].Body)), &m); err != nil {
				t.Fatalf("body not json: %v", err)
			}
			received = &m
			break
		}
	}
	if received == nil || received.ID != "evt_it" {
		t.Fatalf("did not receive published message: %#v", received)
	}
}

func TestAWSSNSToSQSFanout(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	endpoint := startLocalStack(t)
	ctx := context.Background()

	awsCfg, err := newAWSConfig(ctx, "us-east-1", endpoint, "test", "test")
	if err != nil {
		t.Fatal(err)
	}
	sqsClient := sqs.NewFromConfig(awsCfg)

	// Create an SQS queue, then an SNS topic via the publisher, then subscribe
	// the queue to the topic and verify a published message arrives.
	pub, err := NewSNSPublisher(ctx, snsFromConfig(awsCfg), SNSConfig{
		Region: "us-east-1", Endpoint: endpoint, AccessKeyID: "test", SecretAccessKey: "test",
		Targets: []SNSTarget{{Name: "it-topic", Create: true}},
	})
	if err != nil {
		t.Fatalf("NewSNSPublisher: %v", err)
	}
	if err := pub.Ready(ctx); err != nil {
		t.Fatalf("Ready: %v", err)
	}

	qOut, err := sqsClient.CreateQueue(ctx, &sqs.CreateQueueInput{QueueName: aws.String("it-sub-queue")})
	if err != nil {
		t.Fatal(err)
	}
	subscribeSQSToSNS(t, ctx, awsCfg, "it-topic", aws.ToString(qOut.QueueUrl))

	if err := pub.Publish(ctx, "alerts", "it-topic", Message{ID: "evt_sns"}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		out, err := sqsClient.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
			QueueUrl: qOut.QueueUrl, MaxNumberOfMessages: 1, WaitTimeSeconds: 2,
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(out.Messages) > 0 {
			var m Message
			if err := json.Unmarshal([]byte(aws.ToString(out.Messages[0].Body)), &m); err != nil {
				t.Fatalf("body not json: %v", err)
			}
			if m.ID != "evt_sns" {
				t.Fatalf("unexpected message ID: got %q, want %q", m.ID, "evt_sns")
			}
			return
		}
	}
	t.Fatal("SNS message was not delivered to the subscribed SQS queue")
}

func TestAWSSQSReconcileUpdatesExistingQueue(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	endpoint := startLocalStack(t)
	ctx := context.Background()
	awsCfg, err := newAWSConfig(ctx, "us-east-1", endpoint, "test", "test")
	if err != nil {
		t.Fatal(err)
	}
	client := sqs.NewFromConfig(awsCfg)

	// Pre-create a queue with a visibility timeout that differs from config.
	if _, err := client.CreateQueue(ctx, &sqs.CreateQueueInput{
		QueueName:  aws.String("drift-queue"),
		Attributes: map[string]string{"VisibilityTimeout": "60"},
	}); err != nil {
		t.Fatal(err)
	}

	// reconcile policy: should update VisibilityTimeout to 30.
	if _, err := NewSQSPublisher(ctx, client, SQSConfig{
		Policy:  ConflictReconcile,
		Targets: []SQSTarget{{Name: "drift-queue", Create: false, VisibilityTimeout: 30 * time.Second}},
	}); err != nil {
		t.Fatalf("NewSQSPublisher: %v", err)
	}

	urlOut, err := client.GetQueueUrl(ctx, &sqs.GetQueueUrlInput{QueueName: aws.String("drift-queue")})
	if err != nil {
		t.Fatal(err)
	}
	attrs, err := client.GetQueueAttributes(ctx, &sqs.GetQueueAttributesInput{
		QueueUrl:       urlOut.QueueUrl,
		AttributeNames: []sqstypes.QueueAttributeName{sqstypes.QueueAttributeNameVisibilityTimeout},
	})
	if err != nil {
		t.Fatal(err)
	}
	if attrs.Attributes["VisibilityTimeout"] != "30" {
		t.Fatalf("reconcile did not update VisibilityTimeout: got %q", attrs.Attributes["VisibilityTimeout"])
	}
}

func TestAWSSQSWarnLeavesExistingQueueUnchanged(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	endpoint := startLocalStack(t)
	ctx := context.Background()
	awsCfg, err := newAWSConfig(ctx, "us-east-1", endpoint, "test", "test")
	if err != nil {
		t.Fatal(err)
	}
	client := sqs.NewFromConfig(awsCfg)

	if _, err := client.CreateQueue(ctx, &sqs.CreateQueueInput{
		QueueName:  aws.String("warn-queue"),
		Attributes: map[string]string{"VisibilityTimeout": "60"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := NewSQSPublisher(ctx, client, SQSConfig{
		Policy:  ConflictWarn,
		Targets: []SQSTarget{{Name: "warn-queue", Create: false, VisibilityTimeout: 30 * time.Second}},
	}); err != nil {
		t.Fatalf("NewSQSPublisher: %v", err)
	}
	urlOut, _ := client.GetQueueUrl(ctx, &sqs.GetQueueUrlInput{QueueName: aws.String("warn-queue")})
	attrs, err := client.GetQueueAttributes(ctx, &sqs.GetQueueAttributesInput{
		QueueUrl:       urlOut.QueueUrl,
		AttributeNames: []sqstypes.QueueAttributeName{sqstypes.QueueAttributeNameVisibilityTimeout},
	})
	if err != nil {
		t.Fatal(err)
	}
	if attrs.Attributes["VisibilityTimeout"] != "60" {
		t.Fatalf("warn must not modify the queue; got VisibilityTimeout=%q", attrs.Attributes["VisibilityTimeout"])
	}
}

func TestAWSSQSErrorPolicyFailsOnDrift(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	endpoint := startLocalStack(t)
	ctx := context.Background()
	awsCfg, err := newAWSConfig(ctx, "us-east-1", endpoint, "test", "test")
	if err != nil {
		t.Fatal(err)
	}
	client := sqs.NewFromConfig(awsCfg)
	if _, err := client.CreateQueue(ctx, &sqs.CreateQueueInput{
		QueueName:  aws.String("error-queue"),
		Attributes: map[string]string{"VisibilityTimeout": "60"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := NewSQSPublisher(ctx, client, SQSConfig{
		Policy:  ConflictError,
		Targets: []SQSTarget{{Name: "error-queue", Create: false, VisibilityTimeout: 30 * time.Second}},
	}); err == nil {
		t.Fatal("error policy should fail on drift against the live queue")
	}
}

func snsFromConfig(cfg aws.Config) *snsservice.Client {
	return snsservice.NewFromConfig(cfg)
}

func subscribeSQSToSNS(t *testing.T, ctx context.Context, cfg aws.Config, topicName, queueURL string) {
	t.Helper()
	snsClient := snsservice.NewFromConfig(cfg)
	sqsClient := sqs.NewFromConfig(cfg)

	topics, err := snsClient.ListTopics(ctx, &snsservice.ListTopicsInput{})
	if err != nil {
		t.Fatal(err)
	}
	var topicARN string
	for _, topic := range topics.Topics {
		if strings.HasSuffix(aws.ToString(topic.TopicArn), ":"+topicName) {
			topicARN = aws.ToString(topic.TopicArn)
		}
	}
	if topicARN == "" {
		t.Fatalf("topic %q not found", topicName)
	}
	attrs, err := sqsClient.GetQueueAttributes(ctx, &sqs.GetQueueAttributesInput{
		QueueUrl:       aws.String(queueURL),
		AttributeNames: []sqstypes.QueueAttributeName{sqstypes.QueueAttributeNameQueueArn},
	})
	if err != nil {
		t.Fatal(err)
	}
	queueARN := attrs.Attributes[string(sqstypes.QueueAttributeNameQueueArn)]
	if _, err := snsClient.Subscribe(ctx, &snsservice.SubscribeInput{
		TopicArn: aws.String(topicARN),
		Protocol: aws.String("sqs"),
		Endpoint: aws.String(queueARN),
		Attributes: map[string]string{"RawMessageDelivery": "true"},
	}); err != nil {
		t.Fatal(err)
	}
}
