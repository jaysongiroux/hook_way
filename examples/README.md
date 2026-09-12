# Examples

This directory contains complete, valid Hookway configurations organized by concern.
Every `.yaml` file is a self-contained config that passes `go run ./cmd/hookway validate`.

## Canonical config

| File | Description |
|------|-------------|
| `config.yaml` | Minimal canonical config: one RabbitMQ destination, one `hmac_sha256` endpoint. This is the default for `make validate` and `make run`. |
| `filters.yaml` | Filtering reference: endpoint-level filtering with a silent drop, route-level filtering that fans out selectively, and a configured non-match status (`filter_response`) using numeric, regex, and `exists` operators. See `docs/configuration.md` § Filtering. |
| `variables.yaml` | Routing variables reference: per-request values from a URL path segment, header, query string, or body fill `{name}` placeholders in route targets and a webhook `url`; enumerated (`values`) vs pattern variables, `default`, and the `on_unresolved_variable` fallback route. See `docs/configuration.md` § Variables. |
| `replay.yaml` | Webhook-replay reference: one RabbitMQ endpoint plus `replay` enabled with a bounded Postgres-backed store (`ttl`, `max_events`, `cleanup_interval`) and its protected API on a separate `replay.addr` listener, authenticated via `security.api_key`. See `docs/configuration.md` § Replay and `docs/security.md` § Replay. |

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
| `destinations/mongodb.yaml` | MongoDB destination: one BSON document per event into a collection named by the route `target`; full event envelope with `_id` = event id; nested body/headers/query/auth; id dedup via duplicate-key no-op; no DDL (collections auto-create). |
| `destinations/temporal.yaml` | Temporal destination: one workflow execution per event; `target` is the task queue; per-route `temporal_options` set the workflow type, envelope-or-body input, workflow-id derivation (static/endpoint/header/body + prefix, event id fallback), id reuse policy, and timeouts; already-started executions count as delivered. |
| `destinations/webhook.yaml` | Webhook destination: async outbound delivery (store-and-forward, 202=accepted) to an HTTP/HTTPS endpoint; envelope vs proxy payload modes; HMAC, API key, or no-auth outbound auth; retries with backoff on 429/5xx/timeout. |

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
