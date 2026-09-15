# Configuration

Hookway reads one YAML config file. Set it with `HOOKWAY_CONFIG` or pass `--config`; the flag wins. YAML string values support `${ENV_NAME}` expansion.

Startup parses and validates the config before connecting to its destinations or serving traffic. Invalid YAML, unknown fields, missing required values, invalid durations, duplicate or overlapping endpoint paths, and unsupported backend/auth types stop the app with field-level errors and suggestions.

Example validation output:

```txt
config validation failed
- server.addr: listen address is required Suggestion: set server.addr to a host:port such as ":8080"
- endpoints.paddle_billing.auth.secret: secret is required Suggestion: reference an environment variable such as ${WEBHOOK_SECRET}
```

## Server

- `server.addr`: required listen address, example `":8080"`.
- `server.read_timeout`: optional duration, default `5s`. If set explicitly, must be positive; omit the field (rather than setting `0s` or a negative value) to use the default.
- `server.write_timeout`: optional duration, default `10s`. Same non-positive restriction as `read_timeout`.
- `server.idle_timeout`: optional duration, default `60s`. Same non-positive restriction as `read_timeout`.
- `server.shutdown_timeout`: optional duration, default `10s`. Same non-positive restriction as `read_timeout`.
- `server.publish_timeout`: optional duration, default `10s` (matches `write_timeout`/`shutdown_timeout`). Same non-positive restriction as `read_timeout`. Bounds the fan-out publish for a single webhook request; the publish runs detached from the inbound request's cancellation (a sender disconnect or timeout does not abort in-flight publishes) but is still bounded by this deadline so a stalled destination cannot hang a delivery indefinitely. Config validation rejects `publish_timeout` greater than `shutdown_timeout` (a slow synchronous publish could otherwise make a clean SIGTERM shutdown time out and report failure) or greater than `write_timeout` (a sync-mode response can't be written after `write_timeout` elapses, so the sender would see a spurious timeout and retry). Lower `publish_timeout` or raise the other value to resolve either error.
- `server.max_body_bytes`: required positive integer.
- `server.max_inflight_requests`: optional integer. Omitted or explicit `0` defaults to `256`, capping concurrently processed webhook requests; once reached, further requests are shed with `503 Service Unavailable` and a `Retry-After` header before any body is buffered. Explicit `-1` disables the limit (unlimited in-flight requests). Any other negative value is rejected. Health, readiness, and metrics endpoints are never subject to this limit.
- `server.fanout_concurrency`: optional non-negative integer. Caps concurrent publishes per webhook request for endpoints using `delivery_failure_policy: skip_failed`. `0` (default) uses `8`.
- `server.readiness_require_all`: optional boolean, default `false` for schema version 2. When false, `/ready` returns 200 with a per-flow body while at least one flow is ready. When true, `/ready` returns 503 if any flow is unready (V1 AND-aggregation). Schema version 1 always AND-aggregates.

## Secret references (schema_version 2)

`auth.secret_ref` is an alternative to inline `auth.secret`. Exactly one of `secret` or `secret_ref` is required. The resolver is cached and refreshed on a timer — never fetched per request.

```yaml
- id: auth
  type: auth
  scheme: hmac_sha256_hex
  secret_ref: {provider: env, name: WEBHOOK_SECRET, refresh: 15m, overlap: 5m}
  signature_header: X-Hub-Signature-256
```

- `provider`: `env` (read `name` as an environment variable, V1 `${VAR}` equivalent), `file` (read `name` as a path), or `aws_secrets_manager` (list matching names, newest `LastChangedDate` wins).
- `name`: env var, file path, or Secrets Manager name/prefix.
- `refresh`: optional duration, default `15m`.
- `overlap`: optional duration, default `0`. During this window after a value change, **both** the previous and current secrets verify, so rotation is not a flag day.

A refresh failure keeps serving the last known-good value, emits `hookway_secret_refresh_failures_total`, and logs at error. AWS credentials use the default SDK chain; `AWS_ENDPOINT_URL` targets LocalStack.

## Security

- `security.trusted_proxy_headers`: optional boolean. When false (default), the source IP is always the TCP peer (`RemoteAddr`) and `X-Forwarded-For`/`X-Real-IP` are ignored.
- `security.trusted_proxies`: optional list of IP addresses or CIDR ranges. When `trusted_proxy_headers` is true *and* this list is non-empty, forwarding headers are only honored when the immediate TCP peer falls inside one of these ranges; otherwise the peer IP is used. This prevents clients from spoofing `X-Forwarded-For` to bypass the IP allowlist and per-IP rate limiting. Leaving it empty with the flag on is the legacy permissive behavior and should only be used when clients cannot reach Hookway directly.
- `security.request_id_header`: optional request ID header name.
- `security.redacted_header_denylist`: optional list of exact HTTP header names that are redacted in logs and in the published envelope. This augments automatic redaction of obvious secrets; it does not replace it.
- `security.forwarded_header_allowlist`: optional list of HTTP header names. When non-empty, only these headers are logged and forwarded to the queue (fail-safe); every other header is dropped entirely. The denylist and built-in sensitive rules still apply on top, so an allowlisted-but-sensitive header is redacted rather than passed through. When empty, behavior is the legacy denylist filter (everything except denylisted/sensitive headers is forwarded).
- `security.loop_protection`: detects webhook delivery loops (a webhook destination that points back at Hookway). See [Loop protection](#loop-protection).
  - `enabled`: optional boolean, default `true`. When true, an inbound request whose hop count has reached `max_hops` is rejected.
  - `max_hops`: optional positive integer, default `3`. Hookway rejects an inbound request when its `X-Hookway-Hops` value is greater than or equal to this. Raise it if you intentionally chain more than three Hookway instances; lower it to break tight self-loops sooner.

```yaml
security:
  trusted_proxy_headers: true
  trusted_proxies:
    - "10.0.0.0/8"
    - "192.168.0.0/16"
  request_id_header: "X-Request-ID"
  redacted_header_denylist:
    - "Authorization"
    - "Cookie"
    - "Paddle-Signature"
    - "X-Hub-Signature-256"
  # Optional fail-safe: forward only these headers to consumers/logs.
  forwarded_header_allowlist:
    - "Content-Type"
    - "X-Request-ID"
  # Optional. Defaults to enabled with a 3-hop ceiling.
  loop_protection:
    enabled: true
    max_hops: 3
```

### `security.api_key`

Optional shared API key protecting the metrics endpoint and the config API
(`hookway editor`). When set, callers must send `Authorization: Bearer <key>`.
When empty or omitted, those endpoints are unauthenticated. The value may be an
`${ENV}` reference (expanded on config load), e.g.:

```yaml
security:
  api_key: "${HOOKWAY_API_KEY}"
```

### Loop protection

A webhook destination can point back at a Hookway endpoint (directly, or through a chain of Hookways), creating a delivery loop that would amplify traffic without bound. Hookway detects this with a hop counter carried in the `X-Hookway-Hops` header:

- Every outbound **webhook** delivery is stamped with `X-Hookway-Hops: <inbound count + 1>`, in both envelope and proxy payload modes. A request that never passed through Hookway has no header and counts as `0`.
- On the inbound side, when `security.loop_protection.enabled` is true, Hookway rejects a request whose hop count is greater than or equal to `max_hops`, before authentication and publishing. The rejection is a terminal `400` so a well-behaved sender — including an upstream Hookway, whose delivery engine treats `4xx` as non-retryable — stops rather than retrying the loop.

Because the hop count increments per Hookway hop (not per loop iteration), a tight self-loop is broken after at most `max_hops` iterations, and an intentional finite chain of up to `max_hops` Hookways still works. A malformed or negative header value counts as `0`, so it can only reset the counter, never bypass the limit. The header is always stamped on outbound deliveries even when this instance has enforcement disabled, so a downstream Hookway can still detect the loop.

This mechanism relies on the header surviving each hop; a loop that transits an intermediary which strips unknown headers will not be caught. Rejections are counted by `hookway_loop_rejected_total{endpoint, destination_queue_id, queue}` and logged at Warn.

## Rate Limit

- `rate_limit.enabled`: boolean.
- `rate_limit.requests_per_minute`: positive integer when enabled.
- `rate_limit.burst`: positive integer when enabled.
- `rate_limit.key_by`: must be `ip` for MVP.

## Metrics

- `metrics.enabled`: boolean.
- `metrics.path`: path beginning with `/`, usually `/metrics`.
- `metrics.addr`: optional listen address such as `":9090"`. When set, metrics are served on a dedicated listener instead of the public webhook port, so `/metrics` is not exposed on the internet-facing port. When empty, metrics are mounted on the main server at `metrics.path`.
- `metrics.runtime_metrics`: optional boolean, default `true`. When enabled, registers the standard Go runtime and process collectors (`go_goroutines`, `go_memstats_*`, `process_cpu_seconds_total`, `process_resident_memory_bytes`, open FDs). `process_*` metrics require Linux (`/proc`); Go runtime metrics are cross-platform. Set to `false` if your platform already collects container CPU/memory (e.g. cAdvisor) and you want to avoid duplication.

When disabled, the metrics route is not registered.

Metrics include HTTP request counts, auth failures, rate limits, load-shedding
saturation (`hookway_saturated_total`), filter drops (`hookway_filtered_total`), loop
rejections (`hookway_loop_rejected_total`), publish latency,
RabbitMQ reconnect/confirm/retry/DLQ/unroutable (`hookway_rabbitmq_unroutable_total`) counters, config load failures, and —
when `runtime_metrics` is enabled — Go runtime and process CPU/memory metrics. Startup
config failures still block serving traffic, so config failure metrics are
recorded in-process before exit and are most useful for tests or future reload
paths.

## Destinations

`destinations` is a top-level map of backends keyed by stable id. Endpoint `destinations` route to these ids and choose the target (queue, topic, or routing key) for each. Each entry has a `type` field selecting the backend. Supported types are `rabbitmq`, `sqs`, `sns`, `s3`, `redis_stream`, `redis_list`, `redis_pubsub`, `redis_kv`, `postgres`, `mongodb`, `kafka`, `webhook`, and `temporal`. See `examples/destinations/` for a complete reference config per type.

### RabbitMQ

RabbitMQ fields live under `destinations.<id>.rabbitmq`:

- `destinations.<id>.type`: required backend type; use `rabbitmq`.
- `destinations.<id>.rabbitmq.url`: required AMQP URL, usually `${RABBITMQ_URL}`.
- `destinations.<id>.rabbitmq.exchange`: required direct exchange name.
- `destinations.<id>.rabbitmq.exchange_type`: required, use `direct` for MVP.
- `destinations.<id>.rabbitmq.durable`: boolean for exchange/queue durability.
- `destinations.<id>.rabbitmq.publish_timeout`: optional duration, default `5s`.
- `destinations.<id>.rabbitmq.declare_queues`: boolean. If true, Hookway declares and binds allowed queues on startup.
- `destinations.<id>.rabbitmq.publisher_channels`: optional non-negative integer. Bounds how many AMQP channels publish concurrently; each channel carries one publisher-confirm round-trip at a time, so this is the effective publish concurrency. Defaults to `8` when omitted or `0`.
- `destinations.<id>.rabbitmq.queue_defaults`: optional queue arguments applied to each endpoint route queue Hookway declares for that destination. Destination route `queue_options` override these defaults.
  - `message_ttl`: optional positive duration mapped to RabbitMQ `x-message-ttl` in milliseconds. Stale messages are expired from the queue.
  - `expires`: optional positive duration mapped to `x-expires` in milliseconds. Useful for temporary/test queues; usually omit for production webhook queues.
  - `max_length`: optional positive integer mapped to `x-max-length` to cap backlog size.
  - `overflow`: optional overflow mode. Supported values are `reject-publish` and `reject-publish-dlx`; `reject-publish` is recommended so providers retry instead of silently losing older webhook messages.
  - `dead_letter_routing_key`: optional default RabbitMQ `x-dead-letter-routing-key` override.
- `destinations.<id>.rabbitmq.tls`: optional TLS settings for `amqps://` URLs. All fields are optional — with no `tls` block, `amqps://` still works against brokers whose certificates chain to the system trust store (e.g. CloudAMQP). Configure these only for private CAs, mutual TLS, or a non-default server name.
  - `ca_cert`: path to a PEM bundle to trust instead of/in addition to system roots (for private/self-signed CAs).
  - `client_cert` / `client_key`: PEM paths for mutual TLS. Both must be set together.
  - `server_name`: overrides the SNI/verification hostname.
  - `insecure_skip_verify`: disables certificate verification. Dev-only; logs a warning at startup. Do not use in production.

  Bad or unreadable CA/cert paths fail fast at startup. Note: the published container image must contain a CA bundle for `amqps://` to work at all — the provided Dockerfile copies `ca-certificates` into the `scratch` image for this reason.
- `destinations.<id>.rabbitmq.dead_letter`: optional dead-letter exchange/queue declaration settings.
- `destinations.<id>.rabbitmq.retry`: optional publish retry/backoff, covering both a nacked confirm and a transient publish/connect failure. This does NOT retry an unroutable message (see below) — that is a config error, not a transient failure.
  - `max_attempts`: default `3`.
  - `initial_backoff`: default `100ms`.
  - `max_backoff`: default `2s`.

Every publish is sent `mandatory`: if the exchange has no queue bound for the routing key, the broker returns the message instead of silently dropping it. Hookway surfaces this as a `Publish` error (so the inbound webhook request gets a non-2xx response rather than an incorrect `202`) and increments `hookway_rabbitmq_unroutable_total{destination_queue_id, queue}`. This is treated as a terminal configuration problem — a missing queue binding — so it is not retried.

```yaml
      retry:
        max_attempts: 3
        initial_backoff: "100ms"
        max_backoff: "2s"
```

```yaml
destinations:
  billing:
    type: "rabbitmq"
    rabbitmq:
      url: "${BILLING_RABBITMQ_URL}"
      exchange: "hookway.billing"
      exchange_type: "direct"
      durable: true
      publish_timeout: "5s"
      declare_queues: true
      queue_defaults:
        message_ttl: "24h"
        max_length: 100000
        overflow: "reject-publish"
      dead_letter:
        enabled: true
        exchange: "hookway.billing.dlx"
        exchange_type: "direct"
        queue: "hookway.billing.dlq"
        routing_key: "webhooks.dead"
        durable: true
        create_queue: true
  audit:
    type: "rabbitmq"
    rabbitmq:
      url: "${AUDIT_RABBITMQ_URL}"
      exchange: "hookway.audit"
      exchange_type: "direct"
      durable: true
      publish_timeout: "5s"
      declare_queues: true
```

Dead-letter fields:

- `enabled`: when true, queue declaration code can attach endpoint queues to the dead-letter exchange.
- `exchange`: required safe exchange name when enabled.
- `exchange_type`: required when enabled, usually `direct`.
- `queue`: required only when `create_queue` is true.
- `routing_key`: required when enabled.
- `durable`: durability for dead-letter exchange/queue declaration.
- `create_queue`: when true, queue layer can declare the dead-letter queue and bind it to the dead-letter exchange.

### AWS SQS

SQS fields live under `destinations.<id>.sqs`. Credentials are optional: when both `access_key_id` and `secret_access_key` are set they are used as static credentials; otherwise the default AWS credential chain (env, IAM role, shared profile) applies.

- `destinations.<id>.type`: set to `sqs`.
- `destinations.<id>.sqs.region`: required AWS region, e.g. `us-east-1`.
- `destinations.<id>.sqs.endpoint`: optional endpoint override; set to `http://localhost:4566` to target LocalStack. Usually `${AWS_ENDPOINT}` (left empty in production).
- `destinations.<id>.sqs.access_key_id` / `secret_access_key`: optional static credentials, usually `${AWS_ACCESS_KEY_ID}` / `${AWS_SECRET_ACCESS_KEY}`. Omit both to use the default credential chain.

Per-endpoint SQS queue settings are configured on the route via `sqs_options` and (for `.fifo` targets) `fifo` — see Destination route fields below.

```yaml
destinations:
  jobs:
    type: "sqs"
    on_existing_conflict: "reconcile"
    sqs:
      region: "us-east-1"
      # endpoint: "${AWS_ENDPOINT}"          # http://localhost:4566 for LocalStack
      # access_key_id: "${AWS_ACCESS_KEY_ID}"
      # secret_access_key: "${AWS_SECRET_ACCESS_KEY}"
```

`endpoint`, `access_key_id`, and `secret_access_key` are optional: to use the default AWS credential chain, omit them entirely rather than referencing an env var and leaving it unset — `${ENV_NAME}` references require the variable to be set (empty is fine; unset is a config-load error, see [Secrets](security.md#secrets)).

### AWS SNS

SNS fields live under `destinations.<id>.sns` and mirror SQS (region, endpoint override, optional static credentials):

- `destinations.<id>.type`: set to `sns`.
- `destinations.<id>.sns.region`: required AWS region.
- `destinations.<id>.sns.endpoint`: optional endpoint override (LocalStack).
- `destinations.<id>.sns.access_key_id` / `secret_access_key`: optional static credentials; omit both for the default chain.

Per-endpoint SNS topic settings are configured on the route via `sns_options` and (for `.fifo` targets) `fifo`.

### AWS S3

S3 fields live under `destinations.<id>.s3`. Hookway writes each webhook event as a single JSON object (`Content-Type: application/json`). Credentials are optional: when both `access_key_id` and `secret_access_key` are set they are used as static credentials; otherwise the default AWS credential chain (env, IAM role, shared profile) applies.

- `destinations.<id>.type`: set to `s3`.
- `destinations.<id>.s3.region`: required AWS region, e.g. `us-east-1`.
- `destinations.<id>.s3.endpoint`: optional endpoint override; set to `http://localhost:4566` to target LocalStack, or reference `${AWS_ENDPOINT}` if you export it. Omit the field for production (default AWS endpoint).
- `destinations.<id>.s3.access_key_id` / `secret_access_key`: optional static credentials, e.g. `${AWS_ACCESS_KEY_ID}` / `${AWS_SECRET_ACCESS_KEY}`. Omit both fields entirely to use the default credential chain.
- `destinations.<id>.s3.bucket`: required S3 bucket name.
- `destinations.<id>.s3.create`: when `true`, auto-create the bucket at startup if it does not exist. `us-east-1` uses no `LocationConstraint`; all other regions set it automatically. The bucket is verified via `HeadBucket` at startup regardless of this setting.
- `destinations.<id>.s3.sse`: server-side encryption mode. Leave empty (default) to rely on the bucket's or account's default encryption. Use `"AES256"` for SSE-S3 managed keys or `"aws:kms"` for SSE-KMS.
- `destinations.<id>.s3.sse_kms_key_id`: KMS key ARN or alias. Only used when `sse` is `"aws:kms"`. Omit to use the default `aws/s3` KMS key.

**Object key layout:** the per-route `target` is the base folder (key prefix). Each event is stored at:

```
<target>/yyyy/mm/dd/<timestamp>-<id>.json
```

The date partition and timestamp are derived from the event's `ReceivedAt` in UTC. The timestamp is colon-free so the key is usable in all contexts. Example with `target: "orders"`:

```
orders/2026/06/29/20260629T143000.000000000Z-evt_1.json
```

**`on_existing_conflict`:** accepted for config parity but is a NO-OP for S3. S3 has no mutable per-target attributes to reconcile; the bucket is verified at startup via `HeadBucket` but is never mutated by this policy.

```yaml
destinations:
  events:
    type: "s3"
    on_existing_conflict: "reconcile"     # accepted but no-op for S3
    s3:
      region: "us-east-1"
      # endpoint: "${AWS_ENDPOINT}"       # http://localhost:4566 for LocalStack
      # access_key_id: "${AWS_ACCESS_KEY_ID}"
      # secret_access_key: "${AWS_SECRET_ACCESS_KEY}"
      bucket: "my-webhook-events"
      create: true
      sse: "AES256"                       # or "aws:kms"; omit for default encryption
      # sse_kms_key_id: "${S3_KMS_KEY_ID}"
```

### Postgres

Postgres fields live under `destinations.<id>.postgres`. Hookway inserts each webhook event as one row into a single shared table using [jackc/pgx/v5](https://github.com/jackc/pgx) (pgxpool). The table is created unconditionally at startup with `CREATE TABLE IF NOT EXISTS`; no per-route `create` flag is used.

- `destinations.<id>.type`: set to `postgres`.
- `destinations.<id>.postgres.url`: required libpq DSN. `sslmode` is honored inline (e.g. `postgres://user:pass@host:5432/db?sslmode=require`). Usually `${POSTGRES_URL}`.
- `destinations.<id>.postgres.schema`: optional Postgres schema name, default `"public"`.
- `destinations.<id>.postgres.table`: required table name. Only letters, digits, and underscores; must start with a letter or underscore. The table is quoted via `pgx.Identifier.Sanitize()` before use.
- `destinations.<id>.postgres.max_conns`: optional pool size cap. `0` (default) uses the pgx pool default.
- `destinations.<id>.postgres.tls`: optional TLS block. Only needed for a custom/private CA or mutual TLS (client certificate); `sslmode` in the `url` controls whether TLS is used. Fields mirror the RabbitMQ/Redis TLS block:
  - `ca_cert`: path to a PEM bundle for private/self-signed CAs.
  - `client_cert` / `client_key`: paths for mutual TLS. Both must be set together.
  - `server_name`: overrides the SNI/verification hostname.
  - `insecure_skip_verify`: dev-only; disables certificate verification.

**`on_existing_conflict` is not valid for Postgres destinations** — the field is rejected at config validation time. Do not set it on `postgres` entries.

**Table schema:** at startup Hookway runs `CREATE TABLE IF NOT EXISTS` with fixed columns for the Message envelope:

| Column | Type | Notes |
|--------|------|-------|
| `id` | `text` | Event id; PRIMARY KEY |
| `target` | `text` | Per-route `target` value (not a table name) |
| `idempotency_key` | `text` | Provider delivery id (nullable) |
| `source_ip` | `text` | Client IP (nullable) |
| `method` | `text` | HTTP method |
| `path` | `text` | Request path |
| `received_at` | `timestamptz` | UTC receive time |
| `headers` | `jsonb` | HTTP headers (nullable) |
| `query` | `jsonb` | Query parameters (nullable) |
| `body` | `jsonb` | Request body (nullable) |
| `auth` | `jsonb` | Auth metadata (nullable) |

Two indexes are also created `IF NOT EXISTS`: one on `target` and one on `received_at`.

**`target` as a column:** the per-route `target` field is stored in the `target` column rather than selecting a different table. Use it to distinguish event sources when multiple endpoints share the same destination.

**Deduplication:** inserts use `INSERT ... ON CONFLICT (id) DO NOTHING`. At-least-once retries from a provider are safe — duplicate event ids are silently ignored.

**Pre-existing table caveat:** `CREATE TABLE IF NOT EXISTS` is a no-op against an existing table. If the table already exists with an incompatible schema, inserts will fail at runtime. Hookway does not reconcile or migrate columns in v1 — ensure the table matches the expected schema or drop and recreate it before starting.

```yaml
destinations:
  warehouse:
    type: "postgres"
    postgres:
      url: "${POSTGRES_URL}"           # libpq DSN; sslmode honored inline
      schema: "public"                 # optional, default "public"
      table: "webhook_events"          # required
      max_conns: 0                     # optional; 0 = pgx default
      # tls:
      #   ca_cert: "/etc/ssl/certs/pg-ca.pem"
      #   client_cert: "/etc/ssl/certs/pg-client.pem"
      #   client_key: "/etc/ssl/private/pg-client.key"
      #   server_name: "pg.internal"
      #   insecure_skip_verify: false
```

### MongoDB

MongoDB fields live under `destinations.<id>.mongodb`. Hookway inserts each webhook event as one BSON document into a collection within the configured database using [go.mongodb.org/mongo-driver/v2](https://www.mongodb.com/docs/drivers/go/current/).

- `destinations.<id>.type`: set to `mongodb`.
- `destinations.<id>.mongodb.uri`: required MongoDB connection URI (e.g. `mongodb://user:pass@host:27017/?replicaSet=rs0`). Usually `${MONGODB_URI}`.
- `destinations.<id>.mongodb.database`: required database name to write into. The collection is determined by the per-route `target`.
- `destinations.<id>.mongodb.max_pool_size`: optional connection pool cap. `0` (default) uses the driver default.
- `destinations.<id>.mongodb.tls`: optional TLS block. Only needed for a custom/private CA or mutual TLS (client certificate). Fields mirror the RabbitMQ/Redis TLS block:
  - `ca_cert`: path to a PEM bundle for private/self-signed CAs.
  - `client_cert` / `client_key`: paths for mutual TLS. Both must be set together.
  - `server_name`: overrides the SNI/verification hostname.
  - `insecure_skip_verify`: dev-only; disables certificate verification.

**`on_existing_conflict` is not valid for MongoDB destinations** — the field is rejected at config validation time. Do not set it on `mongodb` entries.

**Collection-per-target:** the per-route `target` field names the MongoDB collection within `database`. Use different targets to send events from different endpoints into separate collections.

**Document shape:** each event is stored as one BSON document. The stored document carries the same envelope fields (`_id`, `endpoint`, `received_at`, `headers`, `query`, `body`, `auth`) that other destinations emit, stored as native BSON — top-level scalars map to BSON string/datetime types and nested objects (`headers`, `query`, `body`, `auth`) become nested BSON documents.

**No DDL:** Hookway does not create collections or indexes at startup. Collections auto-create on first insert; `_id` is automatically indexed by MongoDB, so deduplication works with no setup required.

**Deduplication:** inserts use `InsertOne`. A duplicate `_id` (MongoDB E11000 duplicate key error) is treated as a no-op success (first-write-wins). At-least-once retries from a provider are safe — only the first write for a given event id is persisted.

```yaml
destinations:
  archive:
    type: "mongodb"
    mongodb:
      uri: "${MONGODB_URI}"         # mongodb:// connection string
      database: "webhooks"          # required; collection comes from route target
      max_pool_size: 0              # optional; 0 = driver default
      # tls:
      #   ca_cert: "/etc/ssl/certs/mongo-ca.pem"
      #   client_cert: "/etc/ssl/certs/mongo-client.pem"
      #   client_key: "/etc/ssl/private/mongo-client.key"
      #   server_name: "mongo.internal"
      #   insecure_skip_verify: false
```

### Kafka

Kafka fields live under `destinations.<id>.kafka`. Hookway publishes each webhook event as one message to a topic using [segmentio/kafka-go](https://github.com/segmentio/kafka-go).

- `destinations.<id>.type`: set to `kafka`.
- `destinations.<id>.kafka.brokers`: required list of one or more bootstrap broker addresses (e.g. `["broker1:9092", "broker2:9092"]`).
- `destinations.<id>.kafka.acks`: producer acknowledgement level — `all` (default, durable — waits for all in-sync replicas), `one`, or `none`.
- `destinations.<id>.kafka.compression`: `none` (default), `gzip`, `snappy`, `lz4`, or `zstd`.
- `destinations.<id>.kafka.publish_timeout`: optional duration, default `5s`.
- `destinations.<id>.kafka.sasl`: optional SASL auth. Omit the whole block for no SASL.
  - `mechanism`: `plain`, `scram-sha-256`, or `scram-sha-512`.
  - `username` / `password`: SASL credentials, usually `${KAFKA_USERNAME}` / `${KAFKA_PASSWORD}`.
- `destinations.<id>.kafka.tls`: optional TLS settings. Omit or set `enabled: false` for plaintext. Fields mirror the RabbitMQ/Redis TLS block:
  - `ca_cert`: path to a PEM bundle for private/self-signed CAs.
  - `client_cert` / `client_key`: paths for mutual TLS. Both must be set together.
  - `server_name`: overrides the SNI/verification hostname.
  - `insecure_skip_verify`: dev-only; disables certificate verification.
- `destinations.<id>.kafka.retry`: optional producer retry/backoff, mapped onto the kafka-go writer.
  - `max_attempts`: default `3`.
  - `initial_backoff`: default `100ms`.
  - `max_backoff`: default `2s`.

**Security note:** omitting both `tls.enabled` and `sasl` connects to the brokers in plaintext with no authentication. Hookway logs a startup warning in this case; use it only for local development.

**`target` is the topic.** The per-route `target` field is the Kafka topic name. Setting the route's `create: true` makes Hookway pre-create the topic via an admin `CreateTopics` call at startup (idempotent — an already-existing topic is left as-is). This replaces relying on broker-side auto-creation, which raced the first publish. Omit `create: true` when topics are pre-provisioned; `create: false` (or omitted) routes must already exist on the broker.

**Partition key:** the optional per-route `kafka_options.partition_key` block derives the Kafka message key (which selects the partition), reusing the same source model as FIFO group ids:

- `source`: one of `static` | `endpoint` | `header` | `body`. `static` uses `value`; `header`/`body` extract `path` (a header name, or a dotted path into the JSON body); `endpoint` uses the endpoint name.
- `fallback`: a literal value, or `endpoint`; used when extraction yields nothing.

Keyed messages preserve per-key ordering within the topic. Omit `partition_key` entirely for round-robin distribution across partitions.

**Topic creation options:** for `create: true` routes, two optional per-route settings tune the pre-created topic:

- `kafka_options.partitions`: number of partitions for the topic. Omit (or `0`) to use the broker default.
- `kafka_options.replication_factor`: replication factor for the topic. Omit (or `0`) to use the broker default.

See `examples/destinations/kafka.yaml` for a complete reference config.

```yaml
destinations:
  events:
    type: "kafka"
    kafka:
      brokers:
        - "${KAFKA_BROKER_1}"
        - "${KAFKA_BROKER_2}"
      acks: "all"
      compression: "snappy"
      publish_timeout: "5s"
      sasl:
        mechanism: "scram-sha-256"
        username: "${KAFKA_USERNAME}"
        password: "${KAFKA_PASSWORD}"
      tls:
        enabled: true
      retry:
        max_attempts: 3
        initial_backoff: "100ms"
        max_backoff: "2s"
```

### Temporal

Temporal fields live under `destinations.<id>.temporal`. Hookway starts one workflow execution per webhook event using the official [Temporal Go SDK](https://github.com/temporalio/sdk-go). It only starts workflows (no signals, no worker of its own); your existing workers on the task queue pick the execution up.

- `destinations.<id>.type`: set to `temporal`.
- `destinations.<id>.temporal.host_port`: required frontend gRPC address, e.g. `localhost:7233` (self-hosted) or `<namespace>.<account>.tmprl.cloud:7233` (Temporal Cloud).
- `destinations.<id>.temporal.namespace`: required Temporal namespace.
- `destinations.<id>.temporal.api_key`: optional Temporal Cloud API key, sent as a bearer credential. Requires `tls.enabled: true`.
- `destinations.<id>.temporal.connect_timeout`: optional duration, default `10s`. Bounds the startup Dial; the destination fails fast if the frontend is unreachable (same behaviour as the Postgres and MongoDB destinations).
- `destinations.<id>.temporal.start_timeout`: optional duration, default `5s`. Bounds each start-workflow RPC. Keep it below `server.publish_timeout`.
- `destinations.<id>.temporal.tls`: optional TLS settings. Omit or set `enabled: false` for plaintext. Fields mirror the Kafka TLS block:
  - `ca_cert`: path to a PEM bundle for private/self-signed CAs.
  - `client_cert` / `client_key`: paths for mutual TLS. Both must be set together.
  - `server_name`: overrides the SNI/verification hostname.
  - `insecure_skip_verify`: dev-only; disables certificate verification.

**Security note:** omitting both `tls.enabled` and `api_key` connects to the frontend in plaintext with no authentication. Hookway logs a startup warning in this case. The published image is built `FROM scratch`, so certificate paths must be mounted into the container.

**`target` is the task queue.** The per-route `target` field is the Temporal task queue name. `create: true` has no meaning for Temporal and is ignored. Task queue names must not end in `.fifo`.

**Per-route `temporal_options`** (required on every route that points at a `temporal` destination):

- `workflow_type` (required): the workflow type name registered on the worker.
- `input`: `envelope` (default) sends the full Hookway event envelope as the single workflow argument, the same JSON every other destination emits. `body` sends only the decoded webhook body, for workflows that expect a plain object. When the body was not JSON, `body` mode falls back to the envelope and logs a warning so a JSON-typed worker still receives an object.
- `workflow_id`: derives the workflow id. Omit the block to use the Hookway event id (unique per event).
  - `source`: `static` | `endpoint` | `header` | `body`. `static` uses `value`; `header`/`body` extract `path` (a header name, or a dotted path into the JSON body); `endpoint` uses the endpoint name. An unresolved value falls back to the event id.
  - `prefix`: prepended to the resolved value, e.g. `prefix: "order-"` with `path: "order.id"` yields `order-<id>`. May contain `{variable}` placeholders declared under the endpoint's `variables` (e.g. `"{envName}-aan-"`), rendered per event.
- `id_reuse_policy`: `allow_duplicate`, `allow_duplicate_failed_only`, `reject_duplicate`, or `terminate_if_running`. Omit for the server default (`allow_duplicate`).
- `execution_timeout`, `run_timeout`, `task_timeout`: optional per-execution timeouts. `run_timeout` must not exceed `execution_timeout`.

Routes in different endpoints that share a task queue must use identical `temporal_options`.

**Duplicates count as delivered.** When the server reports that an execution with the derived workflow id already exists, Hookway logs it at debug level and returns success, so sender retries with a deterministic workflow id are idempotent (first-start-wins).

**Failure handling.** There is no dead-letter path for this destination. Any other start error (frontend unreachable, `start_timeout` exceeded, unknown namespace, rejected start) fails the publish: under the default `stop_on_failure` policy the sender receives a 503 and is expected to retry; under `skip_failed` the route is marked failed and the other routes still deliver. The SDK retries transient gRPC errors internally within `start_timeout`. A stopped worker is not a failure: the start succeeds and the execution waits on the task queue. To recover events whose start still failed, enable the [replay store](#replay), which records every event with a per-destination `delivered`/`failed` outcome and can re-publish it.

**Readiness.** The destination's readiness probe calls the frontend gRPC health service; a proxy that blocks it will report the destination unready even though publishes succeed.

**Payload encoding.** Inputs are encoded with the SDK's default JSON data converter. Clusters that require a custom payload codec (for example, encryption at rest) are not supported yet.

A temporal destination cannot be used as a webhook `dead_letter` target because a dead-letter route has no `workflow_type`.

See `examples/destinations/temporal.yaml` for a complete reference config.

```yaml
destinations:
  orchestrator:
    type: "temporal"
    temporal:
      host_port: "${TEMPORAL_ADDRESS}"
      namespace: "${TEMPORAL_NAMESPACE}"
      connect_timeout: "10s"
      start_timeout: "5s"
      tls:
        enabled: false

endpoints:
  order_events:
    path: "/webhooks/orders"
    destinations:
      - destination: "orchestrator"
        target: "order-events"
        temporal_options:
          workflow_type: "OrderWebhookWorkflow"
          input: "body"
          workflow_id:
            source: "body"
            path: "order.id"
            prefix: "order-"
          id_reuse_policy: "reject_duplicate"
```

### Redis / Valkey

Hookway supports four Redis destination types. All share a common `redis:` connection block on the destination, and per-route settings under `redis_options` on each endpoint route.

**Available types:**

| Type | Primitive | Notes |
|------|-----------|-------|
| `redis_stream` | XADD to a Stream — durable, consumer groups, replay, bounded trim | Recommended for at-least-once delivery |
| `redis_list` | LPUSH to a List — FIFO work queue; LTRIM cap bounds memory | Simple competing-consumer queue |
| `redis_pubsub` | PUBLISH to a channel — live broadcast; lossy, not durable | Fire-and-forget; subscribers offline at delivery time miss the message |
| `redis_kv` | SET a key per webhook with TTL — storage/lookup, not a queue | Latest-event overwrite; keys expire automatically |

`redis_stream` is the recommended type for at-least-once webhook handoff. `redis_pubsub` is intentionally lossy — if no subscriber is connected at publish time, the message is silently dropped; use it only for live broadcast scenarios where occasional loss is acceptable.

[Valkey](https://valkey.io/) is fully supported: it speaks the RESP protocol and behaves identically to Redis.

**Shared `redis:` connection block** — configure once on the destination, reused by every endpoint route that targets it:

- `mode`: deployment topology. `single` (default) for one node or a proxied endpoint; `cluster` for Redis Cluster; `sentinel` for Redis Sentinel.
- `url`: redis:// or rediss:// URL. Required for `single` mode. Usually `${REDIS_URL}`.
- `addrs`: list of node addresses. Required for `cluster` and `sentinel` modes (e.g. `["redis-1:6379", "redis-2:6379"]`).
- `master_name`: sentinel primary name. Required for `sentinel` mode (e.g. `"mymaster"`).
- `username` / `password`: optional auth credentials.
- `db`: optional database index. Applies to `single` and `sentinel`; Redis Cluster ignores it.
- `key_prefix`: optional string automatically prepended to stream/list/channel targets for `redis_stream`, `redis_list`, and `redis_pubsub` (formatted as `<prefix>:<target>`). For `redis_kv`, the prefix is available via the `{prefix}` token in `key_template` but is not automatically prepended.
- `tls`: optional TLS settings — mirror of the RabbitMQ TLS block.
  - `enabled`: boolean.
  - `ca_cert`: path to a PEM bundle for private/self-signed CAs.
  - `client_cert` / `client_key`: paths for mutual TLS. Both must be set together.
  - `server_name`: overrides the SNI/verification hostname.
  - `insecure_skip_verify`: dev-only; disables certificate verification.

```yaml
destinations:
  events:
    type: redis_stream
    redis:
      mode: single
      url: "${REDIS_URL}"   # redis:// or rediss://
      # Cluster example:
      # mode: cluster
      # addrs: ["redis-1:6379", "redis-2:6379", "redis-3:6379"]
      # Sentinel example:
      # mode: sentinel
      # addrs: ["sentinel-1:26379", "sentinel-2:26379"]
      # master_name: "mymaster"
      key_prefix: "hookway"   # optional
```

See `examples/destinations/redis_stream.yaml` for a full annotated reference with TLS and cluster/sentinel comments.

**Best practices for `redis_stream`:**

- Use `max_len` with approximate trimming (`XADD MAXLEN ~`): the actual stream length may slightly exceed the configured cap, which is intentional — approximate trimming is far more efficient than exact trimming in Redis.
- Set `consumer_group` when multiple workers compete for messages. Hookway creates the group at startup when `create: true` and `consumer_group` is set (idempotent: safe in production).
- Delivery is at-least-once: a message appended via XADD is acknowledged to the webhook provider before the consumer confirms processing, so consumers must tolerate duplicates (deduplicate on `idempotency_key`).
- Redis connectivity for a `redis_stream` destination is **not** verified at process startup unless a consumer group is being created (`create: true` with `consumer_group` set, which dials Redis immediately). Otherwise, the connection is established lazily — verified at the `/ready` (`/readyz`) probe and on the first publish.

### Webhook

Webhook fields live under `destinations.<id>.webhook`. Hookway delivers each inbound webhook event to a configured HTTP/HTTPS endpoint as an outbound HTTP POST.

**Delivery mode (`delivery_mode`):** `async` (default) or `sync`.

- **`async` (default):** `Publish()` enqueues the delivery job and returns immediately with `nil` (success). The inbound sender receives Hookway's own `202 Accepted` — this means the event was _accepted for delivery_, NOT that it was delivered. Actual delivery outcome is observable via logs, the `hookway_webhook_*` Prometheus metrics, and (optionally) a `dead_letter` destination. This is the default so upgrades preserve existing behavior.
- **`sync`:** `Publish()` performs the delivery inline (including retries/backoff) and returns the outcome, so the inbound request succeeds only when the downstream delivery does (a terminal `4xx`, exhausted `5xx`/timeout, or a cancelled request context returns an error → the inbound sender sees a non-2xx). This adds up to roughly `max_attempts × timeout` (plus backoff) of latency to the inbound request. **Recommended for critical, low-volume hooks** where the sender should observe delivery failure directly. `dead_letter` does not apply to sync mode (the caller already learns of the failure).

**Per-destination delivery engine and worker pool (async):** each webhook destination has its **own** delivery engine (goroutine pool + queue), so a slow or failing target cannot head-of-line-block deliveries to other destinations. The number of concurrent delivery workers per engine is controlled by `server.fanout_concurrency`. The engine's job queue is bounded by `max_queue` (**per-destination**, default 1000). When the queue is full, `Publish()` returns an error and the upstream inbound request receives a non-2xx response — the upstream sender is expected to retry, which creates natural backpressure. Readiness (`/readyz`) reports a webhook destination as not-ready when its queue is ≥90% full.

**Graceful drain on shutdown (async):** on SIGTERM / shutdown, Hookway drains the in-flight and queued deliveries within `drain_timeout` (defaults to `server.shutdown_timeout`) before the process exits. Graceful deploys and restarts do not lose in-flight events. A hard crash (kill -9, OOM, host failure) loses any events that were queued or in-flight but not yet confirmed — unless a `dead_letter` destination is configured.

**NON-DURABLE by default:** the delivery queue is in-memory only. There is no write-ahead log or persistent queue. For durability of terminally-failed deliveries, configure a `dead_letter` destination pointing at a durable backend; for full durability of every event, use a durable queue destination (RabbitMQ, SQS, Kafka, etc.) directly.

**Dead-letter (`dead_letter`, async only):** when an async delivery fails terminally — a terminal `4xx`, or `5xx`/timeout/error after retries are exhausted — Hookway can redirect the message to another destination instead of dropping it (log-and-drop is the default when no `dead_letter` is set). The redirected message is the original envelope enriched with a `dead_letter` block (`destination`, `target`, `outcome`, `attempts`, `failed_at`). The referenced destination MUST exist and MUST NOT itself be a `webhook` destination — this makes delivery loops structurally impossible (no cycle-detection needed). The dead-letter target queue/topic is auto-collected into the backend destination (and declared for RabbitMQ when `declare_queues: true`) even if it has no direct endpoint route.

**Not a transparent proxy (async):** Hookway does not return the downstream HTTP response to the inbound sender. In async mode the inbound sender always sees Hookway's own `202 Accepted` (queue-accept) or an error. In sync mode the inbound sender still sees Hookway's status (2xx/non-2xx), never the downstream body.

#### Fields

- `destinations.<id>.type`: set to `webhook`.
- `destinations.<id>.webhook.url`: required. The target endpoint URL. Must be `http://` or `https://`.
- `destinations.<id>.webhook.delivery_mode`: optional. `async` (default) or `sync`. See [Delivery mode](#webhook) above.
- `destinations.<id>.webhook.timeout`: optional per-request HTTP timeout. Default `10s`.
- `destinations.<id>.webhook.max_queue`: optional bound on the in-memory delivery queue (async only, per-destination). Default `1000`. When the queue fills, inbound `Publish` calls fail (backpressure).
- `destinations.<id>.webhook.drain_timeout`: optional graceful-drain deadline on shutdown (async only). Defaults to `server.shutdown_timeout`.
- `destinations.<id>.webhook.dead_letter`: optional (async only). Redirect terminally-failed deliveries to another destination.
  - `destination`: id of a configured **non-webhook** destination. Required with `target`.
  - `target`: the queue/topic/target name on that destination. Required with `destination`.
- `destinations.<id>.webhook.retry`: optional retry/backoff settings.
  - `max_attempts`: total attempts (including the first). Default `3`.
  - `initial_backoff`: first retry wait. Default `500ms`.
  - `max_backoff`: maximum retry wait. Default `30s`.
- `destinations.<id>.webhook.auth`: outbound authentication. See [Auth types](#outbound-auth-types) below.
- `destinations.<id>.webhook.tls`: optional TLS block for a custom CA or mutual TLS. Fields mirror the RabbitMQ TLS block:
  - `ca_cert`: path to a PEM bundle for private/self-signed CAs.
  - `client_cert` / `client_key`: paths for mutual TLS. Both must be set together.
  - `server_name`: overrides the SNI/verification hostname.
  - `insecure_skip_verify`: dev-only; disables certificate verification. Never use in production.

**`on_existing_conflict` is not valid for webhook destinations** — the field is rejected at config validation time. Do not set it on `webhook` entries.

#### Retry classification

| HTTP status / error | Behavior |
|---------------------|----------|
| `2xx` | Success — delivery complete |
| `429` | Retry with backoff |
| `5xx` | Retry with backoff |
| Network error / timeout | Retry with backoff |
| Other `4xx` (400, 401, 403, 404, …) | Terminal — no retry; dead-lettered if configured, else dropped |

Retries use exponential backoff (doubling, +/-20% jitter, capped at `retry.max_backoff`) — jitter keeps many destinations that all start failing at once (e.g. a shared downstream outage) from retrying in lockstep. A `429` response with a valid `Retry-After` header (integer seconds or an HTTP-date, per RFC 9110) overrides the computed backoff for that wait, still capped at `retry.max_backoff`; an absent or unparseable header falls back to the computed backoff. After `retry.max_attempts` the delivery is abandoned; the final outcome is observable via `hookway_webhook_delivery_total{outcome=...}` and an Error log entry.

#### Payload modes

**Envelope mode (default):** the outbound POST body is the Hookway JSON `Message` envelope (`Content-Type: application/json`) containing the normalized event fields (`id`, `endpoint`, `received_at`, `headers`, `query`, `body`, `auth`). Used for all auth types except `proxy`.

**Proxy mode (`auth.type: proxy`):** Hookway forwards the _original raw inbound body_ verbatim, preserving the original `Content-Type`, and copies the original inbound headers (minus hop-by-hop headers: `Connection`, `Transfer-Encoding`, `Upgrade`, etc.) to the outbound request. No hook_way auth headers are added — the vendor's original auth headers (e.g. `X-Stripe-Signature`) pass through as-is. Use proxy mode when the downstream endpoint expects to verify the vendor's original signature.

#### Outbound auth types

**`none`**: no authentication headers are added. Suitable for internal/trusted networks only.

**`api_key`**: sets a static header on every outbound request. Configure `auth.header` (the header name) and `auth.value` (the value, usually `${WEBHOOK_API_KEY}`).

**`hmac`** (default): signs each outbound request with HMAC-SHA256. Configure `auth.secret` (the signing secret). Three signing schemes are supported via `auth.scheme`:

| Scheme | Signature header format | Signing string |
|--------|------------------------|----------------|
| `hmac_sha256_ts_dot` (default) | `t=<unix_ts>,v1=<hex>` | `<unix_ts>.<raw_body>` |
| `hmac_sha256_ts_colon` | `ts=<unix_ts>;h1=<hex>` | `<unix_ts>:<raw_body>` |
| `hmac_sha256_hex` | `sha256=<hex>` | `<raw_body>` (no timestamp) |

The signature header name defaults to `X-Hookway-Signature` (override with `auth.header`). For `hmac_sha256_ts_dot` and `hmac_sha256_ts_colon`, Hookway also sends an event-id header (default `X-Hookway-Event-Id`, override with `auth.event_id_header`) to enable replay detection.

**`proxy`**: forwards the original inbound request's raw body and headers (minus hop-by-hop) to the downstream endpoint unchanged. No hook_way auth is added. See [Payload modes](#payload-modes) above.

#### Consumer-side auth verification

Your downstream endpoint (the receiver) should verify incoming requests from Hookway. The verification recipe differs by auth type.

**api_key**: read the configured header from the incoming request and compare it to the expected value using a constant-time comparison function to prevent timing attacks.

```
expected = config.auth.value
received = request.headers[config.auth.header]
assert constant_time_equal(received, expected)
```

**hmac (hmac_sha256_ts_dot / hmac_sha256_ts_colon)**: parse the signature header, recompute the HMAC, compare, and reject stale or replayed requests.

```
# Step 1: parse the signature header
# hmac_sha256_ts_dot:   "t=<ts>,v1=<hex>"
# hmac_sha256_ts_colon: "ts=<ts>;h1=<hex>"
ts, received_hex = parse_signature_header(request.headers["X-Hookway-Signature"])

# Step 2: reject stale timestamps (replay window, e.g. 5 minutes)
now = current_unix_timestamp()
assert abs(now - ts) <= 300   # seconds; tune to your tolerance

# Step 3: recompute HMAC over the RAW received bytes
# IMPORTANT: use the raw request body bytes, not a re-serialised struct
# signing_string is a BYTE sequence: ASCII-encode the timestamp integer,
# then the separator byte, then the raw body bytes — concatenated as bytes.
if scheme == "hmac_sha256_ts_dot":
    signing_string = ascii_bytes(ts) + b"." + raw_body_bytes
else:  # hmac_sha256_ts_colon
    signing_string = ascii_bytes(ts) + b":" + raw_body_bytes
expected_hex = hex(HMAC_SHA256(secret, signing_string))

# Step 4: constant-time compare — never use == on HMAC hex strings
assert constant_time_equal(received_hex, expected_hex)

# Step 5: deduplicate replayed requests (optional but recommended)
event_id = request.headers["X-Hookway-Event-Id"]
assert not seen_event_ids.contains(event_id)
seen_event_ids.add(event_id, ttl=300)
```

Key points:
- Always compare over the **raw** received bytes, not a parsed/re-serialised body — any whitespace change invalidates the signature.
- Use a constant-time comparison (`hmac.Equal` in Go, `hmac.compare_digest` in Python, `crypto.timingSafeEqual` in Node.js) to prevent timing attacks.
- Reject any request whose timestamp is outside your replay window (e.g. ±5 minutes).
- Optionally track `X-Hookway-Event-Id` in a short-lived store (TTL = replay window) to detect duplicate deliveries from retries.

**hmac_sha256_hex**: no timestamp is included in this scheme. Verify as above but the signing string is the raw body alone and there is no `ts` to check. Replay protection must rely on `X-Hookway-Event-Id` deduplication only.

```
received_hex = parse "sha256=<hex>" from request.headers["X-Hookway-Signature"]
expected_hex = hex(HMAC_SHA256(secret, raw_body_bytes))
assert constant_time_equal(received_hex, expected_hex)
# No timestamp — rely on X-Hookway-Event-Id dedup for replay protection
```

**proxy**: Hookway does not add any hook_way auth headers in proxy mode. The original vendor auth headers are forwarded verbatim. Verify using the vendor's own webhook verification procedure (e.g. validate `X-Stripe-Signature` using Stripe's SDK).

#### Metrics

| Metric | Labels | Description |
|--------|--------|-------------|
| `hookway_webhook_delivery_total` | `destination_queue_id`, `queue`, `outcome` | Delivery attempts by outcome. `outcome` values: `2xx`, `4xx`, `5xx`, `429`, `timeout`, `error`. |
| `hookway_webhook_retries_total` | `destination_queue_id`, `queue` | Incremented per retry attempt. |
| `hookway_webhook_dropped_total` | `destination_queue_id`, `queue`, `reason` | Async delivery dropped before it was ever attempted. `reason` is `queue_full` (buffer full at enqueue — backpressure) or `engine_closing` (enqueue during shutdown). NOT incremented on retry exhaustion or terminal 4xx. |
| `hookway_webhook_dead_letter_total` | `destination_queue_id`, `queue`, `outcome` | Terminally-failed deliveries successfully redirected to the `dead_letter` destination. `outcome` is the last delivery outcome (`4xx`, `5xx`, `429`, `timeout`, `error`). |
| `hookway_webhook_dead_letter_failures_total` | `destination_queue_id`, `queue` | Dead-letter redirects that themselves failed (or fired with no resolver). The event is then logged and dropped. |
| `hookway_webhook_queue_depth` | `destination_queue_id` | Current async delivery queue depth (gauge). |
| `hookway_webhook_queue_capacity` | `destination_queue_id` | Configured async delivery queue capacity (gauge; set once at startup). |

> **Metric change:** `hookway_webhook_dropped_total` gained a `reason` label in this release. Dashboards/alerts that aggregated the metric without a `reason` selector continue to work; those that pinned an exact label set must add `reason`.

Retry-exhausted or terminal-4xx deliveries are observable via `hookway_webhook_delivery_total{outcome="5xx"|"4xx"|"timeout"|"error"}` and an Error log entry — they do NOT increment `hookway_webhook_dropped_total`. When a `dead_letter` destination is configured, such failures also increment `hookway_webhook_dead_letter_total` (on successful redirect) or `hookway_webhook_dead_letter_failures_total` (on redirect failure).

The generic `Publish` metric (`hookway_publish_total{status}`) reflects queue-accept (`accepted`) or queue-full drop (`dropped`) at the point where the inbound event is enqueued, before any delivery attempt.

```yaml
destinations:
  forward:
    type: "webhook"
    webhook:
      url: "${WEBHOOK_URL}"
      delivery_mode: "async"    # sync | async (default async)
      timeout: "10s"
      max_queue: 1000           # async only; per-destination
      drain_timeout: "30s"      # async only
      dead_letter:              # async only; must reference a non-webhook destination
        destination: "dlq-rabbit"
        target: "webhook-dead-letters"
      retry:
        max_attempts: 5
        initial_backoff: "500ms"
        max_backoff: "30s"
      auth:
        type: "hmac"
        secret: "${WEBHOOK_SECRET}"
        # scheme: "hmac_sha256_ts_dot"    # default
        # header: "X-Hookway-Signature"   # default
        # event_id_header: "X-Hookway-Event-Id"  # default
      # tls:
      #   ca_cert: "/etc/ssl/certs/ca-bundle.pem"
      #   client_cert: "/etc/ssl/certs/client.pem"
      #   client_key: "/etc/ssl/private/client.key"
      #   server_name: "internal.example.com"
      #   insecure_skip_verify: false

  # The dead_letter destination referenced above — a durable, non-webhook backend.
  dlq-rabbit:
    type: "rabbitmq"
    rabbitmq:
      url: "${RABBITMQ_URL}"
      exchange: "hookway.dlq"
      exchange_type: "direct"
      declare_queues: true   # declares the "webhook-dead-letters" queue on startup
```

See `examples/destinations/webhook.yaml` for the full annotated reference config with all four auth types shown.

### Resource provisioning and drift (`on_existing_conflict`)

For `sqs` and `sns` destinations, `destinations.<id>.on_existing_conflict` controls what happens when a target queue/topic already exists with attributes that differ from the config. Allowed values are `error`, `warn`, and `reconcile`; the default is `reconcile`.

- `reconcile` (default): edit the existing resource's mutable attributes (SQS `VisibilityTimeout`, `MessageRetentionPeriod`, `ContentBasedDeduplication`; SNS `ContentBasedDeduplication`) to match config, logging each change. Requires `sqs:SetQueueAttributes` / `sns:SetTopicAttributes` IAM permissions.
- `warn`: log each differing attribute and use the existing resource unchanged.
- `error`: fail startup on any difference.

Immutable conflicts — the FIFO-vs-standard nature of a queue/topic — always fail startup regardless of the policy, since they cannot be edited and would otherwise cause failed publishes. The field is only valid on `sqs`/`sns` destinations; setting it on a `rabbitmq` destination is a config error.

When `create: true` and the target does not exist, it is created with the configured attributes (no reconcile needed).

## Endpoints

Endpoints define the public webhook paths and target queues. Endpoint names are internal stable identifiers; the HTTP path comes from `path`, not from the endpoint name.

```yaml
endpoints:
  paddle_billing:
    enabled: true
    path: "/webhooks/paddle/billing"
    delivery_failure_policy: "stop_on_failure"
    destinations:
      - destination: "billing"
        target: "billing-events"
        create: true
        queue_options:
          message_ttl: "72h"
      - destination: "audit"
        target: "billing-audit-events"
        create: true
    max_body_bytes: 2097152
    rate_limit:
      enabled: true
      requests_per_minute: 60
      burst: 10
      key_by: "ip"
    replay_protection:
      enabled: true
      ttl: "10m"
      max_entries: 50000
    ip_allowlist:
      - "203.0.113.10"
      - "198.51.100.0/24"
    auth:
      type: "hmac_sha256"
      scheme: "hmac_sha256_ts_colon"
      secret: "${WEBHOOK_SECRET}"
      signature_header: "Paddle-Signature"
      timestamp_tolerance: "5m"

  github_events:
    enabled: true
    path: "/webhooks/github/events"
    destinations:
      - destination: "billing"
        target: "github-events"
        create: true
    rate_limit:
      enabled: false
    replay_protection:
      enabled: true
      ttl: "5m"
      max_entries: 25000
    ip_allowlist:
      - "192.30.252.0/22"
      - "185.199.108.0/22"
      - "140.82.112.0/20"
      - "143.55.64.0/20"
    auth:
      type: "hmac_sha256"
      scheme: "hmac_sha256_hex"
      secret: "${GITHUB_WEBHOOK_SECRET}"
      signature_header: "X-Hub-Signature-256"
      idempotency_header: "X-GitHub-Delivery"

  revenuecat_billing:
    enabled: true
    path: "/webhooks/revenuecat/billing"
    destinations:
      - destination: "billing"
        target: "revenuecat-events"
        create: true
    replay_protection:
      enabled: true
      ttl: "10m"
      max_entries: 50000
    auth:
      type: "header_exact"
      scheme: "exact"
      secret: "${REVENUECAT_WEBHOOK_AUTH}"
      signature_header: "Authorization"
      # Strip a leading Bearer/bearer prefix before comparing to secret.
      remove_bearer: true
```

Set `REVENUECAT_WEBHOOK_AUTH` to the token value only when `remove_bearer` is true (e.g. `rc-secret`). Omit `remove_bearer` or set it to `false` when the secret must match the full header value exactly (e.g. `Bearer rc-secret`).

Endpoint fields:

- `enabled`: only enabled endpoints are registered.
- `path`: public HTTP path; must start with `/` and be unique among enabled endpoints. Parent/child overlaps such as `/webhooks/paddle` and `/webhooks/paddle/billing` are rejected to avoid ambiguous webhook routing.
- `delivery_failure_policy`: optional route failure behavior. Defaults to `stop_on_failure`. Use `skip_failed` when partial delivery is acceptable.
- `destinations`: required list of destination route objects. Each route is published once. With `skip_failed`, multiple routes fan out concurrently up to `server.fanout_concurrency`; with `stop_on_failure`, routes publish sequentially in config order.
- `max_body_bytes`: optional positive endpoint override. Omit or set `0` to inherit `server.max_body_bytes`.
- `rate_limit`: optional endpoint override. Omit to inherit global `rate_limit`; set `enabled: false` to disable endpoint-specific limiting in later HTTP code.
- `replay_protection`: optional endpoint replay cache settings.
- `ip_allowlist`: optional list of IP addresses or CIDR ranges accepted for this endpoint.
- `auth`: endpoint authentication settings.
- `filters`: optional endpoint-level filter chain. All conditions must pass or the payload is not published to any destination. See [Filtering](#filtering).
- `filter_response`: optional. Controls the response when the endpoint filter chain rejects a payload. `status: 0` (default) returns a silent `202` ack with no destinations; a non-2xx status (e.g. `422`) is returned to the sender instead. Has no effect without `filters`.

Destination route fields:

- `destination`: required target key under `destinations`.
- `target`: required queue/topic/routing-key target for this destination; must match `^[a-zA-Z0-9._-]{1,128}$`, or be a template with `{variable}` placeholders declared under the endpoint's `variables` (see [Variables](#variables)); the rendered value must satisfy the same rule.
- `create`: when true, queue layer can declare and bind this route target during startup.
- `queue_options`: optional RabbitMQ queue arguments (only valid when the destination is `rabbitmq`). Same fields as `destinations.<id>.rabbitmq.queue_defaults`; route values override defaults. Use this for source-specific or destination-specific retention/backlog policy. Routes sharing one queue within one destination must use identical queue options.
- `sqs_options`: optional SQS queue attributes (only valid when the destination is `sqs`). `visibility_timeout` and `message_retention` are durations expressed in whole seconds within the SQS-accepted bounds (visibility `0`–`12h`, retention `60s`–`14d`).
- `sns_options`: optional SNS settings (only valid when the destination is `sns`). `subject` sets the SNS message Subject (≤100 printable-ASCII characters; not permitted on FIFO topics).
- `redis_options`: optional per-route Redis settings (only valid when the destination is a `redis_*` type). Not all fields apply to all Redis types — validation enforces the coupling:
  - `max_len`: approximate maximum number of entries in the stream or list. Hookway uses `XADD MAXLEN ~` (stream) for efficient approximate trimming. `0` or omit for unbounded. Valid for `redis_stream` and `redis_list`.
  - `consumer_group`: name of the consumer group Hookway creates at startup when `create: true`. Valid for `redis_stream` only.
  - `ttl`: key expiry duration (e.g. `"168h"`). Valid for `redis_kv` only.
  - `key_template`: key naming template for `redis_kv`. Tokens `{prefix}`, `{target}`, `{endpoint}`, `{id}`, and `{idempotency_key}` are substituted at publish time. Valid for `redis_kv` only.
- `kafka_options`: optional per-route Kafka settings (only valid when the destination is `kafka`). `partition_key` derives the Kafka message key using the same source model as `fifo.group_id` (`source`: `static` | `endpoint` | `header` | `body`, plus `value`/`path`/`fallback`); omit it for round-robin partition distribution. `partitions` and `replication_factor` tune the topic Hookway pre-creates for `create: true` routes; omit either (or leave at `0`) to use the broker default.
- `temporal_options`: required per-route Temporal settings (only valid when the destination is `temporal`). `workflow_type` names the workflow to start; `input` is `envelope` (default) or `body`; `workflow_id` derives the workflow id using the same source model as `fifo.group_id` (`source`: `static` | `endpoint` | `header` | `body`, plus `value`/`path`) with an optional `prefix` and an event-id fallback; `id_reuse_policy` and the `execution_timeout`/`run_timeout`/`task_timeout` durations map onto the SDK start options. See [Temporal](#temporal).
- `fifo`: required when `target` ends in `.fifo` (and only allowed then). Controls FIFO ordering and deduplication:
  - `group_id.source`: one of `static` | `endpoint` | `header` | `body` — where the `MessageGroupId` comes from. `static` uses `group_id.value`; `header`/`body` extract `group_id.path` (a header name, or a dotted path into the JSON body); `endpoint` uses the endpoint name. `group_id.fallback` (a literal, or `endpoint`) is used when extraction yields nothing.
  - `dedup.use_idempotency_key`: when true, use the envelope `idempotency_key` as the `MessageDeduplicationId` when present.
  - `dedup.content_based_fallback`: must be `true` for FIFO targets so messages without an idempotency key are still deduplicated via the resource's content-based deduplication.
- `filters`: optional route-level filter chain. When it fails, only this route is skipped — the payload can still reach other routes. A skipped route is never counted as a delivery failure. See [Filtering](#filtering).

Delivery failure policies:

- `stop_on_failure` (default): publish routes sequentially in config order. The first failed route stops later routes from starting and Hookway returns `503`.
- `skip_failed`: publish every route. Hookway returns `202` when at least one route succeeds and includes `failed_destinations` in the response. If every route fails, Hookway returns `503`.

In schema_version 2 the same policies are `flows.<name>.delivery_failure_policy`. Fan-out is the set of `publish` successors of a node. A `filter` that drops a branch is not a delivery failure. Partial success under `skip_failed` releases the `replay_guard` reservation so a sender retry is accepted (I17); consumers still dedupe on `idempotency_key`.

`publish.message_attributes` attaches SNS/SQS MessageAttributes. Values may be strings (templated from envelope variables), numbers, YAML arrays (`String.Array`), or `{data_type, value}` maps. `String.Array` is required for subscription filter policies such as `fields_changed`. Attribute names are at most 256 bytes, values at most 256 KiB, and at most 10 attributes per message (AWS limits). Oversized values fail the publish rather than being silently dropped.

Endpoint rate-limit fields match global rate-limit fields. `key_by` must be `ip` for MVP.

Replay protection fields:

- `enabled`: enables endpoint replay protection in later HTTP code.
- `ttl`: positive duration for replay cache entries.
- `max_entries`: positive per-process entry cap.

Replay protection is in-process schema/config for MVP. In multi-node deployments, each node has its own replay cache unless later code uses shared storage, so identical replayed requests can pass if routed to a different node. The reservation is only committed after a successful publish, so a transient broker outage does not turn a provider's legitimate retry into a permanent `409 replay detected`.

In schema_version 2 the same process-local cache is the `replay_guard` node. That is weakness W5, kept deliberately by decision D11 (no shared state on the hot path): N pods means N allowed replays. Consumers should still dedupe on `idempotency_key`, which is the documented retry contract. `rate_limit` is likewise pod-scoped; N pods means N× the configured RPM.

## Code node (schema_version 2)

`code` runs JavaScript on a goja VM compiled at config load. A syntax error fails `hookway validate`, not the first request. Known transforms (`changed_fields`, `field_map`, `xml_prune`) belong on `transform`, not in JS.

```js
// `input` is provided; return an object {body, vars}.
// There is no I/O, no require, no timers, and no access to other flows.
return { body: input.body, vars: { computed: String(input.body.id).toUpperCase() } };
```

Input is a deep copy of `{body, vars, headers}`. Sensitive headers are redacted. The script cannot see `Verified`, raw request bytes, `Upstream`, or auth secrets. Return `{body, vars}`; omitted keys keep the previous envelope values. XML bodies are rejected — use `transform` `xml_prune`.

Options:

- `script`: required JavaScript. Top-level `return` is allowed (the runtime wraps the script in a function).
- `timeout`: optional duration, default `1s` (C1). A missing timeout is a lint warning because goja has no memory cap. See [Security § Code node](security.md#code-node).
- `max_output_bytes`: optional, default `1048576` (C5). Oversize output is refused, not truncated.
- `max_call_stack`: optional, default `1000` (C2).

The interrupt is checked between JavaScript statements. A pathological regex may run until it finishes; use `filter` instead. Per-request VMs are not pooled (C6).

Fan-out is at-least-once per destination, not atomic across destinations. Confirmed broker messages cannot be canceled or recalled. With `stop_on_failure`, if one destination succeeds and a later destination fails, Hookway returns `503` so the provider retries. Earlier destinations may receive the same event again, so consumers must dedupe with `idempotency_key`. With `skip_failed`, partial success is acknowledged with `202` and failed routes are reported in the response.

### Filtering

Filters gate a payload based on its headers and JSON body before it reaches a destination. A filter chain is a list of conditions combined with **AND** — every condition must pass. Filters attach at two levels:

- **Endpoint** (`endpoints.<name>.filters`): evaluated first, after authentication and body parsing. If the chain fails, nothing is published. The response is controlled by `filter_response` (default: a silent `202` ack).
- **Route** (`endpoints.<name>.destinations[].filters`): evaluated per destination. A route whose chain fails is dropped from delivery; other routes still receive the payload. Route filtering **always** acks (`202`) — `filter_response` applies only to the endpoint chain. When every route is filtered out, the response is a normal `202` with an empty `destinations` list.

Each condition has four fields:

```yaml
filters:
  - source: header        # header | body
    path: X-GitHub-Event  # header name, or dotted body path such as repository.private
    operator: eq
    value: "push"
```

- `source`: `header` reads the request header named by `path` (case-insensitive; the first value wins for multi-valued headers). `body` walks `path` as a dotted path into the parsed JSON body (e.g. `repository.private`). Array indexing is not supported.
- `operator`: see the table below.
- `value`: the comparison value. Its type (string, number, boolean) selects the comparison mode for `eq`/`ne`. Omit `value` for `exists`/`not_exists`.

Operators:

| Operator | Value type | Meaning |
|---|---|---|
| `eq`, `ne` | string, number, boolean | equal / not equal (type-aware) |
| `gt`, `gte`, `lt`, `lte` | number | numeric comparison |
| `contains`, `prefix`, `suffix` | string | substring / prefix / suffix |
| `regex` | string | Go RE2 match; the pattern is compiled and validated at config load |
| `exists`, `not_exists` | (none) | value present / absent at `path` |

**Type coercion.** Header values are always strings, so numeric and boolean operators parse them (`"42"` matches `eq: 42`; `"true"`/`"1"` match `eq: true`). Body values keep their JSON type: a numeric operator on a JSON number compares directly, and a body string `"42"` is also parsed for `eq: 42`, but a JSON number does **not** match a string `value` such as `"42"`. When a value is missing or its type cannot be coerced to the operator's type, the condition **fails** (it never errors) — including `ne`, which requires the value to be present, comparable, and different. A non-JSON request body makes every `body` condition fail except `not_exists`, which passes. A JSON `null` leaf counts as present: `exists` passes, comparisons fail.

**Header source.** Filters read the original, unredacted request headers, so behaviour does not change when `security.redacted_header_denylist` or `forwarded_header_allowlist` are adjusted.

**Env expansion caveat.** Config files are passed through `os.ExpandEnv` before parsing, so `$name` or `${...}` sequences inside any value — including a `regex` value — are replaced with environment variables (usually empty). Anchors and character classes such as `^refs/heads/\w+$` are unaffected; escape or avoid literal `$`-prefixed tokens.

Drops are counted by `hookway_filtered_total{endpoint, destination_queue_id, queue, level}` (`level` is `endpoint` or `route`) and logged at Info with the failing condition's index, source, path, and operator — never the header or body value.

**Body filters require a JSON-parseable body.** `source: body` conditions read the parsed JSON body; a request whose body did not parse as JSON (e.g. form-encoded or plain-text payloads) fails every `body` condition except `not_exists`, exactly as described above. To make that silent-looking drop visible to operators, when the *failing* condition is body-sourced and the body did not parse as JSON, `level` is `body_unparsed` instead of `endpoint`/`route`, and a `"body filter evaluated against unparsed body"` warning is logged with the endpoint and filter index. This is purely a visibility signal — match semantics and the delivery outcome are unchanged. A `header` condition failing against a non-JSON body, or a `body` condition legitimately failing against present JSON, is not flagged this way.

Auth fields:

- `type`: authentication family; `hmac_sha256` for HMAC body signatures (the digest is chosen per-scheme — most use SHA-256, `hmac_sha1_hex` uses SHA-1), `header_exact` for static header secrets, `jwt` for Bearer JSON Web Tokens, `no_auth` to disable verification entirely.
- `scheme`: the wire format *within* the auth type. Scheme names describe the mechanism (digest, encoding, and whether the header carries a signed timestamp), not any provider. Optional for `hmac_sha256`, defaults to `hmac_sha256_ts_colon`. Built-in schemes:
  - `hmac_sha256_ts_colon` (`hmac_sha256`): timestamped, labeled header `ts=<unix>;h1=<hex_hmac>` validated over `ts + ":" + raw_body`; requires `timestamp_tolerance`. *Example: Paddle's `Paddle-Signature`.*
  - `hmac_sha256_ts_dot` (`hmac_sha256`): timestamped, labeled header `t=<unix>,v1=<hex_hmac>` validated over `t + "." + raw_body`; requires `timestamp_tolerance`. Extra comma-separated pairs are ignored. *Example: Stripe's `Stripe-Signature` (its legacy `v0=` pair is ignored).*
  - `hmac_sha256_hex` (`hmac_sha256`): prefixed hex HMAC-SHA256 over the exact raw body, no signed timestamp (so `timestamp_tolerance` is not required); the header carries a `<prefix>=<hex>` value. *Example: GitHub's `X-Hub-Signature-256: sha256=<hex>`.*
  - `hmac_sha256_base64` (`hmac_sha256`): raw **base64** HMAC-SHA256 over the exact raw body (no prefix, no timestamp). *Example: Shopify's `X-Shopify-Hmac-SHA256`.*
  - `hmac_sha1_hex` (`hmac_sha256`): prefixed hex HMAC-**SHA1** over the exact raw body; for legacy integrations that have not migrated to SHA-256. *Example: GitHub's legacy `X-Hub-Signature: sha1=<hex>`.*
  - `exact` (`header_exact`): constant-time equality check between `signature_header` and `secret`; body is not verified. Use `remove_bearer: true` to strip a leading `Bearer`/`bearer` prefix before comparing. *Example: a static token such as RevenueCat's `Authorization` header.*
  - `basic` (`header_exact`): HTTP Basic — `Authorization: Basic <base64(user:pass)>`; constant-time compare against the configured `user:pass` `secret`; body not verified.
  - `no_auth` (`no_auth`): **accepts every request without verification.** Not recommended — Hookway logs a startup warning for any endpoint using it. Reserve it for trusted internal traffic where authentication is enforced upstream (e.g. a private network or gateway). `secret` and `signature_header` are not required for this scheme.
  - `jwt_hs256` / `jwt_rs256` / `jwt_es256` (`jwt`): verify a Bearer JWT read from `signature_header` (typically `Authorization`). Each scheme pins exactly one signing algorithm — a token whose `alg` does not match (including `alg: none`) is rejected, defeating algorithm-confusion attacks. `secret` holds the key: the shared HMAC secret for `jwt_hs256`, or a PEM-encoded public key for `jwt_rs256` (RSA) / `jwt_es256` (EC P-256). `exp` is required; `timestamp_tolerance` is the clock-skew leeway. Optional `expected_issuer`/`expected_audience` enforce `iss`/`aud`. `body_hash_claim` is required: it names a claim carrying the hex SHA-256 of the raw body, binding the token to the payload. Set `allow_unbound_body: true` to opt out (the JWT then authenticates the caller but not the body).
  - New providers are added by registering a scheme in `internal/auth`, without changing the request path or config shape.
- `secret`: required (except for `no_auth`); reference an environment variable such as `${WEBHOOK_SECRET}`. For `jwt_rs256` and `jwt_es256`, this must be a PEM-encoded public key (e.g. `${JWT_PUBLIC_KEY}`).
- `signature_header`: required HTTP header Hookway reads for verification (signature or static secret); not required for `no_auth`. For `jwt` schemes, set to `Authorization` (the JWT is read as `Bearer <token>`).
- `remove_bearer`: optional; when `true` on `header_exact` endpoints, strips a leading `Bearer`/`bearer` prefix (case-insensitive) and following whitespace from the header value before comparing to `secret`. Ignored for `hmac_sha256` endpoints (config validation rejects it).
- `timestamp_tolerance`: optional duration, default `5m`; required (and enforced positive) only for timestamped HMAC schemes (e.g. `hmac_sha256_ts_colon`) and `jwt` schemes, where it is the clock-skew leeway for `exp`/`nbf` validation. If set explicitly on any scheme, it must be a positive duration — omit the field to use the default, or a negative/zero value is rejected at config load.
- `idempotency_header`: optional header carrying a stable provider delivery/event id (e.g. `X-GitHub-Delivery`). Its value becomes the envelope `idempotency_key` and the AMQP `x-idempotency-key` header so consumers can deduplicate. When omitted on `hmac_sha256` endpoints, Hookway falls back to a hash of the verified signature. When omitted on `header_exact` endpoints, Hookway falls back to a hash of the raw body.
- `expected_issuer`: optional; for `jwt` schemes, enforces the `iss` claim must match this value.
- `expected_audience`: optional; for `jwt` schemes, enforces the `aud` claim must match this value.
- `body_hash_claim`: required for `jwt` schemes unless `allow_unbound_body` is true. Names a JWT claim containing the hex-encoded SHA-256 hash of the raw request body. Hookway rejects any request where the body does not match the hash in the token, binding the JWT to the exact payload and preventing body-swap attacks.
- `allow_unbound_body`: optional; `jwt` only. When true, `body_hash_claim` may be omitted and the JWT authenticates the caller but not the payload. Do not enable this unless a gateway in front of Hookway already binds the body.

### Variables

Variables let one route serve many environments or tenants. An endpoint declares named variables derived from each request, and `{name}` placeholders in route targets and in a webhook destination `url` are filled per request. Without a `variables` block nothing changes: targets are plain strings, exactly as before.

```yaml
endpoints:
  digifi:
    path: "/webhooks/{envName}/digifi"
    variables:
      envName:
        source: path                       # header | body | query | path
        values: [qa-gm3, staging-portal, portal]
      tenant:
        source: body
        path: "data.object.organizationId"
        pattern: "^[a-z0-9]{1,24}$"
    on_unresolved_variable:
      destination: "archive"
      target: "unrouted"
    destinations:
      - destination: "archive"
        target: "{envName}-events"
      - destination: "hub"                 # webhook url: https://{envName}-api.htap.sh/api/v3/digifi/webhook
        target: "event"
      - destination: "audit"
        target: "{envName}-{tenant}"
```

**Declaring a variable** (`endpoints.<name>.variables.<var>`):

- `source` (required): `header`, `body`, `query`, `path`, or `lookup` (see below).
- `path`: what to read. A header name, a dotted JSON body path (`data.object.id`; arrays are not indexed), a query-string key (first value wins), or the name of a `{segment}` in the endpoint `path`. Defaults to the variable's own name. Header lookups use the raw request headers, like filters.
- Exactly one of:
  - `values`: the permitted values. Routes using the variable are **expanded at startup** into one concrete route per value (a Cartesian product across variables, capped at 256 routes per endpoint), so queue and topic provisioning (`create: true`), validation, metrics, and replay behave exactly as for static targets.
  - `pattern`: an anchored regular expression (`^...$`, RE2, at most 256 characters). The target is rendered per request and the destination receives a target it has never seen; see the per-type note below.
- `default`: used when the value is missing, empty, or not accepted. Must itself be accepted.

Values are trimmed, must be 1-128 characters, and the rendered target must still match the target name rule (`^[a-zA-Z0-9._-]{1,128}$`). A variable may not smuggle a `.fifo` suffix onto a template that does not end in a literal `.fifo`; FIFO targets keep the suffix in the template. Names `prefix`, `target`, `endpoint`, `id`, and `idempotency_key` are reserved (they are `redis_kv` key-template tokens). Every declared variable must be referenced by the endpoint path, a route target, or a webhook `url`.

**Lookup source.** `source: lookup` resolves the variable through an external provider keyed by a value read from the request:

```yaml
variables:
  envByApp:
    source: lookup
    lookup:
      key: { source: body, path: "data.object.applicationId" }   # header | body | query | path
      provider: dynamodb                                          # dynamodb | http | postgres
      dynamodb:
        table: "sandbox_ephemeral_routing_data"
        index: "CreatedAtIndex"                                   # optional
        key_attribute: "id"                                       # default id
        filter: { attribute: "id_type", value: "application" }    # optional equality filter
        value_attribute: "env_name"
        # region, endpoint, access_key_id, secret_access_key as for sqs; consistent_read: false
        # (consistent_read is rejected by DynamoDB on a global secondary index; use it only with the base table or a local index)
      timeout: "2s"                                               # default 2s per provider call
      cache: { ttl: "5m", negative_ttl: "0s", max_entries: 10000 } # defaults
    pattern: "^[a-z0-9-]{1,32}$"                                  # optional: values/pattern still gate the result
```

- `key` uses the same source model as a plain variable (path defaults to the variable name). The key is trimmed; an empty key or one longer than 256 characters is a miss without calling the provider.
- `dynamodb` runs a Query on the key attribute (newest first when the index has a sort key) and applies the `filter` client-side, so a limit can never hide the matching row. It needs `dynamodb:Query` on the table and, when `index` is set, on `<table>/index/*`.
- `http` sends a GET (or POST with `{"key": "..."}`) to `url`, which must contain exactly one `{key}` (URL-escaped, and not in the host, so a request cannot choose the resolver), with optional `headers`, and reads the dotted `value_path` from the JSON response. A 404 is a miss; any other non-2xx status or an unparsable body is an error.
- `postgres` runs `query` with exactly one `{key}` placeholder bound as `$1` (never interpolated; `{key}` is used because `$1` would be treated as an environment reference at load) and reads the first column of the first row. No row is a miss.
- A lookup variable may declare `values` or `pattern` to gate the returned value, or neither; the rendered target is still checked for a safe name. `default` applies on a miss or a provider error.
- Hits are cached per variable for `cache.ttl`; misses are not cached unless `negative_ttl` is set (the row is often written moments before the first webhook arrives). Concurrent first lookups of one key make a single provider call.
- A provider error (timeout, throttling, permissions, bad response) is logged at warn and counted under `hookway_variable_lookup_total{result="error"}` but routes exactly like a miss: `default`, then the fallback route, then 400. Results are otherwise labelled `hit`, `miss`, or `cached`; `hookway_variable_lookup_duration_seconds` records latency.
- Lookup latency sits on the request path before publish and is bounded by `timeout`. Replays never repeat a lookup: the stored envelope already carries `variables`.
- Environments that do not need the lookup (production, staging) should use a header, path, or `values` source instead; only the config for the shared sandbox declares lookup variables.

**Where placeholders are allowed:** route `target` strings, `destinations.<id>.webhook.url`, and the Temporal route's `temporal_options.workflow_id.prefix`. Every placeholder in a webhook `url` must be declared by every endpoint that routes to that destination. A placeholder in the url **host** must be followed by a literal domain suffix (`{envName}-api.example.com`), so a request can pick at most one label under a fixed domain and never an arbitrary host. `${ENV_VAR}` references are unaffected: they are expanded at load and never treated as variables.

**Path placeholders:** `path: "/webhooks/{envName}/digifi"` matches exactly one non-empty segment per placeholder and has no trailing-slash tolerance, like literal paths. Each placeholder needs a variable with `source: path`. Two enabled endpoints whose paths could match the same request (a literal where the other has a placeholder, or a parent/child pair) are a validation error, so matching never needs precedence rules.

**Unresolved variables.** After filters have run, Hookway resolves the variables the remaining routes need. If one cannot be resolved (and has no `default`), or a rendered target is not a valid name:

- with `on_unresolved_variable: {destination, target}` the event is published **only** to that static fallback route, the response is 202 with `unresolved_variables: [..]`, and `hookway_variable_unresolved_total{action="fallback"}` increments. The fallback destination cannot be a `webhook` or `temporal` destination and its target must be static.
- otherwise the sender receives `400 {"error":"unresolved_variable","variable":"envName"}`, nothing is published, the replay-protection reservation is released, and `hookway_variable_unresolved_total{action="rejected"}` increments.

A filtered request is never a variable error, and a variable used only by a route a route-level filter dropped is not required.

**What each layer sees.** The publisher, the response `destinations[].queue`, and the replay store get the **rendered** target, so replays re-deliver to the same queue without re-resolving. The event envelope carries the resolved values under `variables` (omitted when the endpoint declares none), which is also how a templated webhook `url` is rendered on replay. Metrics keep the **authored template** as the `queue` label (`hookway_publish_total{queue="{envName}-events"}`), so cardinality is bounded by the config rather than by the request stream.

**Per destination type.** Enumerated variables work with every type and expand at startup. Pattern variables render per request, and the destination resolves the rendered target on first sight (once per distinct name, cached, with concurrent first sights coalesced):

- `s3` (folder), `postgres` (column value), `mongodb` (collection), `redis_list`, `redis_pubsub`, `redis_kv`, `webhook` (appended path segment), and `temporal` (task queue) need no remote work.
- `sqs` looks the queue up by name, creates it when the route has `create: true` (with the route's `sqs_options` and `fifo` settings; a template ending in `.fifo` yields FIFO queues), and reconciles an existing queue under `on_existing_conflict`. A missing queue with `create: false` fails that publish (503, so the sender retries) and is not cached.
- `sns` lists topics to find the ARN, creating the topic when `create: true`.
- `kafka` creates the topic when `create: true` (with the route's partitions and replication factor); otherwise a missing topic surfaces as a write error, since broker auto-creation is disabled.
- `redis_stream` derives the prefixed stream key and creates the consumer group when `create: true` and `redis_options.consumer_group` is set.
- `rabbitmq` passes the rendered routing key through; the queue must be pre-bound, and `create: true` is rejected on such routes because a queue whose name is unknown cannot be declared up front.

First-sight provisioning runs inside `server.publish_timeout`. The per-publisher cache of rendered targets holds 10,000 entries and evicts the oldest with a warning; an eviction only costs a re-lookup.

See `examples/variables.yaml` for a complete reference config.

## Replay

`replay` configures an optional store of accepted inbound events (Postgres-backed) and a protected HTTP API to list, inspect, and re-deliver them. Disabled (the zero value) by default: nothing is captured, no listener is started, and no Postgres connection is required.

- `replay.enabled`: optional boolean, default `false`.
- `replay.addr`: required when enabled. The dedicated listener address for the replay API, e.g. `":9091"`. Separate from `server.addr` so the replay API is never accidentally exposed on the public webhook port.
- `replay.skip_unauthenticated`: optional boolean, default `false`. When true, skips capture for endpoints whose `auth.type` is `no_auth`, since such endpoints have no caller identity to attribute a captured event to.
- `replay.ttl`: optional duration, default `0` (keep forever). Bounds how long a captured event is retained before the background cleanup sweep deletes it.
- `replay.max_events`: optional non-negative integer, default `0` (no cap). Caps the total number of retained events; the cleanup sweep evicts the oldest events first once the cap is exceeded.
- `replay.cleanup_interval`: optional duration, default `1h` when `replay.enabled` is true and the field is left unset. How often the background sweep runs to enforce `ttl` and `max_events`.
- `replay.postgres`: required when enabled. Reuses the same connection shape as `destinations.<id>.postgres` (`url`, `schema`, `max_conns`, `tls`); unlike a postgres destination, `table` is not a required single-sink table name — the replay store uses its own fixed table names.

Set at least one of `ttl`/`max_events` to bound storage — `ttl` bounds by age, `max_events` by row count; the cleanup loop enforces both.

```yaml
security:
  api_key: "${REPLAY_API_KEY}"

replay:
  enabled: true
  addr: ":9091"
  skip_unauthenticated: true
  ttl: "168h"
  max_events: 1000000
  cleanup_interval: "1h"
  postgres:
    url: "${REPLAY_DB_URL}"
```

**Authentication:** the replay API is protected by [`security.api_key`](#securityapi_key) (`Authorization: Bearer <key>`), the same key used by the metrics endpoint and config API. Config validation **requires** a non-empty `security.api_key` whenever `replay.enabled` is true — an unauthenticated API that can re-inject traffic into destinations is rejected at config load rather than silently left open.

**Capture requires authentication to pass.** Only events that pass the endpoint's own authentication are captured — a request rejected by auth (bad signature, expired token, etc.) is never written to the replay store. `replay.skip_unauthenticated` additionally excludes `no_auth` endpoints from capture even though such requests do pass "authentication" (there being none to fail).

### Replay API

The replay API is served on `replay.addr`, distinct from the webhook listener:

- `GET /replay/events`: list captured events. Query params: `endpoint`, `destination`, `target`, `status`, `since`/`until` (RFC 3339 timestamps), `limit`, `cursor` (opaque, from a previous response's `next_cursor`). Response: `{"events": [...], "next_cursor": "..."}`, where each event summary has `id`, `endpoint`, `status`, `idempotency_key`, `received_at`.
- `GET /replay/events/{id}`: fetch one captured event, including its full stored envelope. Response: `{"id", "endpoint", "status", "idempotency_key", "received_at", "envelope"}`. `404` when not found.
- `POST /replay/events/{id}/replay`: re-deliver one captured event to every destination it was originally recorded as delivered/failed to. Body (optional): `{"reprocess": false}`. Response: `{"replayed_event_id", "original_event_id", "destinations": [...]}`. `404` when the event doesn't exist; `422` when the event has no recorded deliveries (see Limitations below).
- `POST /replay/replay`: bulk re-delivery over a filtered set of events. Body (all fields optional): `{"endpoint", "destination", "target", "status", "since", "until", "reprocess": false, "dry_run": false, "max": 100}`. With `dry_run: true`, returns `{"matched": <count>, "dry_run": true}` and replays nothing. Otherwise replays up to `max` matching events (default and per-request ceiling; the store additionally clamps list pages to its own maximum) and returns `{"replayed": <count>, "capped": <bool>, "results": [...]}` — `capped` is true when more events matched the filter than were replayed in this request. `destination`/`target` here only **select** which events to replay (events that were recorded as delivered/failed to that destination) — they do not restrict delivery. Each matched event is still re-delivered to **all** of its own recorded destinations, not just the one used to select it.

**`reprocess`:** a replayed event is published as a new event (a fresh event id, `replay_of` set to the original event's id, and hop count reset to 0). When `reprocess` is `false` (default), the original `idempotency_key` is preserved, so a consumer that already deduplicated the original delivery treats the replay as a no-op (dedup-safe re-send). When `true`, a fresh idempotency key is generated, so consumers process the replayed copy as a new event.

### Limitations

An event with no recorded deliveries is not replayable (`422 Unprocessable Entity` on single replay; skipped with a reason in bulk replay). That is now limited to events that never reached publish (for example every route was filtered out). On `stop_on_failure`, the failed destination and any not-yet-attempted routes are recorded as `failed` so the event can be replayed.

Proxy-mode webhook destinations are replayable: capture persists the original raw body and request headers in the replay store (`raw_body_base64` and `orig_headers`). Those fields are not forwarded to other destination types.

## Logging & observability

Hookway emits structured `slog` JSON logs. Each log record carries a consistent set of field keys so log queries and alerts are stable across destinations.

- `logging.level`: optional, one of `debug`, `info`, `warn`, `error`; default `info`. Controls the minimum severity written to stdout — e.g. set `debug` to see the startup verify/exists checks and retry-attempt lines called out below, which are otherwise suppressed at the default `info` level.

### Canonical log fields

| Key | Present on | Description |
|-----|-----------|-------------|
| `destination` | publish path, startup | Stable destination id from the config map (e.g. `billing`). |
| `target` | publish path, startup | Queue/topic/routing-key/stream name (e.g. `stripe-events`). |
| `event_id` | publish path | Envelope event id; correlates with the `idempotency_key` in the published message. |
| `endpoint` | publish path | Webhook endpoint name (e.g. `stripe_billing`). |
| `request_id` | publish path | Value of the configured `request_id_header`, when present. |
| `trace_id` | publish path | Trace id propagated from the inbound request, when present. |
| `error` | error/warn records | Go error string. |
| `subscribers` | `redis_pubsub` | Integer subscriber count at publish time; `0` triggers a drop warning. |
| `attempt` | `rabbitmq` retry | Publish attempt number. `attempt=1` is the initial try (not logged); the first logged retry is `attempt=2`. |
| `group` | `redis_stream` | Consumer group name used or created at startup. |
| `attr` / `want` / `got` / `policy` | `sqs`, `sns` reconcile | Attribute name, expected value, actual value, and policy action during queue/topic attribute reconciliation at startup. |

> **Breaking change (log keys only):** publish logs previously used `destination_queue_id` and `queue` as field keys. They are now `destination` and `target`. Prometheus metric label names and the JSON response/envelope body are unchanged — only the structured log field names changed.

### Log levels by event

| Level | Event |
|-------|-------|
| **Error** | Publish failure — logged once centrally at the request handler, not per-publisher. |
| **Info** | Startup resource creation: "created queue", "created topic", "created bucket", "ensured table", "created consumer group". |
| **Debug** | Startup verify/exists checks; `redis_list` and `redis_kv` per-target registration; RabbitMQ publish retry (with `attempt`); MongoDB duplicate-key skip ("skipped duplicate event"). |
| **Warn** | `redis_pubsub` no-subscriber drop ("published to channel with no subscribers; message dropped") — also increments `hookway_redis_pubsub_dropped_no_subscriber_total`. |
| **Warn** | RabbitMQ connection lost; initial-connect failure; dead-letter delivery. |
| **Info** | RabbitMQ reconnected. |
