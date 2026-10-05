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
4. Stop all replay replicas, allowing any final claimed runs to finish, then
   stop all core replicas using their full shutdown budget. Verify both have
   exited successfully and no running replay jobs remain. Investigate any
   unfinished work or timeout instead of assuming it was delivered. Do not
   migrate while old replay processes run.
5. Back up the quiescent replay database and verify the restore procedure. Keep
   old images/configuration and this snapshot together. Record a reconciliation
   checkpoint for downstream deliveries before proceeding.
6. Run `hookway-replay migrate --config <file>` using the new release. Stop if it
   fails. Start new core replicas, then the matching replay release. Keep public
   ingress and operator retries paused until both pass readiness and configuration
   checks. Investigate any expired `unknown` replay runs before reissuing them.
7. Exercise a signed canary through the real destination, then a controlled
   captured failure and operator retry. Check destination receipts and replay
   outcomes. Resume traffic and retries; monitor errors, latency, capacity,
   capture drops, and queue depth.

## Rollback procedure

Before migration, restore the previous image pair and configuration normally.
After migration, stop both services and repeat the quiescence steps above.
Restore the previous database snapshot to a separate database, verify it, then
point the previous replay release at that restored database and start the
previous core/replay pair. Preserve the upgraded database for reconciliation.

Restoring a snapshot discards replay records and state changes made after it.
Do not automatically re-run restored jobs: a job marked queued in the snapshot
may already have delivered. Reconcile downstream receipts/event IDs and the
preserved upgraded database first. Configuration changes can also make existing
snapshots incompatible with a flow; a matching binary pair alone cannot repair
those records.

## Release acceptance evidence

Automated tests cover readiness withdrawal, continued service during withdrawal,
admitted-request draining, rejection of queued/new work, concurrent listener
shutdown, detached redrive ordering, and the total cleanup ceiling. A process
SIGTERM test reconciles destination receipts across same-build core replacement.
`make replay-acceptance-test` also exercises quiescent replacement of both
services, reruns the current migration, and verifies a retained PostgreSQL failure
can be retried without redelivering its successful sibling. These tests do not
demonstrate a production load balancer, database snapshot restore, or an
old-release/new-release database migration.

For each release pair, retain staging evidence of: sender IDs versus destination
receipts during termination; measured readiness-removal time; no resource close
beneath admitted work; successful quiescent backup/restore; candidate migration;
canary plus replay delivery; rollback to the preserved image/config/database
set; and reconciliation of any uncertain outcomes. A failed or unperformed
migration/rollback rehearsal remains an explicit release gate.
