# Local Development

## Start RabbitMQ and Hookway

```bash
make dev-up
```

`examples/docker-compose.yaml` hardcodes the container's environment —
`RABBITMQ_URL: amqp://guest:guest@rabbitmq:5672/` and
`GITHUB_WEBHOOK_SECRET: change-me` — with no `${VAR}` passthrough, so
exporting either variable in your shell first has no effect. The dev signing
secret is `change-me`; sign test requests with it or you get a bare `401`.
To change it, edit the compose file.

Note `rabbitmq` (the compose service name) as the broker host, not
`localhost` — `localhost` is only correct for the host-run case below.

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
export GITHUB_WEBHOOK_SECRET='change-me'
go run ./cmd/hookway validate --config examples/config.yaml
HOOKWAY_CONFIG=examples/config.yaml go run ./cmd/hookway
```

`make validate` runs the same config validation command against `examples/config.yaml`.

## Example Provider Destination

Create a provider notification destination pointing to:

```txt
http://localhost:8080/webhooks/github
```

`examples/config.yaml` uses `scheme: hmac_sha256_hex` on `X-Hub-Signature-256` (`sha256=<hex>` over the raw body). Other schemes live under `examples/auth/` — Paddle-style timestamped HMAC is `hmac_sha256_ts_colon` (`ts + ":" + raw_body`).

For `exact` (`header_exact`) flows, set `remove_bearer: true` on the `auth` node when the provider sends `Authorization: Bearer <token>` but your secret holds only `<token>`. Otherwise configure `secret` to match the full header value exactly.

## Tests

```bash
make unit
make race
make integration-test
make docker-build
```

`make integration-test` requires Docker because it uses testcontainers to run RabbitMQ.

## Container Probes

The Docker image healthcheck calls `GET /healthz` on `127.0.0.1:8080`. Orchestrators should use `GET /readyz` or `GET /ready` for readiness: those probes unique destinations referenced by `publish` nodes (for this example, RabbitMQ) and report per flow.

## RabbitMQ Dead Letters

If local testing needs a dead-letter path, enable RabbitMQ `dead_letter` config so Hookway declares the DLX/DLQ and binds declared queues with dead-letter arguments. If your local RabbitMQ topology is managed outside Hookway, keep declaration disabled and create those resources yourself.
