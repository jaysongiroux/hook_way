package queue

import (
	"context"
	"errors"
	"fmt"
	"sort"
)

// RoutingPublisher dispatches publish calls to a message-queue publisher by
// destination id while keeping readiness and shutdown checks aggregate.
type RoutingPublisher struct {
	publishers map[string]Publisher
}

func NewRoutingPublisher(publishers map[string]Publisher) *RoutingPublisher {
	copied := make(map[string]Publisher, len(publishers))
	for id, publisher := range publishers {
		copied[id] = publisher
	}
	return &RoutingPublisher{publishers: copied}
}

func (r *RoutingPublisher) Publish(ctx context.Context, destinationQueueID, queueName string, msg Message) error {
	publisher, ok := r.publishers[destinationQueueID]
	if !ok {
		return fmt.Errorf("message queue destination %q is not configured", destinationQueueID)
	}
	return publisher.Publish(ctx, destinationQueueID, queueName, msg)
}

func (r *RoutingPublisher) Ready(ctx context.Context) error {
	var errs []error
	for _, id := range r.sortedIDs() {
		if err := r.publishers[id].Ready(ctx); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", id, err))
		}
	}
	return errors.Join(errs...)
}

// ReadyByID probes every destination and returns a per-id readiness result (nil
// = ready). Unlike Ready, it preserves which destination produced which error
// so the background readiness prober can drive the per-destination
// hookway_destination_ready gauge. The map always contains an entry for every
// configured destination.
func (r *RoutingPublisher) ReadyByID(ctx context.Context) map[string]error {
	results := make(map[string]error, len(r.publishers))
	for id, publisher := range r.publishers {
		results[id] = publisher.Ready(ctx)
	}
	return results
}

// Close shuts down every publisher, closing webhook publishers first.
//
// Why the split: a *WebhookPublisher.Close() synchronously drains its engine,
// and terminal failures during that drain can dead-letter into ANOTHER
// configured destination via the shared publishers map (see factory.go's
// dlqPublish resolver). If Close() closed publishers in a single sorted pass,
// a dead-letter destination whose id sorts before the webhook destination's id
// (e.g. "dlq" < "notify") would already be closed by the time the webhook
// engine's drain tries to dead-letter into it, silently dropping the message
// against a real backend that rejects post-Close publishes.
//
// Closing all webhook publishers before any other publisher guarantees every
// possible dead-letter target is still open while webhook engines drain. This
// is safe because config validation forbids a webhook-typed destination from
// being used as a dead_letter target, so a webhook publisher's drain can only
// dead-letter into a non-webhook publisher — a single "webhooks first, rest
// second" split cannot itself introduce a reordering problem. Order within
// each phase remains deterministic (sorted) so error aggregation is stable.
func (r *RoutingPublisher) Close() error {
	var errs []error
	var webhookIDs, otherIDs []string
	for _, id := range r.sortedIDs() {
		if _, ok := r.publishers[id].(*WebhookPublisher); ok {
			webhookIDs = append(webhookIDs, id)
		} else {
			otherIDs = append(otherIDs, id)
		}
	}
	for _, id := range webhookIDs {
		if err := r.publishers[id].Close(); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", id, err))
		}
	}
	if err := errors.Join(errs...); errors.Is(err, ErrWebhookDrainTimeout) {
		// Undrained engines can still dead-letter into these publishers. The
		// process shutdown must fail without closing their dependencies.
		return err
	}
	for _, id := range otherIDs {
		if err := r.publishers[id].Close(); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", id, err))
		}
	}
	return errors.Join(errs...)
}

func (r *RoutingPublisher) sortedIDs() []string {
	ids := make([]string, 0, len(r.publishers))
	for id := range r.publishers {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
