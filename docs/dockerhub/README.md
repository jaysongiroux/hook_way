# Hookway

Hookway is a small webhook intake service for forwarding verified third-party webhook events to a message destination (RabbitMQ, AWS SQS, AWS SNS, AWS S3, Redis / Valkey, Postgres, MongoDB, Kafka, Temporal, or Webhook).

It is designed for production webhook ingress where the receiving service should validate signatures, normalize event envelopes, and hand work off to consumers through a queue or topic. Configuration is `schema_version: 2` YAML (`flows` of typed nodes), not `endpoints:`.

## Features

- Webhook verification: HMAC (SHA-256/SHA-1, multiple header layouts), static header match, and JWT Bearer tokens
- Destinations: RabbitMQ, AWS SQS, AWS SNS, AWS S3, Redis / Valkey, Postgres, MongoDB, Kafka, Temporal, and Webhook — with fan-out to multiple destinations per flow
  - Redis Streams (redis_stream): durable XADD; consumers may use their own groups. YAML does not set MAXLEN or create a consumer group
  - Redis Lists (redis_list): LPUSH work-queue (unbounded unless you trim from the consumer side)
  - Redis Pub/Sub (redis_pubsub): fire-and-forget channel broadcast via PUBLISH (lossy; not durable)
  - Redis Key-Value (redis_kv): SET at `{prefix}:{flow}:{id}` by default; YAML does not set a TTL
  - Postgres: one row per event into a shared table (fixed envelope columns + jsonb), id-deduplicated via ON CONFLICT DO NOTHING
  - MongoDB: one BSON document per event into a collection per target (full event envelope, _id-deduplicated)
  - Kafka: one message per event to a topic, with optional SASL/TLS and topic creation. YAML does not set a partition key
  - Temporal: one workflow execution per event; `target` is the task queue. YAML cannot set `workflow_type` / `workflow_id`
  - Webhook: async store-and-forward outbound delivery to an HTTP/HTTPS URL; none, API-key, HMAC-signed, or proxy (passthrough) auth; retries with backoff; graceful drain on shutdown
- Optional auto-provisioning of queues/topics/streams, with a configurable policy for reconciling pre-existing SQS/SNS resources
- RabbitMQ optional dead-letter exchange and queue setup
- Request size limits, IP allowlists, replay protection, and rate limiting
- Health, readiness, and Prometheus metrics endpoints
- Small scratch-based runtime image

## Image Tags

- latest: latest build from the default branch
- vX.Y.Z: semantic release tags, for example v1.0.0

## Runtime

Hookway reads YAML configuration from a file path supplied by the HOOKWAY_CONFIG environment variable or the --config flag.

The container entrypoint is /usr/local/bin/hookway. The image includes a built-in healthcheck command, so the runtime image does not need shell utilities.

## Links

- Source: https://github.com/jaysongiroux/hook_way
- Configuration and local development details are in the project README.
