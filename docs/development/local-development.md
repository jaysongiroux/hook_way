# Local development

Run these commands from the repository root. The container walkthrough needs
Docker with Compose and OpenSSL. Running Go or editing the frontend on the host
also needs the versions and setup in [Contributing](../../CONTRIBUTING.md).

## Start the dev stack

```sh
make dev-up
```

This builds core, replay, and the frontend, then follows their logs. Leave it
running and use a second terminal for the commands below.

| Service | Purpose | Host address |
| --- | --- | --- |
| `hookway` | Core; signed GitHub flow at `/webhooks/github` | http://localhost:8080 |
| `rabbitmq` | Event destination | AMQP: `localhost:5672`; management: http://localhost:15672 (`guest` / `guest`) |
| `notify-sink` | Second, synchronous HTTP destination | Internal only |
| `replay` | Failure inspection and retry API | http://localhost:8081 |
| `console` | Replay console and public builder | http://localhost:8082 and http://localhost:8082/builder |
| `postgres` | Replay database | Internal only |
| `migrate` | Applies the replay schema, then exits | — |

The Compose file is [`examples/docker-compose.yaml`](../../examples/docker-compose.yaml).
Core uses [`examples/dev/core.yaml`](../../examples/dev/core.yaml); replay uses
[`examples/dev/replay.yaml`](../../examples/dev/replay.yaml).

The GitHub flow sends an event to RabbitMQ and `notify-sink`. Its `skip_failed`
policy attempts both deliveries and returns 202 if at least one succeeds.
Replay can retain the failed delivery after that success response; capture is
best effort and happens in the background.

No environment file is needed. To override a default, export the variable
**before** starting Compose:

| Variable | Default | Purpose |
| --- | --- | --- |
| `GITHUB_WEBHOOK_SECRET` | `change-me` | Sign test webhooks |
| `RABBITMQ_URL` | `amqp://guest:guest@rabbitmq:5672/` | Broker URL inside core's container |
| `REPLAY_OPERATOR_TOKEN` | `dev-operator-token-0123456789abcde` | Console sign-in and operator API calls |
| `REPLAY_API_URL` | `http://localhost:8081` | API URL reached by your browser |
| `REPLAY_CONSOLE_ORIGIN` | `http://localhost:8082` | Exact frontend origin allowed by replay CORS |

Capture, control, and metrics keys also have local defaults in the Compose file.
All these credentials are for local use. Container-to-container URLs use service
names such as `rabbitmq`; browser URLs use `localhost` and published host ports.

Open the console and sign in with the operator token above. It has `read`,
`payload`, and `retry` scopes. The public builder needs no sign-in. There is no
`console.enabled` setting: running the frontend is the deployment choice.

## Send a signed webhook

Use the same secret you passed to Compose:

```sh
body='{"action":"opened","issue":{"number":1}}'
sig=$(printf '%s' "$body" | openssl dgst -sha256 \
  -hmac "${GITHUB_WEBHOOK_SECRET:-change-me}" -r | cut -d' ' -f1)
curl -i http://localhost:8080/webhooks/github \
  -H 'Content-Type: application/json' \
  -H "X-Hub-Signature-256: sha256=$sig" \
  --data-binary "$body"
```

Expect `202 Accepted`. In RabbitMQ's management UI, find the
`github-webhooks` queue to inspect the event.

## Catch a missed delivery and retry it

1. Stop the HTTP destination:

   ```sh
   docker compose -f examples/docker-compose.yaml stop notify-sink
   ```

2. Send the signed webhook again, changing the issue number and recalculating
   its signature. RabbitMQ still receives it, so the flow returns 202 even though
   the HTTP delivery fails.

3. Wait a few seconds for background capture, then refresh the console inbox.
   You can also list failures from the API:

   ```sh
   REPLAY_OPERATOR_TOKEN=${REPLAY_OPERATOR_TOKEN:-dev-operator-token-0123456789abcde}
   auth="Authorization: Bearer $REPLAY_OPERATOR_TOKEN"
   curl -sS -H "$auth" 'http://localhost:8081/v1/failures?status=open'
   ```

4. Copy the returned failure ID into `FAILURE_ID` and inspect it. The missed
   delivery has `node_id: notify` and `destination_id: notify`.

   ```sh
   FAILURE_ID='paste-the-failure-id-here'
   curl -sS -H "$auth" "http://localhost:8081/v1/failures/$FAILURE_ID"
   curl -sS -H "$auth" "http://localhost:8081/v1/failures/$FAILURE_ID/payload"
   ```

5. Restore the destination and request a retry:

   ```sh
   docker compose -f examples/docker-compose.yaml start notify-sink
   curl -sS -X POST -H "$auth" -H 'Idempotency-Key: my-first-retry' \
     "http://localhost:8081/v1/failures/$FAILURE_ID/retries"
   ```

6. Copy the returned run ID into `RUN_ID` and poll its result:

   ```sh
   RUN_ID='paste-the-run-id-here'
   curl -sS -H "$auth" "http://localhost:8081/v1/retries/$RUN_ID"
   ```

The run should become `succeeded` and the failure `resolved`. This retry starts
at the failed node; it does not send the event to RabbitMQ again. Repeating the
same submission key returns the same run. Use a new key for a deliberately new
retry, and retain the original key after a lost response.

If the outcome is `unknown`, check the destination before retrying again. See
[Replay investigation](../operations/replay-investigation.md). The
[console guide](../operations/replay-console.md) explains the equivalent browser actions.

## Run Hookway on the host

Keep RabbitMQ running, but stop containerized core to free port 8080:

```sh
docker compose -f examples/docker-compose.yaml stop hookway
export RABBITMQ_URL='amqp://guest:guest@localhost:5672/'
export GITHUB_WEBHOOK_SECRET='change-me'
go run ./cmd/hookway validate --config examples/config.yaml
HOOKWAY_CONFIG=examples/config.yaml go run ./cmd/hookway
```

This minimal example uses RabbitMQ without replay. `make validate` and `make run`
use the same config by default. A remote webhook provider cannot reach your
`localhost`; use the signed local request above, or supply a reachable HTTPS
endpoint with the same flow's signature scheme.

## Frontend development and checks

After installing `web/` dependencies:

```sh
make web-dev                       # marketing and builder development server
```

For the console, use a separate terminal:

```sh
make wasm                          # matching Go validator assets
cd web
REPLAY_API_URL=http://localhost:8081 npm run dev:console -- --port 8082 --strictPort
```

Run the two dev servers in separate terminals when needed. Stop the Compose
console before starting Vite on its port. Replay's default local CORS origin
already permits `http://localhost:8082`.

`make ui` assembles the optional Go-hosted editor; `make build` builds native
binaries. `make console-e2e` runs the standalone frontend on port 5183 against
its real fixture API on 5184 and a disposable PostgreSQL database.

`make compose-e2e-test COMPOSE_E2E_BUILD=dockerfile` automates the signed-event,
failed-destination, and retry walkthrough using the Dockerfiles. It uses its own
Compose project and random loopback ports, then removes its containers/volumes.
See [Contributing](../../CONTRIBUTING.md#make-and-check-a-change) for other checks.

## Probes and cleanup

Use `/healthz` for liveness and `/readyz` (alias `/ready`) for readiness.
Readiness checks the flow's referenced destinations and configured protection
stores, and also reflects shutdown state. The container healthcheck uses
liveness; it does not prove destinations are ready.

```sh
make dev-down
```

This removes the dev containers and their volumes, including broker and replay
data. The next `make dev-up` starts empty.
