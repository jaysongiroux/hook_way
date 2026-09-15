package queue

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	snstypes "github.com/aws/aws-sdk-go-v2/service/sns/types"
)

type fakeSNS struct {
	topics     map[string]string            // name -> arn
	attrs      map[string]map[string]string // arn -> attributes
	published  []*sns.PublishInput
	created    map[string]map[string]string
	setCalls   []*sns.SetTopicAttributesInput
	attrErrors map[string]error // arn -> error returned by GetTopicAttributes
}

func newFakeSNS() *fakeSNS {
	return &fakeSNS{
		topics:     map[string]string{},
		attrs:      map[string]map[string]string{},
		created:    map[string]map[string]string{},
		attrErrors: map[string]error{},
	}
}

func (f *fakeSNS) CreateTopic(ctx context.Context, in *sns.CreateTopicInput, _ ...func(*sns.Options)) (*sns.CreateTopicOutput, error) {
	name := aws.ToString(in.Name)
	arn := "arn:aws:sns:us-east-1:000000000000:" + name
	f.topics[name] = arn
	f.created[name] = in.Attributes
	f.attrs[arn] = in.Attributes
	return &sns.CreateTopicOutput{TopicArn: aws.String(arn)}, nil
}

func (f *fakeSNS) GetTopicAttributes(ctx context.Context, in *sns.GetTopicAttributesInput, _ ...func(*sns.Options)) (*sns.GetTopicAttributesOutput, error) {
	arn := aws.ToString(in.TopicArn)
	if err, ok := f.attrErrors[arn]; ok {
		return nil, err
	}
	return &sns.GetTopicAttributesOutput{Attributes: f.attrs[arn]}, nil
}

func (f *fakeSNS) SetTopicAttributes(ctx context.Context, in *sns.SetTopicAttributesInput, _ ...func(*sns.Options)) (*sns.SetTopicAttributesOutput, error) {
	f.setCalls = append(f.setCalls, in)
	arn := aws.ToString(in.TopicArn)
	if f.attrs[arn] == nil {
		f.attrs[arn] = map[string]string{}
	}
	f.attrs[arn][aws.ToString(in.AttributeName)] = aws.ToString(in.AttributeValue)
	return &sns.SetTopicAttributesOutput{}, nil
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

// seedTopic registers a pre-existing topic with attributes.
func (f *fakeSNS) seedTopic(name string, attrs map[string]string) string {
	arn := "arn:aws:sns:us-east-1:000000000000:" + name
	f.topics[name] = arn
	f.attrs[arn] = attrs
	return arn
}

func TestSNSPublisherCreatesTopicAndPublishes(t *testing.T) {
	fake := newFakeSNS()
	pub, err := NewSNSPublisher(context.Background(), fake, SNSConfig{
		Region:  "us-east-1",
		Policy:  ConflictReconcile,
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

func TestSNSPublisherAttachesMessageAttributes(t *testing.T) {
	fake := newFakeSNS()
	pub, err := NewSNSPublisher(context.Background(), fake, SNSConfig{
		Region:  "us-east-1",
		Policy:  ConflictReconcile,
		Targets: []SNSTarget{{Name: "billing-alerts", Create: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	err = pub.Publish(context.Background(), "alerts", "billing-alerts", Message{
		ID: "evt_1",
		MessageAttributes: map[string]MessageAttribute{
			"fields_changed": {DataType: "String.Array", StringValue: `["status"]`},
			"event":          {DataType: "String", StringValue: "opened"},
			"count":          {DataType: "Number", StringValue: "2"},
		},
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	got := fake.published[0].MessageAttributes
	if aws.ToString(got["fields_changed"].DataType) != "String.Array" || aws.ToString(got["fields_changed"].StringValue) != `["status"]` {
		t.Fatalf("fields_changed = %+v", got["fields_changed"])
	}
	if aws.ToString(got["event"].DataType) != "String" {
		t.Fatalf("event DataType = %q", aws.ToString(got["event"].DataType))
	}
	if aws.ToString(got["count"].DataType) != "Number" {
		t.Fatalf("count DataType = %q", aws.ToString(got["count"].DataType))
	}
}

func TestSNSPublisherRejectsOversizeMessageAttribute(t *testing.T) {
	fake := newFakeSNS()
	pub, err := NewSNSPublisher(context.Background(), fake, SNSConfig{
		Region:  "us-east-1",
		Policy:  ConflictReconcile,
		Targets: []SNSTarget{{Name: "billing-alerts", Create: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	err = pub.Publish(context.Background(), "alerts", "billing-alerts", Message{
		MessageAttributes: map[string]MessageAttribute{
			"too_big": {DataType: "String", StringValue: strings.Repeat("x", maxMessageAttributeValueBytes+1)},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "AWS limit") {
		t.Fatalf("err = %v, want AWS size-limit error", err)
	}
	if len(fake.published) != 0 {
		t.Fatal("oversize attributes must fail closed, not publish")
	}
}

func TestSNSPublisherFIFOSetsGroupAndDedup(t *testing.T) {
	fake := newFakeSNS()
	pub, err := NewSNSPublisher(context.Background(), fake, SNSConfig{
		Region: "us-east-1",
		Policy: ConflictReconcile,
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
		Policy: ConflictReconcile,
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

// TestSNSPublisherFIFOCreateFalseRequiresContentBasedDedup asserts that when
// an existing FIFO topic is missing ContentBasedDeduplication and the policy is
// ConflictError, NewSNSPublisher returns an error mentioning the attribute.
func TestSNSPublisherFIFOCreateFalseRequiresContentBasedDedup(t *testing.T) {
	fake := newFakeSNS()
	fake.seedTopic("orders.fifo", map[string]string{"FifoTopic": "true"}) // CBD absent

	_, err := NewSNSPublisher(context.Background(), fake, SNSConfig{
		Region: "us-east-1",
		Policy: ConflictError,
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
	fake.seedTopic("orders.fifo", map[string]string{
		"FifoTopic":                 "true",
		"ContentBasedDeduplication": "true",
	})

	pub, err := NewSNSPublisher(context.Background(), fake, SNSConfig{
		Region: "us-east-1",
		Policy: ConflictReconcile,
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

func TestSNSReconcileSetsContentBasedDedup(t *testing.T) {
	fake := newFakeSNS()
	arn := fake.seedTopic("alerts.fifo", map[string]string{"FifoTopic": "true"}) // CBD absent
	_, err := NewSNSPublisher(context.Background(), fake, SNSConfig{
		Policy:  ConflictReconcile,
		Targets: []SNSTarget{{Name: "alerts.fifo", Create: false, FIFO: &FIFOSettings{GroupIDSource: "endpoint", DedupContentBasedFallback: true}}},
	})
	if err != nil {
		t.Fatalf("NewSNSPublisher: %v", err)
	}
	if fake.attrs[arn]["ContentBasedDeduplication"] != "true" {
		t.Fatalf("expected CBD reconciled to true, got %q", fake.attrs[arn]["ContentBasedDeduplication"])
	}
}

func TestSNSWarnLeavesTopicUnchanged(t *testing.T) {
	fake := newFakeSNS()
	arn := fake.seedTopic("alerts.fifo", map[string]string{"FifoTopic": "true"})
	_, err := NewSNSPublisher(context.Background(), fake, SNSConfig{
		Policy:  ConflictWarn,
		Targets: []SNSTarget{{Name: "alerts.fifo", Create: false, FIFO: &FIFOSettings{GroupIDSource: "endpoint", DedupContentBasedFallback: true}}},
	})
	if err != nil {
		t.Fatalf("NewSNSPublisher: %v", err)
	}
	if len(fake.setCalls) != 0 {
		t.Fatal("warn must not call SetTopicAttributes")
	}
	if _, ok := fake.attrs[arn]["ContentBasedDeduplication"]; ok {
		t.Fatal("warn must leave the topic unchanged")
	}
}

func TestSNSImmutableFifoMismatchErrors(t *testing.T) {
	fake := newFakeSNS()
	fake.seedTopic("alerts.fifo", map[string]string{"FifoTopic": ""}) // existing standard topic
	_, err := NewSNSPublisher(context.Background(), fake, SNSConfig{
		Policy:  ConflictReconcile, // immutable still errors
		Targets: []SNSTarget{{Name: "alerts.fifo", Create: false, FIFO: &FIFOSettings{GroupIDSource: "endpoint", DedupContentBasedFallback: true}}},
	})
	if err == nil {
		t.Fatal("expected immutable FifoTopic conflict error")
	}
}

// TestSNSLogsCreatedTopic asserts that NewSNSPublisher emits an Info
// "created topic" log when the topic is absent and Create=true.
func TestSNSLogsCreatedTopic(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	fake := newFakeSNS() // no topics seeded → will create
	_, err := NewSNSPublisher(context.Background(), fake, SNSConfig{
		DestinationID: "alerts", Logger: logger,
		Targets: []SNSTarget{{Name: "billing-events", Create: true}},
	})
	if err != nil {
		t.Fatalf("ctor: %v", err)
	}
	line := lastQueueLogLine(t, &buf, "created topic")
	if line == nil || line["level"] != "INFO" || line["destination"] != "alerts" || line["target"] != "billing-events" {
		t.Fatalf("missing created-topic info log: %v", line)
	}
}

// TestSNSLogsTopicExists asserts that NewSNSPublisher emits a Debug
// "topic exists" log when the topic already exists.
func TestSNSLogsTopicExists(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	fake := newFakeSNS()
	fake.seedTopic("payments", map[string]string{})
	_, err := NewSNSPublisher(context.Background(), fake, SNSConfig{
		DestinationID: "billing", Logger: logger,
		Targets: []SNSTarget{{Name: "payments"}},
	})
	if err != nil {
		t.Fatalf("ctor: %v", err)
	}
	line := lastQueueLogLine(t, &buf, "topic exists")
	if line == nil || line["level"] != "DEBUG" || line["destination"] != "billing" || line["target"] != "payments" {
		t.Fatalf("missing topic-exists debug log: %v", line)
	}
}
