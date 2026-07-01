package queue

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

type fakeSQS struct {
	created  map[string]map[string]string
	sent     []*sqs.SendMessageInput
	urls     map[string]string
	attrs    map[string]map[string]string // url -> attributes
	setCalls []*sqs.SetQueueAttributesInput
	// attrErrors is kept for Ready probe tests
	attrErrors map[string]error // url -> error returned by GetQueueAttributes
}

func newFakeSQS() *fakeSQS {
	return &fakeSQS{
		created:    map[string]map[string]string{},
		urls:       map[string]string{},
		attrs:      map[string]map[string]string{},
		attrErrors: map[string]error{},
	}
}

func (f *fakeSQS) GetQueueUrl(ctx context.Context, in *sqs.GetQueueUrlInput, _ ...func(*sqs.Options)) (*sqs.GetQueueUrlOutput, error) {
	url, ok := f.urls[aws.ToString(in.QueueName)]
	if !ok {
		return nil, &sqstypes.QueueDoesNotExist{}
	}
	return &sqs.GetQueueUrlOutput{QueueUrl: aws.String(url)}, nil
}

func (f *fakeSQS) CreateQueue(ctx context.Context, in *sqs.CreateQueueInput, _ ...func(*sqs.Options)) (*sqs.CreateQueueOutput, error) {
	name := aws.ToString(in.QueueName)
	f.created[name] = in.Attributes
	url := "http://localstack/000000000000/" + name
	f.urls[name] = url
	f.attrs[url] = in.Attributes
	return &sqs.CreateQueueOutput{QueueUrl: aws.String(url)}, nil
}

func (f *fakeSQS) GetQueueAttributes(ctx context.Context, in *sqs.GetQueueAttributesInput, _ ...func(*sqs.Options)) (*sqs.GetQueueAttributesOutput, error) {
	if err, ok := f.attrErrors[aws.ToString(in.QueueUrl)]; ok {
		return nil, err
	}
	return &sqs.GetQueueAttributesOutput{Attributes: f.attrs[aws.ToString(in.QueueUrl)]}, nil
}

func (f *fakeSQS) SetQueueAttributes(ctx context.Context, in *sqs.SetQueueAttributesInput, _ ...func(*sqs.Options)) (*sqs.SetQueueAttributesOutput, error) {
	f.setCalls = append(f.setCalls, in)
	url := aws.ToString(in.QueueUrl)
	if f.attrs[url] == nil {
		f.attrs[url] = map[string]string{}
	}
	for k, v := range in.Attributes {
		f.attrs[url][k] = v
	}
	return &sqs.SetQueueAttributesOutput{}, nil
}

func (f *fakeSQS) SendMessage(ctx context.Context, in *sqs.SendMessageInput, _ ...func(*sqs.Options)) (*sqs.SendMessageOutput, error) {
	f.sent = append(f.sent, in)
	return &sqs.SendMessageOutput{MessageId: aws.String("m-1")}, nil
}

// seedQueue registers a pre-existing queue with the given attributes.
func (f *fakeSQS) seedQueue(name string, attrs map[string]string) string {
	url := "http://localstack/000000000000/" + name
	f.urls[name] = url
	f.attrs[url] = attrs
	return url
}

func TestSQSPublisherCreatesQueueAndSends(t *testing.T) {
	fake := newFakeSQS()
	pub, err := NewSQSPublisher(context.Background(), fake, SQSConfig{
		Region:  "us-east-1",
		Policy:  ConflictReconcile,
		Targets: []SQSTarget{{Name: "billing-jobs", Create: true}},
	})
	if err != nil {
		t.Fatalf("NewSQSPublisher: %v", err)
	}
	if _, ok := fake.created["billing-jobs"]; !ok {
		t.Fatalf("queue was not created: %#v", fake.created)
	}
	if err := pub.Publish(context.Background(), "jobs", "billing-jobs", Message{ID: "evt_1", Body: map[string]any{"x": 1}}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if len(fake.sent) != 1 {
		t.Fatalf("expected 1 send, got %d", len(fake.sent))
	}
	var decoded Message
	if err := json.Unmarshal([]byte(aws.ToString(fake.sent[0].MessageBody)), &decoded); err != nil {
		t.Fatalf("body not json: %v", err)
	}
	if decoded.ID != "evt_1" {
		t.Fatalf("bad body: %#v", decoded)
	}
	if fake.sent[0].MessageGroupId != nil {
		t.Fatalf("standard queue must not set group id")
	}
}

func TestSQSPublisherFIFOSetsGroupAndDedup(t *testing.T) {
	fake := newFakeSQS()
	pub, err := NewSQSPublisher(context.Background(), fake, SQSConfig{
		Region: "us-east-1",
		Policy: ConflictReconcile,
		Targets: []SQSTarget{{
			Name: "billing-jobs.fifo", Create: true,
			FIFO: &FIFOSettings{GroupIDSource: "endpoint", DedupUseIdempotencyKey: true},
		}},
	})
	if err != nil {
		t.Fatalf("NewSQSPublisher: %v", err)
	}
	attrs := fake.created["billing-jobs.fifo"]
	if attrs["FifoQueue"] != "true" {
		t.Fatalf("expected FifoQueue=true attribute, got %#v", attrs)
	}
	if err := pub.Publish(context.Background(), "jobs", "billing-jobs.fifo", Message{ID: "evt_1", Endpoint: "billing", IdempotencyKey: "idem-1"}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	got := fake.sent[0]
	if aws.ToString(got.MessageGroupId) != "billing" || aws.ToString(got.MessageDeduplicationId) != "idem-1" {
		t.Fatalf("group/dedup = %q/%q", aws.ToString(got.MessageGroupId), aws.ToString(got.MessageDeduplicationId))
	}
}

func TestSQSPublisherUnknownTargetErrors(t *testing.T) {
	fake := newFakeSQS()
	fake.urls["known"] = "http://localstack/000000000000/known" // seed BEFORE construction
	pub, err := NewSQSPublisher(context.Background(), fake, SQSConfig{
		Region:  "us-east-1",
		Policy:  ConflictReconcile,
		Targets: []SQSTarget{{Name: "known"}},
	})
	if err != nil {
		t.Fatalf("NewSQSPublisher: %v", err)
	}
	if err := pub.Publish(context.Background(), "jobs", "unknown", Message{ID: "x"}); err == nil {
		t.Fatal("expected error for unknown target")
	}
}

// TestSQSPublisherFailsFastOnMissingQueue asserts that NewSQSPublisher fails
// immediately when a target has Create:false and the queue does not exist.
func TestSQSPublisherFailsFastOnMissingQueue(t *testing.T) {
	fake := newFakeSQS() // empty — no URLs seeded
	_, err := NewSQSPublisher(context.Background(), fake, SQSConfig{
		Region:  "us-east-1",
		Policy:  ConflictReconcile,
		Targets: []SQSTarget{{Name: "missing", Create: false}},
	})
	if err == nil {
		t.Fatal("expected NewSQSPublisher to fail for missing queue with Create:false")
	}
}

// TestSQSPublisherReadyProbesAllTargets asserts that Ready checks every resolved
// queue, not just the last one. A publisher with two targets where one target's
// GetQueueAttributes returns an error must cause Ready to return an error. When
// all targets are healthy Ready returns nil.
func TestSQSPublisherReadyProbesAllTargets(t *testing.T) {
	fake := newFakeSQS()
	fake.urls["q1"] = "http://localstack/000000000000/q1"
	fake.urls["q2"] = "http://localstack/000000000000/q2"

	pub, err := NewSQSPublisher(context.Background(), fake, SQSConfig{
		Region: "us-east-1",
		Policy: ConflictReconcile,
		Targets: []SQSTarget{
			{Name: "q1"},
			{Name: "q2"},
		},
	})
	if err != nil {
		t.Fatalf("NewSQSPublisher: %v", err)
	}

	// All healthy — Ready must return nil.
	if err := pub.Ready(context.Background()); err != nil {
		t.Fatalf("Ready with all healthy targets: %v", err)
	}

	// Inject a failure on q1's URL (the first target).
	fake.attrErrors["http://localstack/000000000000/q1"] = fmt.Errorf("connection refused")
	if err := pub.Ready(context.Background()); err == nil {
		t.Fatal("expected Ready to return an error when one target is unhealthy")
	}
}

// TestSQSPublisherFIFOCreateFalseRequiresContentBasedDedup tests that when a
// pre-existing FIFO queue is missing ContentBasedDeduplication and Policy is
// ConflictError, NewSQSPublisher fails with a drift error.
func TestSQSPublisherFIFOCreateFalseRequiresContentBasedDedup(t *testing.T) {
	fake := newFakeSQS()
	fake.seedQueue("orders.fifo", map[string]string{"FifoQueue": "true"}) // CBD absent

	_, err := NewSQSPublisher(context.Background(), fake, SQSConfig{
		Region: "us-east-1",
		Policy: ConflictError,
		Targets: []SQSTarget{{
			Name:   "orders.fifo",
			Create: false,
			FIFO:   &FIFOSettings{GroupIDSource: "endpoint", DedupContentBasedFallback: true},
		}},
	})
	if err == nil {
		t.Fatal("expected constructor to fail when ContentBasedDeduplication is not enabled")
	}
	if !containsAny(err.Error(), "ContentBasedDeduplication", "differs") {
		t.Fatalf("expected drift error mentioning ContentBasedDeduplication, got: %v", err)
	}
}

func TestSQSPublisherFIFOCreateFalseWithContentBasedDedupSucceeds(t *testing.T) {
	fake := newFakeSQS()
	fake.seedQueue("orders.fifo", map[string]string{
		"FifoQueue":                 "true",
		"ContentBasedDeduplication": "true",
	})

	pub, err := NewSQSPublisher(context.Background(), fake, SQSConfig{
		Region: "us-east-1",
		Policy: ConflictReconcile,
		Targets: []SQSTarget{{
			Name:   "orders.fifo",
			Create: false,
			FIFO:   &FIFOSettings{GroupIDSource: "endpoint", DedupContentBasedFallback: true},
		}},
	})
	if err != nil {
		t.Fatalf("expected success when ContentBasedDeduplication enabled: %v", err)
	}
	_ = pub
}

// --- New reconcile tests ---

func TestSQSReconcileEditsMutableAttribute(t *testing.T) {
	fake := newFakeSQS()
	url := fake.seedQueue("orders", map[string]string{"VisibilityTimeout": "60"})
	_, err := NewSQSPublisher(context.Background(), fake, SQSConfig{
		Policy:  ConflictReconcile,
		Targets: []SQSTarget{{Name: "orders", Create: false, VisibilityTimeout: 30 * time.Second}},
	})
	if err != nil {
		t.Fatalf("NewSQSPublisher: %v", err)
	}
	if len(fake.setCalls) != 1 {
		t.Fatalf("expected 1 SetQueueAttributes call, got %d", len(fake.setCalls))
	}
	if fake.attrs[url]["VisibilityTimeout"] != "30" {
		t.Fatalf("expected reconciled VisibilityTimeout=30, got %q", fake.attrs[url]["VisibilityTimeout"])
	}
}

func TestSQSWarnLeavesAttributesUnchanged(t *testing.T) {
	fake := newFakeSQS()
	url := fake.seedQueue("orders", map[string]string{"VisibilityTimeout": "60"})
	_, err := NewSQSPublisher(context.Background(), fake, SQSConfig{
		Policy:  ConflictWarn,
		Targets: []SQSTarget{{Name: "orders", Create: false, VisibilityTimeout: 30 * time.Second}},
	})
	if err != nil {
		t.Fatalf("NewSQSPublisher: %v", err)
	}
	if len(fake.setCalls) != 0 {
		t.Fatal("warn must not call SetQueueAttributes")
	}
	if fake.attrs[url]["VisibilityTimeout"] != "60" {
		t.Fatal("warn must leave the existing attribute unchanged")
	}
}

func TestSQSErrorPolicyFailsOnDrift(t *testing.T) {
	fake := newFakeSQS()
	fake.seedQueue("orders", map[string]string{"VisibilityTimeout": "60"})
	_, err := NewSQSPublisher(context.Background(), fake, SQSConfig{
		Policy:  ConflictError,
		Targets: []SQSTarget{{Name: "orders", Create: false, VisibilityTimeout: 30 * time.Second}},
	})
	if err == nil {
		t.Fatal("error policy should fail on drift")
	}
}

func TestSQSFifoMissingContentBasedDedupReconciles(t *testing.T) {
	fake := newFakeSQS()
	url := fake.seedQueue("orders.fifo", map[string]string{"FifoQueue": "true"}) // CBD absent
	_, err := NewSQSPublisher(context.Background(), fake, SQSConfig{
		Policy:  ConflictReconcile,
		Targets: []SQSTarget{{Name: "orders.fifo", Create: false, FIFO: &FIFOSettings{GroupIDSource: "endpoint", DedupContentBasedFallback: true}}},
	})
	if err != nil {
		t.Fatalf("NewSQSPublisher: %v", err)
	}
	if fake.attrs[url]["ContentBasedDeduplication"] != "true" {
		t.Fatalf("expected CBD reconciled to true, got %q", fake.attrs[url]["ContentBasedDeduplication"])
	}
}

// TestSQSLogsCreatedQueue asserts that NewSQSPublisher emits an Info
// "created queue" log when the queue is absent and Create=true.
func TestSQSLogsCreatedQueue(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	fake := newFakeSQS() // GetQueueUrl → QueueDoesNotExist; CreateQueue → ok
	_, err := NewSQSPublisher(context.Background(), fake, SQSConfig{
		DestinationID: "jobs", Logger: logger,
		Targets: []SQSTarget{{Name: "orders", Create: true}},
	})
	if err != nil {
		t.Fatalf("ctor: %v", err)
	}
	line := lastQueueLogLine(t, &buf, "created queue")
	if line == nil || line["level"] != "INFO" || line["destination"] != "jobs" || line["target"] != "orders" {
		t.Fatalf("missing created-queue info log: %v", line)
	}
}

// TestSQSLogsQueueExists asserts that NewSQSPublisher emits a Debug
// "queue exists" log when the queue already exists.
func TestSQSLogsQueueExists(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	fake := newFakeSQS()
	fake.seedQueue("payments", map[string]string{})
	_, err := NewSQSPublisher(context.Background(), fake, SQSConfig{
		DestinationID: "billing", Logger: logger,
		Targets: []SQSTarget{{Name: "payments"}},
	})
	if err != nil {
		t.Fatalf("ctor: %v", err)
	}
	line := lastQueueLogLine(t, &buf, "queue exists")
	if line == nil || line["level"] != "DEBUG" || line["destination"] != "billing" || line["target"] != "payments" {
		t.Fatalf("missing queue-exists debug log: %v", line)
	}
}

// containsAny returns true if s contains any of the given substrings.
func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if len(s) >= len(sub) {
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
		}
	}
	return false
}
