# Delivery guarantees and recovery

A successful HTTP response describes the configured flow's acknowledgement
contract. It does not always mean the destination received the event, and does
not by itself establish that a downstream consumer finished processing it. Hookway has no durable
ingress spool and does not provide exactly-once delivery.

## Choose the acknowledgement boundary

| Configuration | What a normal successful publish means | Remaining failure window |
|---|---|---|
| Durable broker destination, default `stop_on_failure` | The configured broker's publish operation succeeded. RabbitMQ uses publisher confirms, mandatory routing and persistent messages. Other destinations have their own acceptance contract. | Broker durability/replication settings still matter. A lost HTTP response can cause sender retries after a successful publish. Consumers can fail after receiving a message. |
| Webhook destination with `delivery_mode: sync` | The downstream HTTP delivery completed successfully within the publish budget. | The downstream may commit work and lose its response. Retries can duplicate that work. A 2xx response does not establish the downstream's storage durability. |
| Webhook destination with `delivery_mode: async` (the default) | The job entered a bounded, process-local memory queue. | A crash, forced termination or expired drain deadline can lose acknowledged queued or in-flight jobs. `dead_letter` and replay cannot remove this window. |
| `http_request` followed by `respond` | The configured response, optionally the upstream status/body, was returned. | This is a request/response proxy contract. An upstream side effect can succeed even if its response or the caller connection is lost. |

For reliable ingestion, start with
[`examples/reliable-ingestion.yaml`](../../examples/reliable-ingestion.yaml): verify
the sender, publish directly to a durable broker, and acknowledge only after the
publish completes. Put slow processing in a broker consumer. Configure broker
replication, persistence, retention and consumer acknowledgement separately.
`create: true` alone does not establish those operational guarantees.

Async webhook mode remains the default for compatibility. Select it explicitly
only when its loss model is acceptable. A dead-letter destination can receive a
terminal delivery failure while the process is running; it cannot recover a
queue that disappeared in a crash. Graceful shutdown attempts to drain the
queue, but the configured deadline must be sufficient and can still expire.

## Fan-out and acknowledged failures

`stop_on_failure` is the default. It stops after a failed publication and normally
returns a non-2xx response. Publications that already succeeded are not rolled
back, and later publications may not have been attempted. Sender redelivery can
therefore duplicate the successful branches.

`skip_failed` applies to sibling publish nodes: it attempts the destinations and
normally returns success if at least one succeeds. That acknowledgement can hide
failed destinations from the sender. It is not an atomic multicast transaction.
A partial or uncertain delivery retains the `replay_guard` reservation so an
inbound retry cannot repeat already attempted effects. Retry only the missed
destination through replay when a retained record is available. Consumers still
need idempotency; a success acknowledgement does not make fan-out atomic.

Explicit `responses` policies can return 2xx for `publish_error`,
`partial_failure`, or other failures. An `on_error` path also marks a failure as
handled if its recovery succeeds. Returning a success response on that path
without publishing to a fallback intentionally accepts the failure; it does not
create a replay record. A successful fallback publish inherits its destination's
contract, including the volatile-queue contract of an async webhook destination.

`hookway validate` warns about used async destinations, partial-success fan-out,
2xx failure response policies, `on_error` paths that return 2xx without a fallback
publish, and explicit replay opt-out for eligible publish flows. Warnings preserve
ordinary validation and runtime compatibility. `hookway validate --strict`
rejects warnings when your deployment requires that stricter policy. These
checks are guidance, not proof that every custom flow is durable.

## Replay is best-effort recovery

The optional replay service captures eligible failed deliveries **after the
sender is acknowledged**, when no successful `on_error` or dead-letter path
handled them. Capture is bounded and can be dropped if core crashes, the buffer
fills, uploads fail, or replay refuses a record. It must not delay healthy
ingestion or change readiness. Monitor capture drops and delivery failures.

No replay configuration means no capture. `capture_failures: false` disables it
for a flow even when replay is configured. Batch failure capture is unsupported;
batch flows must explicitly disable capture when replay is configured. Their
operators need a separate reconciliation process for child outcomes.

Replay cannot recover rejected requests it never stored, unattempted jobs lost
from an async queue, or every uncertain downstream side effect. See
[Replay service](replay-service.md) for retention, payload limits, scoped access,
and operator-triggered retry. A replay retry may repeat a side effect if the
original destination accepted the request but its response was lost.

## Event identity and duplicate handling

Hookway's envelope `id` is stable across its internal delivery retries and replay.
Each new inbound request gets a new envelope ID, including a sender's retry of
the same event. Do not use that ID alone to deduplicate sender retries.

Preserve the provider's stable event identity in the published payload or an
explicitly allowed header. The example preserves `X-GitHub-Delivery`; a Stripe
consumer can use the JSON event's `id`. Scope downstream keys by provider,
account/tenant and event ID, and retain them for the full sender retry and
operator-redrive window. Choose an identity appropriate to the provider's event
semantics; a delivery-attempt ID may change on redelivery. Treat these values as
untrusted input and validate them; signature verification does not necessarily
cover every identity header.

A consumer should atomically record the deduplication key with its business
transaction, or use the downstream API's idempotency mechanism. Recording the
key before doing work and crashing can permanently suppress unfinished work.
`replay_guard` reduces repeated requests during its TTL, using memory for one
process or Redis/PostgreSQL for shared protection. Memory restarts and shared
backend data loss can admit the same event again. See [Protection stores](../guides/protection-stores.md)
for expiry, ownership and failure recovery.

## Recover when the sender does not retry

A non-2xx response makes failure visible; it does not guarantee sender redelivery.
For providers without automatic retries, monitor failed delivery attempts in the
provider and use its delivery-history/manual-redelivery API or reconcile against
its source-of-truth API. Keep enough history to span the outage. If the provider
cannot retain or replay events, place a durable ingress service before Hookway
or explicitly accept that loss model. Never acknowledge an unavailable broker
solely to make the provider's failure indicator disappear.

Before launch, exercise broker outage, partial fan-out, caller disconnect, hard
process termination, replay outage and recovery. Reconcile sender attempts,
HTTP acknowledgements, destination receipts and consumer outcomes. A disconnect
is an uncertain outcome: normal publication continues independently of caller
cancellation within its timeout, so the safe recovery is an idempotent retry,
not an assumption that no work happened.

## Validation, protection and replay together

Put signature verification before parsing and verified quotas/duplicate checks,
then validate the JSON shape before outbound work. A guard reservation may be
released by its owner after a validation failure that produced no side effect.
Consumed quota is not refunded. Partial fan-out retains the duplicate reservation;
retry the captured failed destination rather than reposting the whole event and
repeating an already successful destination. Unknown outcomes retain protection
and need independent receipt investigation before any repeat attempt.

The native acceptance suite verifies this sequence with two cores and Redis or
PostgreSQL, including backend outages and state-preserving policy changes. Capture
is still best effort: retained protection does not prove a recoverable replay
record exists. Monitor capture drops and maintain provider/business reconciliation.
Restoring an earlier database backup can rewind both protection and replay history;
quiesce work and reconcile destination receipts before resuming. No transaction
spans the protection store, replay database and external destination.
