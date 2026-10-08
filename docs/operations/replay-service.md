# Replay service

Use the optional replay service to inspect and retry missed deliveries after
the sender got a success response. Core can send eligible failures to
`hookway-replay`, which stores them in PostgreSQL. Operators use its API or the
[replay console](replay-console.md) to recover them.

Retries are operator initiated. Capture is best effort; replay is not a durable
queue for every incoming event. Read [Delivery guarantees](delivery-guarantees.md)
when choosing how your flow acknowledges a request.

## What gets captured

Core captures a failure only when it answered the sender with a 2xx and no
`on_error` or `dead_letter` path handled the failure: a publish that
failed under `delivery_failure_policy: skip_failed`, a success continuation
that failed after its publish, or an async webhook delivery that failed for
good (retries exhausted or a terminal 4xx) with no dead letter, or whose dead
letter itself failed. A non-2xx leaves the retry to the sender, so it is never
captured. A flow opts out with `capture_failures: false`.

Each failure record uses `kind: failure` and a `replay_mode` of `resume` or
`publish_only`. It carries metadata (flow, node, destination, rendered target, event
and request ids, error class and sanitized message, attempts, and hashes of
the node's and destination's configuration with credentials removed), a
snapshot of the failed node's input, and the payload the node was sending. A
snapshot or payload over `max_record_bytes`, or one that cannot be rebuilt
exactly, makes the record metadata-only and not retryable, and so does a
failure whose bodies alone would overflow core's 64 MiB upload buffer. Capture never
changes a response or readiness; uploads are best effort and bounded
(see [Configuration § Replay](../reference/configuration.md#replay)).

## View-only observations

An inbound `inspect` node captures the current webhook body/context independently
of failure capture and final response status. Records use `kind: inspection`,
`retryable: false`, `not_retryable_reason: inspection_only`, and `occurred_at`.
They have a versioned JSON payload and no replay snapshot, failure message or
attempt count. Oversized/unsupported inspections are metadata-only with
`payload_unavailable_reason: too_large` or `unsupported`; their retry prohibition
remains `not_retryable_reason: inspection_only`. Retry and delivery-resolution paths
reject them; dismissal, payload permission, byte accounting and expiry use the
existing record store. Failure validation is unchanged.

`GET /v1/records` lists both kinds by default. Use `kind=failure` for missed
deliveries and `kind=inspection` for observations. Both use `occurred_at` for
ordering and date filters. The API advertises `record_api` and `inspection_records`
with contract version 1. [Configuration](../reference/configuration.md#inspecting-webhooks)
explains inline and sampled branches; [console instructions](replay-console.md#view-only-inspections)
include a runnable local example. Retention and upload/storage capacity are shared.
Apply migrations and deploy the replay API first, then matching console assets,
then core configurations using Inspect.

## How a retry runs

```
operator ──POST /v1/records/{id}/retries──▶ replay (queues a run)
replay worker ──POST /internal/replay/v1/deliver──▶ core control listener
core ──resumes the flow at the failed node──▶ destinations
core ──per-node outcomes + child records──▶ replay worker
```

A worker claims a queued run, sends the record's snapshot to a core replica's
control listener, and records the outcome. Core checks the record against its
**current** configuration, then resumes the flow at the failed node (a
`replay_mode: resume` record) or re-sends the prepared message (`publish_only`, async
webhooks), delivering inline so the reported outcome is real. Upstream gates
(`replay_guard`, rate limits, auth) never rerun; the envelope `id` and other
identity values are preserved so consumers can dedupe.

| Outcome | Run | Record |
|---|---|---|
| The node delivered (downstream failures become child records) | `succeeded` | `resolved` |
| The node failed again | `failed` (`node_failed`) | `open` |
| The configuration no longer fits (block reason) | `blocked` | `open` |
| Core refused the token (401/403) | `failed` (`executor_auth`) | `open` |
| Core refused the request before running it (another 3xx/4xx, e.g. `malformed_snapshot`) | `failed` (`executor_rejected`) | `open` |
| The snapshot is missing or fails its digest when the run is claimed | `failed` (`snapshot_unavailable`); core is not called | `open` |
| Core was busy or draining (`503 not_started`), or never received the whole request | sent again — three attempts in all — then `failed` (`executor_unavailable`) | `open` |
| The response was lost or timed out, core answered another 5xx or a malformed 200, or the lease expired | `unknown` | `unknown` |

Block reasons: `unsupported_snapshot_version`, `hash_mismatch`,
`flow_missing`, `flow_broken`, `node_missing`, `node_type_changed`,
`destination_changed`, `target_not_configured`. Nothing was sent.

Each run is sent to core at most once, except when core
provably never started it. A redrive never triggers capture,
dead-lettering, or compensations. There is no exactly-once promise: an
`unknown` record may or may not have been delivered, so retrying it requires
`"acknowledge_duplicate_risk": true`.

### Delivery state and dismissal

| From | Event | To |
|---|---|---|
| `open` | retry requested | `retrying` |
| `retrying` | run succeeded | `resolved` (terminal) |
| `retrying` | run failed or blocked | `open` |
| `retrying` | response lost or lease expired | `unknown` |
| `unknown` | independently confirmed by an operator, with evidence and no active retry | `resolved` (`operator_confirmed`) |
| `unknown` | retry requested with `acknowledge_duplicate_risk` | `retrying` |


Dismissal is separate from delivery state: `dismissed_at` records when artifacts
were removed and quota released. It preserves the prior `delivery_status` and
audit evidence. Available inspections and open/unknown failures can be dismissed;
retrying and resolved failures cannot. Inspections have no `delivery_status`.

A child record has its own resume node strictly downstream of its parent's,
the same `event_id`, and the root's expiry.

## Running it

Both services ship from one Dockerfile: `docker build --target core` and
`docker build --target replay` (scratch images, UID 65532). For commands, flags and exit codes, use the
[CLI reference](../reference/cli.md#hookway-replay). Run migrations before serving;
`serve` refuses an older, newer, legacy or incomplete database schema before
opening listeners or starting workers. Readiness requires the exact embedded
schema version and the generic-record table/column contract, not just a
reachable PostgreSQL server or a `records` table.

`--config` defaults to `$HOOKWAY_REPLAY_CONFIG`. The
[`examples/replay/`](../../examples/replay) Compose stack runs core ×2, replay ×2,
PostgreSQL and a one-shot migration. Replicas are stateless and share one
PostgreSQL database. Size replica counts and connection pools for that database.

Run `hookway-replay migrate --config replay.yaml` as a one-shot container before
starting or upgrading the replay service. The Compose stack waits for successful
migration completion before starting replay. On AWS ECS, use the replay image
with the same configuration and database connection for a one-off migration
task; require successful completion before updating the service. If migration
fails, inspect its logs and stop the upgrade.

## Configuration

Strict decoding and `${VAR}` expansion, like core. Defaults in parentheses.

| Key | Meaning |
|---|---|
| `server.addr` (`:8080`) | API listener. |
| `logging.level` (`info`) | `debug`, `info`, `warn`, or `error`. |
| `metrics.enabled`, `path`, `addr`, `runtime_metrics` | Prometheus endpoint, the same shape as core's. With no `addr` it is served on `server.addr`. Requires a key with the `metrics` scope; enabling metrics without one is a configuration error. |
| `database.url` | PostgreSQL connection string. |
| `database.max_conns` (`10`) | Pool size. GC holds one connection while it sweeps. |
| `database.tls.*` | `ca_cert`, `client_cert`, `client_key`, `server_name`, `insecure_skip_verify` (warns). When set, TLS is mandatory. |
| `core.control_url` | Core's control listener, e.g. `http://hookway-core-control:9092`. |
| `core.control_token` | Must equal core's `replay.control.token`. |
| `core.tls.*` | Client TLS toward core's control listener. The listener itself serves plain HTTP, so use this only when a proxy or service mesh terminates TLS in front of it. |
| `server.cors.allowed_origins` (`[]`) | Exact browser origins permitted to call the API; no paths. All operations still require scoped keys. |
| `retention.ttl` (`168h`) | How long a record lives; retries never extend it. |
| `retention.max_records` (`100000`), `max_bytes` (`10 GiB`) | Capacity. At either limit, new captures get `507` and core drops them (children of runs are always recorded). Dismissed records do not count. |
| `retention.max_record_bytes` (`16 MiB`) | Largest accepted artifact; at most `max_bytes`. |
| `retention.gc_interval` (`15m`) | In-process GC period; `0` disables it (run `hookway-replay gc` on a schedule). |
| `workers.concurrency` (`4`, max 256) | Runs one replica executes at once. |
| `workers.execution_timeout` (`30s`) | Per run; core's budget is this minus 5 s. |
| `workers.lease` (`60s`) | At least `execution_timeout + 15s`. A run still running past its lease becomes `unknown`. |
| `keys[]` | `name` (audit identity), `token` (32–256 bytes, unique, not the control token), `scopes`: `capture`, `read`, `payload`, `retry`, `metrics`. Give core a `capture`-only key and Prometheus a `metrics`-only key; keep `payload` and `retry` for operators. See [key generation and rotation](../reference/security.md#replay-api-keys-and-abuse-limits). |

Core's side is the `replay:` block — see
[Configuration § Replay](../reference/configuration.md#replay).

## API

Bearer tokens, compared in constant time; errors are RFC 9457
`application/problem+json` with a stable `code`.

| Endpoint | Scope | Behavior |
|---|---|---|
| `GET /v1/me` | `read` | Authenticated key name, scopes and implemented API capabilities; no credential. |
| `POST /v1/actions/validate` | `read` | Pure Go/WASM action-input validation. Does not execute an action or check record eligibility. |
| `POST /v1/records/{id}/resolve` | `retry` | Resolve an unexpired unknown record with a required investigation note, optional reference and `Idempotency-Key`. Preserves artifacts and original unknown evidence. |
| `POST /v1/records` | `capture` | Ingest (core only). `201`; `200` duplicate; `409 conflict`; `413 record_too_large`; `507 capacity_exceeded`. |
| `GET /v1/records` | `read` | Summaries, newest first, cursor-paginated. Omitted `kind` lists both failures and inspections, ordered by `occurred_at` then ID. Filters: `flow`, `node_id`, `destination_id`, `delivery_status`, `kind` (`failure` / `inspection`), `replay_mode` (`resume` / `publish_only`), `dismissed` (`true` / `false`), `retryable`, `since`, `until` (RFC 3339), `limit` (default 50, max 500), `cursor`. |
| `GET /v1/records/{id}` | `read` | Metadata, artifact sizes and digests (never bytes), runs, history, parent and children. |
| `GET /v1/records/{id}/payload` | `payload` | The stored payload bytes after verifying their digest (`500 payload_corrupt` otherwise). Recorded in history. |
| `POST /v1/records/{id}/retries` | `retry` | Requires `Idempotency-Key`. Body `{"acknowledge_duplicate_risk": false}`. `202 {"run_id": …}`; `409 run_active`; `422 not_retryable`, `duplicate_risk_unacknowledged`, `idempotency_key_mismatch`. |
| `POST /v1/retries` | `retry` | Batch: `{"record_ids": [...], "acknowledge_duplicate_risk": false}`, up to 100, one `Idempotency-Key`; per-item results. |
| `GET /v1/retries/{run_id}` | `read` | Run status and per-node result. |
| `POST /v1/records/{id}/dismiss` | `retry` | Available inspections or open/unknown failures only; sets `dismissed_at` and deletes artifacts without changing delivery state. |
| `GET /healthz`, `GET /readyz` | none | Liveness; readiness is the database reachable and the schema current. |

Set `REPLAY_URL` to the browser/host-reachable API URL, `ID` to a failure ID,
and `RETRY_KEY` to a new key for this deliberate retry. Keep that key and request
body if the reply is lost, so resubmission cannot create a second run.

```bash
curl -sS -H "Authorization: Bearer $REPLAY_OPERATOR_TOKEN" \
  "$REPLAY_URL/v1/records?kind=failure&delivery_status=open&dismissed=false&flow=orders&limit=20"
curl -sS -X POST -H "Authorization: Bearer $REPLAY_OPERATOR_TOKEN" \
  -H "Idempotency-Key: $RETRY_KEY" -H "Content-Type: application/json" \
  -d '{"acknowledge_duplicate_risk": false}' \
  "$REPLAY_URL/v1/records/$ID/retries"
```

Resolution is labeled `operator_confirmed`; successful retry execution is labeled
`retry_succeeded`. An operator note does not convert an unknown run into a
confirmed adapter result. See the [investigation runbook](replay-investigation.md)
for evidence, receipts, idempotency and races. Resolution retains
artifacts and capacity usage until ordinary expiry, whereas dismissal deletes
artifacts immediately.

## Shared record storage

Failures and inspections share PostgreSQL storage and retention limits.
Payload/snapshot bytes are stored separately from metadata; list responses
do not load them.

The current generic-record database uses migration version 1. Older
failure-shaped databases also used version 1 and are not compatible.
`migrate` and `serve` check the schema shape as well as its version; they do
not convert records or repair incompatible tables. Preserve legacy data and
use a fresh database for the current service. See
[legacy preservation](recovery.md#legacy-replay-databases).

A failed initial migration rolls back without advancing the version. Repair the
reported connection/permission problem and rerun the same matching migration.
If tables exist without the applied migration, preserve and investigate them
rather than deleting or editing migration history. Serving never migrates;
require successful migration before starting workers.

Use matching core, replay, console and WASM versions. The API uses
`/v1/records`; legacy failure routes are not provided.

## Operations

Monitor capture drops, storage usage, queued/unknown runs, worker outcomes and
database health. Import the [replay dashboard](../../metrics/grafana/hookway-replay.json)
and load the [alert rules](../../metrics/prometheus/alerts.yaml). The
[monitoring guide](observability.md#signals-and-aggregation) explains aggregation
across replicas and how to spot stale samples.

Scrape a private metrics listener with a dedicated metrics-only key. Keep its
credential file readable only by Prometheus; never put it in the frontend image.
For example, with replay `metrics.addr: ":9090"` and `metrics.path: /metrics`:

```yaml
scrape_configs:
  - job_name: hookway-replay
    metrics_path: /metrics
    authorization:
      type: Bearer
      credentials_file: /run/secrets/hookway_replay_metrics_key
    static_configs:
      - targets: ["hookway-replay:9090"]
```

Missing or incorrect keys are rejected. Use HTTPS when the scrape crosses an
untrusted network; TLS termination and listener network policy belong to the deployment.

**Dashboards.** `metrics/grafana/hookway-replay.json`; the core overview has
a "Capture and redrive" row.

**Alerts** (`metrics/prometheus/alerts.yaml`, group `hookway.replay`): capture drops, usage
over 80% of a limit, capacity rejections, a queued run older than 5 minutes,
any `unknown` record, a run failure ratio over 20%, a replica without its
database, and GC failing or stalled.

### Runbook

- **Capture drops** — `reason` says why: `buffer_full` (core's 128-record /
  64 MiB buffer filled: replay slow or down, or a burst of large failures), `upload_failed` (three attempts in 5 s
  failed), `capacity` (replay at a retention limit), `rejected` (replay
  refused the record or core could not build it; core logs the reason),
  `shutdown`. Dropped failures are gone. Representative core Error logs identify
  events by `event_id`, but repeated diagnostics are sampled during storms;
  counters still count every drop. Logs are not a durable per-event loss ledger.
- **At capacity** — run GC for already expired records, dismiss recovery work
  you intentionally abandon, or raise `max_records`/`max_bytes`. A successful retry
  retains artifacts until expiry and does not free capacity. Shortening
  `retention.ttl` affects newly captured records, not existing expiry timestamps.
- **Unknown records** — core may or may not have delivered. Follow the
  [investigation runbook](replay-investigation.md): confirm destination receipt
  before resolving with a note/reference, or assess duplicate risk before
  retrying. Dismissal discards recovery evidence; it does not confirm delivery.
- **Blocked runs** — the configuration moved on (the block reason says how).
  Fix the configuration and retry, or dismiss.
- **Stale queue** — no replica is claiming: check replica health,
  `workers.concurrency`, and that core's control listener answers.

The complete [incident instructions](observability.md) cover auth/admission,
protection, storage/cleanup, scrape failures and notification ownership. Monitoring
is optional; structured logs and health diagnostics do not require it.

## Security

- Records hold request bodies and, for proxy redrive, the original headers.
  Encrypt the database's storage (for example RDS with KMS) and restrict who
  holds `payload` and `retry` keys.
- Keep the replay API and core's control listener on private networks. The
  control token lets its holder make core deliver captured payloads to any
  configured destination; the capture token lets its holder add records.
- Neither the replay API nor core's control listener terminates TLS; they
  serve plain HTTP. Keep them on a trusted network, or put a TLS-terminating
  proxy or service mesh in front and point `replay.url` / `core.control_url`
  at it (`replay.tls` and `core.tls` configure the client side).
- Core's upload TLS (`replay.tls`), replay's database and control TLS
  (`database.tls`, `core.tls`) all warn when `insecure_skip_verify` is set.
- Snapshot and payload bytes are never logged; metadata never carries
  credentials (URLs in error messages keep only scheme and host; configuration
  hashes drop secret fields, headers, URL userinfo and queries).

## Performance

See the dated [replay capture and ingest benchmarks](../evidence/performance.md#replay-capture-and-ingest-benchmarks)
for the measured environment, payloads, contention and sizing limits.

## Deployment notes

- **Connection poolers.** GC takes a session-level advisory lock and runs its
  batches on that one connection. Behind PgBouncer in transaction-pooling mode
  the lock is not tied to the session; connect replay directly or through
  session pooling.
- **Memory.** An in-flight run holds its snapshot, the encoded request (a
  second copy), and core's answer, which may carry child records up to 64 MiB:
  budget about `2 × max_record_bytes + 64 MiB` per worker slot. An ingest
  holds about twice `max_record_bytes`. So 4 workers with 16 MiB records need
  roughly 400 MiB for runs alone; the maximum concurrency of 256 needs tens of
  GiB — size `workers.concurrency` to the replica.
- **Clocks.** Leases and expiry use each replica's clock; keep NTP running.
- **Shutdown and upgrades.** Follow the [coordinated withdrawal, drain, migration
  and rollback procedure](upgrades.md). Core and replay must use the same release.


## Separate migration and runtime database roles

On a dedicated replay database, migrate using the schema owner. A separate
runtime login can use these grants after migration:

```sql
-- On a dedicated replay database, remove inherited DDL access first.
REVOKE CREATE ON SCHEMA public FROM PUBLIC;
GRANT USAGE ON SCHEMA public TO replay_runtime;
GRANT SELECT ON goose_db_version TO replay_runtime;
GRANT SELECT, INSERT, UPDATE, DELETE
  ON records, record_artifacts, retry_runs, record_events, usage, delivery_resolutions
  TO replay_runtime;
GRANT USAGE, SELECT ON SEQUENCE record_events_id_seq TO replay_runtime;
```

Provision database `CONNECT` and the login secret separately. No schema ownership,
`CREATE`, or migration-table writes are needed. Update these grants
when a migration adds new live objects. This database role is distinct from
HTTP API key scopes; a read-only operator token does not require a separate
read-only service database connection.
