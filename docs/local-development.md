# Local Development

## Start RabbitMQ and Hookway

```bash
export RABBITMQ_URL='amqp://guest:guest@rabbitmq:5672/'
export PADDLE_WEBHOOK_SECRET='change-me'
export GITHUB_WEBHOOK_SECRET='change-me'
make dev-up
```

RabbitMQ management UI:

```txt
http://localhost:15672
```

Credentials:

```txt
guest / guest
```

## Run Hookway Locally

Start RabbitMQ first, then:

```bash
export RABBITMQ_URL='amqp://guest:guest@localhost:5672/'
export PADDLE_WEBHOOK_SECRET='change-me'
export GITHUB_WEBHOOK_SECRET='change-me'
go run ./cmd/hookway validate --config examples/config.yaml
HOOKWAY_CONFIG=examples/config.yaml go run ./cmd/hookway
```

`make validate` runs the same config validation command against `examples/config.yaml`.

## Example Provider Destination

Create a provider notification destination pointing to:

```txt
http://localhost:8080/webhooks/paddle/billing
```

For local-only manual testing, generate the configured `signature_header` over the exact raw body with your configured secret. Paddle uses `Paddle-Signature`, but the Hookway auth mode is generic `hmac_sha256`. The signed payload is:

```txt
ts + ":" + raw_body
```

## Tests

```bash
make test
make test-race
make integration-test
make docker-build
```

`make integration-test` requires Docker because it uses testcontainers to run RabbitMQ.

## Container Probes

The Docker image healthcheck calls `GET /healthz` on `127.0.0.1:8080`. Orchestrators should use `GET /readyz` or `GET /ready` for readiness because it verifies RabbitMQ connectivity before routing webhook traffic.

## RabbitMQ Dead Letters

If local testing needs a dead-letter path, enable RabbitMQ `dead_letter` config so Hookway declares the DLX/DLQ and binds endpoint queues with dead-letter arguments. If your local RabbitMQ topology is managed outside Hookway, keep declaration disabled and create those resources yourself.
