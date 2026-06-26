package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

type fakeSQS struct {
	created    map[string]map[string]string // name -> attributes
	sent       []*sqs.SendMessageInput
	urls       map[string]string
	attrErrors map[string]error            // url -> error returned by GetQueueAttributes
	queueAttrs map[string]map[string]string // url -> attributes
}

func newFakeSQS() *fakeSQS {
	return &fakeSQS{
		created:    map[string]map[string]string{},
		urls:       map[string]string{},
		attrErrors: map[string]error{},
		queueAttrs: map[string]map[string]string{},
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
	return &sqs.CreateQueueOutput{QueueUrl: aws.String(url)}, nil
}

func (f *fakeSQS) GetQueueAttributes(ctx context.Context, in *sqs.GetQueueAttributesInput, _ ...func(*sqs.Options)) (*sqs.GetQueueAttributesOutput, error) {
	if err, ok := f.attrErrors[aws.ToString(in.QueueUrl)]; ok {
		return nil, err
	}
	attrs := map[string]string{}
	if m, ok := f.queueAttrs[aws.ToString(in.QueueUrl)]; ok {
		for k, v := range m {
			attrs[k] = v
		}
	}
	return &sqs.GetQueueAttributesOutput{Attributes: attrs}, nil
}

func (f *fakeSQS) SendMessage(ctx context.Context, in *sqs.SendMessageInput, _ ...func(*sqs.Options)) (*sqs.SendMessageOutput, error) {
	f.sent = append(f.sent, in)
	return &sqs.SendMessageOutput{MessageId: aws.String("m-1")}, nil
}

func TestSQSPublisherCreatesQueueAndSends(t *testing.T) {
	fake := newFakeSQS()
	pub, err := NewSQSPublisher(context.Background(), fake, SQSConfig{
		Region:  "us-east-1",
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
	pub, err := NewSQSPublisher(context.Background(), fake, SQSConfig{Region: "us-east-1", Targets: []SQSTarget{{Name: "known"}}})
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

func TestSQSPublisherFIFOCreateFalseRequiresContentBasedDedup(t *testing.T) {
	fake := newFakeSQS()
	url := "http://localstack/000000000000/orders.fifo"
	fake.urls["orders.fifo"] = url
	// queueAttrs not set — ContentBasedDeduplication absent

	_, err := NewSQSPublisher(context.Background(), fake, SQSConfig{
		Region: "us-east-1",
		Targets: []SQSTarget{{
			Name:   "orders.fifo",
			Create: false,
			FIFO:   &FIFOSettings{GroupIDSource: "endpoint", DedupContentBasedFallback: true},
		}},
	})
	if err == nil {
		t.Fatal("expected constructor to fail when ContentBasedDeduplication is not enabled")
	}
	if !strings.Contains(err.Error(), "ContentBasedDeduplication") {
		t.Fatalf("expected ContentBasedDeduplication in error, got: %v", err)
	}
}

func TestSQSPublisherFIFOCreateFalseWithContentBasedDedupSucceeds(t *testing.T) {
	fake := newFakeSQS()
	url := "http://localstack/000000000000/orders.fifo"
	fake.urls["orders.fifo"] = url
	fake.queueAttrs[url] = map[string]string{"ContentBasedDeduplication": "true"}

	pub, err := NewSQSPublisher(context.Background(), fake, SQSConfig{
		Region: "us-east-1",
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
