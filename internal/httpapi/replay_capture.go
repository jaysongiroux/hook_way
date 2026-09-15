package httpapi

import (
	"context"
	"fmt"
	"runtime/debug"
	"time"

	"github.com/jasongiroux/hookway/internal/queue"
	"github.com/jasongiroux/hookway/internal/replaystore"
	"github.com/jasongiroux/hookway/internal/webhook"
)

// replayCaptureTimeout bounds how long a single best-effort replay-store
// write (captureEvent's INSERT or recordOutcome's UPDATE) may add to request
// latency. It is deliberately independent of server.publish_timeout: the
// replay store and the destination fan-out are two different downstream
// systems with two different budgets, and a slow-but-successful store write
// must never eat into the publish deadline (or vice versa).
const replayCaptureTimeout = 5 * time.Second

// ReplayCapturer is the seam httpapi depends on for webhook-replay capture --
// just the two write operations FlowServer needs, so this package depends
// only on replaystore's exported types (Event/Delivery/Outcome*) and never on
// its concrete Postgres-backed Store. A nil Server.replay means capture is
// disabled: every call site below guards on it before doing any work.
type ReplayCapturer interface {
	Capture(ctx context.Context, e replaystore.Event) error
	RecordOutcome(ctx context.Context, id string, deliveries []replaystore.Delivery) error
}

// SetReplayCapturer wires a replay store into the server after construction
// (main.go, once config.Replay.Enabled and the store connects) or a test's
// fake. Capture stays disabled (Server.replay == nil) until this is called.
func (s *Server) SetReplayCapturer(c ReplayCapturer) {
	s.replay = c
	if pinger, ok := c.(interface{ Ping(context.Context) error }); ok {
		s.readiness.setReplayPing(pinger.Ping)
	}
}

// envelopeToMessage copies every event-level field from env into a
// queue.Message, leaving DestinationQueueID/Queue at env's own endpoint-level
// values (not any one route's). publishRoute calls this and then overrides
// those two fields with the per-route destination/target; replay capture
// stores this same shared base as Event.Envelope, which guarantees the
// captured envelope matches what is actually published, field-for-field,
// with no risk of the two drifting apart.
func envelopeToMessage(env webhook.Envelope) queue.Message {
	return queue.Message{
		SchemaVersion: env.SchemaVersion, ID: env.ID, IdempotencyKey: env.IdempotencyKey, RequestID: env.RequestID, TraceID: env.TraceID,
		Traceparent: env.Traceparent,
		Endpoint:    env.Endpoint, DestinationQueueID: env.DestinationQueueID, Queue: env.Queue, ReceivedAt: env.ReceivedAt,
		Method: env.Method, Path: env.Path, Headers: env.Headers, Query: env.Query,
		Body: env.Body, RawBodyBase64: env.RawBodyBase64, SourceIP: env.SourceIP, Auth: env.Auth,
		Variables: env.Variables,
		RawBody:   env.RawBody, OrigHeaders: env.OrigHeaders, Hops: env.Hops,
	}
}

// captureEvent inserts a captured event into the replay store. It is
// strictly best-effort: an error or panic from s.replay.Capture is caught,
// logged, metered (hookway_replay_capture_failures_total{op="insert"}), and
// swallowed here -- it never propagates to FlowServer, so a replay-store
// outage can never change the webhook's response or block it beyond this
// bounded, synchronous call.
//
// reqCtx is the inbound request's context (not pubCtx): captureEvent derives
// its own short-lived context here, detached from the request's cancellation
// (context.WithoutCancel, same rationale as pubCtx -- a sender disconnect
// must not abort a write already committed to happening) and bounded by
// replayCaptureTimeout rather than server.publish_timeout. That keeps this
// call's budget independent of, and unable to consume, the publish deadline
// used by publishRoutes.
func (s *Server) captureEvent(reqCtx context.Context, ev replaystore.Event) {
	defer func() {
		if rec := recover(); rec != nil {
			s.logger.Error("replay capture panicked",
				"location", "replay_capture",
				"panic", fmt.Sprint(rec),
				"stack", string(debug.Stack()),
				"event_id", ev.ID, "endpoint", ev.Endpoint,
			)
			s.metrics.ReplayCaptureFailure("insert")
		}
	}()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(reqCtx), replayCaptureTimeout)
	defer cancel()
	if err := s.replay.Capture(ctx, ev); err != nil {
		s.logger.Warn("replay capture failed", "event_id", ev.ID, "endpoint", ev.Endpoint, "error", err.Error())
		s.metrics.ReplayCaptureFailure("insert")
		return
	}
	s.metrics.ReplayCaptured(ev.Endpoint)
}

// recordOutcome updates a captured event's per-destination delivery outcome.
// Same best-effort discipline as captureEvent: an error or panic is caught,
// logged, metered (op="update"), and swallowed.
//
// reqCtx is the inbound request's context, not pubCtx: recordOutcome always
// runs after publishRoutes, when pubCtx may already be near (or fully)
// exhausted from a full-timeout publish, so reusing it here would risk the
// UPDATE never getting a fair budget -- or, symmetrically, a slow UPDATE
// stealing from a publish that hasn't happened yet. recordOutcome derives its
// own short-lived, detached context the same way captureEvent does
// (context.WithoutCancel + replayCaptureTimeout), independent of
// server.publish_timeout.
//
// An empty deliveries slice is a no-op UPDATE (the row is already "pending"
// and there is nothing to record), so it short-circuits before ever calling
// s.replay.RecordOutcome. stop_on_failure now returns the failed route plus
// any unattempted routes in `failed`, so this short-circuit is only for a
// caller that truly has nothing to record.
func (s *Server) recordOutcome(reqCtx context.Context, id string, deliveries []replaystore.Delivery) {
	if len(deliveries) == 0 {
		return
	}
	defer func() {
		if rec := recover(); rec != nil {
			s.logger.Error("replay record outcome panicked",
				"location", "replay_capture",
				"panic", fmt.Sprint(rec),
				"stack", string(debug.Stack()),
				"event_id", id,
			)
			s.metrics.ReplayCaptureFailure("update")
		}
	}()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(reqCtx), replayCaptureTimeout)
	defer cancel()
	if err := s.replay.RecordOutcome(ctx, id, deliveries); err != nil {
		s.logger.Warn("replay record outcome failed", "event_id", id, "error", err.Error())
		s.metrics.ReplayCaptureFailure("update")
	}
}

// deliveriesFromResults converts FlowServer's published/failed
// []map[string]string route-result slices (keys "destination_queue_id" and
// "queue") into replaystore.Delivery values for RecordOutcome.
func deliveriesFromResults(published, failed []map[string]string) []replaystore.Delivery {
	deliveries := make([]replaystore.Delivery, 0, len(published)+len(failed))
	for _, p := range published {
		deliveries = append(deliveries, replaystore.Delivery{
			DestinationID: p["destination_queue_id"], Target: p["queue"], Outcome: replaystore.OutcomeDelivered,
		})
	}
	for _, f := range failed {
		deliveries = append(deliveries, replaystore.Delivery{
			DestinationID: f["destination_queue_id"], Target: f["queue"], Outcome: replaystore.OutcomeFailed,
		})
	}
	return deliveries
}
