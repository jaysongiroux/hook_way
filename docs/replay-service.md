# Replay service

The replay service (`hookway-replay`) keeps the failed deliveries core
acknowledged to their senders and lets an operator inspect, download, retry,
or dismiss them. It is optional: core runs exactly as before without it.
Retries are operator-initiated through an HTTP API — there is no UI and no
automatic re-drive.

## What gets captured

Core captures a failure only when it answered the sender with a 2xx and no
`on_error` or `dead_letter` path handled the failure (I20): a publish that
failed under `delivery_failure_policy: skip_failed`, a success continuation
that failed after its publish, or an async webhook delivery that failed for
good (retries exhausted or a terminal 4xx) with no dead letter, or whose dead
letter itself failed. A non-2xx leaves the retry to the sender, so it is never
captured. A flow opts out with `capture_failures: false`.

Each record carries metadata (flow, node, destination, rendered target, event
and request ids, error class and sanitized message, attempts, and hashes of
the node's and destination's configuration with credentials removed), a
snapshot of the failed node's input, and the payload the node was sending. A
snapshot or payload over `max_record_bytes`, or one that cannot be rebuilt
exactly, makes the record metadata-only and not retryable, and so does a
failure whose bodies alone would overflow core's 64 MiB upload buffer. Capture never
changes a response or readiness (I15); uploads are best effort and bounded
(see [Configuration § Replay](configuration.md#replay)).

## How a retry runs

```
operator ──POST /v1/failures/{id}/retries──▶ replay (queues a run)
replay worker ──POST /internal/replay/v1/deliver──▶ core control listener
core ──resumes the flow at the failed node──▶ destinations
core ──per-node outcomes + child records──▶ replay worker
```

A worker claims a queued run, sends the record's snapshot to a core replica's
control listener, and records the outcome. Core checks the record against its
**current** configuration, then resumes the flow at the failed node (a
`resume` record) or re-sends the prepared message (`publish_only`, async
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

**Guarantees.** Each run is sent to core at most once, except when core
provably never started it. A redrive never triggers capture,
dead-lettering, or compensations (I21). There is no exactly-once promise: an
`unknown` record may or may not have been delivered, so retrying it requires
`"acknowledge_duplicate_risk": true`.

### Record lifecycle

| From | Event | To |
|---|---|---|
| `open` | retry requested | `retrying` |
| `retrying` | run succeeded | `resolved` (terminal) |
| `retrying` | run failed or blocked | `open` |
| `retrying` | response lost or lease expired | `unknown` |
| `unknown` | retry requested with `acknowledge_duplicate_risk` | `retrying` |
| `open`, `unknown` | dismissed | `dismissed` (terminal; artifacts deleted) |

A child record has its own resume node strictly downstream of its parent's,
the same `event_id`, and the root's expiry.

## Running it

Both services ship from one Dockerfile: `docker build --target core` and
`docker build --target replay` (scratch images, UID 65532). The replay binary:

| Command | Does |
|---|---|
| `hookway-replay serve --config replay.yaml` | Runs the API, the workers, and the GC loop. Refuses to start when the database schema is behind the binary. |
| `hookway-replay migrate --config replay.yaml` | Applies versioned schema migrations. Run it before every upgrade. |
| `hookway-replay gc --config replay.yaml` | Runs one GC sweep (for `retention.gc_interval: 0`). |
| `hookway-replay validate --config replay.yaml` | Checks a config file. |
| `hookway-replay healthcheck [--url …]` | The image's health check. |

`--config` defaults to `$HOOKWAY_REPLAY_CONFIG`. Examples:
[`examples/replay/`](../examples/replay) (Compose: core ×2, replay ×2,
PostgreSQL, a one-shot migration) and [`deploy/kubernetes/`](../deploy/kubernetes)
(Deployments, Services, a migrate Job, NetworkPolicies, a Secret). Replicas are
stateless and share one PostgreSQL database; any number can run.

For the Kubernetes example, replace the Secret placeholders and image names,
then deploy in this order. Run the Job step on every upgrade: `generateName`
creates a new Job even when the previous run still exists.

```bash
kubectl apply -f deploy/kubernetes/secrets.yaml &&
kubectl apply -f deploy/kubernetes/network-policies.yaml &&
kubectl apply -f deploy/kubernetes/replay-config.yaml &&
job="$(kubectl create -f deploy/kubernetes/migrate-job.yaml -o name)" &&
kubectl wait --for=condition=complete "$job" --timeout=10m &&
kubectl apply -f deploy/kubernetes/replay.yaml &&
kubectl apply -f deploy/kubernetes/core.yaml
```

If the Job fails, inspect its logs and stop the upgrade before applying the
Deployments. The Job has a ten-minute TTL for cleanup, not for scheduling a
future migration run.

## Configuration

Strict decoding and `${VAR}` expansion, like core. Defaults in parentheses.

| Key | Meaning |
|---|---|
| `server.addr` (`:8080`) | API listener. |
| `logging.level` (`info`) | `debug`, `info`, `warn`, or `error`. |
| `metrics.enabled`, `path`, `addr`, `runtime_metrics` | Prometheus endpoint, the same shape as core's. With no `addr` it is served on `server.addr`. Guarded by keys with the `metrics` scope; unauthenticated (with a `validate` warning) when no key has it. |
| `database.url` | PostgreSQL connection string. |
| `database.max_conns` (`10`) | Pool size. GC holds one connection while it sweeps. |
| `database.tls.*` | `ca_cert`, `client_cert`, `client_key`, `server_name`, `insecure_skip_verify` (warns). When set, TLS is mandatory. |
| `core.control_url` | Core's control listener, e.g. `http://hookway-core-control:9092`. |
| `core.control_token` | Must equal core's `replay.control.token`. |
| `core.tls.*` | Client TLS toward core's control listener. The listener itself serves plain HTTP, so use this only when a proxy or service mesh terminates TLS in front of it. |
| `retention.ttl` (`168h`) | How long a record lives; retries never extend it. |
| `retention.max_records` (`100000`), `max_bytes` (`10 GiB`) | Capacity. At either limit, new captures get `507` and core drops them (children of runs are always recorded). Dismissed records do not count. |
| `retention.max_record_bytes` (`16 MiB`) | Largest accepted artifact; at most `max_bytes`. |
| `retention.gc_interval` (`15m`) | In-process GC period; `0` disables it (run `hookway-replay gc` on a schedule). |
| `workers.concurrency` (`4`, max 256) | Runs one replica executes at once. |
| `workers.execution_timeout` (`30s`) | Per run; core's budget is this minus 5 s. |
| `workers.lease` (`60s`) | At least `execution_timeout + 15s`. A run still running past its lease becomes `unknown`. |
| `keys[]` | `name` (audit identity), `token` (≥ 32 characters, unique, not the control token), `scopes`: `capture`, `read`, `payload`, `retry`, `metrics`. Give core a `capture`-only key and Prometheus a `metrics`-only key; keep `payload` and `retry` for operators. |

Core's side is the `replay:` block — see
[Configuration § Replay](configuration.md#replay).

## API

Bearer tokens, compared in constant time; errors are RFC 9457
`application/problem+json` with a stable `code`.

| Endpoint | Scope | Behavior |
|---|---|---|
| `POST /v1/failures` | `capture` | Ingest (core only). `201`; `200` duplicate; `409 conflict`; `413 record_too_large`; `507 capacity_exceeded`. |
| `GET /v1/failures` | `read` | Summaries, newest first, cursor-paginated. Filters: `flow`, `node_id`, `destination_id`, `status`, `kind`, `retryable`, `since`, `until` (RFC 3339), `limit` (default 50, max 500), `cursor`. |
| `GET /v1/failures/{id}` | `read` | Metadata, artifact sizes and digests (never bytes), runs, history, parent and children. |
| `GET /v1/failures/{id}/payload` | `payload` | The stored payload bytes after verifying their digest (`500 payload_corrupt` otherwise). Recorded in history. |
| `POST /v1/failures/{id}/retries` | `retry` | Requires `Idempotency-Key`. Body `{"acknowledge_duplicate_risk": false}`. `202 {"run_id": …}`; `409 run_active`; `422 not_retryable`, `duplicate_risk_unacknowledged`, `idempotency_key_mismatch`. |
| `POST /v1/retries` | `retry` | Batch: `{"failure_ids": [...], "acknowledge_duplicate_risk": false}`, up to 100, one `Idempotency-Key`; per-item results. |
| `GET /v1/retries/{run_id}` | `read` | Run status and per-node result. |
| `POST /v1/failures/{id}/dismiss` | `retry` | `open` or `unknown` only; deletes the artifacts. |
| `GET /healthz`, `GET /readyz` | none | Liveness; readiness is the database reachable and the schema current. |

```bash
curl -s -H "Authorization: Bearer $REPLAY_OPERATOR_TOKEN" \
  "http://replay:8080/v1/failures?status=open&flow=orders&limit=20"
curl -s -X POST -H "Authorization: Bearer $REPLAY_OPERATOR_TOKEN" \
  -H "Idempotency-Key: retry-$(date +%s)" -H "Content-Type: application/json" \
  -d '{"acknowledge_duplicate_risk": false}' \
  "http://replay:8080/v1/failures/$ID/retries"
```

## Operations

**Metrics.** Core: `hookway_capture_total{flow,outcome}`,
`hookway_capture_dropped_total{reason}`, `hookway_capture_buffer_bytes`,
`hookway_control_runs_total{flow,outcome}`, `hookway_control_inflight`.
Replay: every series is prefixed `hookway_replay_` — HTTP, auth, ingest
(`captures_total{outcome}`), downloads, sampled state gauges (`records{status}`,
`runs{status}`, `usage_*`, `capacity_*`, `oldest_queued_run_seconds`, `db_up`,
`schema_version`), workers (`runs_total{status}`, `executor_calls_total{outcome}`,
`workers_busy`, `leases_expired_total`, `late_results_total`,
`children_created_total`), GC, and the database pool. Every replica samples
the shared database, so aggregate the state gauges with `max`, never `sum`.

**Dashboards.** `grafana/dashboards/hookway-replay.json`; the core overview has
a "Failure capture and redrive" row.

**Alerts** (`deploy/alerts.yaml`, group `hookway.replay`): capture drops, usage
over 80% of a limit, capacity rejections, a queued run older than 5 minutes,
any `unknown` record, a run failure ratio over 20%, a replica without its
database, and GC failing or stalled.

### Runbook

- **Capture drops** — `reason` says why: `buffer_full` (core's 128-record /
  64 MiB buffer filled: replay slow or down, or a burst of large failures), `upload_failed` (three attempts in 5 s
  failed), `capacity` (replay at a retention limit), `rejected` (replay
  refused the record or core could not build it; core logs the reason),
  `shutdown`. Dropped failures are gone; their events are identified in
  core's Error log by `event_id`.
- **At capacity** — dismiss or retry `open` records, shorten `retention.ttl`,
  or raise `max_records`/`max_bytes`.
- **Unknown records** — core may or may not have delivered. Check the
  destination for the event `id`, then retry with
  `acknowledge_duplicate_risk: true` or dismiss.
- **Blocked runs** — the configuration moved on (the block reason says how).
  Fix the configuration and retry, or dismiss.
- **Stale queue** — no replica is claiming: check replica health,
  `workers.concurrency`, and that core's control listener answers.

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

Measured with `make bench-replay` on an Apple M4 Pro (arm64, 12 cores), Go
1.25.4, PostgreSQL 16 in a local container — single-process measurements to
size a deployment, not throughput claims, from one run each. Core's figures
are the request path of a `skip_failed` flow with one of two destinations down
and a raw body (`BenchmarkCaptureOverhead`, 200 requests each; p99 is the
198th-fastest); replay's are ingests of one snapshot and one payload of the
given size each (`BenchmarkIngest`, 30 records each, one ingester and
GOMAXPROCS ingesters).

| Payload | Core p99 without capture | Core p99 with capture | Record uploaded | Replay ingest, 1 ingester | 12 ingesters |
|---|---|---|---|---|---|
| 1 KiB | 0.29 ms | 0.46 ms | full | ~480 records/s | ~175 records/s |
| 256 KiB | 3.3 ms | 4.0 ms | full | ~135 records/s | ~145 records/s |
| 4 MiB | 49 ms | 46 ms | full | ~12 records/s | ~12 records/s |
| 16 MiB | 177 ms | 175 ms | metadata-only (`too_large`) | ~3 records/s | ~3.5 records/s |

Capture adds bookkeeping, not work, to a request: the failure is recorded and
the response is written; the record is built and uploaded afterwards on
background workers, so above small payloads the difference is run-to-run
noise. A failure whose retained bodies alone exceed the 64 MiB upload buffer —
here a 16 MiB raw body, whose envelope copies and base64 form add up past it —
is uploaded as its metadata-only `too_large` record instead of being held.

Replay ingest is serialized by the usage row every ingest updates: more
ingesters (or more replicas) do not raise it, and at small sizes contention
lowers it. The figures are therefore limits for the whole cluster, bounded by
writing the artifacts to PostgreSQL (roughly 70–115 MB/s here, per-transaction
latency at 1 KiB); size `retention` and the database for the capture rate you
expect.

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
- **Shutdown.** Core's control listener waits for in-flight redrives (bounded
  by the deadline replay sent) before its publishers close, even past
  `server.shutdown_timeout`: give core pods a termination grace period of at
  least `workers.execution_timeout` plus `server.shutdown_timeout`.
- **Upgrades.** Upgrade core and the replay service together: their wire
  contract is strict — both sides reject fields they do not know — so mixed
  versions are unsupported (captures from a newer core are refused and
  dropped; a newer core's redrive results come back `unknown`). Run `migrate`
  first; `serve` refuses an older schema.
