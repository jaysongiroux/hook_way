package queue

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
)

var fifoEnvMatcher = mustMatcher("{env}-jobs.fifo", map[string]string{"env": "^qa-gm[0-9]+$"})

func TestSQSPublisherDynamicTargetCreatesOnFirstSight(t *testing.T) {
	fake := newFakeSQS()
	fake.seedQueue("static", nil)
	pub, err := NewSQSPublisher(context.Background(), fake, SQSConfig{
		DestinationID: "queues", Policy: ConflictReconcile,
		Targets: []SQSTarget{
			{Name: "static"},
			{Name: "{env}-events", Create: true, Template: envMatcher, VisibilityTimeout: 30e9},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := fake.created["{env}-events"]; ok {
		t.Fatal("template must not be created at startup")
	}
	for range 2 {
		if err := pub.Publish(context.Background(), "queues", "qa-gm3-events", dynamicMsg()); err != nil {
			t.Fatalf("dynamic publish: %v", err)
		}
	}
	attrs, created := fake.created["qa-gm3-events"]
	if !created || attrs["VisibilityTimeout"] != "30" {
		t.Fatalf("rendered queue should be created once with template attrs: %#v", fake.created)
	}
	if len(fake.sent) != 2 || aws.ToString(fake.sent[0].QueueUrl) != fake.urls["qa-gm3-events"] {
		t.Fatalf("sends should target the rendered queue url: %#v", fake.sent)
	}
	if err := pub.Publish(context.Background(), "queues", "prod-events", dynamicMsg()); err == nil {
		t.Fatal("non-matching rendered target must be rejected")
	}
	// Ready probes only the static queue.
	fake.attrErrors[fake.urls["qa-gm3-events"]] = errors.New("boom")
	if err := pub.Ready(context.Background()); err != nil {
		t.Fatalf("Ready must ignore dynamic targets: %v", err)
	}
}

func TestSQSPublisherDynamicTargetMissingWithoutCreateFails(t *testing.T) {
	fake := newFakeSQS()
	pub, err := NewSQSPublisher(context.Background(), fake, SQSConfig{DestinationID: "queues", Targets: []SQSTarget{{Name: "{env}-events", Template: envMatcher}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := pub.Publish(context.Background(), "queues", "qa-gm3-events", dynamicMsg()); err == nil {
		t.Fatal("missing queue with create:false must fail the publish")
	}
	// The failure is not cached: once the queue exists the next publish succeeds.
	fake.seedQueue("qa-gm3-events", nil)
	if err := pub.Publish(context.Background(), "queues", "qa-gm3-events", dynamicMsg()); err != nil || len(fake.sent) != 1 {
		t.Fatalf("publish after queue appears: err=%v sent=%d", err, len(fake.sent))
	}
}

func TestSQSPublisherDynamicExistingQueueIsReconciled(t *testing.T) {
	fake := newFakeSQS()
	url := fake.seedQueue("qa-gm3-events", map[string]string{"VisibilityTimeout": "10"})
	pub, err := NewSQSPublisher(context.Background(), fake, SQSConfig{
		DestinationID: "queues", Policy: ConflictReconcile,
		Targets: []SQSTarget{{Name: "{env}-events", Create: true, Template: envMatcher, VisibilityTimeout: 30e9}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := pub.Publish(context.Background(), "queues", "qa-gm3-events", dynamicMsg()); err != nil {
		t.Fatal(err)
	}
	if len(fake.setCalls) != 1 || aws.ToString(fake.setCalls[0].QueueUrl) != url || fake.setCalls[0].Attributes["VisibilityTimeout"] != "30" {
		t.Fatalf("existing dynamic queue should be reconciled: %#v", fake.setCalls)
	}
}

func TestSQSPublisherDynamicFIFOTemplate(t *testing.T) {
	fake := newFakeSQS()
	pub, err := NewSQSPublisher(context.Background(), fake, SQSConfig{
		DestinationID: "queues",
		Targets: []SQSTarget{{
			Name: "{env}-jobs.fifo", Create: true, Template: fifoEnvMatcher,
			FIFO: &FIFOSettings{GroupIDSource: "endpoint", DedupContentBasedFallback: true},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := pub.Publish(context.Background(), "queues", "qa-gm3-jobs.fifo", dynamicMsg()); err != nil {
		t.Fatal(err)
	}
	if fake.created["qa-gm3-jobs.fifo"]["FifoQueue"] != "true" || fake.created["qa-gm3-jobs.fifo"]["ContentBasedDeduplication"] != "true" {
		t.Fatalf("fifo attrs from rendered name: %#v", fake.created)
	}
	if aws.ToString(fake.sent[0].MessageGroupId) != "ep" {
		t.Fatalf("fifo group id from template settings: %#v", fake.sent[0])
	}
}

func TestSNSPublisherDynamicTarget(t *testing.T) {
	fake := newFakeSNS()
	fake.topics["existing-events"] = "arn:aws:sns:us-east-1:000000000000:existing-events"
	fake.attrs[fake.topics["existing-events"]] = map[string]string{}
	pub, err := NewSNSPublisher(context.Background(), fake, SNSConfig{
		DestinationID: "topics", Policy: ConflictReconcile,
		Targets: []SNSTarget{{Name: "{env}-events", Create: true, Template: mustMatcher("{env}-events", map[string]string{"env": "^[a-z]+$"}), Subject: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Existing topic found via ListTopics: no create.
	if err := pub.Publish(context.Background(), "topics", "existing-events", dynamicMsg()); err != nil {
		t.Fatal(err)
	}
	if _, created := fake.created["existing-events"]; created {
		t.Fatal("existing topic must not be re-created")
	}
	if len(fake.published) != 1 || aws.ToString(fake.published[0].TopicArn) != fake.topics["existing-events"] || aws.ToString(fake.published[0].Subject) != "hi" {
		t.Fatalf("publish to existing topic: %#v", fake.published)
	}
	// Unknown topic: created on first sight, once.
	for range 2 {
		if err := pub.Publish(context.Background(), "topics", "fresh-events", dynamicMsg()); err != nil {
			t.Fatal(err)
		}
	}
	if _, created := fake.created["fresh-events"]; !created || len(fake.created) != 1 {
		t.Fatalf("fresh topic should be created exactly once: %#v", fake.created)
	}
	if err := pub.Publish(context.Background(), "topics", "Bad-Events", dynamicMsg()); err == nil {
		t.Fatal("non-matching rendered target must be rejected")
	}
}

func TestSNSPublisherDynamicMissingWithoutCreateFails(t *testing.T) {
	fake := newFakeSNS()
	pub, err := NewSNSPublisher(context.Background(), fake, SNSConfig{DestinationID: "topics", Targets: []SNSTarget{{Name: "{env}-events", Template: envMatcher}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := pub.Publish(context.Background(), "topics", "qa-gm3-events", dynamicMsg()); err == nil {
		t.Fatal("missing topic with create:false must fail")
	}
}

func TestKafkaPublisherDynamicTargetEnsuresTopic(t *testing.T) {
	fake := &fakeKafkaOps{}
	pub := NewKafkaPublisher(fake, KafkaPublisherConfig{DestinationID: "events", Targets: []KafkaTarget{
		{Topic: "{env}-events", Create: true, Template: envMatcher, Partitions: 3, PartitionKey: &FIFOSettings{GroupIDSource: "endpoint"}},
		{Topic: "{env}-audit", Template: mustMatcher("{env}-audit", map[string]string{"env": "^qa-gm[0-9]+$"})},
	}})
	for range 2 {
		if err := pub.Publish(context.Background(), "events", "qa-gm3-events", dynamicMsg()); err != nil {
			t.Fatalf("dynamic publish: %v", err)
		}
	}
	if len(fake.ensured) != 1 || fake.ensured[0].Topic != "qa-gm3-events" || fake.ensured[0].Partitions != 3 {
		t.Fatalf("topic should be ensured once with template settings: %#v", fake.ensured)
	}
	if len(fake.writes) != 2 || fake.writes[0].topic != "qa-gm3-events" || fake.writes[0].key != "ep" {
		t.Fatalf("writes to rendered topic with template partition key: %#v", fake.writes)
	}
	if err := pub.Publish(context.Background(), "events", "qa-gm3-audit", dynamicMsg()); err != nil {
		t.Fatal(err)
	}
	if len(fake.ensured) != 1 {
		t.Fatalf("create:false template must not ensure topics: %#v", fake.ensured)
	}
}

func TestRedisStreamPublisherDynamicTargetCreatesGroup(t *testing.T) {
	var groups []string
	fake := &fakeRedisOpsWithGroupTracking{fakeRedisOps: newFakeRedisOps(), onXGroupCreate: func(stream, group, _ string) { groups = append(groups, stream+"/"+group) }}
	pub, err := NewRedisStreamPublisher(context.Background(), fake, RedisStreamConfig{
		KeyPrefix: "hw", DestinationID: "streams",
		Targets: []RedisStreamTarget{{Name: "{env}-events", MaxLen: 100, ConsumerGroup: "workers", Create: true, Template: envMatcher}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 0 {
		t.Fatalf("template must not create a group at startup: %v", groups)
	}
	for range 2 {
		if err := pub.Publish(context.Background(), "streams", "qa-gm3-events", dynamicMsg()); err != nil {
			t.Fatalf("dynamic publish: %v", err)
		}
	}
	if len(groups) != 1 || groups[0] != "hw:qa-gm3-events/workers" {
		t.Fatalf("group should be created once on the rendered stream: %v", groups)
	}
	xadds := fake.fakeRedisOps.xaddCalls
	if len(xadds) != 2 || xadds[0].stream != "hw:qa-gm3-events" || xadds[0].approxMaxLen != 100 {
		t.Fatalf("xadd to rendered stream with template maxlen: %#v", xadds)
	}
}
