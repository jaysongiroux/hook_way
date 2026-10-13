package queue

import (
	"context"
	"testing"
	"time"

	"github.com/jaysongiroux/hook_way/internal/config"
	"github.com/jaysongiroux/hook_way/internal/testenv"

	tcmongo "github.com/testcontainers/testcontainers-go/modules/mongodb"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// startMongo boots a MongoDB container and returns a connection URI. It
// self-skips when Docker is unavailable, mirroring startPostgres.
func startMongo(t *testing.T) string {
	t.Helper()
	testenv.SkipIfShort(t)
	ctx := context.Background()
	container, err := tcmongo.Run(ctx, "mongo:7")
	testenv.RequireContainer(t, err, "mongo")
	t.Cleanup(func() { _ = container.Terminate(ctx) })
	uri, err := container.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("get mongo connection string: %v", err)
	}
	return uri
}

// TestMongoPublishRoundTrip publishes a Message via MongoPublisher and
// verifies the document contents with a raw mongo client:
//   - exactly 1 document after publishing the same id twice (duplicate-key no-op)
//   - endpoint field and nested body round-trip correctly
func TestMongoPublishRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	uri := startMongo(t)
	ctx := context.Background()

	ops, err := newMongoClient(ctx, config.MongoConfig{URI: uri, Database: "webhooks"})
	if err != nil {
		t.Fatalf("newMongoClient: %v", err)
	}
	defer func() { _ = ops.Close() }()

	pub, err := NewMongoPublisher(ctx, ops, MongoConfig{
		DestinationID: "archive",
		Targets:       []MongoTarget{{Name: "events"}},
	})
	if err != nil {
		t.Fatalf("NewMongoPublisher: %v", err)
	}
	if err := pub.Ready(ctx); err != nil {
		t.Fatalf("Ready: %v", err)
	}

	msg := Message{
		ID: "evt_1", Endpoint: "orders_webhook", DestinationQueueID: "archive",
		Queue: "events", ReceivedAt: time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC),
		Body: map[string]any{"hello": "world"},
	}
	if err := pub.Publish(ctx, "archive", "events", msg); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	// Publish the same id again — duplicate-key must be a no-op, keeping one doc.
	if err := pub.Publish(ctx, "archive", "events", msg); err != nil {
		t.Fatalf("Publish (dup): %v", err)
	}

	// Verify with a raw mongo client.
	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatalf("mongo.Connect: %v", err)
	}
	defer func() { _ = client.Disconnect(ctx) }()
	coll := client.Database("webhooks").Collection("events")

	count, err := coll.CountDocuments(ctx, bson.D{{Key: "_id", Value: "evt_1"}})
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 doc after duplicate publish, got %d", count)
	}

	// Decode into bson.D — mongo-driver/v2 decodes nested documents as bson.D
	// (ordered key/value pairs), not map[string]any, so we use bson.D throughout.
	var got bson.D
	if err := coll.FindOne(ctx, bson.D{{Key: "_id", Value: "evt_1"}}).Decode(&got); err != nil {
		t.Fatalf("find: %v", err)
	}

	// Helper to look up a top-level field in a bson.D.
	lookup := func(d bson.D, key string) any {
		for _, e := range d {
			if e.Key == key {
				return e.Value
			}
		}
		return nil
	}

	if endpoint := lookup(got, "endpoint"); endpoint != "orders_webhook" {
		t.Fatalf("endpoint = %v", endpoint)
	}
	bodyRaw := lookup(got, "body")
	bodyDoc, ok := bodyRaw.(bson.D)
	if !ok {
		t.Fatalf("nested body is not bson.D: %T %#v", bodyRaw, bodyRaw)
	}
	if hello := lookup(bodyDoc, "hello"); hello != "world" {
		t.Fatalf("nested body did not round-trip: hello = %v", hello)
	}
}
