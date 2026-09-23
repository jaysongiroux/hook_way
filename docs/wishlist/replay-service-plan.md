# Optional Replay Service for Failed Deliveries

## Summary

Build `replay-service` as a separate Go executable and container in the existing repository. It stores failed deliveries, exposes inspection/download/retry APIs, and owns retention cleanup.

Two decisions are confirmed: retry **only the failed destination**, and support **all publish destination types**. Workflow nodes and successful branches do not rerun. A failure means a publish error observable by Hookway; later failures inside downstream consumers remain outside this service's responsibility.

## Architecture and Defaults

| Decision | Default and Reason |
|---|---|
| Deployment | Five Hookway nodes behind public ingress; two replay-service nodes behind an internal service address. Both remain stateless. |
| Storage | Shared PostgreSQL with payloads in `BYTEA` and metadata in relational/JSONB columns. Payload and metadata commit together. [PostgreSQL binary data](https://www.postgresql.org/docs/current/datatype-binary.html) |
| Transport | Authenticated HTTP initially. Introducing JetStream would add another persistent system to operate. |
| Retry execution | replay-service schedules work; Hookway executes it through its existing publishers, retaining ownership of destination credentials. |
| Capture guarantee | Best-effort until replay-service confirms database commit. Outages and crashes before that point can lose failure records. |
| Retry scheduling | Operator-initiated single or batch retries. Existing publishers retain their bounded retry policies; no continuous automatic redrive. |
| Retention | Seven days, configurable. Retries do not reset expiration. |
| Initial limits | Configurable defaults: 16 MiB per payload, 100,000 failure records, and 10 GiB retained payload bytes. These are operating limits, not database-size guarantees. |

Only replay-service and its cleanup command require the replay database. Hookway reaches replay-service through its internal load-balanced address; retries return through a separate, private Hookway control listener. No sticky sessions are required.

## Capture and Payload Contract

- Introduce a versioned `DeliverySnapshot` contract independent of the existing JSON representation of `queue.Message`. Separate preparation from sending where needed so initial delivery and retry use the same prepared application payload.
- Capture one immutable snapshot per failed publish invocation, after branch transforms. Include a unique delivery ID, original event/request IDs, flow and node IDs, destination type/ID, rendered target, failure details, payload size/hash, and adapter-specific execution metadata.
- Preserve exact application payload bytes for JSON envelopes, XML, raw bodies, multipart bodies, and object uploads. Preserve multipart boundaries and content encoding. Structured destinations retain their typed operation data; this is not a capture of network protocol frames.
- Freeze values affecting delivery identity: S3 object keys, Redis keys, message attributes, FIFO identifiers, Temporal workflow IDs/types, and equivalent adapter settings. Retain original event IDs and idempotency keys; each retry gets a separate run ID.
- Capture synchronous publish failures, async webhook terminal failures, and enqueue rejection when a prepared delivery exists. Filtered/unselected branches and unattempted branches have no failed-delivery snapshot. Preparation errors remain ordinary workflow errors.
- Keep existing dead-letter behavior. Capture the original delivery failure independently; internal fallback and redrive operations must not recursively create failure records.
- Send snapshots through a bounded in-memory uploader: four workers, at most 128 records and 64 MiB including active uploads. Use at most three HTTP attempts within a five-second total budget, with backoff and a stable delivery ID. Queue overflow or final failure logs an error and increments a metric.
- Preserve webhook response behavior and readiness during replay-service outages. Oversized payloads produce metadata-only, non-retryable records when capture succeeds. Reaching service capacity rejects new captures explicitly rather than silently deleting unexpired records.
- Store payload and execution metadata transactionally. Duplicate delivery IDs with identical content return the existing record; conflicting content returns `409`. Metadata listing must never load payload columns.

## APIs, Authentication, and Workers

| Endpoint | Behavior |
|---|---|
| `POST /v1/failures` | Internal ingestion; multipart metadata plus binary payload. Return `201` after commit, `200` for an identical duplicate. |
| `GET /v1/failures` | Cursor-paginated summaries, filterable by flow, destination, status, and time. |
| `GET /v1/failures/{id}` | Sanitized metadata, payload availability, and retry history. |
| `GET /v1/failures/{id}/payload` | Exact stored application bytes as an attachment; verify size/hash before returning; `application/octet-stream`, `Cache-Control: no-store`, no ranges. |
| `POST /v1/failures/{id}/retries` | Create a durable retry job and return `202` with its run ID. |
| `POST /v1/retries` | Create jobs for up to 100 explicit failure IDs, returning per-item results. |
| `GET /v1/retries/{run_id}` | Read queued, running, succeeded, failed, or unknown outcome. |
| `POST /internal/replay/v1/deliver` | Private Hookway endpoint; synchronously execute one stored delivery and report its actual adapter result, including for normally async webhooks. |

- Use named, scoped bearer keys over TLS: failure ingestion, metadata reading, payload download, and retry execution are separate permissions. The private Hookway executor uses a dedicated service credential. Missing required credentials fail configuration validation.
- Managed outbound authentication uses current destination credentials and fresh signatures. Proxy-mode webhooks retain original forwarded headers in restricted execution metadata; original signatures may expire and cannot be renewed by Hookway. Never expose those headers in summary APIs or logs.
- Retry only the original configured destination. Validate destination type, target, and resource identity against current configuration; missing or changed resources block execution with an explicit reason. Credential rotation remains allowed. Do not accept arbitrary replacement URLs or destination credentials through the API.
- Retry creation requires an idempotency key and permits only one active run per delivery. Workers claim jobs with short PostgreSQL transactions using `FOR UPDATE SKIP LOCKED`; network calls occur after commit. [PostgreSQL queue locking](https://www.postgresql.org/docs/current/sql-select.html)
- Use renewable leases and ownership tokens so stale workers cannot overwrite results. Default execution deadline: 30 seconds; lease: 60 seconds; heartbeat: 15 seconds. Limit each replica to four concurrent retries.
- A lost execution response or expired running lease becomes `unknown` and is never automatically resent. Retrying an unknown outcome requires explicit duplicate-risk acknowledgment. Preserve identifiers for downstream deduplication; do not promise exactly-once side effects.
- A successful run means the destination adapter confirmed acceptance, not that downstream processing completed. Redrive failures update the existing record and run history without another capture or dead-letter cycle.

## Retention, Rollout, and Verification

- Provide `replay-service serve`, `migrate`, and `gc` commands. Run `gc` every 15 minutes through cron or a Kubernetes CronJob. Use a PostgreSQL advisory lock and bounded deletion batches; protect active jobs and cascade deletion to payloads and history.
- Leave autovacuum enabled and document that deletion makes database space reusable without necessarily shrinking files. Track physical database/WAL growth separately from logical payload quotas. [PostgreSQL vacuuming](https://www.postgresql.org/docs/current/routine-vacuuming.html)
- Add versioned schema migrations and a small storage interface. Replace embedded replay configuration with remote service configuration; reject obsolete settings with actionable guidance. Leave existing database tables untouched and document manual retirement, since old records cannot reliably reconstruct branch-specific deliveries. Preserve the independent `replay_guard` node.
- Supply container builds, Compose examples, and Kubernetes examples for the five-ingestion/two-replay topology, private control routing, migrations, and cleanup. Keep destination configurations consistent across ingestion replicas. Add metrics and alerts for capture loss, capacity, oldest queued retry, unknown outcomes, database health, and cleanup failures.
- Test every destination adapter's snapshot round-trip and retry identity, including binary/XML/multipart fidelity, branch isolation, object filenames/content types, message attributes, current managed credentials, and expired proxy signatures.
- Exercise two replay replicas against one PostgreSQL database: duplicate capture, concurrent retry requests, worker termination, lost responses, lease fencing, cleanup races, payload corruption, and capacity exhaustion. Test five concurrent ingestion clients under complete destination failure to verify bounded memory and unchanged ingestion readiness.
- Implement in order: snapshot contract and adapter tests; database/API/worker service; Hookway capture and private executor; deployment/docs and acceptance tests. Run repository unit/race, conformance, affected integration, configuration/schema, build, and release checks. Benchmark representative payload sizes before claiming production throughput.
