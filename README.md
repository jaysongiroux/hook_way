# Hookway

Hookway receives third-party webhook POST requests, verifies authentication (`hmac_sha256` signatures, `header_exact` static headers, or `jwt` Bearer tokens), and publishes event envelopes to RabbitMQ.

## Quick Start

```bash
export RABBITMQ_URL='amqp://guest:guest@localhost:5672/'
export PADDLE_WEBHOOK_SECRET='your_paddle_endpoint_secret'
export GITHUB_WEBHOOK_SECRET='your_github_endpoint_secret'
export REVENUECAT_WEBHOOK_AUTH='your_revenue_cat_webhook_secret'

make dev-up
```

Validate config before starting:

```bash
go run ./cmd/hookway validate --config examples/config.yaml
make validate
```

Example Paddle-style webhook URL:

```txt
http://localhost:8080/webhooks/paddle
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

- Configured endpoint paths, such as `POST /webhooks/paddle`, accept webhook events.
- `GET /healthz` confirms the process is alive.
- `GET /ready` checks every configured message queue destination used by enabled endpoints, including fan-out destinations.
- `GET /readyz` checks every configured message queue destination used by enabled endpoints, including fan-out destinations.
- `GET /health` reports the same readiness state as `/readyz`.
- `GET /metrics` exposes Prometheus metrics when enabled.

Successful webhook response:

```json
{
  "status": "accepted",
  "endpoint": "paddle_billing",
  "destinations": [
    {"destination_queue_id": "primary", "queue": "paddle-webhooks"},
    {"destination_queue_id": "audit", "queue": "paddle-audit-webhooks"}
  ],
  "event_id": "018f...",
  "request_id": "req_..."
}
```

## Consuming Messages

Hookway publishes JSON envelopes to each endpoint `destinations` route. Every route names a `destination_queue_id` under `message_queues` and its own RabbitMQ `queue` routing key. With `declare_queues: true`, route queues are declared and bound automatically.

Queue backends are configured under top-level `message_queues`, keyed by destination id. MVP supports `type: rabbitmq` for each entry. SQS is the likely next backend. BullMQ may come later if worker ecosystems need Redis-native delivery.

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

On non-PR runs, the release workflow publishes the Docker image to Docker Hub. In GitHub, set Actions repository secrets `DOCKERHUB_USERNAME`, `DOCKERHUB_NAMESPACE`, `DOCKERHUB_IMAGE_NAME`, and `DOCKERHUB_TOKEN`. `DOCKERHUB_USERNAME` is the Docker Hub account that owns the access token; `DOCKERHUB_NAMESPACE` is the user or organization that owns the image repository. Pushes to `main` publish `latest`; Git tags such as `v1.0.0` publish matching Docker tags. The workflow also syncs `docs/dockerhub/README.md` to the Docker Hub repository description. Docker Hub description sync requires `DOCKERHUB_TOKEN` to have read/write/delete scope and admin access to the Docker Hub repository.

See:

- [CLI](docs/cli.md)
- [Configuration](docs/configuration.md)
- [Security](docs/security.md)
- [Local Development](docs/local-development.md)
