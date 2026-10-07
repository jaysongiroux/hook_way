# Observability and incident response

Separate the response to the webhook sender, destination acceptance and external
consumer completion. A 2xx, a queued replay run and a business transaction are
different observations. Start with the [delivery contract](delivery-guarantees.md)
and retain independent destination receipts before making a recovery decision.

Monitoring is optional: core and replay run without Prometheus, Grafana, a cloud
account or a notification service. Structured logs and liveness/readiness remain
available. Monitoring failures must not become an application startup dependency.
The [optional local monitoring recipe](../../examples/production-local/monitoring/README.md)
adds authenticated scrapes and a fixture receiver to the same production-local
stack without changing application configuration or requiring cloud credentials.

## Operational targets and ownership

Use the [local production recipe](../development/local-development.md) as a
reference configuration, not a supported workstation minimum or capacity claim.
Validate the actual configuration and measure its traffic and failure backlog
before promotion. Native binaries and the same container images/configuration
remain supported locally; private destinations and proxy-terminated TLS do not
require a developer-only bypass.

There is no published availability, latency, consumer-completion or recovery SLA.
Historical [benchmarks](../evidence/performance.md) and tiny local recovery drills
describe their measured environments only. Choose targets with the deployment
owner rather than converting a benchmark or default alert threshold into a
promise. Record this deployment-specific worksheet:

| Decision | Record before production acceptance |
| --- | --- |
| Traffic | Sustained/peak events per second, burst duration, payload distribution, authentication/transform work, fan-out, destination and acknowledgement mode. |
| Sender response | Latency percentile/window, transport errors and rejection policy; distinguish expected authentication/quota/backpressure rejects from service failures. |
| Delivery | Destination acceptance deadline, external receipt/consumer checkpoint and idempotency owner; include async volatile-loss and best-effort capture limits. |
| Recovery | Accepted lost-data interval/RPO, reconciled-resume RTO, backup dataset and off-host/key recovery rehearsal. A couple-second local preference is not an agreed SLA or data-loss tolerance. |
| Diagnosis | Scrape/evaluation/notification intervals, alert hold periods, log retention/access and who acknowledges a notification. |

The shipped alert thresholds are starting operational signals, not SLOs. Tune
them to measured traffic without hiding known data loss or uncertain delivery.
For low-traffic services, a missing rate series or lack of notifications does
not establish that traffic is succeeding.

| Logical owner | Responsibility and escalation |
| --- | --- |
| Service/deployment operator | Configuration, admission/publication/shutdown budgets, canaries and incident coordination; contact destination and storage owners when their evidence is needed. |
| Credential/security owner | Named keys/scopes, sender signing formats, rotation, private listeners and TLS; follow [security reporting](../../SECURITY.md) for suspected vulnerabilities. |
| Destination/consumer owner | Independent acceptance/business receipts, stable event identity, duplicate-risk decisions and deduplication. |
| Storage/recovery owner | Runtime/migration grants, state fingerprints, retention, encrypted backups/key recovery and approval of reconciled restores. |
| Monitoring owner | Private scraping, rule/dashboard configuration, notification routing/access and a verified real responder route before deployment acceptance. |
| Release owner | Matching image/config/schema/assets and exact supported upgrade/rollback rehearsal. |

These are roles to assign in your deployment, not named Hookway staff, a 24/7
support service or a response-time commitment. Define contacts, incident handover
and the fallback channel when monitoring itself is unavailable.

## Signals and aggregation

Use `/healthz` for liveness and `/readyz` for admission/dependency readiness.
Core's cached readiness is 200 while any flow is ready unless
`server.readiness_require_all` is enabled; inspect the per-flow result before
calling a partially degraded deployment healthy. Replay readiness checks database
reachability and compatibility. A live process can intentionally be unready;
restart loops do not repair an unavailable backend or missing protection history.

Scrape private metrics listeners with their own credentials, not an operator or
capture key. The [replay scrape example](replay-service.md#operations) uses a
metrics-only token file. Restrict the file to the scraper, protect the network
and use verified TLS across untrusted networks. Credentials never belong in
dashboard URLs, browser assets, metric labels or incident attachments.
See [Prometheus scrape configuration](https://prometheus.io/docs/prometheus/latest/configuration/configuration/).

Replay replicas report the **same shared database**: aggregate record, usage and
queued/running state with `max` within one logical database's scrape job, never
sum replicas. Keep record kind, delivery status and dismissal labels distinct.
Independent replay databases need distinct jobs; do not put unrelated datasets
into the same aggregate. Process counters such as completed runs are different:
sum their rates/increases across replicas within the intended job.
See [PromQL aggregation](https://prometheus.io/docs/prometheus/latest/querying/operators/).

Database gauges are sampled, not transaction-exact live counts. Inspect
`hookway_replay_db_up` and
`hookway_replay_state_last_success_timestamp_seconds` alongside them. Failed
samples retain prior state values; those values do not prove current capacity,
queue state or the absence of unknown work. The last-success timestamp is zero
until the first successful sample. Default sampling is every 15 seconds with a
five-second query budget; a scrape reads cached/in-memory state.

| Cause | Primary diagnostic | What it does not establish |
| --- | --- | --- |
| Authentication/scope rejection | HTTP 401/403; `hookway_auth_failures_total` or `hookway_replay_auth_failures_total`, bounded reason. | A destination failure or successful mutation. |
| Admission/async saturation | HTTP 503/Retry-After, admitted/waiting/queue count and byte gauges; `hookway_saturated_total`, webhook drops. | That already admitted work was canceled or that removing limits is safe. |
| Destination failure | Cached destination readiness, publish/webhook outcomes, relevant replay record/run. | That a sender's 2xx proves external consumer completion. |
| Protection outage/conflict | Protection readiness/operation result, policy status and compensation/cleanup counters. | That resetting state or changing namespace preserves quotas/reservations. |
| Capture loss | Failure/inspection drop counters and reasons plus buffer usage. | That every lost event has a recoverable artifact or individual retained log. |
| Unknown delivery | Undismissed unknown failure state, lease-expiry/executor outcomes, original run/audit. | Nondelivery, safe automatic retry or a verified downstream receipt. |
| Retention pressure | Sampled usage versus configured capacity, ingest capacity rejects. | Physical database disk headroom or automatic freeing after a successful retry. |
| Store exhaustion/failure | Generic API 500 or readiness 503 plus `hookway_replay_store_failures_total{reason="storage_exhausted"}` for PostgreSQL SQLSTATE 53100; other unexpected store failures use `reason="other"`. | A full physical-disk drill, or a promise that every backend/platform reports the same error. |
| Stale work/state | Oldest queued age, workers/control outcomes and state sample freshness. | A complete live queue count during a database outage. |
| Cleanup failure | Replay/protection GC results and last successful cleanup. | That another replica's locked/skipped sweep failed, or that logical expiry was extended. |
| Telemetry suppression | Component suppression counters and fixed-category log summaries. | Missing business transactions, sampled outcome counters or durable per-event log history. |

The maintained [rules](../../metrics/prometheus/alerts.yaml),
[core dashboard](../../metrics/grafana/hookway-overview.json) and
[replay dashboard](../../metrics/grafana/hookway-replay.json) are operational
assets. Validate their selectors against your job/dataset naming and effective
configuration before treating a dashboard total as an incident checkpoint.

## Authentication

Impact: the sender or operator was not authorized; a denied request is not a
successful recovery action. First check HTTP status, the bounded rejection reason,
named key/scopes, expected provider signing format and recent rotation. A secret
refresh failure can leave core using its last known-good verifier value.

Mitigation: repair the intended credentials/format with the credential owner;
do not disable authentication or add a blanket unsafe mode. Replay's named bearer
keys have no intrinsic expiry timer; removal/replacement in the running service's
startup configuration takes effect through a controlled restart/redeployment,
not hot key administration. A console 401 clears its session; 403 means
permission is missing, not that a mutation succeeded. Preserve any uncertain
submission identity before changing actors or credentials.

Retain sanitized status/reason, key **name**, rotation/config identity and
request/incident IDs, never the secret. Confirm recovery with a signed canary or
`GET /v1/me` and the intended least-privilege operation. Investigate repeated
unexpected rejects as possible hostile traffic rather than broadening scopes.

## Saturation

Impact: newly rejected traffic was not admitted; already admitted work may still
complete. First check count/byte budgets, waiting handlers, per-destination latency,
async queue depth and the effective sender/write/publish deadlines.

Mitigation: use sender backoff/durable upstream buffering and isolate or repair
the slow destination. Raise a legitimate workload budget only with measured
memory/headroom; do not remove limits blindly or acknowledge work that was not
accepted. An async queue acceptance is still memory-backed, not a durable spool.

Retain rates, resource limits, p95/p99/maximum observations, active/waiting counts,
rejects and independent receipts. Confirm bounded queues/memory return to their
normal range and a controlled signed request reaches the intended destination.
See [resource sizing](resource-limits.md).

## Destination

Impact: a branch can fail while another succeeds, or while `skip_failed` returns
2xx to the sender. Inspect destination readiness/outcomes, the selected flow
policy, failed branch and independent destination receipts.

Mitigation: repair the destination before retrying eligible captured failures;
retry only the captured failure branch, not successful siblings or the whole
inbound flow. A timeout/lost acknowledgement can follow acceptance: consult the
destination owner before resending. Async drops/dead-letter failures need their
own retained receipt evidence, not an assumption that replay stored everything.

Retain stable provider identity, record/run ID, destination/target, configuration
hashes and external receipts. Confirm a destination canary and the retained run
result, including any child failures. See [delivery guarantees](delivery-guarantees.md).

## Protection

Impact: protected requests can reject or become unready; failed compensation can
retain a reservation and reject legitimate retries until expiry. Check store
connectivity, permissions, schema, namespace/policy fingerprint, maintenance
status and live-identity capacity. Separate expected quota/duplicate rejects
from unavailable/conflicting infrastructure.

Mitigation: restore connectivity or resume the same approved policy-update target.
Do not recreate missing policies, reset state, change namespace or use FLUSH as
recovery. Missing history can admit duplicates even when a surviving policy
looks healthy. Escalate actual state loss to the storage and destination owners.

Retain policy definitions/fingerprints, status, owner/expiry and consumed-quota
evidence without credentials or unnecessary identities. Confirm the intended
quota/reservation behavior survives recovery and protected readiness is restored.
See [protection recovery](../guides/protection-stores.md#runtime-grants-and-state-loss-recovery).

## Capture loss

Impact: failures or view-only observations were not stored, so later recovery or
inspection may lack artifacts. Failure capture is best effort and does not alter
the already selected sender acknowledgement. Inspection loss is not proof of a
delivery failure.

First check the drop reason: buffer full, failed upload, storage capacity,
rejection/build failure or shutdown. Mitigate the actual dependency/capacity
problem and preserve independent receipts. Dropped artifacts are gone; logging
cannot recreate them. Repeated event diagnostics are sampled during storms,
while counters still count drops. Do not promise an individual log for every loss.

Retain counter increases/reasons, sample freshness, capture buffer/retention
usage and representative sanitized IDs. Confirm new controlled captures appear
with verified artifacts and counters stop increasing. Historical increases clear
from rate-window alerts only after the configured window/hold period.

## Replay capacity

Impact: capture can reject at configured retention limits independently of
physical disk exhaustion. Check **fresh** shared usage/capacity, ingest rejection
outcomes, unexpected-store reason counters, PostgreSQL disk/connection telemetry
and recent capture size/volume. PostgreSQL's disk-full SQLSTATE 53100 has a distinct
bounded `storage_exhausted` counter reason while the public API error stays
generic. Wrapped-error classification tests are not physical-disk-failure proof.

Mitigation: run supported GC for already expired records, increase reviewed
capacity with storage headroom, or dismiss only recovery evidence intentionally
abandoned after an explicit decision. Successful retry retains artifacts until
expiry. Shorter TTL affects new captures, not existing expiry timestamps. Never
delete tables/volumes or edit usage counters to silence a capacity alert.

Retain logical counts/digests, limits, expiry distribution, disk observations and
any irreversible operator decision. Confirm a controlled capture succeeds and
fresh sampled usage is within the intended budget. See [replay operations](replay-service.md#runbook).

## Unknown delivery

Impact: a previous execution may already have caused an external effect. Inspect
the original run, late results, record lineage and independent destination receipt
or deduplication history. Lease expiry does not cancel a running core execution.

Mitigation: keep the record unknown until investigated; confirm only with
independent evidence, or explicitly accept duplicate risk before another retry.
Dismissal abandons recovery and deletes artifacts; it does not confirm delivery.
Retain the original actor/key/body and receipt after a lost submission reply.

Confirm recovery through the retained receipt/audit and external acceptance,
not merely a resolved badge. Follow [uncertain-delivery investigation](replay-investigation.md).
Keep an older restored queued snapshot offline until reconciled; no workerless
serve or queued-cancellation API exists. See [restore boundaries](recovery.md#a-restored-queued-run-already-delivered).

## Stale work

Impact: queued recovery is not progressing, or cached state may be outdated.
Check state freshness first, then worker readiness/busy state, oldest queued age,
core control reachability/auth and available execution/lease/connection budgets.

Mitigation: restore the intended control/backend path and matching releases;
inspect blocked/unknown outcomes before issuing new work. Do not start extra
workers blindly on an unreconciled restore or infer that a lost control response
means nothing was delivered.

Retain queue age, sample timestamps, worker/core version and run/executor outcomes.
Confirm accepted queued runs reach a recorded terminal outcome with independent
receipt checks, and fresh queue age decreases without repeating successful branches.

## Database

Impact: replay state sampling or API operations cannot reach a compatible store;
previously exported gauges may still show old values. Check readiness, per-replica
`db_up`, sample timestamp, pool usage, connection/TLS/grants, disk and schema.

Mitigation: repair the approved connection/permission/storage problem. Do not
grant runtime DDL, run migrations from every replica or downgrade bookkeeping.
For incompatible or lost state, preserve the database and use the coordinated
[upgrade](upgrades.md) or [restore](recovery.md) procedure with the storage owner.

Retain sanitized errors, schema/image/config identities, pool/resource samples
and newer external receipts. Confirm matching schema/readiness and a fresh
state sample before resuming controlled recovery actions.

## Cleanup

Impact: physically expired state may accumulate and consume capacity. Logical
expiry and ownership fences remain separate from cleanup. Check error counters,
successful sweep age, configured intervals, locks, pool/grants and storage health.
Replay initializes its GC timestamp to process start until the first sweep;
that initial value is not proof of a completed sweep.

Mitigation: repair the specific dependency/grant issue, then use supported
bounded cleanup. `hookway-replay gc --config replay.yaml` operates on the selected
database; verify matching binary/schema/config and the target before running it.
A skipped advisory lock means another replica is sweeping, not a failure.
The automatic staleness alert uses the latest success among enabled replicas
within one database job, allowing four times the largest configured interval
plus a 120-second sweep allowance. Actual GC errors remain per-replica signals.
Keep configurations aligned; a long-cadence replica can delay this conservative
dataset-wide staleness signal. The dashboard retains individual timestamps.
Respect explicitly disabled or long in-process GC intervals; define external
maintenance ownership if automatic cleanup is disabled. Do not remove live
reservations, shorten their absolute expiry or force-delete audit/history.

Retain configured interval, sweep result/deleted counts, lock/permission and
usage evidence. Confirm a successful sweep or externally owned cleanup result,
fresh capacity observations and unchanged active-run/reservation fences.

## Telemetry

Impact: repeated hot-path log records were suppressed, or a recovered panic
needs investigation. Metrics and persisted replay audit are not sampled with logs.
Default info/warn/error operation budgets known repeated categories to ten log
records per second per category, shared across logger clones; suppression is
visible in fixed-category summaries (at most two per category/window) and an
unlabeled component counter. Thirty fixed categories bound the sampler state;
tail omissions remain visible in cumulative counters even if no later log arrives.
Startup,
configuration, shutdown and operator audit messages remain available.

First check severity/category, `hookway_logs_suppressed_total` or
`hookway_replay_logs_suppressed_total`, the original failure metrics and sink
health. Correlation IDs are diagnostic handles, not metric labels or durable
business identities. Request IDs must be one printable ASCII value of at most
128 non-whitespace bytes; invalid/ambiguous IDs are replaced with a generated UUID.

Mitigation: fix the underlying storm and retain counters plus representative
sanitized logs. Use explicit `logging.level: debug` only for bounded troubleshooting
with reviewed access/retention: it bypasses storm sampling and is not a bounded
production log mode. A record-rate budget does not bound arbitrary configured
header content, custom-code diagnostics or a blocking OS/stdout sink.

Confirm original error rates and suppression increases settle, without hiding
remaining unknown/capture loss. Payloads/secrets do not belong in incident logs,
allowlisted headers or operator notes. Preserve panic source/build evidence and
escalate reproducible panics to the release owner.

## Metrics scrape

Impact: the monitoring system cannot observe a target; this is distinct from an
application or database readiness failure. Check target status, private listener,
path, metrics-only credentials, TLS and network reachability. The application
can be running while the scrape fails; absent metrics are not healthy evidence.

Mitigation: repair the intended scrape configuration/secret permissions and keep
the listener private. Do not reuse a broader operator key, expose metrics publicly
or restart a healthy application merely because a scraper is misconfigured.

Retain sanitized scrape error, target/job identity and config fingerprint. Confirm
`up` and expected authenticated metric families return before trusting dashboards.

## Console unavailable: API and CLI alternatives

Use a named least-privilege key and matching API, without the console or Node:

| Need | Supported operation |
| --- | --- |
| Identity/scopes | `GET /v1/me` (`read`). |
| Current eligibility/evidence | `GET /v1/records/{id}` (`read`); no payload retrieval. |
| Input syntax only | `POST /v1/actions/validate` (`read`); validation neither checks current record state nor performs an action. |
| Eligible retry | `POST /v1/records/{id}/retries` (`retry`), preserved `Idempotency-Key` and exact acknowledgement body; 202 is a queued run, not delivery completion. |
| Follow accepted work | `GET /v1/retries/{run_id}` (`read`), record audit and independent destination receipts. |
| Confirm unknown outcome | `POST /v1/records/{id}/resolve` (`retry`), preserved actor/key/evidence body and independent receipt. |
| Deliberately abandon evidence | `POST /v1/records/{id}/dismiss` (`retry`); irreversible artifact deletion, no submission-idempotency receipt. |
| Offline configuration/maintenance | `hookway validate`, `hookway-replay validate`, `hookway state-store status`, `hookway-replay gc`; use their documented flags/config targets. |

The [investigation runbook](replay-investigation.md#identity-and-capabilities)
provides curl examples and exact problem codes. Use HTTPS/private access, keep
tokens out of shell history/process arguments/verbose traces where possible and
prefer a private credential file for automation. Never auto-follow an API redirect
with bearer credentials. Keep request/response evidence without credentials.

After API loss or an interrupted mutation reply, refresh state before any new
action. For retry/resolution, recover the same retained actor/key/body request;
another replica can return its original receipt. For dismissal, inspect state
instead of blindly reissuing deletion. Sign-out/reload loses the console's
in-memory uncertain submission: retain its displayed identity during handover.
Rejecting a repeat request does not establish that the original action failed;
keep the original actor, key and body until its receipt or independent evidence
settles the outcome. A 401 still clears the active browser session.
No API grants extra permissions because the UI is unavailable.

## Notification acceptance

Before relying on alerts, load the shipped rules and route them to an identified
receiver. Verify firing, notification receipt, useful runbook annotations and
resolved notification after recovery. Rule unit tests or a firing state alone
do not prove delivery. Keep the fixture receiver separate from any real responder.
Alertmanager routing and `send_resolved` are configured explicitly; see
[notification configuration](https://prometheus.io/docs/alerting/latest/configuration/).

Synthetic metric-fed rule/notification tests prove that pipeline, not an actual
backend fault. Retain separate actual-runtime incident evidence, full versus
accelerated rule intervals, job/replica topology, source/config/image identity
and the receiver's observed statuses. Deployments must test their real authorized
route, credentials, paging/handovers and production timing before acceptance.
