# Hookway

Hookway receives third-party webhook POST requests, verifies them before work begins, and sends a consistent event envelope to the queue, database, object store, workflow, Lambda, or webhook your consumers already use. It keeps webhook-specific concerns—signature checks, retries, rate limits, replay protection, multipart uploads, and destination fan-out—in one configured service.

Configuration is YAML: named `flows` of typed nodes (`http_trigger`, gates, `auth` / `no_auth`, `envelope`, `publish` / `respond`) wired by `edges`. There is no config version field: `hookway validate` rejects any unknown top-level key. See [Configuration](docs/configuration.md).

```mermaid
flowchart LR
  Sender[Webhook sender] --> Core[Hookway core]
  Builder[Optional config builder] -. creates .-> Config[core.yaml]
  Config -. configures .-> Core
  Core --> Destinations[Queues, stores, databases, Lambda, workflows, or HTTP webhooks]
  Core -. failed acknowledged deliveries .-> Replay[Optional replay service]
  Replay <--> Postgres[(PostgreSQL)]
  Replay -. retries the failed destination .-> Core
```

You do not need to host the builder to run Hookway. Write the YAML directly or
use the builder wherever convenient, then deploy core with that file. The
replay service is a separate optional deployment.

## Architecture: the typed-port node graph

A flow is a DAG, not a fixed pipeline. Every node declares the payload type it
consumes (its **in** port) and the one it produces (its **out** port), and
`edges` are type-checked when the config loads — so an illegal ordering is a
startup error, not a runtime surprise.

The port types are the phases of a request, in order, and each one strictly
contains the last:

| Port | Carries | Produced by |
|---|---|---|
| `PortNone` | nothing — the input side of a trigger | — |
| `PortRequestHead` | method, path, query, headers, source IP, hop count — deliberately **no body** | `http_trigger` |
| `PortRawRequest` | a `RequestHead` plus the raw, unverified body bytes | `read_body` |
| `PortVerified` | a `RawRequest` whose signature was checked against those exact bytes | `auth` / `no_auth` |
| `PortEnvelope` | a `Verified` payload plus the parsed body and resolved variables | `envelope`, and optionally a successful `publish` continuation |
| `PortTerminal` | a leaf: a response was written or a delivery completed, nothing flows on | `respond` (and a `publish` with no success edge) |

Two consequences do the real work:

- **Gates run before the body is buffered.** `loop_guard`, `ip_allowlist`, and
  `rate_limit` consume `RequestHead`, which has no body, so they *cannot* be
  wired after `read_body`. A flood is rejected without paying the buffering
  cost, by construction.
- **Auth bypass is unrepresentable.** The only producer of `Verified` is an
  `auth` or `no_auth` node, and every body-reading node consumes `Envelope`,
  which is reachable only through `Verified`. There is no edge that reaches a
  `publish` without passing an auth node.

`PortType.Accepts` is strict equality, deliberately: widening it to
assignability looks like a simplification and silently destroys those
guarantees, because `RawRequest` *contains* a `RequestHead`. Every leaf must
produce `PortTerminal` (or be a `publish` with no success edge), and exactly
one trigger (`In == PortNone`) is allowed per flow. A successful `publish` may
take one unlabeled success edge that runs after delivery on the same
`publish_timeout` budget.

The lattice lives in `internal/graph/port.go`; the compiler and executor are
the rest of `internal/graph`. The guarantees it buys, and the ones tests rather
than types are holding, are enumerated in
[Request-pipeline invariants](docs/invariants.md).

## Prerequisites

| Tool | Version | Needed for |
|---|---|---|
| Go | 1.26.8 or newer (`go.mod`; CI pins `1.26.x`, the image builds on `golang:1.26.8-alpine`) | building, testing, `go run ./cmd/hookway` |
| Node.js | 20 (`NODE_VERSION` in `.github/workflows/reusable-ci.yml`) | the config builder under `web/` |
| Docker | any recent version | `make dev-up`, `make docker-build`, and every integration suite |
| Python 3 | any | `make wasm-harness` only (it serves the harness with `python3 -m http.server`) |

## Quick Start

```bash
make dev-up
```

`make dev-up` brings up `examples/docker-compose.yaml`: Hookway, RabbitMQ, a
second HTTP destination, and the replay service with its PostgreSQL store.
Core runs `examples/dev/core.yaml`, the GitHub flow from `examples/config.yaml`
fanned out to both destinations. [Local Development](docs/local-development.md)
shows how to take one destination down and retry what it missed.

**The default signing secret is `change-me`.** The compose file defaults
`GITHUB_WEBHOOK_SECRET` to `change-me` and `RABBITMQ_URL` to
`amqp://guest:guest@rabbitmq:5672/`, so `make dev-up` needs no setup. Test
requests must be signed with whichever secret is in effect, or Hookway
fail-closes with a bare `401` that says nothing about why.

Sign and send one:

```bash
BODY='{"zen":"Keep it logically awesome."}'
SIG="sha256=$(printf '%s' "$BODY" | openssl dgst -sha256 -hmac 'change-me' -r | cut -d' ' -f1)"
curl -sS -X POST http://localhost:8080/webhooks/github \
  -H 'Content-Type: application/json' \
  -H "X-Hub-Signature-256: $SIG" \
  --data-binary "$BODY"
```

To use a different secret, export it before bringing the stack up — compose
reads it and falls back to the default only when it is unset:

```bash
export GITHUB_WEBHOOK_SECRET='your-own-secret'
make dev-up
```

Note the broker host: inside the compose network it is `rabbitmq`, not
`localhost`. `localhost:5672` is only correct when you run the binary on the
host — see [Local Development](docs/local-development.md).

Validate config before starting:

```bash
go run ./cmd/hookway validate --config examples/config.yaml
make validate
```

Add `--strict` for the CI-grade check: it promotes lint warnings (a flow with
no `rate_limit`, a `code` node with no explicit `timeout`, and the like) to
blocking errors, so a warning cannot ride into `main` unnoticed. It works on
both the text and `--json` output. See [CLI § hookway validate](docs/cli.md#hookway-validate).

```bash
go run ./cmd/hookway validate --config examples/config.yaml --strict
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

Hookway publishes from each flow `publish` node. The default `payload: envelope` writes a JSON Message. On `s3` and `redis_kv`, `payload: object` stores decoded file bytes instead; S3 also uses the supplied filename and Content-Type when present. See `examples/multipart-split.yaml` and [Configuration § Publish payload modes](docs/configuration.md#publish-payload-modes). Every publish names a `destination` entry and a `target` (queue name, topic name, or RabbitMQ routing key). With `create: true`, route queues or topics are declared automatically at startup.

Backends are configured under top-level `destinations`, keyed by id. Supported types: `rabbitmq`, `sqs`, `sns`, `s3`, `lambda`, `redis_stream`, `redis_list`, `redis_pubsub`, `redis_kv`, `postgres`, `mongodb`, `kafka`, `webhook`, and `temporal`. See `examples/destinations/` for reference configs for each type.

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

## Replay failed deliveries (optional)

A sender that got a 2xx will not send the event again. So when a destination
misses it afterwards (a `skip_failed` publish that failed, or an async webhook
that ran out of retries), the event is gone unless something kept it. The
replay service (`hookway-replay`) is that something: add a `replay:` block to
core's config and core hands each of those failures to it. From there you
list what failed and why, download the payload, and retry or dismiss it
through the replay API. A retry picks the flow up at the node that failed,
under the configuration you run today, so destinations that already got the
event are not sent it again.

`make dev-up` runs it by default. See [docs/replay-service.md](docs/replay-service.md)
for the API and operations, [docs/cli.md](docs/cli.md#hookway-replay) for its
commands, and [examples/replay](examples/replay) for a two-replica setup.

## Config builder

The web UI at `/builder` (served by `hookway editor` after `make ui`, or via
`make web-dev`) is a **config generator**. It exports config YAML
for you to validate and deploy; it does not manage a running instance.

The palette's **Templates** group drops in production-shaped patterns (Stripe and Paddle ingest with signature verification, loop/rate/replay guards, and rotating `secret_ref`; Slack and Discord notifications that POST to an env-held webhook URL) backed by validated configs in `examples/templates/`.

```bash
make web-install   # REQUIRED FIRST on a clean clone — installs web/node_modules
make node-schema   # regenerate web/src/graph/{node,destination}Schema.json
make web-dev       # Vite app on the local web port
go run ./cmd/hookway editor --addr :8090
```

Run `make web-install` once before a web command on a clean clone. `make
web-dev`, `make ui`, and `make web-e2e` build the generated WebAssembly
validator first; Vite then serves it from `web/public/` for in-browser
validation. Run `make wasm` directly only when working on the validator or its
standalone harness. See [CLI § hookway-wasm](docs/cli.md#hookway-wasm).

See [CLI § hookway editor](docs/cli.md#hookway-editor).

## Development

```bash
make test                  # unit tests only (no Docker)
make test-race             # unit tests with the race detector
make test-examples         # boot every shipped example and POST a signed request
make validate-examples     # `hookway validate` the enumerated example configs
make integration-test      # RabbitMQ integration tests (testcontainers)
make aws-integration-test  # SQS/SNS integration tests (LocalStack)
make redis-integration-test # Redis/Valkey integration tests (testcontainers)
make postgres-integration-test
make mongodb-integration-test
make kafka-integration-test
make webhook-integration-test
make temporal-integration-test
make replay-integration-test # replay store, API, workers, two replicas (PostgreSQL testcontainer)
make replay-acceptance-test  # the real hookway + hookway-replay binaries, two replicas each
make compose-e2e-test        # deploy the make dev-up stack in Docker; record and replay a missed delivery
make bench-replay          # capture overhead and replay ingest benchmarks
make test-all              # go test ./... -count=1 — unit plus every container suite
make lint
make docker-build                         # builds the deployable core and replay images
make vuln-scan
```

`make test-all` is `go test ./... -count=1` with no `-short`, so it is not
"unit + RabbitMQ + AWS + Redis": it runs every container-backed suite —
RabbitMQ, AWS (LocalStack), Redis, Postgres, MongoDB, Kafka, Webhook,
and Temporal. Those suites self-skip when Docker is
unavailable.

`make test-examples` walks `examples/` and boots each valid flow. `make
validate-examples` discovers top-level YAML files (except the Compose file),
then validates the `destinations/`, `auth/`, `templates/`, and valid
`showcase/` files by glob. It also checks the named `core.yaml` and
`replay.yaml` files under `dev/` and `replay/`.

Every `*-integration-test` target above and `make test-all` use testcontainers
and require Docker. `make docker-build` builds the core and replay runtime
images without Node.js or the optional builder bundle.

## CI/CD

GitHub Actions is split across **four** workflow files. Three are entrypoints; the fourth holds the actual gate.

- **Reusable CI** (`.github/workflows/reusable-ci.yml`) — the gate itself, called by both CI and Release via `workflow_call`. Its jobs run in three stages, and each stage starts only after every job in the one before has passed:
  1. **Lint** — **Formatting** (gofmt + `git diff --check`), **golangci-lint**, **govulncheck**, **Static checks** (vet, `go mod tidy -diff`, deadcode), and **Web lint** (node-schema drift, knip).
  2. **Test** — **Go** (unit + invariant conformance), **Go race**, **Examples**, **Web** (vitest), **Coverage**, and **Observability** (Grafana dashboard tests + promtool on `deploy/alerts.yaml`).
  3. **Deploy** — **Web e2e** (Playwright), **Image** (docker build of the core and replay images, a smoke run, and Trivy on both), and an 11-way **integration** matrix: RabbitMQ, AWS, Redis, Postgres, MongoDB, Kafka, Webhook, Temporal, Replay, Replay acceptance, and **Compose end-to-end** (the default `examples/docker-compose.yaml` stack, driven with signed webhooks through a capture and a replay).

  A lint failure skips the tests, and a test failure skips the containers, so a typo costs a few minutes instead of a full integration run. **All checks passed** is the single required status; it `needs:` every job and treats a skipped job as a failure.
- **CI** (`.github/workflows/ci.yml`) — pull requests and pushes to `main`. A thin caller of Reusable CI; nothing is published. The **Examples** job validates the enumerated configs under `examples/` and then boots each one, POSTing a signed request to every flow so the files we document cannot drift from what the server actually runs.
- **Release** (`.github/workflows/release.yml`) — pushing a `v*` tag (or dispatching the workflow with an existing tag). Re-runs Reusable CI and, only once it passes, publishes the core and replay Docker images, syncs the Docker Hub description from `docs/dockerhub/README.md`, and creates a GitHub Release with generated notes. Pushes to `main` do not publish.
- **Security** (`.github/workflows/security.yml`) — weekly, on demand, and when `go.mod` / Docker / lockfiles change on `main`. Runs `govulncheck` and a Trivy image scan uploaded to GitHub code scanning.

Cut a release from a commit already on `main`:

```bash
git tag v1.2.3
git push origin v1.2.3
```

Pre-release tags such as `v1.2.3-rc.1` create a GitHub pre-release and skip the Docker `latest` tag.

On Docker Hub, set Actions repository secrets `DOCKERHUB_USERNAME`, `DOCKERHUB_NAMESPACE`, `DOCKERHUB_IMAGE_NAME`, and `DOCKERHUB_TOKEN`. `DOCKERHUB_USERNAME` is the Docker Hub account that owns the access token; `DOCKERHUB_NAMESPACE` is the user or organization that owns the image repository. A `v1.0.0` tag publishes the matching Docker tag; non-prerelease tags also publish `latest`. The release job uses the GitHub Environment `release` (add required reviewers there if you want an approval gate). Docker Hub description sync requires `DOCKERHUB_TOKEN` to have read/write/delete scope and admin access to the Docker Hub repository.

Require the `Checks / All checks passed` status on `main` so a green PR is one check, not a dozen.

See:

- [Examples](examples/README.md)
- [CLI](docs/cli.md)
- [Configuration](docs/configuration.md)
- [Security](docs/security.md)
- [Request-pipeline invariants](docs/invariants.md)
- [Local Development](docs/local-development.md)
- [Design system](docs/design-system.md) — the `web/` palette, type, and chrome rules
