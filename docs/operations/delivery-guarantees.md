# Delivery guarantees and recovery

A successful HTTP response describes the configured flow's acknowledgement
contract. It does not always mean the destination received the event, and does
not by itself establish that a downstream consumer finished processing it. Hookway has no durable
ingress spool and does not provide exactly-once delivery.

## Choose the acknowledgement boundary

| Configuration | Success boundary | Retained state | Duplicate / loss window | Recovery owner |
|---|---|---|---|---|
| Durable broker, default `stop_on_failure` | Broker publish accepted; RabbitMQ confirms mandatory-routed persistent messages. Other brokers have their own contracts. | Message in the configured broker, subject to its durability and retention settings. | Lost confirm or caller reply can produce duplicates. Consumer receipt is not a completed business transaction. | Sender retries visible failures; broker and consumer operators reconcile receipts and unfinished work. |
| Webhook `delivery_mode: sync` | Destination returned a successful HTTP response within the publish budget. | Whatever the destination actually committed; Hookway has no ingress spool. | Destination may commit and lose its response. Retries can repeat work; 2xx does not prove durable storage. | Destination owner supplies idempotency and receipts; sender or replay operator recovers eligible failures. |
| Webhook `delivery_mode: async` (default) | Job admitted to bounded process-local memory. | Volatile queued/active jobs; terminal failures may reach a configured dead-letter target or best-effort capture while core is alive. | Crash, forced kill or expired drain deadline can lose acknowledged jobs, without a recovery record. | Product owner explicitly accepts loss or chooses a durable broker; consumer/provider reconciliation handles gaps. |
| `http_request` then `respond` | Configured response, optionally upstream status/body, was returned. | Destination-side effects, if any; no delivery snapshot recovery for the HTTP request itself. | Upstream work may commit before either response is lost; configured retries can repeat it. | Caller and upstream owner reconcile stable identities; do not treat a timeout as nondelivery. |
| Partial fan-out, `skip_failed` | Normally at least one sibling publish succeeded. | Successful branch effects; eligible unhandled failed branches may be captured after final 2xx. | Not atomic multicast. Capture can be dropped; whole-event redelivery can repeat successful branches. | Replay operator retries only captured failed destinations; destination owners reconcile missing records. |
| `on_error`, fallback or 2xx failure response | The configured recovery/response policy completed, not necessarily the original delivery. | A fallback inherits its destination's contract. A handled error or response-only acknowledgement need not retain recovery work. | Async fallback is still volatile; acknowledging without fallback intentionally accepts the original failure. | Flow owner defines the contract and monitors the chosen fallback or reconciles accepted loss. |
| Batch child processing | `shared`: best-effort 200 accounting; `dedicated`: eligible failures normally return 502 after attempted children finish. | Individual child destination effects and response accounting; no batch snapshot redrive. | Some children may succeed before others fail or remain unattempted. Parent retry can repeat successful children. | Sender/consumer operators reconcile stable child identities and replay from their own source of truth. |

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

No replay configuration means no capture. `capture_failures: false` disables
failure capture for a flow even when replay is configured. Explicit Inspect
nodes still collect view-only observations independently of failures and the
final response status. They use the same bounded, best-effort upload pipeline
and shared replay storage limits. Batch failure capture is unsupported;
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

## Reconcile attempts, not just acknowledgements

Use a synthetic, authenticated provider event ID for each failure drill and keep
an independent ledger outside the core process being terminated. For each event
and destination, record sender attempt count, observed HTTP status (or no reply),
destination attempts, accepted receipts and idempotent business outcomes. Preserve
the Hookway envelope IDs too, but group sender retries by stable provider identity.
Count failed destination attempts separately from accepted receipts.

For example, one confirmed broker publish followed by a discarded caller reply
can look like two sender attempts, one observed 202, two broker receipts with
different envelope IDs, and one idempotent consumer outcome. That is acceptable
at-least-once recovery, not exactly-once transport. Conversely, an async 202
followed by a forced kill can have zero receipts and no replay record.

For partial fan-out, reconcile each branch: the good branch must not be included
in a destination-only replay. For a lost replay action reply, repeat the same
actor, idempotency key and exact body; do not create a new retry key merely because
the reply was lost. See [uncertain-delivery investigation](replay-investigation.md)
before resolving, dismissing or deliberately repeating an unknown outcome.

`make replay-acceptance-test` runs native core/replay replicas with independent
destination ledgers, real PostgreSQL/Redis protection and a RabbitMQ fixture.
`make conformance` checks graph and redrive invariants. These local checks also
work for production-style local development; they do not establish broker HA,
live deployment health, provider retention or recovery from restored backups.

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
