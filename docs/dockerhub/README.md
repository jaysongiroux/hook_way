# Hookway

Hookway is a small webhook intake service for forwarding verified third-party webhook events to a message destination (RabbitMQ, AWS SQS, AWS SNS, AWS S3, or Redis / Valkey).

It is designed for production webhook ingress where the receiving service should validate signatures, normalize event envelopes, and hand work off to consumers through a queue or topic.

## Features

- Webhook verification: HMAC (SHA-256/SHA-1, multiple header layouts), static header match, and JWT Bearer tokens
- Destinations: RabbitMQ, AWS SQS, AWS SNS, AWS S3, and Redis / Valkey — with fan-out to multiple destinations per endpoint
  - Redis Streams (redis_stream): durable, replayable, consumer-group fan-out via XADD
  - Redis Lists (redis_list): LPUSH work-queue with optional LTRIM cap
  - Redis Pub/Sub (redis_pubsub): fire-and-forget channel broadcast via PUBLISH (lossy; not durable)
  - Redis Key-Value (redis_kv): SET with TTL for latest-event caching and idempotency stores
- Optional auto-provisioning of queues/topics/streams, with a configurable policy for reconciling pre-existing resources
- SQS/SNS FIFO support (message group id and deduplication)
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
