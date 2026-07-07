package queue

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

type fakeKafkaOps struct {
	writes    []fakeKafkaWrite
	writeErr  error
	readyErr  error
	readyCall int
	closed    bool
}

type fakeKafkaWrite struct {
	topic string
	key   string
	value []byte
}

func (f *fakeKafkaOps) Write(ctx context.Context, topic string, key, value []byte) error {
	if f.writeErr != nil {
		return f.writeErr
	}
	f.writes = append(f.writes, fakeKafkaWrite{topic: topic, key: string(key), value: value})
	return nil
}
func (f *fakeKafkaOps) Ready(ctx context.Context) error { f.readyCall++; return f.readyErr }
func (f *fakeKafkaOps) Close() error                    { f.closed = true; return nil }

func TestKafkaPublisher_PublishWithPartitionKey(t *testing.T) {
	fake := &fakeKafkaOps{}
	p := NewKafkaPublisher(fake, KafkaPublisherConfig{
		DestinationID: "events",
		Targets: []KafkaTarget{
			{Topic: "billing-events", PartitionKey: &FIFOSettings{GroupIDSource: "body", GroupIDPath: "account.id"}},
		},
	})
	msg := Message{ID: "m1", Endpoint: "ep", Body: map[string]any{"account": map[string]any{"id": "acct-9"}}}
	if err := p.Publish(context.Background(), "events", "billing-events", msg); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if len(fake.writes) != 1 {
		t.Fatalf("expected 1 write, got %d", len(fake.writes))
	}
	w := fake.writes[0]
	if w.topic != "billing-events" {
		t.Errorf("topic: got %q", w.topic)
	}
	if w.key != "acct-9" {
		t.Errorf("key: got %q want acct-9", w.key)
	}
	var decoded Message
	if err := json.Unmarshal(w.value, &decoded); err != nil {
		t.Fatalf("value is not the JSON envelope: %v", err)
	}
	if decoded.ID != "m1" {
		t.Errorf("decoded id: got %q", decoded.ID)
	}
}

func TestKafkaPublisher_PublishWithoutPartitionKey(t *testing.T) {
	fake := &fakeKafkaOps{}
	p := NewKafkaPublisher(fake, KafkaPublisherConfig{
		DestinationID: "events",
		Targets:       []KafkaTarget{{Topic: "t1"}},
	})
	msg := Message{ID: "m1", Endpoint: "ep"}
	if err := p.Publish(context.Background(), "events", "t1", msg); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if fake.writes[0].key != "" {
		t.Errorf("expected empty key for round-robin, got %q", fake.writes[0].key)
	}
}

func TestKafkaPublisher_UnknownTarget(t *testing.T) {
	fake := &fakeKafkaOps{}
	p := NewKafkaPublisher(fake, KafkaPublisherConfig{DestinationID: "events", Targets: []KafkaTarget{{Topic: "t1"}}})
	err := p.Publish(context.Background(), "events", "nope", Message{ID: "m1"})
	if err == nil {
		t.Fatalf("expected error for unknown target")
	}
}

func TestKafkaPublisher_ReadyAndClose(t *testing.T) {
	fake := &fakeKafkaOps{readyErr: errors.New("down")}
	p := NewKafkaPublisher(fake, KafkaPublisherConfig{DestinationID: "events"})
	if err := p.Ready(context.Background()); err == nil {
		t.Fatalf("expected Ready to surface ops error")
	}
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !fake.closed {
		t.Fatalf("Close did not reach ops")
	}
}
