# Examples

This directory contains complete, valid Hookway configurations organized by concern.
Every `.yaml` file is a self-contained flow config that passes `go run ./cmd/hookway validate`.

## Canonical config

| File | Description |
|------|-------------|
| `config.yaml` | Minimal canonical config: one RabbitMQ destination, one `hmac_sha256_hex` GitHub flow (`/webhooks/github`). This is the default for `make validate` and `make run` (core on the host, no replay service). |
| `filters.yaml` | Filtering reference: a filter node after envelope with a silent drop, a branch-level filter that fans out selectively, and `on_no_match: reject` (422) using numeric, regex, and `exists` operators. See `docs/configuration.md` § Filtering. |
| `variables.yaml` | Dynamic routing reference: an environment comes from a path placeholder or filtered header, `field_map` copies a JSON body field to `vars.tenant`, and `{name}` placeholders select a pre-provisioned SQS queue and webhook URL. Unknown environments and unresolved templates fail closed. See `docs/configuration.md` § Variables. |
| `on_error.yaml` | Error-port reference: `http_request` 5xx to a DLQ (`ack:on_error>dlq`) or an explicit 502 (`up:on_error>fail`); `rate_limit:on_error` to a 429; `auth:on_error` to a 401; `envelope:on_error` to a 400. `http_trigger`, `read_body`, and `no_auth` have no error port. Also shows `http_request` `retry` combined with an `on_error` path. See `docs/configuration.md` § Flows. |
| `xml.yaml` | XML reference: `body_format: xml`, a filter on an element path, a `code` node reading the DOM, `xml_prune`, and `body: transformed` forwarding. See `docs/configuration.md` § XML bodies. |
| `multipart.yaml` | Multipart reference: `body_format: multipart` with limits, a filter on `files.document.content_type`, and a `code` node reading upload metadata. See `docs/configuration.md` § Multipart bodies. |
| `multipart-split.yaml` | Multipart fan-out with `publish.payload: object`: decoded file bytes to S3, form fields (+ file metadata) as a JSON envelope to RabbitMQ. |
| `multipart-s3-lambda.yaml` | Same fan-out pattern with Lambda instead of RabbitMQ: file bytes to S3 (`payload: object`), Event invoke of a worker with fields + file metadata (no base64). |

## Destination reference configs

Each file showcases one destination type with all available options documented via comments.

| File | Description |
|------|-------------|
| `destinations/rabbitmq.yaml` | Full RabbitMQ option surface: exchange, dead-letter exchange/queue, queue_defaults, TLS, publisher_channels, and a flow that fans out to two publish nodes. |
| `destinations/sqs.yaml` | AWS SQS destination: two standard queues. `fifo` / `sqs_options` are not publish-node fields. |
| `destinations/sns.yaml` | AWS SNS destination: single topic auto-created at startup. |
| `destinations/lambda.yaml` | AWS Lambda destination: Event (default) or RequestResponse invoke; `target` is the function name / ARN. `create: true` is rejected. For file uploads, see `multipart-s3-lambda.yaml` (S3 object + Lambda notify). |
| `destinations/s3.yaml` | AWS S3 destination: one JSON Message per event under a date-partitioned key prefix, with SSE options and auto-bucket creation. `publish.payload: object` stores decoded file bytes instead (see `multipart-split.yaml`). |
| `destinations/redis_stream.yaml` | Redis / Valkey stream destination: single-node mode; annotated comments for cluster, sentinel, and TLS topology. |
| `destinations/redis_list.yaml` | Redis / Valkey list destination: LPUSH work-queue. `redis_options.max_len` (LTRIM) is not a publish-node field. |
| `destinations/redis_pubsub.yaml` | Redis / Valkey Pub/Sub destination: fire-and-forget channel broadcast; no `redis_options` block (all fields invalid for pubsub). Lossy — subscribers offline at publish time miss the message. |
| `destinations/redis_kv.yaml` | Redis / Valkey key-value destination: SET at the default `{prefix}:{endpoint}:{id}` key; YAML does not set TTL. `publish.payload: object` stores decoded file bytes instead of the JSON Message. |
| `destinations/postgres.yaml` | Postgres destination: one row per event into a shared table with fixed envelope columns + jsonb (headers/query/body/auth); `target` stored as a column; CREATE TABLE IF NOT EXISTS at startup; id dedup via ON CONFLICT DO NOTHING. |
| `destinations/mongodb.yaml` | MongoDB destination: one BSON document per event into a collection named by the publish `target`; full event envelope with `_id` = event id; nested body/headers/query/auth; id dedup via duplicate-key no-op; no DDL (collections auto-create). |
| `destinations/kafka.yaml` | Kafka destination: SASL/TLS, topic `create: true`, two publish nodes. `kafka_options.partition_key` is not a publish-node field. |
| `destinations/temporal.yaml` | Temporal destination: one workflow execution per event; `target` is the task queue. `temporal_options` are not publish-node fields. |
| `destinations/webhook.yaml` | Webhook destination: async outbound delivery (store-and-forward, 202=accepted) to an HTTP/HTTPS URL; envelope vs proxy payload modes; HMAC, API key, or no-auth outbound auth; retries with backoff on 429/5xx/timeout. |

## Auth strategy reference configs

Each file demonstrates one auth type with the exact fields required by that type.

| File | Description |
|------|-------------|
| `auth/hmac_sha256.yaml` | HMAC-SHA256 auth: scheme variants shown — `hmac_sha256_ts_colon` (Paddle) and `hmac_sha256_hex` (GitHub). |
| `auth/header_exact.yaml` | Header exact match auth: `exact` scheme (static Bearer token, `remove_bearer: true`). |
| `auth/jwt.yaml` | JWT auth: RS256 scheme with `expected_issuer`, `expected_audience`, and `body_hash_claim`. |
| `auth/no_auth.yaml` | No-auth passthrough: a `no_auth` node disables verification entirely. |

## Builder / showcase fixtures (`showcase/`)

These are the configs the web builder round-trips. `make validate-examples` skips `invalid-*`.

| File | Description |
|------|-------------|
| `showcase/github.yaml` | Minimal ingest flow used by serving tests. |
| `showcase/proxy.yaml` | Sync reverse proxy (`http_request` + `respond` passthrough). `{rest...}` paths must be quoted. |
| `showcase/production.yaml` | Showcase: switch, transform, code, `http_request` with `on_error`. |
| `showcase/invalid-gate-order.yaml` | Invalid: gate after `read_body` (must fail validate). |
| `showcase/invalid-many.yaml` | Invalid: several distinct problems reported together. |

## Templates (`examples/templates/`)

Each file here is both a runnable config and the source of a builder palette
template. `make validate-examples` validates them like any other example, which
is what stops a template from rotting when a node option changes.

The config UI inserts only the node ids listed in the
`TEMPLATES` manifest (`web/src/graph/templates.ts`). Notification templates
(`slack-notify`, `discord-notify`) insert the format-and-POST pair; the
trigger/auth prefix and trailing `respond` exist so the file is a valid
standalone config. Ingest templates (`stripe-ingest`, `paddle-ingest`) insert
the verified prefix (trigger, loop/rate guards, timestamped HMAC via
`secret_ref`, replay guard, envelope); the trailing `publish` is the wrapper.
Drop an ingest template onto a new flow, then wire envelope to your
destination. `xml-ingest` is the exception to the front/back split: it inserts
the whole pipeline, because `transform` `kind: xml_prune` only validates behind
an `envelope` with `body_format: xml`, so a back half dropped after a JSON
ingest template would not validate. When you rename a node in one of these
files, update the manifest; `web/src/graph/templates.test.ts` fails if the two
disagree.

Every template file is a production-shaped config: secrets live in the
environment (`secret_ref` with rotation overlap, webhook URLs as `${ENV}`),
inbound traffic is authenticated (never `no_auth`), `/metrics` is on a
private port behind `security.api_key`, and envelopes only forward
allowlisted headers.

| File | Pattern |
| --- | --- |
| `stripe-ingest.yaml` | Stripe webhook ingest: loop/rate guards, `hmac_sha256_ts_dot` on `Stripe-Signature` via `secret_ref`, replay guard, JSON envelope. Wrapper publish HMAC-signs the next hop. |
| `paddle-ingest.yaml` | Paddle Billing ingest: same shape with `hmac_sha256_ts_colon` on `Paddle-Signature`. |
| `slack-notify.yaml` | Format an event and POST it to a Slack incoming webhook. Wrapper still HMAC-authenticates the inbound caller. |
| `discord-notify.yaml` | Format an event and POST it to a Discord webhook. Same inbound auth as Slack. |
| `xml-ingest.yaml` | XML webhook ingest and forward: loop/rate guards, `hmac_sha256_hex` over the raw bytes via `secret_ref`, replay guard, `body_format: xml` envelope, a filter on an element path, a `code` node reading the element tree, `xml_prune`, and a `body: transformed` forward as `application/xml`. Inserts the whole pipeline. |

## Running validation

```bash
# Validate the default config
make validate

# Validate all example files (`hookway validate`, no server)
make validate-examples

# Boot every valid example, GET /healthz, and POST a signed request to each flow
make test-examples
```

`make test-examples` is what CI's Examples job runs after `validate-examples`. It uses a fake publisher (no Docker) and a loopback HTTP server for env-driven `http_request` URLs. `invalid-*` files must fail validation.

**The two targets do not have the same coverage rules.** `make test-examples` walks the `examples/` directory, so a new YAML anywhere under it is picked up automatically (except files named `replay.yaml`, which configure the replay service, not core). `make validate-examples` validates every top-level `*.yaml` (except `docker-compose.yaml`), globs `destinations/`, `auth/`, `templates/`, and `showcase/` (skipping `invalid-*`), and names the `dev/` and `replay/` files explicitly — each directory's `core.yaml` with `hookway validate`, its `replay.yaml` with `hookway-replay validate`.

So **a new subdirectory is silently skipped by `validate-examples`** until the Makefile learns about it; place new examples at the top level or in a globbed subdirectory, or add the directory to the recipe.

## Dev stack (`make dev-up`)

| File | Description |
|------|-------------|
| `docker-compose.yaml` | The `make dev-up` stack: core, RabbitMQ, a second HTTP destination (`notify-sink`), the replay service, PostgreSQL, and a one-shot `migrate`. Every variable has a working default. `make compose-e2e-test` deploys this file and drives it end to end. |
| `dev/core.yaml` | Core's config for the stack: the GitHub flow from `config.yaml`, fanned out to RabbitMQ and `notify-sink` under `skip_failed`, with the `replay:` block on. Stop `notify-sink` and the missed delivery waits in the replay service. |
| `dev/replay.yaml` | The replay service's config for the stack: database, core's control URL, retention, workers, and scoped keys. |

[docs/local-development.md](../docs/local-development.md) walks through sending a webhook, taking `notify-sink` down, and retrying what it missed.

## Replay service, two replicas each (`replay/`)

| File | Description |
|------|-------------|
| `replay/docker-compose.yaml` | Core ×2, the replay service ×2, PostgreSQL, a one-shot `migrate`, and two HTTP sinks (stop `audit-sink` for a partial failure that gets captured). `docker compose -f examples/replay/docker-compose.yaml up --build`. |
| `replay/core.yaml` | Core config with the `replay:` block: a `skip_failed` fan-out whose failed publishes are captured. |
| `replay/replay.yaml` | The replay service's config (`hookway-replay validate --config …`): database, core's control URL, retention, workers, and scoped keys. See `docs/replay-service.md`. |
