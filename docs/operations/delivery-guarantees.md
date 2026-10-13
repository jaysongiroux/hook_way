# Delivery guarantees and recovery

A successful HTTP response describes the configured flow's acknowledgement
contract. It does not always mean the destination received the event, and does
not by itself establish that a downstream consumer finished processing it. Hookway has no durable
ingress spool and does not provide exactly-once delivery.

## Choose the acknowledgement boundary

| Flow choice | What success means | Main risk |
| --- | --- | --- |
| Durable broker, default `stop_on_failure` | The broker accepted publication; RabbitMQ confirms persistent, mandatory-routed messages | A lost confirmation/reply can cause duplicates. Broker durability and consumer completion depend on their configuration. |
| Webhook `delivery_mode: sync` | The destination returned success within the publish budget | It may commit work and lose its response; retries can repeat it. |
| Webhook `delivery_mode: async` (default) | A job entered the process's bounded memory queue | A crash or forced shutdown can lose acknowledged jobs with no replay record. |
| `http_request` → `respond` | The configured response, optionally from upstream, reached the caller | Timeout does not prove the upstream did nothing. HTTP side effects are not automatically replayable. |
| Fan-out with `skip_failed` | Normally at least one publish succeeded | Other destinations may have failed. Whole-event redelivery can repeat successful branches. |
| Custom 2xx error policy or `on_error` | Your configured error/response path completed | The original delivery may still have failed, without retained recovery work. |
| Batch | `shared` reports best-effort 200 accounting; `dedicated` normally reports eligible failures as 502 | Some children may succeed while others fail. Parent retry can duplicate them; batch snapshot replay is unsupported. |

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

A caller disconnect does not cancel publication already in progress: it continues
within its publish timeout. Treat a lost reply as uncertain and use stable
identities and downstream idempotency when retrying.

## Reconcile attempts, not just acknowledgements

For a missing event, compare the sender's delivery history, Hookway's logs/replay
record, destination receipts and consumer state. Group sender retries by the
provider's stable event ID, not Hookway's per-request ID.

A broker publish followed by a lost caller reply can produce two sender attempts
and two broker receipts but one idempotent business result. An async 202 followed
by a crash can instead have no receipt and no replay record.

For partial fan-out, check each destination and retry only a captured failed
branch. After a lost replay-action reply, reuse the same actor, idempotency key
and exact body. See [uncertain delivery](replay-investigation.md) before resolving,
dismissing or deliberately repeating an unknown outcome.

## Validation, protection and replay together

Put signature verification before parsing and verified quotas/duplicate checks,
then validate the JSON shape before outbound work. A guard reservation may be
released by its owner after a validation failure that produced no side effect.
Consumed quota is not refunded. Partial fan-out retains the duplicate reservation;
retry the captured failed destination rather than reposting the whole event and
repeating an already successful destination. Unknown outcomes retain protection
and need independent receipt investigation before any repeat attempt.

Capture remains best effort even when the guard retains a reservation. Monitor
capture drops and keep a way to reconcile events from the provider or application.
Restoring a database can rewind both protection and retry history; check
[post-backup deliveries](recovery.md) before resuming. No transaction spans the
protection store, replay database and external destination.
