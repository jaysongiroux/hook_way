# Hookway

Hookway is a small webhook intake service for forwarding verified third-party webhook events to RabbitMQ.

It is designed for production webhook ingress where the receiving service should validate signatures, normalize event envelopes, and hand work off to consumers through a queue.

## Features

- Generic HMAC SHA-256 webhook verification
- Configurable endpoint paths and RabbitMQ routing keys
- RabbitMQ publishing with optional queue declaration
- Optional dead-letter exchange and queue setup
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
