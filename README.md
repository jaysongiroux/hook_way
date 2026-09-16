# Hookway

Hookway receives third-party webhook POST requests, verifies authentication (`hmac_sha256` signatures, `header_exact` static headers, or `jwt` Bearer tokens), and publishes event envelopes to configured destinations (RabbitMQ, AWS SQS, AWS SNS, AWS S3, Redis / Valkey, Postgres, MongoDB, Kafka, Temporal workflows, or another HTTP webhook).

Config is `schema_version: 2`: named `flows` of typed nodes (`http_trigger`, gates, `auth` / `no_auth`, `envelope`, `publish` / `respond`). `hookway validate` rejects `endpoints:` and schema version 1. See [Configuration](docs/configuration.md).

## Quick Start

```bash
export RABBITMQ_URL='amqp://guest:guest@localhost:5672/'
export GITHUB_WEBHOOK_SECRET='your_github_endpoint_secret'

make dev-up
```

Validate config before starting:

```bash
go run ./cmd/hookway validate --config examples/config.yaml
make validate
```

Example webhook URL (`examples/config.yaml`):

```txt
http://localhost:8080/webhooks/github
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

- Configured flow paths, such as `POST /webhooks/github`, accept webhook events.
- `GET /healthz` confirms the process is alive.
- `GET /ready` reports per-flow readiness derived from unique destination probes. It returns 200 while at least one flow is ready (one down destination does not sideline unrelated flows). Set `server.readiness_require_all: true` for 503 if any flow is unready. Served from cache, not probed per request.
- `GET /readyz` reports the same cached readiness state as `/ready`.
- `GET /health` is a liveness alias for `/healthz` (it does NOT report readiness), so infrastructure treating `/health` as a liveness probe will not restart pods on transient destination outages.
- `GET /metrics` exposes Prometheus metrics when enabled.
- When `replay` is enabled, captured events can be listed, inspected, and re-delivered via a protected API on a separate `replay.addr` listener. See [Configuration § Replay](docs/configuration.md#replay) and [Security § Replay](docs/security.md#replay).

Successful webhook response:

```json
{
  "status": "accepted",
  "endpoint": "github_events",
  "destinations": [
    {"destination_queue_id": "primary", "queue": "github-webhooks"}
  ],
  "event_id": "018f...",
  "request_id": "req_..."
}
```

`endpoint` is the **flow name** (compatibility field). `destination_queue_id` is the `destinations:` map key; `queue` is the publish `target`. A `respond` node can replace this envelope with an explicit HTTP status and body (sync proxy).

Flows can set `delivery_failure_policy`. `stop_on_failure` is the default and publishes sequentially until one fails. `skip_failed` attempts every `publish` node and returns `202` when at least one destination succeeds, with `failed_destinations` listing any skipped failures.

## Consuming Messages

Hookway publishes JSON envelopes from each flow `publish` node. Every publish names a `destination` entry and a `target` (queue name, topic name, or RabbitMQ routing key). With `create: true`, route queues or topics are declared automatically at startup.

Backends are configured under top-level `destinations`, keyed by id. Supported types: `rabbitmq`, `sqs`, `sns`, `s3`, `redis_stream`, `redis_list`, `redis_pubsub`, `redis_kv`, `postgres`, `mongodb`, `kafka`, `webhook`, and `temporal`. See `examples/destinations/` for reference configs for each type.

Dead-letter exchange/queue declaration is optional. Use RabbitMQ `dead_letter` config when Hookway should create the DLX/DLQ and bind declared queues with `x-dead-letter-*` arguments; leave it disabled when infrastructure is owned elsewhere.

Published envelope compatibility is governed by `schema_version`: consumers may rely on additive changes within the same major version, and breaking field removals or meaning changes require a new schema version.

RabbitMQ management UI is available at `http://localhost:15672` in the example compose file. Default credentials are `guest` / `guest`.

### AWS Destination Drift Handling

AWS destinations (SQS and SNS) support the `on_existing_conflict` field to handle cases where a queue or topic already exists with attributes that differ from the configuration:

- `reconcile` (default): Hookway will edit the existing resource's mutable attributes to match the configuration. This requires `sqs:SetQueueAttributes` (for SQS) or `sns:SetTopicAttributes` (for SNS) IAM permissions.
- `warn`: Log the attribute difference and use the existing resource unchanged, without updating it.
- `error`: Fail startup if any configured attribute differs from the existing resource.

Immutable conflicts (e.g., FIFO vs standard queue type) always fail startup regardless of the `on_existing_conflict` setting.

**IAM Note:** The default `reconcile` mode requires additional IAM permissions (`sqs:SetQueueAttributes` or `sns:SetTopicAttributes`). If operating under least-privilege IAM policies that cannot grant these permissions, set `on_existing_conflict: warn` or `on_existing_conflict: error` instead.

## Config builder

The web UI at `/builder` (served by `hookway editor` after `make ui`, or via
`make web-dev`) is a **config generator**. It exports `schema_version: 2` YAML
for you to validate and deploy; it does not manage a running instance.

```bash
make node-schema   # regenerate web/src/graph/{node,destination}Schema.json
make web-dev       # Vite app on the local web port
go run ./cmd/hookway editor --addr :8090
```

See [CLI § Editor API](docs/cli.md#editor-api).

## Development

```bash
make test                  # unit tests only (no Docker)
make test-race             # unit tests with the race detector
make integration-test      # RabbitMQ integration tests (testcontainers)
make aws-integration-test  # SQS/SNS integration tests (LocalStack)
make redis-integration-test # Redis/Valkey integration tests (testcontainers)
make test-all              # everything: unit + RabbitMQ + AWS + Redis integration
make validate-examples     # validate every config under examples/
make lint
make docker-build
make vuln-scan
```

`make integration-test`, `make aws-integration-test`, `make redis-integration-test`, and `make test-all` use testcontainers and require Docker.

Release checks run example-config validation, unit tests, race tests, RabbitMQ, AWS (LocalStack), and Redis integration tests, vet, Docker build, and image vulnerability scanning in GitHub Actions.

On non-PR runs, the release workflow publishes the Docker image to Docker Hub. In GitHub, set Actions repository secrets `DOCKERHUB_USERNAME`, `DOCKERHUB_NAMESPACE`, `DOCKERHUB_IMAGE_NAME`, and `DOCKERHUB_TOKEN`. `DOCKERHUB_USERNAME` is the Docker Hub account that owns the access token; `DOCKERHUB_NAMESPACE` is the user or organization that owns the image repository. Pushes to `main` publish `latest`; Git tags such as `v1.0.0` publish matching Docker tags. The workflow also syncs `docs/dockerhub/README.md` to the Docker Hub repository description. Docker Hub description sync requires `DOCKERHUB_TOKEN` to have read/write/delete scope and admin access to the Docker Hub repository.

See:

- [Examples](examples/README.md)
- [CLI](docs/cli.md)
- [Configuration](docs/configuration.md)
- [Security](docs/security.md)
- [Local Development](docs/local-development.md)
