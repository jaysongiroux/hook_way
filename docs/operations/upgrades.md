# Upgrades and rollback

Use this procedure when changing a core/replay release pair. It covers stopping
traffic, draining work, migrating the database, and restoring the previous
version if needed. Keep the matching configuration and database backup with
each image set.

## Core shutdown contract

Core responds to SIGTERM and SIGINT in three stages:

1. **Withdraw:** `/ready` and `/readyz` return 503 with status `withdrawing`.
   `/health` and `/healthz` remain healthy. Existing and new flow requests still
   run during `server.shutdown_delay`, giving the load balancer time to remove
   the replica. Background destination probes cannot restore readiness.
2. **Drain:** new and queued flow requests receive 503 with `Retry-After: 1`.
   All listeners stop accepting connections concurrently; admitted HTTP requests
   and replay redrives finish before shared resources close. HTTP requests have
   `server.shutdown_timeout`; detached redrives retain their own deadlines.
3. **Close:** stop probes and flow resources, then publishers, then replay
   capture. Async publishers may each consume their configured drain timeout;
   capture has a final six-second drain allowance.

`server.shutdown_total_timeout` bounds the entire sequence from the signal,
including the withdrawal delay, all listeners, and resource cleanup. When omitted it defaults
to `max(60s, shutdown_delay + shutdown_timeout + 30s)`, preserving existing longer
HTTP drain settings. An explicit value must exceed `shutdown_delay + shutdown_timeout`. A total timeout, unfinished asynchronous publisher drain, or
failed HTTP drain exits unsuccessfully; dependent resources are not closed under
unfinished handlers. Process exit can lose unfinished work. This is a safety
ceiling, not a delivery guarantee or durable spool.

```yaml
server:
  shutdown_delay: 5s          # default 0s; tune to measured load-balancer removal
  shutdown_timeout: 10s       # HTTP drain after withdrawal
  shutdown_total_timeout: 60s # complete process shutdown budget
```

Allow enough total time for the delay plus the larger of HTTP draining and the
maximum replay execution deadline, followed by sequential publisher drains and
capture cleanup, with margin. A publisher drain defaults to `shutdown_timeout`
when no destination override is set. Multiple async destinations can therefore
require a larger total. Set the orchestrator/container grace period **above**
the total (the Compose examples use 70s for a 60s total). Do not add an external
preStop sleep without also budgeting for it. Abrupt SIGKILL and host failure cannot
drain memory-backed work.

On listener failure core skips the withdrawal delay, stops admission, and exits
with an error after bounded cleanup.

Replay stops claiming new work at shutdown or listener failure. Already claimed
runs retain the remaining lease for snapshot retrieval, execution and finalizing
the result (10s reserved for finalization). HTTP draining allows 30s concurrently.
Set replay's grace period above `max(30s, workers.lease)` with margin. A killed
worker's unfinished run may expire to `unknown`; inspect downstream state before
retrying an uncertain delivery.

## Compatibility policy

Core and replay must use the **same release**. Their wire contract is strict;
mixed versions are unsupported. Replica replacement within one release is tested. Compatibility between
releases must be checked for the specific upgrade.
Use pinned image digests, preserve the previous configuration and image digests,
and treat a release pair plus its replay schema as one deployment unit.

| Boundary | Supported current combination |
| --- | --- |
| Application releases | Matching core, replay API and console/WASM from one release; mixed-release rolling upgrades are not assumed safe. |
| HTTP/WASM record/action contracts | Version 1 consumers and generic `/v1/records` routes; no legacy failure routes. |
| Replay database | Exact current embedded migration version (1 for this baseline), plus the required generic-record tables/types and fencing constraints/indexes. Legacy/future/incomplete shapes fail without repair before serving. |
| Core configuration | Strict current configuration contract; offline validation is not live dependency readiness. Explicit store declarations and matching policy fingerprints remain required. |
| Protection migration | Explicit store-owner migration before core startup; configured runtime roles do not need DDL. |

Schema version and release version are different identifiers. Two databases
reporting schema 1 can have incompatible failure-shaped versus generic-record
tables. Do not edit migration bookkeeping to bypass compatibility checks. See
[legacy preservation and isolated restore](recovery.md#legacy-replay-databases).

The migration CLI applies forward migrations. There is no supported automatic
schema downgrade. A previous replay binary is not assumed to understand a newer
schema, even if it starts. Review migration changes before an upgrade.

## Coordinated upgrade procedure

Use a maintenance window unless release-specific compatibility has been proven.
Test the procedure in staging with copies of configuration and a disposable
restored replay database before using it in production.

1. Record core/replay image digests, configuration, schema version and counts of
   pending/running replay jobs. Validate the candidate core configuration with
   `hookway validate --config <file>` using the new binary and actual environment.
2. Pause operator retries and incoming webhook traffic at the edge. Do not answer
   success for requests you have not accepted. Arrange sender-side redelivery or
   upstream durable buffering for sources that cannot retry the maintenance gap.
3. Keep both services running until admitted/pending core requests, asynchronous
   publisher queues, capture uploads, and queued/running replay jobs have settled.
   Confirm destination receipts and capture outcomes. If work cannot settle,
   resolve or explicitly reconcile it before proceeding; stopping replay while
   core is still producing captures can lose those best-effort uploads.
4. After queued/running retry work has settled and new actions are paused, stop
   all core replicas using their full shutdown budget **while replay capture is
   still reachable**. Then stop replay replicas. Verify successful exits and no
   running jobs; inspect final capture drops and destination receipts. Do not stop
   core underneath a known active retry; if work cannot settle, keep maintenance
   in place and explicitly reconcile the uncertainty. Do not migrate while any
   old replay process runs. Ordinary parallel Compose `down` is not a substitute
   for this controlled release-maintenance sequence.
5. Back up the quiescent replay database and verify the restore procedure. Keep
   old images/configuration and this snapshot together. Record a reconciliation
   checkpoint for downstream deliveries before proceeding.
6. Run `hookway-replay migrate --config <file>` using the new release and
   migration-only owner credentials. Require successful exit, then apply reviewed
   runtime grants for the candidate objects; stop on any failure. Start new core
   replicas, then the matching replay release. Keep public
   ingress and operator retries paused until both pass readiness and configuration
   checks. Investigate any expired `unknown` replay runs before reissuing them.
7. Exercise a signed canary through the real destination, then a controlled
   captured failure and operator retry. Check destination receipts and replay
   outcomes. Resume traffic and retries; monitor errors, latency, capacity,
   capture drops, and queue depth.

Back up coupled protection state too, not replay alone. Use
[Backup and restore](recovery.md) for isolated targets, logical/artifact/grant
checks and reconciliation of work delivered after the snapshot. A failed canary
keeps public ingress and operator retries paused; it is not permission to resume
the candidate or replay the old queue automatically.

## Rollback procedure

Before migration, restore the previous image pair and configuration normally.
After migration, reestablish quiescence and use the coordinated stop steps above.
Restore the previous database snapshot to a separate database and verify it.
Preserve the upgraded database and reconcile all restored queued/running work
against post-snapshot receipts **before starting either rollback service**;
`serve` immediately starts workers. Only after that decision may the matching
previous image/configuration pair point at the reconciled target and run a
controlled canary before public traffic/actions resume.

Restoring a snapshot discards replay records and state changes made after it.
Do not automatically re-run restored jobs: a job marked queued in the snapshot
may already have delivered. Reconcile downstream receipts/event IDs and the
preserved upgraded database first. Configuration changes can also make existing
snapshots incompatible with a flow; a matching binary pair alone cannot repair
those records.

Legacy failure-shaped databases are not compatible with the current record model.
Preserve their data and matching binaries; see
[legacy databases](recovery.md#legacy-replay-databases).

## ECS deployment ordering and stop budgets

Use the same maintenance ordering on ECS. Account for service replacement,
load-balancer removal and each container's stop allowance.

1. Pin the matching candidate digests and prepare migration-only task definitions
   with owner credentials, configuration and database network access. Keep those
   credentials out of the runtime tasks and console. Pause ingress/operator
   actions, settle work and preserve the coupled state/checkpoints first.
2. Stop old runtime tasks only after their required drains. Disable their service
   replacement during maintenance; stopping one task in a live service is not
   quiescence. Keep final core capture reachable before replay is stopped. Verify
   all old workers exited before migration.
3. Run the core protection migration if PostgreSQL protection is selected:
   `hookway state-store migrate --config core.yaml --store cluster`. Then run
   `hookway-replay migrate --config migrate.yaml` using the replay owner. Apply
   reviewed runtime grants for the candidate objects.
4. Require an empty `RunTask.failures` list and exactly the intended task ARN.
   Waiting for `STOPPED` is insufficient: inspect `DescribeTasks` failures, the
   expected migration container's presence and numeric `exitCode: 0`, plus
   `stoppedReason` and logs. Missing exit codes, launch failures or nonzero exit
   stop the rollout. Retain task definition/digest/config identities and logs.
5. Start matching core/replay/console tasks with runtime credentials. Keep the
   edge/actions paused through readiness, signed destination canary and controlled
   capture/retry receipt reconciliation. Only then resume service traffic.

AWS documents placement failures separately from tasks in
[RunTask](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_RunTask.html)
and reports task/container outcomes in
[DescribeTasks](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_DescribeTasks.html).
Do not interpret a successful API call or a stopped task as successful migration.

Set explicit container `stopTimeout` above core's total shutdown budget and above
replay's `max(30s, workers.lease)`, with measured margin. For the local recipe's
60s core total and 60s replay lease, 70s is the documented container allowance;
load-balancer withdrawal/deregistration must be measured separately. Fargate's
default 30s is insufficient for that configuration and its maximum is 120s.
Longer local runtime budgets remain valid, but must be reduced or deployed on
infrastructure supporting the required grace period before choosing Fargate.
Do not silently clamp application deadlines to hide this conflict. See the
[AWS stopTimeout contract](https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_ContainerDefinition.html).

## Check the upgrade before resuming traffic

Confirm the migration exited successfully, both services are ready, a signed
canary reaches its destination, and a controlled captured failure can be retried
without repeating successful branches. Keep the previous images, configuration
and database backup until the new version is working.

Test backup restoration and the exact rollback transition in staging. An
installation smoke test does not check database compatibility, and restoring a
snapshot can repeat work delivered after the backup. Follow
[Backup and restore](recovery.md) before starting restored workers.
