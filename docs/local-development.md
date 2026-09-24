# Local Development

## Start the dev stack

```bash
make dev-up
```

`examples/docker-compose.yaml` builds both images and runs:

| Service | What it is | From the host |
|---|---|---|
| `hookway` | Core, with the GitHub flow at `/webhooks/github` | http://localhost:8080 |
| `rabbitmq` | The main destination | AMQP on `localhost:5672`; management UI on http://localhost:15672 (`guest` / `guest`) |
| `notify-sink` | A second destination: a plain HTTP endpoint | not published |
| `replay` | The replay service's API | http://localhost:8081 |
| `postgres` | The replay service's store | not published |
| `migrate` | Applies the replay schema, then exits | — |

Core's config is `examples/dev/core.yaml`; the replay service's is
`examples/dev/replay.yaml`. The GitHub flow publishes each event to RabbitMQ
and posts it to `notify-sink`, under `delivery_failure_policy: skip_failed`:
if one destination fails, the other still delivers and GitHub still gets its
`202`. The delivery that failed goes to the replay service, where it waits
for you to retry it.

Every variable falls back to a working dev default, so `make dev-up` needs no
setup. Export your own before `make dev-up` to override one:

| Variable | Default | Used for |
|---|---|---|
| `GITHUB_WEBHOOK_SECRET` | `change-me` | Signing test webhooks. A wrong signature gets a bare `401`. |
| `RABBITMQ_URL` | `amqp://guest:guest@rabbitmq:5672/` | Core's broker. Keep the host `rabbitmq` (the compose service name): the URL is resolved inside the container, so `localhost` is wrong here. |
| `REPLAY_OPERATOR_TOKEN` | `dev-operator-token-0123456789abcde` | Your calls to the replay API. |
| `REPLAY_CAPTURE_TOKEN`, `REPLAY_CONTROL_TOKEN`, `REPLAY_METRICS_TOKEN` | `dev-…` (see the compose file) | Core and the replay service authenticating to each other, and Prometheus. |

The token defaults are for local use only.

`make dev-down` removes the stack, including RabbitMQ's and the replay store's
data: the next `make dev-up` starts empty.

## Send a signed webhook

```bash
body='{"action":"opened","issue":{"number":1}}'
sig=$(printf '%s' "$body" | openssl dgst -sha256 -hmac change-me -r | cut -d' ' -f1)
curl -i http://localhost:8080/webhooks/github \
  -H 'Content-Type: application/json' \
  -H "X-Hub-Signature-256: sha256=$sig" \
  -d "$body"
```

Expect `HTTP/1.1 202 Accepted`. The event is in the `github-webhooks` queue
(management UI → Queues).

## Catch a missed delivery and retry it

Stop the second destination:

```bash
docker compose -f examples/docker-compose.yaml stop notify-sink
```

Send another webhook as above, with a different body. GitHub still gets
`202`, because RabbitMQ took the event, so GitHub will not send it again. The
delivery to `notify-sink` failed, and core has handed it to the replay
service. List what is waiting:

```bash
export REPLAY_OPERATOR_TOKEN=dev-operator-token-0123456789abcde
auth="Authorization: Bearer $REPLAY_OPERATOR_TOKEN"
curl -s -H "$auth" 'http://localhost:8081/v1/failures?status=open'
```

One record comes back, with `node_id` and `destination_id` both `notify`, the
event id, and an `error_class`. Look closer:

```bash
curl -s -H "$auth" http://localhost:8081/v1/failures/<id>          # what failed, why, and when
curl -s -H "$auth" http://localhost:8081/v1/failures/<id>/payload  # the envelope notify-sink missed
```

Bring the destination back and retry:

```bash
docker compose -f examples/docker-compose.yaml start notify-sink
curl -s -X POST -H "$auth" -H 'Idempotency-Key: my-first-retry' \
  http://localhost:8081/v1/failures/<id>/retries                   # {"run_id":"…"}
curl -s -H "$auth" http://localhost:8081/v1/retries/<run_id>
```

The run moves to `succeeded` and the record to `resolved`. RabbitMQ is not
sent the event a second time: a retry runs only the delivery that failed.
Sending the same `Idempotency-Key` again returns the same run instead of
starting another.

`make compose-e2e-test` runs this walkthrough automatically against the same
compose file. The replay service's API, outcomes, and operations are in
[replay-service.md](replay-service.md); its commands are in
[cli.md](cli.md#hookway-replay).

## Run Hookway on the host

For a tighter loop, run core with `go run` against the stack's RabbitMQ. This
uses the minimal `examples/config.yaml`, with no replay service:

```bash
export RABBITMQ_URL='amqp://guest:guest@localhost:5672/'
export GITHUB_WEBHOOK_SECRET='change-me'
go run ./cmd/hookway validate --config examples/config.yaml
HOOKWAY_CONFIG=examples/config.yaml go run ./cmd/hookway
```

Stop the compose `hookway` service first
(`docker compose -f examples/docker-compose.yaml stop hookway`), or it keeps
port 8080. `make validate` runs the same validation, and `make run` the same
serve.

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
make compose-e2e-test
make docker-build
```

`make integration-test` requires Docker because it uses testcontainers to run
RabbitMQ. `make compose-e2e-test` requires Docker too: it compiles both
commands for linux on the host, packages them into scratch images that mirror
the release Dockerfile, deploys the dev stack under a throwaway project name
on random loopback ports (so it runs alongside a `make dev-up`), and removes
everything afterwards. `COMPOSE_E2E_BUILD=dockerfile` builds the release
images from the Dockerfile instead, as CI does.

## Container Probes

The Docker image healthcheck calls `GET /healthz` on `127.0.0.1:8080`. Orchestrators should use `GET /readyz` or `GET /ready` for readiness: those probes unique destinations referenced by `publish` nodes (for this example, RabbitMQ) and report per flow.

## RabbitMQ Dead Letters

If local testing needs a dead-letter path, enable RabbitMQ `dead_letter` config so Hookway declares the DLX/DLQ and binds declared queues with dead-letter arguments. If your local RabbitMQ topology is managed outside Hookway, keep declaration disabled and create those resources yourself.
