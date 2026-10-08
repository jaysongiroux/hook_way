# Monitoring and incident response

Hookway runs without Prometheus or Grafana. Start with JSON logs, `/healthz`
(liveness) and `/readyz` (readiness). Add monitoring when you need dashboards
and alerts across replicas.

A webhook response, a destination receipt and completed consumer work are
different things. When investigating missing events, check the destination as
well as Hookway. See [delivery guarantees](delivery-guarantees.md).

## Operational targets and ownership

Choose alert thresholds for your traffic and payload sizes. The supplied rules
are starting points, not availability or recovery guarantees. Make sure someone
owns each alert and can reach the destination and database operators. Decide how
to contact them if monitoring itself is unavailable.

For recovery planning, agree how much data loss is acceptable and how long an
outage can last. Test those expectations with your own data and infrastructure;
[historical benchmarks](../evidence/performance.md) are not sizing guarantees.

## Signals and aggregation

Load the [alert rules](../../metrics/prometheus/alerts.yaml) into Prometheus and
import the [core](../../metrics/grafana/hookway-overview.json) and
[replay](../../metrics/grafana/hookway-replay.json) dashboards into Grafana.
For a local demonstration, use the
[monitoring overlay](../../examples/production-local/monitoring/README.md).

Scrape private listeners with dedicated credentials: core uses `security.api_key`;
replay requires a key with the `metrics` scope. The
[replay scrape example](replay-service.md#operations) shows a token-file setup.
Keep credentials out of dashboards and logs, and use TLS across untrusted networks.

- Core readiness includes per-flow results. Unless `server.readiness_require_all`
  is enabled, one ready flow can keep the overall response at 200. A protection
  store outage makes core unready.
- Replay readiness checks database connectivity and schema compatibility.
- Replay replicas report the same shared database. Use `max`, not `sum`, for
  record counts, usage and queued/running state within one database's scrape job.
  Give independent databases separate jobs. Sum rates of per-process counters,
  such as completed runs, across the relevant replicas.
- Replay state is sampled every 15 seconds by default. Check
  `hookway_replay_db_up` and
  `hookway_replay_state_last_success_timestamp_seconds` before trusting gauges:
  failed samples leave the previous values in place.

## Authentication

For 401/403 responses, check the key name, scopes, signature format and recent
credential changes. A 401 signs the console out; a 403 means the key lacks the
required permission. Neither confirms a recovery action succeeded.

Repair credentials rather than disabling authentication. Replay keys are loaded
at startup; apply removal or replacement through a restart. Core secret references
can refresh separately and retain their last known-good value after a refresh
failure. Confirm the fix with a signed request or `GET /v1/me`.

## Saturation

503 responses with `Retry-After` can mean admission or an async queue is full.
Check active/waiting requests, queue count and bytes, destination latency and
process memory. Work already admitted may still complete.

Use sender backoff or upstream buffering and fix the slow dependency first.
Raise limits only when there is memory and backend headroom. Async webhook queues
are memory-backed; a larger queue does not make accepted work durable.
See [resource sizing](resource-limits.md).

## Destination

Check the failed destination and the flow's response policy. With `skip_failed`,
one destination can fail while another succeeds and the sender still gets 2xx.

Fix the destination before retrying a captured failure. Retry that destination,
not the whole event. A timeout can follow successful delivery, so check receipts
before resending. Capture is best effort; not every failure has a replay record.

## Protection

Check store connectivity, permissions, namespace, policy status and identity
capacity. Quota exhaustion and duplicate rejection are different from store
failure or conflicting policy definitions.

Restore connectivity or resume an interrupted policy update with the same target
and expected fingerprint. Do not reset state, change namespace or flush Redis to
clear an alert: lost history can admit duplicates. Follow
[protection recovery](../guides/protection-stores.md#runtime-grants-and-state-loss-recovery)
if data was lost.

## Capture loss

Inspect `hookway_capture_dropped_total` and
`hookway_inspection_dropped_total` by reason. Common causes are a full capture
buffer, failed uploads, replay capacity, rejected records and shutdown.

Repair the dependency or capacity problem, then confirm new captures arrive.
Dropped artifacts cannot be recovered from logs. Failure capture does not change
the sender's response; a dropped inspection does not mean delivery failed.

## Replay capacity

Check fresh usage/capacity gauges, capture rejections and PostgreSQL disk space.
Retention limits and physical disk exhaustion are separate problems.
`hookway_replay_store_failures_total{reason="storage_exhausted"}` identifies
PostgreSQL disk-full errors; other unexpected store errors use `reason="other"`.

Run supported GC for expired records, increase storage-backed limits, or dismiss
records whose recovery you deliberately abandon. Dismissal deletes artifacts.
Successful retries retain artifacts until expiry; shortening TTL affects new
captures only. Never edit usage counters or delete tables to silence an alert.
See [replay maintenance](replay-service.md#runbook).

## Unknown delivery

Check the original run, audit history and independent destination receipts.
An expired lease or lost reply does not prove nothing was delivered, and lease
expiry does not cancel an execution already running in core.

Keep the outcome unknown until investigated. Confirm it only with downstream
evidence, or acknowledge duplicate risk before retrying. Dismissal abandons
recovery; it does not confirm delivery. Use the
[investigation guide](replay-investigation.md). Keep restored queued work offline
until [reconciled](recovery.md#a-restored-queued-run-already-delivered).

## Stale work

Check sample freshness, oldest queued age, worker readiness and core's control
listener, including its credentials and execution/lease budgets.

Repair the control or backend connection before adding workers. Refresh run
history after a lost reply; do not issue new retries merely because the queue
looks stalled. Confirm queued runs reach a terminal result and check receipts.

## Database

Check replay readiness, `db_up`, pool usage, connection/TLS settings, runtime
grants, disk space and schema compatibility. Dashboard gauges may be stale
during an outage.

Fix the connection, grant or storage problem. Do not grant runtime DDL or edit
migration bookkeeping to make startup pass. For incompatible or lost state, use
the [upgrade](upgrades.md) or [restore](recovery.md) procedure.

## Cleanup

Check GC errors, the last successful sweep, intervals, locks, permissions and
storage health. A skipped advisory lock means another replica is sweeping,
not that GC failed. Replay's initial GC timestamp is process start, not a
completed sweep.

Use `hookway-replay gc --config replay.yaml` for an extra sweep of expired records.
Verify the database target first. If automatic GC is disabled, schedule cleanup
separately. The staleness alert allows for the largest configured interval across
replicas, so check individual timestamps when their settings differ. Do not
delete active reservations or extend/shorten their expiry to clear an alert.

## Telemetry

Repeated hot-path logs are sampled at normal log levels. Counters and persisted
replay audit still count outcomes. Check `hookway_logs_suppressed_total` and
`hookway_replay_logs_suppressed_total` alongside the underlying error metrics.

Fix the source of the storm. Use `logging.level: debug` temporarily if more detail
is needed; it bypasses sampling and can produce much more output. Protect log
access and keep secrets/payloads out of logs. Request IDs help correlate diagnostics;
they are not stable business-event identities.

## Metrics scrape

A failed scrape can be a monitoring problem while the application remains healthy.
Check the private listener, path, metrics-only credential file, TLS and network
access. Do not expose metrics publicly, reuse a broad operator key or restart
a healthy service to fix a scraper.

Confirm Prometheus reports `up` and the expected metrics before trusting dashboards.

## Console unavailable: API and CLI alternatives

Use the [replay API](replay-service.md#api) with the same scoped operator key.
The [investigation guide](replay-investigation.md#identity-and-capabilities) has
curl examples. Offline configuration and maintenance commands are in the
[CLI reference](../reference/cli.md).

After a lost retry or confirmation reply, keep the original key, actor and exact
body and resubmit that request. For dismissal, re-read state before acting again.
The console stores uncertain submissions only in memory; record the displayed
identity before reload, sign-out or handover.

## Notification acceptance

Route alerts to the people who will respond. Test both firing and resolved
notifications, including the links to these runbooks. Prometheus showing a firing
alert does not mean a notification arrived. Configure Alertmanager routing and
`send_resolved` explicitly; see its
[configuration reference](https://prometheus.io/docs/alerting/latest/configuration/).

The local receiver is a demonstration endpoint, not your paging service. Test
your actual route before relying on it during an incident.
