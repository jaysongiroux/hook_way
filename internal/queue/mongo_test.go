package queue

import (
	"context"
	"errors"
	"testing"
	"time"
)

type mongoInsert struct {
	collection string
	doc        any
}

type fakeMongo struct {
	inserts   []mongoInsert
	insertErr error
	pingErr   error
	pinged    int
	closed    int
}

func (f *fakeMongo) InsertOne(ctx context.Context, collection string, doc any) error {
	f.inserts = append(f.inserts, mongoInsert{collection: collection, doc: doc})
	return f.insertErr
}
func (f *fakeMongo) Ping(ctx context.Context) error { f.pinged++; return f.pingErr }
func (f *fakeMongo) Close() error                   { f.closed++; return nil }

func newTestMongoPublisher(t *testing.T, fake *fakeMongo) *MongoPublisher {
	t.Helper()
	pub, err := NewMongoPublisher(context.Background(), fake, MongoConfig{
		DestinationID: "archive",
		Targets:       []MongoTarget{{Name: "events"}},
	})
	if err != nil {
		t.Fatalf("NewMongoPublisher: %v", err)
	}
	return pub
}

func TestMessageToDocument(t *testing.T) {
	msg := Message{
		ID: "evt_1", Endpoint: "orders_webhook", Queue: "events",
		ReceivedAt: time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC),
		Body:       map[string]any{"hello": "world"},
	}
	doc, err := messageToDocument(msg)
	if err != nil {
		t.Fatalf("messageToDocument: %v", err)
	}
	if doc["_id"] != "evt_1" {
		t.Fatalf("_id = %v, want evt_1", doc["_id"])
	}
	if doc["endpoint"] != "orders_webhook" {
		t.Fatalf("endpoint = %v", doc["endpoint"])
	}
	body, ok := doc["body"].(map[string]any)
	if !ok || body["hello"] != "world" {
		t.Fatalf("nested body not preserved: %#v", doc["body"])
	}
}

func TestMongoPublisherInsertsDocument(t *testing.T) {
	fake := &fakeMongo{}
	pub := newTestMongoPublisher(t, fake)
	msg := Message{ID: "evt_1", Endpoint: "orders_webhook", Queue: "events", ReceivedAt: time.Now()}
	if err := pub.Publish(context.Background(), "archive", "events", msg); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if len(fake.inserts) != 1 {
		t.Fatalf("expected 1 insert, got %d", len(fake.inserts))
	}
	if fake.inserts[0].collection != "events" {
		t.Fatalf("collection = %q, want events", fake.inserts[0].collection)
	}
	doc, ok := fake.inserts[0].doc.(map[string]any)
	if !ok || doc["_id"] != "evt_1" {
		t.Fatalf("doc not built correctly: %#v", fake.inserts[0].doc)
	}
}

func TestMongoPublisherSwallowsDuplicateKey(t *testing.T) {
	fake := &fakeMongo{insertErr: errMongoDuplicateKey}
	pub := newTestMongoPublisher(t, fake)
	msg := Message{ID: "evt_1", Queue: "events", ReceivedAt: time.Now()}
	if err := pub.Publish(context.Background(), "archive", "events", msg); err != nil {
		t.Fatalf("duplicate key should be a no-op, got error: %v", err)
	}
}

func TestMongoPublisherPropagatesOtherErrors(t *testing.T) {
	sentinel := errors.New("network down")
	fake := &fakeMongo{insertErr: sentinel}
	pub := newTestMongoPublisher(t, fake)
	msg := Message{ID: "evt_1", Queue: "events", ReceivedAt: time.Now()}
	if err := pub.Publish(context.Background(), "archive", "events", msg); !errors.Is(err, sentinel) {
		t.Fatalf("expected the underlying error to propagate, got %v", err)
	}
}

func TestMongoPublisherRejectsUnknownTarget(t *testing.T) {
	fake := &fakeMongo{}
	pub := newTestMongoPublisher(t, fake)
	if err := pub.Publish(context.Background(), "archive", "not-configured", Message{ID: "x", ReceivedAt: time.Now()}); err == nil {
		t.Fatal("expected error for unknown target")
	}
	if len(fake.inserts) != 0 {
		t.Fatalf("must not insert for unknown target")
	}
}

func TestMongoPublisherReadyAndClose(t *testing.T) {
	fake := &fakeMongo{}
	pub := newTestMongoPublisher(t, fake)
	if err := pub.Ready(context.Background()); err != nil {
		t.Fatalf("Ready: %v", err)
	}
	if fake.pinged == 0 {
		t.Fatal("Ready did not Ping")
	}
	if err := pub.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if fake.closed == 0 {
		t.Fatal("Close did not close ops")
	}
}
