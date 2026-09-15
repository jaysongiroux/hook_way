# Examples

This directory contains complete, valid Hookway configurations organized by concern.
Every `.yaml` file is a self-contained `schema_version: 2` flow config that passes `go run ./cmd/hookway validate`.

## Canonical config

| File | Description |
|------|-------------|
| `config.yaml` | Minimal canonical config: one RabbitMQ destination, one `hmac_sha256` flow. This is the default for `make validate` and `make run`. |
| `filters.yaml` | Filtering reference: a filter node after envelope with a silent drop, a branch-level filter that fans out selectively, and `on_no_match: reject` (422) using numeric, regex, and `exists` operators. See `docs/configuration.md` § Filtering. |
| `variables.yaml` | Routing variables reference: a path placeholder on `http_trigger` (`{envName}`) becomes `envelope.Vars`; a `transform` `field_map` copies a body field to `vars.tenant`; publish targets and a webhook `url` use `{name}` placeholders. Unresolved templates fail closed at publish. See `docs/configuration.md` § Variables. |
| `replay.yaml` | Webhook-replay reference: one RabbitMQ flow plus `replay` enabled with a bounded Postgres-backed store (`ttl`, `max_events`, `cleanup_interval`) and its protected API on a separate `replay.addr` listener, authenticated via `security.api_key`. See `docs/configuration.md` § Replay and `docs/security.md` § Replay. |

## Destination reference configs

Each file showcases one destination type with all available options documented via comments.

| File | Description |
|------|-------------|
| `destinations/rabbitmq.yaml` | Full RabbitMQ option surface: exchange, dead-letter exchange/queue, queue_defaults, TLS, publisher_channels, and a flow that fans out to two publish nodes. |
| `destinations/sqs.yaml` | AWS SQS destination: FIFO queue and a standard queue, both routing via `hmac_sha256`. |
| `destinations/sns.yaml` | AWS SNS destination: single topic auto-created at startup. |
| `destinations/s3.yaml` | AWS S3 destination: one object per event under a date-partitioned key prefix, with SSE options and auto-bucket creation. |
| `destinations/redis_stream.yaml` | Redis / Valkey stream destination: single-node mode; annotated comments for cluster, sentinel, and TLS topology. |
| `destinations/redis_list.yaml` | Redis / Valkey list destination: LPUSH work-queue with an LTRIM cap. |
| `destinations/redis_pubsub.yaml` | Redis / Valkey Pub/Sub destination: fire-and-forget channel broadcast; no `redis_options` block (all fields invalid for pubsub). Lossy — subscribers offline at publish time miss the message. |
| `destinations/redis_kv.yaml` | Redis / Valkey key-value destination: SET with TTL; not a queue — latest-event overwrite semantics. |
| `destinations/postgres.yaml` | Postgres destination: one row per event into a shared table with fixed envelope columns + jsonb (headers/query/body/auth); `target` stored as a column; CREATE TABLE IF NOT EXISTS at startup; id dedup via ON CONFLICT DO NOTHING. |
| `destinations/mongodb.yaml` | MongoDB destination: one BSON document per event into a collection named by the route `target`; full event envelope with `_id` = event id; nested body/headers/query/auth; id dedup via duplicate-key no-op; no DDL (collections auto-create). |
| `destinations/temporal.yaml` | Temporal destination: one workflow execution per event; `target` is the task queue; already-started executions count as delivered. |
| `destinations/webhook.yaml` | Webhook destination: async outbound delivery (store-and-forward, 202=accepted) to an HTTP/HTTPS URL; envelope vs proxy payload modes; HMAC, API key, or no-auth outbound auth; retries with backoff on 429/5xx/timeout. |

## Auth strategy reference configs

Each file demonstrates one auth type with the exact fields required by that type.

| File | Description |
|------|-------------|
| `auth/hmac_sha256.yaml` | HMAC-SHA256 auth: scheme variants shown — `hmac_sha256_ts_colon` (Paddle) and `hmac_sha256_hex` (GitHub). |
| `auth/header_exact.yaml` | Header exact match auth: `exact` scheme (static Bearer token, `remove_bearer: true`). |
| `auth/jwt.yaml` | JWT auth: RS256 scheme with `expected_issuer`, `expected_audience`, and `body_hash_claim`. |
| `auth/no_auth.yaml` | No-auth passthrough: a `no_auth` node disables verification entirely. |

## Running validation

```bash
# Validate the default config
make validate

# Validate all example files
make validate-examples
```
