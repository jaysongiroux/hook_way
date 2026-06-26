package queue

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	snstypes "github.com/aws/aws-sdk-go-v2/service/sns/types"
)

type fakeSNS struct {
	topics     map[string]string            // name -> arn
	published  []*sns.PublishInput
	created    map[string]map[string]string
	attrErrors map[string]error             // arn -> error returned by GetTopicAttributes
	topicAttrs map[string]map[string]string // arn -> attributes
}

func newFakeSNS() *fakeSNS {
	return &fakeSNS{
		topics:     map[string]string{},
		created:    map[string]map[string]string{},
		attrErrors: map[string]error{},
		topicAttrs: map[string]map[string]string{},
	}
}

func (f *fakeSNS) CreateTopic(ctx context.Context, in *sns.CreateTopicInput, _ ...func(*sns.Options)) (*sns.CreateTopicOutput, error) {
	name := aws.ToString(in.Name)
	arn := "arn:aws:sns:us-east-1:000000000000:" + name
	f.topics[name] = arn
	f.created[name] = in.Attributes
	return &sns.CreateTopicOutput{TopicArn: aws.String(arn)}, nil
}

func (f *fakeSNS) GetTopicAttributes(ctx context.Context, in *sns.GetTopicAttributesInput, _ ...func(*sns.Options)) (*sns.GetTopicAttributesOutput, error) {
	if err, ok := f.attrErrors[aws.ToString(in.TopicArn)]; ok {
		return nil, err
	}
	attrs := map[string]string{}
	if m, ok := f.topicAttrs[aws.ToString(in.TopicArn)]; ok {
		for k, v := range m {
			attrs[k] = v
		}
	}
	return &sns.GetTopicAttributesOutput{Attributes: attrs}, nil
}

func (f *fakeSNS) ListTopics(ctx context.Context, in *sns.ListTopicsInput, _ ...func(*sns.Options)) (*sns.ListTopicsOutput, error) {
	out := &sns.ListTopicsOutput{}
	for _, arn := range f.topics {
		out.Topics = append(out.Topics, snstypes.Topic{TopicArn: aws.String(arn)})
	}
	return out, nil
}

func (f *fakeSNS) Publish(ctx context.Context, in *sns.PublishInput, _ ...func(*sns.Options)) (*sns.PublishOutput, error) {
	f.published = append(f.published, in)
	return &sns.PublishOutput{MessageId: aws.String("m-1")}, nil
}

func TestSNSPublisherCreatesTopicAndPublishes(t *testing.T) {
	fake := newFakeSNS()
	pub, err := NewSNSPublisher(context.Background(), fake, SNSConfig{
		Region:  "us-east-1",
		Targets: []SNSTarget{{Name: "billing-alerts", Create: true, Subject: "billing event"}},
	})
	if err != nil {
		t.Fatalf("NewSNSPublisher: %v", err)
	}
	if _, ok := fake.topics["billing-alerts"]; !ok {
		t.Fatalf("topic not created: %#v", fake.topics)
	}
	if err := pub.Publish(context.Background(), "alerts", "billing-alerts", Message{ID: "evt_1"}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if len(fake.published) != 1 {
		t.Fatalf("expected 1 publish, got %d", len(fake.published))
	}
	if aws.ToString(fake.published[0].Subject) != "billing event" {
		t.Fatalf("subject = %q", aws.ToString(fake.published[0].Subject))
	}
	if aws.ToString(fake.published[0].TopicArn) != "arn:aws:sns:us-east-1:000000000000:billing-alerts" {
		t.Fatalf("topic arn = %q", aws.ToString(fake.published[0].TopicArn))
	}
}

func TestSNSPublisherFIFOSetsGroupAndDedup(t *testing.T) {
	fake := newFakeSNS()
	pub, err := NewSNSPublisher(context.Background(), fake, SNSConfig{
		Region: "us-east-1",
		Targets: []SNSTarget{{
			Name: "billing-alerts.fifo", Create: true,
			FIFO: &FIFOSettings{GroupIDSource: "endpoint", DedupUseIdempotencyKey: true},
		}},
	})
	if err != nil {
		t.Fatalf("NewSNSPublisher: %v", err)
	}
	if fake.created["billing-alerts.fifo"]["FifoTopic"] != "true" {
		t.Fatalf("expected FifoTopic=true: %#v", fake.created["billing-alerts.fifo"])
	}
	if err := pub.Publish(context.Background(), "alerts", "billing-alerts.fifo", Message{ID: "evt_1", Endpoint: "billing", IdempotencyKey: "idem-9"}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	got := fake.published[0]
	if aws.ToString(got.MessageGroupId) != "billing" || aws.ToString(got.MessageDeduplicationId) != "idem-9" {
		t.Fatalf("group/dedup = %q/%q", aws.ToString(got.MessageGroupId), aws.ToString(got.MessageDeduplicationId))
	}
}

// TestSNSPublisherFailsFastOnMissingTopic asserts that NewSNSPublisher fails
// immediately when a target has Create:false and the topic does not exist in
// the ListTopics response.
func TestSNSPublisherFailsFastOnMissingTopic(t *testing.T) {
	fake := newFakeSNS() // empty — no topics seeded
	_, err := NewSNSPublisher(context.Background(), fake, SNSConfig{
		Region:  "us-east-1",
		Targets: []SNSTarget{{Name: "missing-topic", Create: false}},
	})
	if err == nil {
		t.Fatal("expected NewSNSPublisher to fail for missing topic with Create:false")
	}
}

// TestSNSPublisherReadyProbesAllTargets asserts that Ready checks every resolved
// topic, not just the last one. A publisher with two targets where one target's
// GetTopicAttributes returns an error must cause Ready to return an error. When
// all targets are healthy Ready returns nil.
func TestSNSPublisherReadyProbesAllTargets(t *testing.T) {
	fake := newFakeSNS()
	// Seed two topics so Create:false resolves via ListTopics.
	fake.topics["alerts-a"] = "arn:aws:sns:us-east-1:000000000000:alerts-a"
	fake.topics["alerts-b"] = "arn:aws:sns:us-east-1:000000000000:alerts-b"

	pub, err := NewSNSPublisher(context.Background(), fake, SNSConfig{
		Region: "us-east-1",
		Targets: []SNSTarget{
			{Name: "alerts-a"},
			{Name: "alerts-b"},
		},
	})
	if err != nil {
		t.Fatalf("NewSNSPublisher: %v", err)
	}

	// All healthy — Ready must return nil.
	if err := pub.Ready(context.Background()); err != nil {
		t.Fatalf("Ready with all healthy targets: %v", err)
	}

	// Inject a failure on alerts-a's ARN (the first target resolved).
	fake.attrErrors["arn:aws:sns:us-east-1:000000000000:alerts-a"] = fmt.Errorf("connection refused")
	if err := pub.Ready(context.Background()); err == nil {
		t.Fatal("expected Ready to return an error when one target is unhealthy")
	}
}

func TestSNSPublisherFIFOCreateFalseRequiresContentBasedDedup(t *testing.T) {
	fake := newFakeSNS()
	arn := "arn:aws:sns:us-east-1:000000000000:orders.fifo"
	fake.topics["orders.fifo"] = arn
	// topicAttrs not set

	_, err := NewSNSPublisher(context.Background(), fake, SNSConfig{
		Region: "us-east-1",
		Targets: []SNSTarget{{
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

func TestSNSPublisherFIFOCreateFalseWithContentBasedDedupSucceeds(t *testing.T) {
	fake := newFakeSNS()
	arn := "arn:aws:sns:us-east-1:000000000000:orders.fifo"
	fake.topics["orders.fifo"] = arn
	fake.topicAttrs[arn] = map[string]string{"ContentBasedDeduplication": "true"}

	pub, err := NewSNSPublisher(context.Background(), fake, SNSConfig{
		Region: "us-east-1",
		Targets: []SNSTarget{{
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
