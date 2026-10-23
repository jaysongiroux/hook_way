# Examples

This appendix links to every YAML example in this directory, grouped by concern.
Core YAML files are complete configurations checked by `hookway validate`.
`replay.yaml` files configure the optional replay service; `docker-compose.yaml`
files start the local demonstrations. Runtime configs stay beside their Compose
examples under this directory. File headers explain each demonstration;
`showcase/invalid-*` files intentionally fail validation.

## Starting configurations

| File | Description |
|------|-------------|
| [config.yaml](config.yaml) | Minimal config: one RabbitMQ destination and a signed GitHub flow (`/webhooks/github`). Default for `make validate` and `make run`, without replay. |
| [sample.yaml](sample.yaml) | Keep a configurable random fraction of events with a Sample node. Sampling drops that branch, not just inspection records. |
| [inspect.yaml](inspect.yaml) | Sample → Inspect on an independent branch, alongside RabbitMQ Publish for every request; uses the local replay stack. See [run instructions](../docs/operations/replay-console.md#view-only-inspections). |
| [filters.yaml](filters.yaml) | Filtering reference: a filter node after envelope with a silent drop, a branch-level filter that fans out selectively, and `on_no_match: reject` (422) using numeric, regex, and `exists` operators. See `docs/reference/configuration.md` § Filtering. |
| [adapters/named-events.yaml](adapters/named-events.yaml) | Authenticated issue webhook, filtering/projection, named event validation and synchronous JSON webhook delivery. See [Named events](../docs/guides/named-events.md). |
| [json-validation.yaml](json-validation.yaml) | Validate required fields, numeric IDs and email formats with local JSON Schema references. Optional explicit conversion; malformed input returns 400. See [Validate JSON](../docs/guides/json-validation.md). |
| [variables.yaml](variables.yaml) | Dynamic routing reference: an environment comes from a path placeholder or filtered header, `field_map` copies a JSON body field to `vars.tenant`, and `{name}` placeholders select a pre-provisioned SQS queue and webhook URL. Unknown environments and unresolved templates fail closed. See `docs/reference/configuration.md` § Variables. |
| [on_error.yaml](on_error.yaml) | Error-port reference: `http_request` 5xx to a DLQ (`ack:on_error>dlq`) or an explicit 502 (`up:on_error>fail`); `rate_limit:on_error` to a 429; `auth:on_error` to a 401; `envelope:on_error` to a 400. `http_trigger`, `read_body`, and `no_auth` have no error port. Also shows `http_request` `retry` combined with an `on_error` path. See `docs/reference/configuration.md` § Flows. |
| [xml.yaml](xml.yaml) | XML reference: `body_format: xml`, a filter on an element path, a `code` node reading the DOM, `xml_prune`, and `body: transformed` forwarding. See `docs/reference/configuration.md` § XML bodies. |
| [multipart.yaml](multipart.yaml) | Multipart reference: `body_format: multipart` with limits, a filter on `files.document.content_type`, and a `code` node reading upload metadata. See `docs/reference/configuration.md` § Multipart bodies. |
| [multipart-split.yaml](multipart-split.yaml) | Multipart fan-out with `publish.payload: object`: decoded file bytes to S3, form fields (+ file metadata) as a JSON envelope to RabbitMQ. |
| [multipart-s3-lambda.yaml](multipart-s3-lambda.yaml) | Same fan-out pattern with Lambda instead of RabbitMQ: file bytes to S3 (`payload: object`), Event invoke of a worker with fields + file metadata (no base64). |

## Shared connections and reliable ingestion

| File | Description |
| --- | --- |
| [connections.yaml](connections.yaml) | Reuse named AWS and PostgreSQL connections across destinations; keep credentials and connection settings in one place. |
| [launchdarkly.yaml](launchdarkly.yaml) | Tenant targeting with boolean, string, numeric and JSON flags, explicit fallbacks and conditional responses. See [feature flags](../docs/guides/feature-flags.md). |
| [reliable-ingestion.yaml](reliable-ingestion.yaml) | Signed GitHub webhook intake with a 202 response after RabbitMQ publisher confirmation. Preserve stable provider identity; broker durability and atomic consumer deduplication remain deployment responsibilities. See [delivery and recovery contracts](../docs/operations/delivery-guarantees.md). |

## Destination reference configs

Each file showcases one destination type with all available options documented via comments.

| File | Description |
|------|-------------|
| [destinations/rabbitmq.yaml](destinations/rabbitmq.yaml) | Full RabbitMQ option surface: exchange, dead-letter exchange/queue, queue_defaults, TLS, publisher_channels, and a flow that fans out to two publish nodes. |
| [destinations/sqs.yaml](destinations/sqs.yaml) | Two standard SQS queues, per-message delay, fair-queue grouping and provisioned queue attributes. |
| [destinations/fifo-sqs.yaml](destinations/fifo-sqs.yaml) | SQS FIFO publication using original JSON representation, digest-based identities, bounded retries and explicit responses. |
| [destinations/sns.yaml](destinations/sns.yaml) | AWS SNS destination: single topic auto-created at startup. |
| [destinations/lambda.yaml](destinations/lambda.yaml) | AWS Lambda destination: Event (default) or RequestResponse invoke; `target` is the function name / ARN. `create: true` is rejected. For file uploads, see `multipart-s3-lambda.yaml` (S3 object + Lambda notify). |
| [destinations/s3.yaml](destinations/s3.yaml) | AWS S3 destination: one JSON Message per event under a date-partitioned key prefix, with SSE options and auto-bucket creation. `publish.payload: object` stores decoded file bytes instead (see `multipart-split.yaml`). |
| [destinations/redis_stream.yaml](destinations/redis_stream.yaml) | Redis / Valkey stream destination: single-node mode; annotated comments for cluster, sentinel, and TLS topology. |
| [destinations/redis_list.yaml](destinations/redis_list.yaml) | Redis / Valkey list destination: LPUSH work-queue. `redis_options.max_len` (LTRIM) is not a publish-node field. |
| [destinations/redis_pubsub.yaml](destinations/redis_pubsub.yaml) | Redis / Valkey Pub/Sub destination: fire-and-forget channel broadcast; no `redis_options` block (all fields invalid for pubsub). Lossy — subscribers offline at publish time miss the message. |
| [destinations/redis_kv.yaml](destinations/redis_kv.yaml) | Redis / Valkey key-value destination: SET at the default `{prefix}:{endpoint}:{id}` key; YAML does not set TTL. `publish.payload: object` stores decoded file bytes instead of the JSON Message. |
| [destinations/postgres.yaml](destinations/postgres.yaml) | Postgres destination: one row per event into a shared table with fixed envelope columns + jsonb (headers/query/body/auth); `target` stored as a column; CREATE TABLE IF NOT EXISTS at startup; id dedup via ON CONFLICT DO NOTHING. |
| [destinations/mongodb.yaml](destinations/mongodb.yaml) | MongoDB destination: one BSON document per event into a collection named by the publish `target`; full event envelope with `_id` = event id; nested body/headers/query/auth; id dedup via duplicate-key no-op; no DDL (collections auto-create). |
| [destinations/kafka.yaml](destinations/kafka.yaml) | Kafka destination: SASL/TLS, topic `create: true`, two publish nodes. `kafka_options.partition_key` is not a publish-node field. |
| [destinations/temporal.yaml](destinations/temporal.yaml) | Temporal destination: one workflow execution per event; `target` is the task queue. `temporal_options` are not publish-node fields. |
| [destinations/webhook.yaml](destinations/webhook.yaml) | Webhook destination: explicitly loss-tolerant async delivery (202 means bounded volatile-queue admission), or synchronous HTTP delivery; envelope vs proxy payload modes; outbound auth and bounded retries. Neither mode provides exactly-once business processing. |

## MCP gateways

See the [MCP gateway guide](../docs/guides/mcp-gateway.md) for deployment and
[OAuth setup](../docs/guides/mcp-gateway.md#oauth-configuration).
Gateway caller authentication is separate from outbound API credentials.

| File | Description |
| --- | --- |
| [templates/mcp-gateway.yaml](templates/mcp-gateway.yaml) | API-key-authenticated HTTP status tool and native greeting tool with independent upstream credentials. |
| [mcp-oauth.yaml](mcp-oauth.yaml) | External-provider OAuth with a dedicated resource audience, discovery scopes and an explicit tool permission. |
| [openapi/api.yaml](openapi/api.yaml), [settings](openapi/settings.yaml), [generated core](openapi/core.yaml) | Import a JSON API as authenticated read/write tools. See [OpenAPI import](../docs/guides/openapi-import.md). |
| [mcp-endpoints.yaml](mcp-endpoints.yaml) | Independent `/services/mcp` and `/utilities/mcp` endpoints with separate caller keys and catalogues. |

## Auth strategy reference configs

Each file demonstrates one auth type with the exact fields required by that type.

| File | Description |
|------|-------------|
| [auth/hmac_sha256.yaml](auth/hmac_sha256.yaml) | HMAC-SHA256 auth: scheme variants shown — `hmac_sha256_ts_colon` (Paddle) and `hmac_sha256_hex` (GitHub). |
| [auth/header_exact.yaml](auth/header_exact.yaml) | Header exact match auth: `exact` scheme (static Bearer token, `remove_bearer: true`). |
| [auth/jwt.yaml](auth/jwt.yaml) | JWT auth: RS256 scheme with `expected_issuer`, `expected_audience`, and `body_hash_claim`. |
| [auth/no_auth.yaml](auth/no_auth.yaml) | No-auth passthrough: a `no_auth` node disables verification entirely. |
| [auth/request-signatures.yaml](auth/request-signatures.yaml) | Timestamped separate-header HMAC, multiple-signature HMAC and P-256 ECDSA over explicit body representations. |

## Ingress adapters

These use neutral resource names and explicit admission. For the complete field
contracts and deployment boundaries, see [Ingress adapters](../docs/guides/ingress-adapters.md).

| File | What it demonstrates |
|---|---|
| [routing/shared.yaml](routing/shared.yaml) | Consistent DynamoDB lookup, environment admission and a resolved broker target. |
| [routing/admission.yaml](routing/admission.yaml) | Shared and dedicated routing profiles that reject unadmitted environments. |
| [routing/registration.yaml](routing/registration.yaml) | Register a route after a successful upstream creation; keep the upstream response when registration needs recovery. |
| [proxy/admitted.yaml](proxy/admitted.yaml) | Relay to an admitted upstream with rate, loop and replay guards. |
| [proxy/request-contract.yaml](proxy/request-contract.yaml) | Byte-exact file relay with method/path/query/header forwarding and GET/HEAD network retries. |
| [credentials/static.yaml](credentials/static.yaml) | Origin-restricted upstream headers from secret references. |
| [credentials/oauth.yaml](credentials/oauth.yaml) | Cached OAuth client-credentials acquisition and one bounded invalid-session refresh. |
| [credentials/token-exchange.yaml](credentials/token-exchange.yaml) | Per-request token exchange before an XML relay. |
| [adapters/batch-shared.yaml](adapters/batch-shared.yaml) | Sequential child processing with per-child environment admission and independent outcomes. |
| [adapters/batch-dedicated.yaml](adapters/batch-dedicated.yaml) | Bounded concurrent children using a dedicated routing profile. |
| [adapters/changed-fields-sns.yaml](adapters/changed-fields-sns.yaml) | Changed-field projection and SNS attributes. |
| [adapters/event-filter.yaml](adapters/event-filter.yaml) | Accept allowed event types and acknowledge filtered events. |
| [adapters/json-projection.yaml](adapters/json-projection.yaml) | Validate a JSON schema, coerce an explicitly selected field type and strip document content. |
| [adapters/xml-ordered.yaml](adapters/xml-ordered.yaml) | Preserve ordered XML elements while pruning selected content. |

## Builder / showcase fixtures (`showcase/`)

These are the configs the web builder round-trips. `make validate-examples` skips `invalid-*`.

| File | Description |
|------|-------------|
| [showcase/github.yaml](showcase/github.yaml) | Minimal ingest flow used by serving tests. |
| [showcase/proxy.yaml](showcase/proxy.yaml) | Sync reverse proxy (`http_request` + `respond` passthrough). `{rest...}` paths must be quoted. |
| [showcase/production.yaml](showcase/production.yaml) | Showcase: Validate JSON, switch, transform, code, `http_request` with `on_error`. |
| [showcase/invalid-gate-order.yaml](showcase/invalid-gate-order.yaml) | Invalid: gate after `read_body` (must fail validate). |
| [showcase/invalid-many.yaml](showcase/invalid-many.yaml) | Invalid: several distinct problems reported together. |

## Templates (`examples/templates/`)

Each file is a runnable configuration and a template in the builder palette.
Notification templates insert the format-and-POST pair. Stripe/Paddle ingest
templates insert the verified prefix; connect its envelope to your destination.
The XML template inserts a complete XML pipeline.

Every template file is a production-shaped config: secrets live in the
environment (`secret_ref` with rotation overlap, webhook URLs as `${ENV}`),
inbound traffic is authenticated (never `no_auth`), `/metrics` is on a
private port behind `security.api_key`, and envelopes only forward
allowlisted headers.

| File | Pattern |
| --- | --- |
| [templates/stripe-ingest.yaml](templates/stripe-ingest.yaml) | Stripe webhook ingest: loop/rate guards, `hmac_sha256_ts_dot` on `Stripe-Signature` via `secret_ref`, replay guard, JSON envelope. Wrapper publish HMAC-signs the next hop. |
| [templates/paddle-ingest.yaml](templates/paddle-ingest.yaml) | Paddle Billing ingest: same shape with `hmac_sha256_ts_colon` on `Paddle-Signature`. |
| [templates/slack-notify.yaml](templates/slack-notify.yaml) | Format an event and POST it to a Slack incoming webhook. Wrapper still HMAC-authenticates the inbound caller. |
| [templates/discord-notify.yaml](templates/discord-notify.yaml) | Format an event and POST it to a Discord webhook. Same inbound auth as Slack. |
| [templates/xml-ingest.yaml](templates/xml-ingest.yaml) | XML webhook ingest and forward: loop/rate guards, `hmac_sha256_hex` over the raw bytes via `secret_ref`, replay guard, `body_format: xml` envelope, a filter on an element path, a `code` node reading the element tree, `xml_prune`, and a `body: transformed` forward as `application/xml`. Inserts the whole pipeline. |

## Running validation

```bash
# Validate the default config
make validate

# Validate all example files (`hookway validate`, no server)
make validate-examples

# Boot every valid example, GET /healthz, and POST a signed request to each flow
make test-examples
```

`make test-examples` boots configs with test publishers and loopback HTTP;
it does not contact your production destinations. `invalid-*` files are
intentional validation failures. When adding an example directory, add it to
`validate-examples` in the Makefile so CI checks it.

## Persistent production-style local stack (`production-local/`)

Use [production-local/docker-compose.yaml](production-local/docker-compose.yaml)
with `make prod-local-bootstrap` (`PROD_LOCAL_MODE=full` adds replay and its console;
`PROD_LOCAL_PROTECTION=1` selects Redis protection). Bootstrap generates private
credentials and copies ordinary YAML once, preserving later operator edits.
The supported workflow uses Make and Compose directly, with no test helper.
Ordinary shutdown preserves named volumes; explicit reset requires the selected
project name. Browser ports bind to loopback, while internal APIs and stores stay
on the Compose network. See [Local development](../docs/development/local-development.md#persistent-local-production-setup)
for validation, operating commands, URLs and architecture limits.

This runs the same application with local HTTP and private destinations.

| File | Components |
| --- | --- |
| [production-local/core.yaml](production-local/core.yaml) | Core and RabbitMQ with process-local rate limiting |
| [production-local/core-shared.yaml](production-local/core-shared.yaml) | Core and RabbitMQ with shared Redis rate limiting |
| [production-local/core-replay.yaml](production-local/core-replay.yaml) | Replay/console with process-local rate limiting |
| [production-local/core-replay-shared.yaml](production-local/core-replay-shared.yaml) | Replay/console with shared Redis rate limiting |
| [production-local/replay.yaml](production-local/replay.yaml) | Replay configuration with distinct scoped credentials |
| [production-local/migration/replay.yaml](production-local/migration/replay.yaml) | Owner-only migration configuration; never serve it |
| [production-local/database/provision.sql](production-local/database/provision.sql) | Provision owner/runtime roles and preserve current-schema data |
| [production-local/database/runtime-grants.sql](production-local/database/runtime-grants.sql) | Explicit runtime grants applied after migrations |
| [production-local/tls/docker-compose.yaml](production-local/tls/docker-compose.yaml) | Optional localhost TLS proxy overlay |
| [production-local/tls/Caddyfile](production-local/tls/Caddyfile) | Manual local certificate termination for core, replay and console |
| [production-local/monitoring/README.md](production-local/monitoring/README.md) | Optional authenticated local monitoring recipe; no cloud or live responder required |
| [production-local/monitoring/compose.yaml](production-local/monitoring/compose.yaml) | Loopback Prometheus/Alertmanager and local fixture receiver overlay |
| [production-local/monitoring/prometheus.yaml](production-local/monitoring/prometheus.yaml) | Private core/replay scrape jobs and shipped alert rules |
| [production-local/monitoring/alertmanager.yaml](production-local/monitoring/alertmanager.yaml) | Local-only firing/resolved webhook route, not a real paging service |

## Disposable dev stack (`make dev-up`)

| File | Description |
|------|-------------|
| [docker-compose.yaml](docker-compose.yaml) | The `make dev-up` stack: core, RabbitMQ, a second HTTP destination (`notify-sink`), the replay service, PostgreSQL, and a one-shot `migrate`. Every variable has a working default. `make compose-e2e-test` deploys this file and drives it end to end. |
| [dev/core.yaml](dev/core.yaml) | Core's config for the stack: the GitHub flow from `config.yaml`, fanned out to RabbitMQ and `notify-sink` under `skip_failed`, with the `replay:` block on. Stop `notify-sink` and the missed delivery waits in the replay service. |
| [dev/replay.yaml](dev/replay.yaml) | The replay service's config for the stack: database, core's control URL, retention, workers, and scoped keys. |

[docs/development/local-development.md](../docs/development/local-development.md) walks through sending a webhook, taking `notify-sink` down, and retrying what it missed.

## Replay service, two replicas each (`replay/`)

| File | Description |
|------|-------------|
| [replay/docker-compose.yaml](replay/docker-compose.yaml) | Core ×2, the replay service ×2, PostgreSQL, a one-shot `migrate`, and two HTTP sinks (stop `audit-sink` for a partial failure that gets captured). `docker compose -f examples/replay/docker-compose.yaml up --build`. |
| [replay/core.yaml](replay/core.yaml) | Core config with the `replay:` block: a `skip_failed` fan-out whose failed publishes are captured. |
| [replay/replay.yaml](replay/replay.yaml) | The replay service's config (`hookway-replay validate --config …`): database, core's control URL, retention, workers, and scoped keys. See `docs/operations/replay-service.md`. |

The dev and replay Compose stacks build a separate `console` frontend using
`web/Dockerfile`. The dev console is at `http://localhost:8082`, with the public
builder at `/builder`; its API URL is `http://localhost:8081`. In the replicated
replay example the browser connects to the API replica at `http://localhost:8090`.
For balanced API traffic, supply an independently deployed browser-reachable API
load balancer URL through `REPLAY_API_URL`. Set `REPLAY_CONSOLE_ORIGIN` to the
frontend's exact origin. Console replicas and replay replicas scale independently;
fixed local port mappings are a convenience for this demo, not an autoscaling
front door. Production ALB/TLS/WAF configuration is deployer-owned.

## Provider ingress contracts

| File | Description |
| --- | --- |
| [providers/webhooks.yaml](providers/webhooks.yaml) | Signed GitHub, Stripe snapshot, Paddle Billing and Slack Events API flows, including Slack URL verification and provider-specific acknowledgements. |

See the [compatibility matrix](../docs/guides/provider-compatibility.md) for local
test evidence, consumer deduplication, recovery requirements and unsupported modes.

## Shared rate limits and duplicate protection

| File | Description |
| --- | --- |
| [protection/shared.yaml](protection/shared.yaml) | PostgreSQL/Redis stores, a pre-auth IP token bucket, a verified credential rolling quota and shared duplicate reservations. |

Set the referenced environment variables, choose the backend used by each node,
and migrate PostgreSQL before serving. Both declared stores are checked at startup,
so remove unused stores. See [Protection stores](../docs/guides/protection-stores.md).

## Multi-YAML configuration

[One YAML per flow](multi-yaml/README.md) keeps central settings in
[multi-yaml/core.yaml](multi-yaml/core.yaml) and references separate
[orders](multi-yaml/flows/orders.yaml) and [billing](multi-yaml/flows/billing.yaml)
flow bodies. Single-file configuration remains supported.

- [MCP event subscriptions](mcp-events.yaml): PostgreSQL-backed subscriptions, authenticated issue webhooks, and signed callbacks.
- [Signed HTTPS callback receiver](mcp-events/callback_receiver.py): standard-library test receiver using your TLS certificate.
