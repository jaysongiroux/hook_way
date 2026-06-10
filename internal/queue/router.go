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

func (r *RoutingPublisher) Close() error {
	var errs []error
	for _, id := range r.sortedIDs() {
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
