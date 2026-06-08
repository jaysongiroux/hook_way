# Hookway

Hookway receives third-party webhook POST requests, verifies generic `hmac_sha256` signatures, and publishes event envelopes to RabbitMQ. MVP scope is intentionally small: YAML config, RabbitMQ, optional Prometheus metrics, request limits, rate limiting, and tests. Paddle is one example provider, not a hard-coded auth mode.

## Quick Start

```bash
export RABBITMQ_URL='amqp://guest:guest@localhost:5672/'
export PADDLE_WEBHOOK_SECRET='your_paddle_endpoint_secret'
export GITHUB_WEBHOOK_SECRET='your_github_endpoint_secret'
make dev-up
```

Validate config before starting:

```bash
go run ./cmd/hookway validate --config examples/config.yaml
make validate
```

Example Paddle-style webhook URL:

```txt
http://localhost:8080/webhooks/paddle/billing
```

Run locally without Docker:

```bash
HOOKWAY_CONFIG=examples/config.yaml go run ./cmd/hookway
```

Or pass a flag:

```bash
go run ./cmd/hookway --config examples/config.yaml
```

## HTTP Endpoints

- Configured endpoint paths, such as `POST /webhooks/paddle/billing`, accept webhook events.
- `GET /healthz` confirms the process is alive.
- `GET /readyz` confirms RabbitMQ is reachable.
- `GET /health` reports the same readiness state as `/readyz`.
- `GET /metrics` exposes Prometheus metrics when enabled.

Successful webhook response:

```json
{
  "status": "accepted",
  "endpoint": "paddle_billing",
  "queue": "billing-events",
  "event_id": "018f..."
}
```

## Consuming Messages

Hookway publishes JSON envelopes to the configured direct exchange using each endpoint's `queue` as routing key. With `declare_queues: true`, endpoint queues are declared and bound automatically.

The queue backend is configured under `message_queue`; MVP supports `type: rabbitmq`. SQS is the likely next backend. BullMQ may come later if worker ecosystems need Redis-native delivery.

Dead-letter exchange/queue declaration is optional. Use RabbitMQ `dead_letter` config when Hookway should create the DLX/DLQ and bind endpoint queues with `x-dead-letter-*` arguments; leave it disabled when infrastructure is owned elsewhere.

Published envelope compatibility is governed by `schema_version`: consumers may rely on additive changes within the same major version, and breaking field removals or meaning changes require a new schema version.

RabbitMQ management UI is available at `http://localhost:15672` in the example compose file. Default credentials are `guest` / `guest`.

## Development

```bash
make test
make test-race
make integration-test
make lint
make docker-build
make vuln-scan
```

`make integration-test` uses testcontainers and requires Docker.

Release checks run config validation, unit tests, race tests, RabbitMQ integration tests, vet, Docker build, and image vulnerability scanning in GitHub Actions.

On non-PR runs, the release workflow publishes the Docker image to Docker Hub. In GitHub, set Actions repository secrets `DOCKERHUB_NAMESPACE`, `DOCKERHUB_IMAGE_NAME`, and `DOCKERHUB_TOKEN`. `DOCKERHUB_TOKEN` should be a Docker Hub access token. Push Git tags such as `v1.0.0` to publish matching Docker tags, and the workflow syncs this README to the Docker Hub repository description.

See:

- [Configuration](docs/configuration.md)
- [Security](docs/security.md)
- [Local Development](docs/local-development.md)
