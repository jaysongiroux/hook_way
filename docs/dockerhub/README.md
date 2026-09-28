# Hookway

Hookway is a webhook intake service that forwards verified third-party events to RabbitMQ, AWS SQS, AWS SNS, AWS S3, AWS Lambda, Redis / Valkey, Postgres, MongoDB, Kafka, Temporal, or another HTTP webhook.

It is designed for production webhook ingress where the receiving service should validate signatures, normalize event envelopes (JSON, XML, multipart, or raw), and hand work off to consumers through a queue, topic, object store, or HTTP webhook. Configuration is YAML: `flows` of typed nodes wired by `edges`.

## Features

- Webhook verification: HMAC (SHA-256/SHA-1, multiple header layouts), static header match, and JWT Bearer tokens
- Body formats: JSON, XML element trees, multipart/form-data (`{fields, files}`), and raw bytes
- Destinations: RabbitMQ, AWS SQS, AWS SNS, AWS S3, Redis / Valkey, Postgres, MongoDB, Kafka, Temporal, and Webhook — with fan-out to multiple destinations per flow
  - AWS S3: one object per event (JSON Message by default). `publish.payload: object` stores decoded file bytes with vendor filename / Content-Type
  - Redis Streams (redis_stream): durable XADD; consumers may use their own groups. YAML does not set MAXLEN or create a consumer group
  - Redis Lists (redis_list): LPUSH work-queue (unbounded unless you trim from the consumer side)
  - Redis Pub/Sub (redis_pubsub): fire-and-forget channel broadcast via PUBLISH (lossy; not durable)
  - Redis Key-Value (redis_kv): SET at `{prefix}:{endpoint}:{id}` by default; YAML does not set a TTL. `publish.payload: object` stores decoded file bytes instead of the JSON Message
  - Postgres: one row per event into a shared table (fixed envelope columns + jsonb), id-deduplicated via ON CONFLICT DO NOTHING
  - MongoDB: one BSON document per event into a collection per target (full event envelope, _id-deduplicated)
  - Kafka: one message per event to a topic, with optional SASL/TLS and topic creation. YAML does not set a partition key
  - Temporal: one workflow execution per event; `target` is the task queue. YAML cannot set `workflow_type` / `workflow_id`
  - Webhook: async store-and-forward outbound delivery to an HTTP/HTTPS URL; none, API-key, HMAC-signed, or proxy (passthrough) auth; retries with backoff; graceful drain on shutdown
- AWS Lambda: invokes each configured function with the JSON event envelope; `Event` is the default invocation type and `RequestResponse` is also supported
- Optional auto-provisioning of queues/topics/streams, with a configurable policy for reconciling pre-existing SQS/SNS resources
- Optional replay service: captures acknowledged failed deliveries in PostgreSQL so an operator can inspect, retry, or dismiss the failed destination
- RabbitMQ optional dead-letter exchange and queue setup
- Request size limits, IP allowlists, replay protection, and rate limiting
- Health, readiness, and Prometheus metrics endpoints
- Small scratch-based runtime image

## Image Tags

- latest: latest stable GitHub release (a `vX.Y.Z` tag, not every commit on main)
- vX.Y.Z: semantic release tags, for example v1.0.0
- Pre-release tags such as vX.Y.Z-rc.1 are published but do not move latest

## Runtime

Hookway reads YAML configuration from a file path supplied by the HOOKWAY_CONFIG environment variable or the --config flag.

The core container entrypoint is /usr/local/bin/hookway. Published images contain
only the deployable runtime and do not require Node.js or the optional config
builder. The image includes a built-in healthcheck command, so the runtime image
does not need shell utilities.

The matching `<image>-replay` image runs the optional `hookway-replay` service. It uses PostgreSQL for captured failed deliveries; run `hookway-replay migrate --config replay.yaml` before starting or upgrading it. See the [replay service guide](https://github.com/jaysongiroux/hook_way/blob/main/docs/replay-service.md).

## Links

- Source: https://github.com/jaysongiroux/hook_way
- Configuration and local development details are in the project README.
