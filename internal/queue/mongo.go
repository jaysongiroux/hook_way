package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
)

// MongoTarget is a single route target. For Mongo the target name is the
// collection the document is inserted into.
type MongoTarget struct {
	Name string
}

// MongoConfig carries everything MongoPublisher needs. The database lives in the
// mongoOps client; the publisher only needs the target (collection) set.
type MongoConfig struct {
	DestinationID string
	Logger        *slog.Logger
	Targets       []MongoTarget
}

// MongoPublisher inserts each webhook envelope as one BSON document into the
// collection named by the route target. targets is written only in the
// constructor and read-only during Publish (safe for concurrent fan-out).
type MongoPublisher struct {
	ops     mongoOps
	destID  string
	targets map[string]struct{}
	logger  *slog.Logger
}

// NewMongoPublisher records the configured target (collection) names. There is
// no DDL — collections auto-create on first insert and _id is auto-indexed.
func NewMongoPublisher(ctx context.Context, ops mongoOps, cfg MongoConfig) (*MongoPublisher, error) {
	targets := make(map[string]struct{}, len(cfg.Targets))
	for _, t := range cfg.Targets {
		targets[t.Name] = struct{}{}
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &MongoPublisher{ops: ops, destID: cfg.DestinationID, targets: targets, logger: logger}, nil
}

// messageToDocument renders the Message as its JSON-envelope shape (Message has
// only json tags, so JSON round-trip yields the same keys other destinations
// emit), then sets _id to the event id for dedup.
func messageToDocument(msg Message) (map[string]any, error) {
	b, err := json.Marshal(msg)
	if err != nil {
		return nil, err
	}
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, err
	}
	doc["_id"] = msg.ID
	return doc, nil
}

// Publish inserts msg as one document into the target collection. A duplicate
// _id is a no-op (errMongoDuplicateKey is swallowed) so at-least-once retries
// do not error.
// targets is written only in NewMongoPublisher and read-only during Publish,
// so concurrent fan-out reads need no lock.
func (p *MongoPublisher) Publish(ctx context.Context, destinationQueueID, target string, msg Message) error {
	if _, ok := p.targets[target]; !ok {
		return fmt.Errorf("mongodb %q: target %q is not configured", p.destID, target)
	}
	doc, err := messageToDocument(msg)
	if err != nil {
		return fmt.Errorf("mongodb %q: build document: %w", p.destID, err)
	}
	if err := p.ops.InsertOne(ctx, target, doc); err != nil {
		if errors.Is(err, errMongoDuplicateKey) {
			p.logger.Debug("skipped duplicate event",
				slog.String("destination", p.destID),
				slog.String("target", target),
				slog.String("event_id", msg.ID),
			)
			return nil
		}
		return fmt.Errorf("mongodb %q: insert: %w", p.destID, err)
	}
	return nil
}

// Ready confirms the Mongo connection is reachable.
func (p *MongoPublisher) Ready(ctx context.Context) error { return p.ops.Ping(ctx) }

// Close disconnects the Mongo client.
func (p *MongoPublisher) Close() error { return p.ops.Close() }
