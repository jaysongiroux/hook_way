# Back up and restore state

Persistence across restart is not a backup. Recover a matching application,
configuration and state set, and reconcile destination receipts before resuming
work. A restored queued retry may already have delivered after the snapshot.
Never start replay workers on an unreconciled restore.

Use [upgrades](upgrades.md) for quiescence and coordinated termination;
[delivery guarantees](delivery-guarantees.md) and
[investigation](replay-investigation.md) explain uncertain side effects.
The same procedure applies to production-style local operation without a cloud
account. Do not use `make dev-down` or `prod-local-reset` for an upgrade.

## Define the recovery set

| State | Retain and verify |
| --- | --- |
| Application | Matching core/replay/console release and source identity; immutable image digests, WASM/JS assets, configuration checksums, secrets/role provisioning and network settings. Keep secrets separately access-controlled. |
| Replay PostgreSQL | Migration identity; records and artifacts including their digests; retry runs, audit events, operator resolutions, retained action idempotency and usage/sequence state. |
| Protection | Backend, namespace and policy fingerprints; consumed quotas, reservation owners/expiry and any interrupted maintenance target. Memory has no recoverable shared history. |
| Destinations | Stable provider IDs, per-destination receipt/checkpoint history and consumer deduplication state. A Hookway database cannot undo external delivery. Broker storage needs its own deployment-specific backup policy. |

If replay and PostgreSQL protection share a database, take one full-database
snapshot. Otherwise pause writes to both and record each snapshot's timestamp;
independent snapshots are not a distributed transaction. Include Redis in the
same quiescent recovery window when it owns protection. Restoring only replay
can rewind retries while keeping incompatible protection or destination history.

Record the required recovery point objective (RPO: acceptable lost-data interval)
and recovery time objective (RTO: time until reconciled service can resume).
Measure drain, dump, restore, verification, reconciliation and canary separately.
Local timings on a small synthetic dataset are not production targets. Without
agreed targets, report measurements and leave production acceptance open.

## Quiesce and preserve

1. Pause public ingress and operator actions at the edge. Arrange provider
   redelivery or durable upstream buffering; do not manufacture a success reply
   for traffic you did not accept. Restrict direct access as well as load-balanced
   traffic during the maintenance window.
2. Let admitted requests, async queues, capture uploads and retry runs settle.
   Check independent receipts, unknown runs and capture drops. Keep replay's
   capture API reachable while final core capture is flushed. If work cannot
   settle, record the uncertainty before proceeding.
3. Follow the coordinated stop procedure. Confirm successful process exits;
   failed drain or forced kill is not proof that work was absent. With all writers
   stopped, record schema/policy identity, counts, representative payload/snapshot
   digests and the destination checkpoint.
4. Save a new, uniquely named backup. Keep the original database/volumes and
   previously known-good backup. Encrypt/access-control actual backups and
   test their off-host availability; compressed dump files are not encrypted.

PostgreSQL `pg_dump` makes a consistent snapshot of one database, but does not
include global roles/tablespaces. Preserve role provisioning separately. Restoring
a dump can execute code from its source; restore only trusted backups in an
isolated environment. See [PostgreSQL backup tooling](https://www.postgresql.org/docs/17/app-pgdump.html).

## Local PostgreSQL rehearsal

This example targets the dedicated **full production-local** database and a new
database inside its PostgreSQL container, not an existing production target.
Use the saved custom directory/project if different. First quiesce the application
as above; keep PostgreSQL running. Do not run an unconditional full-stack `up`
while the restore is still being examined.

```sh
set -eu
export HOOKWAY_LOCAL_DIRECTORY="$(pwd)/examples/production-local/.local"
umask 077
task_backup_dir=$(mktemp -d "${TMPDIR:-/tmp}/hookway-recovery.XXXXXX")

docker compose --env-file "$HOOKWAY_LOCAL_DIRECTORY/.env" \
  -f examples/production-local/docker-compose.yaml exec -T postgres \
  pg_dump --username=replay --dbname=replay --format=custom \
  > "$task_backup_dir/replay.dump"

shasum -a 256 "$task_backup_dir/replay.dump"
docker compose --env-file "$HOOKWAY_LOCAL_DIRECTORY/.env" \
  -f examples/production-local/docker-compose.yaml exec -T postgres \
  pg_restore --list < "$task_backup_dir/replay.dump"

# Choose a NEW target. createdb refuses an existing database; no DROP or --clean.
task_restore_db=hookway_restore_rehearsal
docker compose --env-file "$HOOKWAY_LOCAL_DIRECTORY/.env" \
  -f examples/production-local/docker-compose.yaml exec -T postgres \
  createdb --username=replay --owner=replay_owner "$task_restore_db"

docker compose --env-file "$HOOKWAY_LOCAL_DIRECTORY/.env" \
  -f examples/production-local/docker-compose.yaml exec -T postgres \
  pg_restore --username=replay --role=replay_owner \
  --dbname="$task_restore_db" --no-owner --no-acl --single-transaction \
  < "$task_backup_dir/replay.dump"
```

Stop on any command failure; do not pipe an incomplete dump into a live database.
Inspect dump warnings and verify its checksum before restore. This example uses
the local bootstrap administrator for backup/target creation, then restores as
the dedicated migration owner. `--no-owner --no-acl` deliberately does **not**
recreate source ownership or grants. Provision database access and reapply the
reviewed runtime grants **on the restored database**, not the original. For a
database containing protection owned by another migration role, preserve/recreate
those owners and schema-specific grants rather than assigning everything to
`replay_owner`. Do not grant runtime DDL access to make restoration convenient.

`--single-transaction` makes this restore all-or-nothing and implies exit-on-error;
it is appropriate for this small rehearsal, not an unmeasured large restore.
See [PostgreSQL restore options](https://www.postgresql.org/docs/17/app-pgrestore.html).
Keep the archive and private directory until the recovery is accepted; retire
them explicitly afterward. Never commit backups, DSNs or copied `.env` files.

### Verify before starting workers

Compare the source checkpoint to the isolated restore:

- Migration identity and required schema objects, plus configuration/policy
  fingerprints. A matching numeric migration version alone is insufficient.
- Canonical logical rows across records, artifacts, runs, audit, resolutions and
  usage; representative artifact lengths and SHA-256 digests. A successful
  `pg_restore` exit code alone is insufficient.
- Audit sequence continuity and idempotent action receipts. Inspection records
  remain view-only; resolved/unknown delivery evidence remains distinct.
- Protection quotas and reservation owner/expiry. Expiry uses absolute time;
  a reservation that expires during recovery must not be artificially extended.
- Restricted runtime permissions and secret/config routing to the new target.
  Test startup with the matching binary/config only after reconciliation permits
  execution; `serve` immediately starts workers, it is not a read-only verifier.

Use a read-only database connection for initial inspection. Do not set a zero
worker concurrency or use a blanket unsafe runtime mode as a recovery bypass.
The native acceptance suite performs synthetic logical/digest comparisons and
checks receipts outside the killed/replaced core process. Your production
dataset, extensions, roles and managed backup mechanism need their own rehearsal.

## A restored queued run already delivered

Preserve the newer/source database and independent receipt ledger. For each
queued/running snapshot run, correlate its record/run ID, stable provider ID,
destination/target, payload digest and later audit history. A missing receipt
search result alone does not establish nondelivery.

Do not release the restore to workers until each pending run is reconciled.
There is no built-in restore-quarantine mode or bulk cancellation API. The
runtime rejects dismissal/resolution of active runs; do not bypass that with
ad-hoc row edits. Prefer the preserved newer database when its state is sound.
If only the snapshot remains, keep it offline and design an explicit recovery
decision with the destination owner; downstream idempotency must cover any
deliberately repeated effects. Do not label restored queued work `unknown` or
`resolved` merely because the backup is old.

Only after reconciliation, grants/schema/config checks and a controlled canary
may workers/ingress resume. Retain the old database and post-snapshot evidence
until the recovery owner accepts the lost-data interval.

## Redis protection recovery

Distinguish connectivity loss from data loss. Existing policies/entries recover
after connectivity returns; readiness probes do not recreate missing policies.
A missing policy fails closed, while a missing individual reservation with an
intact policy can admit a duplicate. Restarting blindly can create fresh policies
and silently discard the protection history you intended to preserve.

Stop protected writes before backup/restoration, retain namespace/fingerprints
and expiry timestamps, and reconcile quotas/receipts before admission resumes.
An interrupted policy update stays fail-closed; resume the same explicit target
and expected fingerprint, not a new namespace or policy ID.

Use the deployed Redis version's supported persistence/backup procedure. Redis 7
multi-part AOF includes a manifest and base/incremental files; copying one live
AOF file is not a complete backup. For this local recipe, quiesce, stop only its
identified Redis container and preserve its complete data volume/configuration
before restoring into a separate target. Do not overwrite the original volume or
run FLUSH commands as recovery. Durability depends on persistence/fsync and
replication configuration, not just `noeviction` or `appendonly yes`.
See [Redis persistence](https://redis.io/docs/latest/operate/oss_and_stack/management/persistence/)
and [Hookway protection recovery](../guides/protection-stores.md#runtime-grants-and-state-loss-recovery).

## Legacy replay databases

Older failure-shaped databases are not supported upgrades to the generic-record
baseline. Preserve the old database, matching binaries/configuration and backups;
inventory retained failures, audit and destination receipts. Provision a **new**
database and matching current components only after deciding who owns recovery
of old work. A new installation does not convert or resolve legacy deliveries.

Do not delete old volumes, edit the Goose version, rename tables or invent a
lossless migration. If existing deployments require conversion, that is a scoped
design/data decision before cutover. Mixed releases and schema downgrades remain
unsupported; see [compatibility](upgrades.md#compatibility-policy).

## Release acceptance boundaries

Record full restore and reconciled-resume durations, snapshot timestamp/lost-data
interval, dataset size, tool/backend versions, architecture, resources and exact
application/config identity. A same-schema installation or same-build restore
is not cross-release migration/rollback proof. Retain a real previous supported
release and its database set when one exists; otherwise identify first-install
recovery as the tested boundary. Primary failover, off-host loss, encryption/key
recovery and ECS deployment are separate deployment-owner acceptance gates.
