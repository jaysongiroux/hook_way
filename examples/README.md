# Examples

This directory contains complete, valid Hookway configurations organized by concern.
Every `.yaml` file is a self-contained config that passes `go run ./cmd/hookway validate`.

## Canonical config

| File | Description |
|------|-------------|
| `config.yaml` | Minimal canonical config: one RabbitMQ destination, one `hmac_sha256` endpoint. This is the default for `make validate` and `make run`. |

## Destination reference configs

Each file showcases one destination type with all available options documented via comments.

| File | Description |
|------|-------------|
| `destinations/rabbitmq.yaml` | Full RabbitMQ option surface: exchange, dead-letter exchange/queue, queue_defaults, TLS, publisher_channels, and per-route queue_options. |
| `destinations/sqs.yaml` | AWS SQS destination: FIFO queue (with `fifo.group_id` and dedup options) and a standard queue, both routing via `hmac_sha256`. |
| `destinations/sns.yaml` | AWS SNS destination: single topic with `sns_options.subject`, auto-created at startup. |
| `destinations/s3.yaml` | AWS S3 destination: one object per event under a date-partitioned key prefix, with SSE options and auto-bucket creation. |
| `destinations/redis_stream.yaml` | Redis / Valkey stream destination: single-node mode with `redis_options.max_len` and `consumer_group`; annotated comments for cluster, sentinel, and TLS topology. |
| `destinations/redis_list.yaml` | Redis / Valkey list destination: LPUSH work-queue with `redis_options.max_len` LTRIM cap. |
| `destinations/redis_pubsub.yaml` | Redis / Valkey Pub/Sub destination: fire-and-forget channel broadcast; no `redis_options` block (all fields invalid for pubsub). Lossy — subscribers offline at publish time miss the message. |
| `destinations/redis_kv.yaml` | Redis / Valkey key-value destination: SET with TTL using `redis_options.ttl` and `key_template`; not a queue — latest-event overwrite semantics. |
| `destinations/postgres.yaml` | Postgres destination: one row per event into a shared table with fixed envelope columns + jsonb (headers/query/body/auth); `target` stored as a column; CREATE TABLE IF NOT EXISTS at startup; id dedup via ON CONFLICT DO NOTHING. |

## Auth strategy reference configs

Each file demonstrates one auth type with the exact fields required by that type.

| File | Description |
|------|-------------|
| `auth/hmac_sha256.yaml` | HMAC-SHA256 auth: three scheme variants shown — `hmac_sha256_ts_colon` (Paddle), `hmac_sha256_hex` (GitHub), and `hmac_sha256_ts_dot` (Stripe). |
| `auth/header_exact.yaml` | Header exact match auth: `exact` scheme (static Bearer token, `remove_bearer: true`) and `basic` scheme (HTTP Basic Authorization). |
| `auth/jwt.yaml` | JWT auth: RS256 scheme with `expected_issuer`, `expected_audience`, and `body_hash_claim`. |
| `auth/no_auth.yaml` | No-auth passthrough: disables verification entirely. Requires `scheme: no_auth` explicitly to avoid defaulting to the HMAC scheme. |

## Running validation

```bash
# Validate the default config
make validate

# Validate all example files
make validate-examples
```
