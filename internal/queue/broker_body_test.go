package queue

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/smithy-go"

	"github.com/jaysongiroux/hook_way/internal/deliverymeta"
)

func TestBrokerBodyModes(t *testing.T) {
	msg := Message{ID: "event", Body: map[string]any{"x": 1}}
	want, _ := json.Marshal(msg)
	got, err := BrokerBody(msg)
	if err != nil || string(got) != string(want) {
		t.Fatalf("default changed: %s %v", got, err)
	}
	for _, mode := range []string{"raw", "json", "json_js"} {
		msg.Publish = &deliverymeta.Publish{WireBody: mode, Body: []byte(`{"b":2,"a":1}`)}
		got, err = BrokerBody(msg)
		if err != nil || string(got) != string(msg.Publish.Body) {
			t.Fatalf("mode %s: %s %v", mode, got, err)
		}
	}
	msg.Publish.Body = nil
	if _, err := BrokerBody(msg); err == nil {
		t.Fatal("accepted empty body")
	}
}

func TestBrokersSendFrozenFIFOBody(t *testing.T) {
	msg := Message{ID: "transport-event", Publish: &deliverymeta.Publish{WireBody: "json_js", Body: []byte(`{"b":2,"a":1}`), FIFO: &deliverymeta.FIFO{GroupID: "legacy-group", DeduplicationID: "legacy-dedup"}}, MessageAttributes: map[string]MessageAttribute{"changed": {DataType: "String.Array", StringValue: `["state"]`}}}
	sq := newFakeSQS()
	sp, err := NewSQSPublisher(context.Background(), sq, SQSConfig{Targets: []SQSTarget{{Name: "events.fifo", Create: true}}})
	if err != nil {
		t.Fatal(err)
	}
	if err = sp.Publish(context.Background(), "broker", "events.fifo", msg); err != nil {
		t.Fatal(err)
	}
	input := sq.sent[0]
	if aws.ToString(input.MessageBody) != string(msg.Publish.Body) || aws.ToString(input.MessageGroupId) != "legacy-group" || aws.ToString(input.MessageDeduplicationId) != "legacy-dedup" {
		t.Fatalf("SQS input = %+v", input)
	}
	sn := newFakeSNS()
	np, err := NewSNSPublisher(context.Background(), sn, SNSConfig{Targets: []SNSTarget{{Name: "events.fifo", Create: true, Subject: "unused"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err = np.Publish(context.Background(), "broker", "events.fifo", msg); err != nil {
		t.Fatal(err)
	}
	publication := sn.published[0]
	if aws.ToString(publication.Message) != string(msg.Publish.Body) || aws.ToString(publication.MessageGroupId) != "legacy-group" || aws.ToString(publication.MessageDeduplicationId) != "legacy-dedup" || publication.Subject != nil || aws.ToString(publication.MessageAttributes["changed"].DataType) != "String.Array" {
		t.Fatalf("SNS input = %+v", publication)
	}
	msg.Publish.FIFO.GroupID = "invalid space"
	if err = sp.Publish(context.Background(), "broker", "events.fifo", msg); err == nil || len(sq.sent) != 1 {
		t.Fatal("invalid ID reached broker")
	}
	msg.Publish.Body = []byte(strings.Repeat("x", 1048577))
	if err = sp.Publish(context.Background(), "broker", "events.fifo", msg); err == nil || len(sq.sent) != 1 {
		t.Fatal("oversized body reached broker")
	}
}

func TestBrokerRetriesAreBoundedAndClassified(t *testing.T) {
	msg := Message{Publish: &deliverymeta.Publish{Retry: &deliverymeta.Retry{MaxAttempts: 3, InitialBackoff: int64(time.Millisecond), MaxBackoff: int64(2 * time.Millisecond)}}}
	transient := &smithy.GenericAPIError{Code: "ThrottlingException", Message: "retry", Fault: smithy.FaultClient}
	attempts := 0
	err := brokerRetry(context.Background(), msg, func(explicit bool) error {
		if !explicit {
			t.Error("SDK retry not disabled")
		}
		attempts++
		if attempts < 3 {
			return transient
		}
		return nil
	})
	if err != nil || attempts != 3 {
		t.Fatalf("attempts=%d, err=%v", attempts, err)
	}
	attempts = 0
	permanent := errors.New("invalid parameter")
	err = brokerRetry(context.Background(), msg, func(bool) error { attempts++; return permanent })
	if !errors.Is(err, permanent) || attempts != 1 {
		t.Fatalf("permanent retried: %d %v", attempts, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	attempts = 0
	err = brokerRetry(ctx, msg, func(bool) error { attempts++; cancel(); return transient })
	if !errors.Is(err, context.Canceled) || attempts != 1 {
		t.Fatalf("cancellation: %d %v", attempts, err)
	}
	attempts = 0
	err = brokerRetry(context.Background(), Message{}, func(explicit bool) error {
		attempts++
		if explicit {
			t.Error("changed default SDK retry")
		}
		return nil
	})
	if err != nil || attempts != 1 {
		t.Fatal("default retry changed")
	}
}

func TestSQSRawBodyCharacterConstraints(t *testing.T) {
	if err := validateSQSBody([]byte("tab\tnewline\nCR\rUnicode é 😀")); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{"nul\x00", "control\x01", "noncharacter\uffff"} {
		if err := validateSQSBody([]byte(body)); err == nil {
			t.Fatal("accepted an unsupported SQS body character")
		}
	}
}

func BenchmarkSQSBodyValidation(b *testing.B) {
	body := []byte(strings.Repeat("JSON text with Unicode é 😀\t\n", 200))
	b.ReportAllocs()
	b.SetBytes(int64(len(body)))
	for b.Loop() {
		if err := validateSQSBody(body); err != nil {
			b.Fatal(err)
		}
	}
}

func TestStandardBrokerOptions(t *testing.T) {
	delay := int32(45)
	msg := Message{Publish: &deliverymeta.Publish{WireBody: "raw", Body: []byte(`{"default":"hello","sqs":"queue text"}`), GroupID: "sample-group", DelaySeconds: &delay}}
	sq := newFakeSQS()
	sp, err := NewSQSPublisher(context.Background(), sq, SQSConfig{Targets: []SQSTarget{{Name: "standard", Create: true, Attributes: map[string]string{"DelaySeconds": "10", "SqsManagedSseEnabled": "true"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := sp.Publish(context.Background(), "broker", "standard", msg); err != nil {
		t.Fatal(err)
	}
	in := sq.sent[0]
	if in.DelaySeconds != 45 || aws.ToString(in.MessageGroupId) != "sample-group" || in.MessageDeduplicationId != nil {
		t.Fatalf("SQS input: %+v", in)
	}
	msg.Publish.DelaySeconds = nil
	msg.Publish.Subject = "sample subject"
	msg.Publish.MessageStructure = "json"
	sn := newFakeSNS()
	np, err := NewSNSPublisher(context.Background(), sn, SNSConfig{Targets: []SNSTarget{{Name: "standard", Create: true, Attributes: map[string]string{"TracingConfig": "PassThrough"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := np.Publish(context.Background(), "broker", "standard", msg); err != nil {
		t.Fatal(err)
	}
	out := sn.published[0]
	if aws.ToString(out.Subject) != "sample subject" || aws.ToString(out.MessageStructure) != "json" || aws.ToString(out.MessageGroupId) != "sample-group" || out.MessageDeduplicationId != nil {
		t.Fatalf("SNS input: %+v", out)
	}
	for _, invalid := range []string{`{}`, `{"default":1}`, `null`, `[]`} {
		msg.Publish.Body = []byte(invalid)
		if np.Publish(context.Background(), "broker", "standard", msg) == nil {
			t.Fatalf("accepted invalid protocol message %s", invalid)
		}
	}
	if len(sn.published) != 1 {
		t.Fatal("invalid messages reached SNS")
	}
}

func TestBrokerBinaryAndCustomAttributeTypes(t *testing.T) {
	attrs := map[string]MessageAttribute{"sample": {DataType: "Binary", StringValue: "AP8="}}
	sq, err := sqsMessageAttributes(attrs)
	if err != nil {
		t.Fatal(err)
	}
	sn, err := snsMessageAttributes(attrs)
	if err != nil {
		t.Fatal(err)
	}
	if string(sq["sample"].BinaryValue) != string([]byte{0, 255}) || sq["sample"].StringValue != nil || string(sn["sample"].BinaryValue) != string([]byte{0, 255}) || sn["sample"].StringValue != nil {
		t.Fatal("binary value was sent as text")
	}
	if err := validateBrokerSize([]byte("x"), attrs, 15); err != nil {
		t.Fatal("size counted base64 instead of decoded bytes", err)
	}
	attrs["sample"] = MessageAttribute{DataType: "Number.int", StringValue: "42"}
	if _, err := sqsMessageAttributes(attrs); err != nil {
		t.Fatal(err)
	}
	if _, err := snsMessageAttributes(attrs); err == nil {
		t.Fatal("SNS accepted an unsupported custom type")
	}
	for _, attr := range []MessageAttribute{{DataType: "Binary", StringValue: "bad"}, {DataType: "Number", StringValue: "NaN"}, {DataType: "String.Array", StringValue: "null"}} {
		if _, err := sqsMessageAttributes(map[string]MessageAttribute{"sample": attr}); err == nil {
			t.Fatalf("invalid attribute accepted: %+v", attr)
		}
	}
}

func TestExistingBrokerBindingsAndResourceReconciliation(t *testing.T) {
	sq := newFakeSQS()
	url := "http://localstack/111111111111/sample"
	sq.urls["sample"] = url
	sq.attrs[url] = map[string]string{"DelaySeconds": "0"}
	sp, err := NewSQSPublisher(context.Background(), sq, SQSConfig{Policy: ConflictReconcile, Targets: []SQSTarget{{Name: "sample", QueueOwnerAccountID: "111111111111", Attributes: map[string]string{"DelaySeconds": "10"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(sq.lookups) != 1 || aws.ToString(sq.lookups[0].QueueOwnerAWSAccountId) != "111111111111" || sq.attrs[url]["DelaySeconds"] != "10" {
		t.Fatal("cross-account lookup or attribute reconciliation lost")
	}
	if err := sp.Publish(context.Background(), "broker", "sample", Message{ID: "event"}); err != nil {
		t.Fatal(err)
	}
	sn := newFakeSNS()
	arn := "arn:aws:sns:us-east-1:111111111111:sample"
	sn.attrs[arn] = map[string]string{}
	np, err := NewSNSPublisher(context.Background(), sn, SNSConfig{Policy: ConflictReconcile, Targets: []SNSTarget{{Name: "sample", ARN: arn, Attributes: map[string]string{"TracingConfig": "PassThrough"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if sn.listCalls != 0 || sn.attrs[arn]["TracingConfig"] != "PassThrough" {
		t.Fatal("explicit ARN used discovery or dropped managed attributes")
	}
	if err := np.Publish(context.Background(), "broker", "sample", Message{ID: "event"}); err != nil {
		t.Fatal(err)
	}
	if aws.ToString(sn.published[0].TopicArn) != arn {
		t.Fatal("explicit topic ARN was lost")
	}
}
