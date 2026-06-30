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
- `server.read_timeout`: optional duration, default `5s`.
- `server.write_timeout`: optional duration, default `10s`.
- `server.idle_timeout`: optional duration, default `60s`.
- `server.shutdown_timeout`: optional duration, default `10s`.
- `server.max_body_bytes`: required positive integer.
- `server.max_inflight_requests`: optional non-negative integer. When `> 0`, caps concurrently processed webhook requests; once reached, further requests are shed with `503 Service Unavailable` and a `Retry-After` header before any body is buffered. `0` (default) means unlimited. Health, readiness, and metrics endpoints are never subject to this limit.
- `server.fanout_concurrency`: optional non-negative integer. Caps concurrent publishes per webhook request for endpoints using `delivery_failure_policy: skip_failed`. `0` (default) uses `8`.

## Security

- `security.trusted_proxy_headers`: optional boolean. When false (default), the source IP is always the TCP peer (`RemoteAddr`) and `X-Forwarded-For`/`X-Real-IP` are ignored.
- `security.trusted_proxies`: optional list of IP addresses or CIDR ranges. When `trusted_proxy_headers` is true *and* this list is non-empty, forwarding headers are only honored when the immediate TCP peer falls inside one of these ranges; otherwise the peer IP is used. This prevents clients from spoofing `X-Forwarded-For` to bypass the IP allowlist and per-IP rate limiting. Leaving it empty with the flag on is the legacy permissive behavior and should only be used when clients cannot reach Hookway directly.
- `security.request_id_header`: optional request ID header name.
- `security.redacted_header_denylist`: optional list of exact HTTP header names that are redacted in logs and in the published envelope. This augments automatic redaction of obvious secrets; it does not replace it.
- `security.forwarded_header_allowlist`: optional list of HTTP header names. When non-empty, only these headers are logged and forwarded to the queue (fail-safe); every other header is dropped entirely. The denylist and built-in sensitive rules still apply on top, so an allowlisted-but-sensitive header is redacted rather than passed through. When empty, behavior is the legacy denylist filter (everything except denylisted/sensitive headers is forwarded).

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
```

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
saturation (`hookway_saturated_total`), publish latency,
RabbitMQ reconnect/confirm/retry/DLQ counters, config load failures, and —
when `runtime_metrics` is enabled — Go runtime and process CPU/memory metrics. Startup
config failures still block serving traffic, so config failure metrics are
recorded in-process before exit and are most useful for tests or future reload
paths.

## Destinations

`destinations` is a top-level map of backends keyed by stable id. Endpoint `destinations` route to these ids and choose the target (queue, topic, or routing key) for each. Each entry has a `type` field selecting the backend. Supported types are `rabbitmq`, `sqs`, `sns`, `s3`, `redis_stream`, `redis_list`, `redis_pubsub`, `redis_kv`, and `postgres`. See `examples/destinations/` for a complete reference config per type.

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
      endpoint: "${AWS_ENDPOINT:-}"          # http://localhost:4566 for LocalStack
      access_key_id: "${AWS_ACCESS_KEY_ID:-}"
      secret_access_key: "${AWS_SECRET_ACCESS_KEY:-}"
```

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
- `destinations.<id>.s3.endpoint`: optional endpoint override; set to `http://localhost:4566` to target LocalStack. Usually `${AWS_ENDPOINT}` (left empty in production).
- `destinations.<id>.s3.access_key_id` / `secret_access_key`: optional static credentials, usually `${AWS_ACCESS_KEY_ID}` / `${AWS_SECRET_ACCESS_KEY}`. Omit both to use the default credential chain.
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
      endpoint: "${AWS_ENDPOINT:-}"       # http://localhost:4566 for LocalStack
      access_key_id: "${AWS_ACCESS_KEY_ID:-}"
      secret_access_key: "${AWS_SECRET_ACCESS_KEY:-}"
      bucket: "my-webhook-events"
      create: true
      sse: "AES256"                       # or "aws:kms"; omit for default encryption
      # sse_kms_key_id: "${S3_KMS_KEY_ID:-}"
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

Destination route fields:

- `destination`: required target key under `destinations`.
- `target`: required queue/topic/routing-key target for this destination; must match `^[a-zA-Z0-9._-]{1,128}$`.
- `create`: when true, queue layer can declare and bind this route target during startup.
- `queue_options`: optional RabbitMQ queue arguments (only valid when the destination is `rabbitmq`). Same fields as `destinations.<id>.rabbitmq.queue_defaults`; route values override defaults. Use this for source-specific or destination-specific retention/backlog policy. Routes sharing one queue within one destination must use identical queue options.
- `sqs_options`: optional SQS queue attributes (only valid when the destination is `sqs`). `visibility_timeout` and `message_retention` are durations expressed in whole seconds within the SQS-accepted bounds (visibility `0`–`12h`, retention `60s`–`14d`).
- `sns_options`: optional SNS settings (only valid when the destination is `sns`). `subject` sets the SNS message Subject (≤100 printable-ASCII characters; not permitted on FIFO topics).
- `redis_options`: optional per-route Redis settings (only valid when the destination is a `redis_*` type). Not all fields apply to all Redis types — validation enforces the coupling:
  - `max_len`: approximate maximum number of entries in the stream or list. Hookway uses `XADD MAXLEN ~` (stream) for efficient approximate trimming. `0` or omit for unbounded. Valid for `redis_stream` and `redis_list`.
  - `consumer_group`: name of the consumer group Hookway creates at startup when `create: true`. Valid for `redis_stream` only.
  - `ttl`: key expiry duration (e.g. `"168h"`). Valid for `redis_kv` only.
  - `key_template`: key naming template for `redis_kv`. Tokens `{prefix}`, `{target}`, `{endpoint}`, `{id}`, and `{idempotency_key}` are substituted at publish time. Valid for `redis_kv` only.
- `fifo`: required when `target` ends in `.fifo` (and only allowed then). Controls FIFO ordering and deduplication:
  - `group_id.source`: one of `static` | `endpoint` | `header` | `body` — where the `MessageGroupId` comes from. `static` uses `group_id.value`; `header`/`body` extract `group_id.path` (a header name, or a dotted path into the JSON body); `endpoint` uses the endpoint name. `group_id.fallback` (a literal, or `endpoint`) is used when extraction yields nothing.
  - `dedup.use_idempotency_key`: when true, use the envelope `idempotency_key` as the `MessageDeduplicationId` when present.
  - `dedup.content_based_fallback`: must be `true` for FIFO targets so messages without an idempotency key are still deduplicated via the resource's content-based deduplication.

Delivery failure policies:

- `stop_on_failure` (default): publish routes sequentially in config order. The first failed route stops later routes from starting and Hookway returns `503`.
- `skip_failed`: publish every route. Hookway returns `202` when at least one route succeeds and includes `failed_destinations` in the response. If every route fails, Hookway returns `503`.

Endpoint rate-limit fields match global rate-limit fields. `key_by` must be `ip` for MVP.

Replay protection fields:

- `enabled`: enables endpoint replay protection in later HTTP code.
- `ttl`: positive duration for replay cache entries.
- `max_entries`: positive per-process entry cap.

Replay protection is in-process schema/config for MVP. In multi-node deployments, each node has its own replay cache unless later code uses shared storage, so identical replayed requests can pass if routed to a different node. The reservation is only committed after a successful publish, so a transient broker outage does not turn a provider's legitimate retry into a permanent `409 replay detected`.

Fan-out is at-least-once per destination, not atomic across destinations. Confirmed broker messages cannot be canceled or recalled. With `stop_on_failure`, if one destination succeeds and a later destination fails, Hookway returns `503` so the provider retries. Earlier destinations may receive the same event again, so consumers must dedupe with `idempotency_key`. With `skip_failed`, partial success is acknowledged with `202` and failed routes are reported in the response.

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
  - `jwt_hs256` / `jwt_rs256` / `jwt_es256` (`jwt`): verify a Bearer JWT read from `signature_header` (typically `Authorization`). Each scheme pins exactly one signing algorithm — a token whose `alg` does not match (including `alg: none`) is rejected, defeating algorithm-confusion attacks. `secret` holds the key: the shared HMAC secret for `jwt_hs256`, or a PEM-encoded public key for `jwt_rs256` (RSA) / `jwt_es256` (EC P-256). `exp` is required; `timestamp_tolerance` is the clock-skew leeway. Optional `expected_issuer`/`expected_audience` enforce `iss`/`aud`. Optional `body_hash_claim` names a claim carrying the hex SHA-256 of the raw body, binding the token to the payload (without it, the JWT authenticates the caller but not the body).
  - New providers are added by registering a scheme in `internal/auth`, without changing the request path or config shape.
- `secret`: required (except for `no_auth`); reference an environment variable such as `${WEBHOOK_SECRET}`. For `jwt_rs256` and `jwt_es256`, this must be a PEM-encoded public key (e.g. `${JWT_PUBLIC_KEY}`).
- `signature_header`: required HTTP header Hookway reads for verification (signature or static secret); not required for `no_auth`. For `jwt` schemes, set to `Authorization` (the JWT is read as `Bearer <token>`).
- `remove_bearer`: optional; when `true` on `header_exact` endpoints, strips a leading `Bearer`/`bearer` prefix (case-insensitive) and following whitespace from the header value before comparing to `secret`. Ignored for `hmac_sha256` endpoints (config validation rejects it).
- `timestamp_tolerance`: positive duration; required only for timestamped HMAC schemes (e.g. `hmac_sha256_ts_colon`). For `jwt` schemes, used as clock-skew leeway for `exp`/`nbf` validation.
- `idempotency_header`: optional header carrying a stable provider delivery/event id (e.g. `X-GitHub-Delivery`). Its value becomes the envelope `idempotency_key` and the AMQP `x-idempotency-key` header so consumers can deduplicate. When omitted on `hmac_sha256` endpoints, Hookway falls back to a hash of the verified signature. When omitted on `header_exact` endpoints, Hookway falls back to a hash of the raw body.
- `expected_issuer`: optional; for `jwt` schemes, enforces the `iss` claim must match this value.
- `expected_audience`: optional; for `jwt` schemes, enforces the `aud` claim must match this value.
- `body_hash_claim`: optional; for `jwt` schemes, names a JWT claim containing the hex-encoded SHA-256 hash of the raw request body. When set, Hookway rejects any request where the body does not match the hash in the token, binding the JWT to the exact payload and preventing body-swap attacks.
