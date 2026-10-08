# Local development

Run these commands from the repository root. The container walkthrough needs
Docker with Compose and OpenSSL. Running Go or editing the frontend on the host
also needs the versions and setup in [Contributing](../../CONTRIBUTING.md).

Choose `make dev-up` for the disposable walkthrough below. For persistent data
and generated credentials, use the production-style recipe in the next section.
Both support local HTTP and private Docker addresses without a cloud account.

## Persistent local production setup

The [production-local Compose recipe](../../examples/production-local/docker-compose.yaml)
uses real RabbitMQ storage and authenticated webhooks. Full mode adds PostgreSQL,
replay migrations, the replay API and console. Shared Redis protection is optional.
Docker with Compose, Make and OpenSSL are required. The recipe uses ordinary YAML
and standard Compose commands. Source image builds download their Go/Node
dependencies inside the shipped Dockerfiles; Node is not a host runtime requirement.

```sh
make prod-local-bootstrap
make prod-local-validate
make prod-local-up
make prod-local-status
```

Bootstrap copies the selected ordinary YAML into a private setup directory once:

| Config | Components |
| --- | --- |
| [core.yaml](../../examples/production-local/core.yaml) | Core and RabbitMQ; process-local rate limiting |
| [core-shared.yaml](../../examples/production-local/core-shared.yaml) | Core and RabbitMQ; shared Redis rate limiting |
| [core-replay.yaml](../../examples/production-local/core-replay.yaml) | Core, RabbitMQ, replay and console; process-local rate limiting |
| [core-replay-shared.yaml](../../examples/production-local/core-replay-shared.yaml) | Full stack with shared Redis rate limiting |
| [replay.yaml](../../examples/production-local/replay.yaml) | Replay settings and scoped credentials |
| [migration/replay.yaml](../../examples/production-local/migration/replay.yaml) | Migration-only owner connection; never serve this file |

Choose the components at bootstrap. For a separate full stack that can coexist
with the core example:

```sh
HOOKWAY_HTTP_PORT=28080 REPLAY_HTTP_PORT=28081 CONSOLE_HTTP_PORT=28082 \
  RABBITMQ_HTTP_PORT=25673 make prod-local-bootstrap \
  PROD_LOCAL_DIRECTORY=examples/production-local/.local-full \
  PROD_LOCAL_PROJECT=hookway-production-full PROD_LOCAL_MODE=full PROD_LOCAL_PROTECTION=1
make prod-local-validate PROD_LOCAL_DIRECTORY=examples/production-local/.local-full
make prod-local-up PROD_LOCAL_DIRECTORY=examples/production-local/.local-full
```

Bootstrap generates distinct random credentials in `.env` with owner-only
permissions, and preserves existing credentials and operator edits to the copied
`core.yaml` and `replay.yaml` on rerun. Repeat the original mode/protection options
when rerunning bootstrap; a different selection requires a separate directory.
The default private directory is `examples/production-local/.local`. Keep custom
directories outside the Docker build context and out of Git. YAML retains
environment references; do not paste `.env` contents into logs or issues.

`COMPOSE_PROJECT_NAME` and `COMPOSE_PROFILES` in `.env` select the saved project
and optional services. Make commands infer the saved project, reject an explicitly
mismatched `PROD_LOCAL_PROJECT`, and use `PROD_LOCAL_DIRECTORY` for a custom setup.
The setup uses the same runtime controls as production without a cloud account.
The [optional monitoring overlay](../../examples/production-local/monitoring/README.md)
adds private authenticated scrapes and a local fixture receiver; omit it when
logs and health endpoints are sufficient. It is not an application dependency.

| Service | Default browser/host address | Available in |
| --- | --- | --- |
| Core webhook | http://localhost:18080/webhooks/github | Core and full |
| RabbitMQ management | http://localhost:15673 | Core and full |
| Replay API | http://localhost:18081 | Full |
| Console and builder | http://localhost:18082 and http://localhost:18082/builder | Full |
| PostgreSQL, Redis, core control/metrics | Docker network only | Selected components |

Published ports bind to loopback. Bootstrap persists the four port variables
and derives matching browser URLs. Export `REPLAY_API_URL` and
`REPLAY_CONSOLE_ORIGIN` before bootstrap when a proxy supplies different public
addresses. Full mode with dynamically assigned port `0` requires explicit public
addresses; those addresses must actually reach the services for browser use.
Container URLs use Compose names such as `rabbitmq` and `replay`. The console
receives the API URL, never an operator token.

Read `REPLAY_OPERATOR_TOKEN` privately from `.env` to sign in, or
`GITHUB_WEBHOOK_SECRET` to sign the webhook shown below. RabbitMQ uses username
`hookway` and its generated password. For the signed request, use port `18080`
and the generated secret instead of the disposable stack's defaults.

The full recipe demonstrates partial failure with a synchronous HTTP notification
sink under `skip_failed`. Acknowledgements follow the selected flow policy and
replay capture remains best effort. See [delivery guarantees](../operations/delivery-guarantees.md)
when choosing a production contract.

```sh
make prod-local-logs
make prod-local-down
make prod-local-up PROD_LOCAL_BUILD=0
make prod-local-restart
```

`prod-local-validate` builds source images and checks core plus replay in full
mode. Set `PROD_LOCAL_BUILD=0` to validate images already available locally or
start with selected published images without rebuilding. Override
`HOOKWAY_CORE_IMAGE`, `HOOKWAY_REPLAY_IMAGE` and `HOOKWAY_CONSOLE_IMAGE` to use
matching immutable release images. Initial source builds need network access.
Set `PROD_LOCAL_STRICT=1` to fail core validation on supported-risk warnings.
Full mode intentionally reports the partial-delivery warning; mandatory errors
always fail. Offline validation does not establish live dependency connectivity.

`prod-local-up` validates before starting services. In full mode it waits for
PostgreSQL, then requires provisioning, migration and runtime grants to succeed
before starting the application services. Repeated `up` with the same supported
release remains available. `prod-local-restart` validates before stopping the
healthy stack; a subsequent migration failure leaves application services down
instead of serving against an unverified schema. Restart includes downtime.
Changing releases requires the coordinated [upgrade procedure](../operations/upgrades.md),
not a live migration against running workers.

Ordinary shutdown and restart preserve named volumes and copied configuration.
Back them up before upgrades; persistence alone is not a backup. To intentionally
erase only the selected project's containers and stored data:

```sh
make prod-local-reset PROD_LOCAL_CONFIRM_RESET=hookway-production
```

Reset requires the exact saved project name and retains credentials/configuration.
Use the custom directory and its project confirmation when resetting another
setup. It is separate from ordinary shutdown and is not an upgrade step.

For a quiescent database backup and restore into a **separate** rehearsal target,
follow [Backup and restore](../operations/recovery.md). Keep the original volumes,
copied configuration and newer destination receipts until recovery is accepted;
do not start replay workers on an unreconciled older snapshot.

### Separate database roles and existing setups

Full mode runs `db-provision`, `migrate`, and `db-grants` before starting replay.
The PostgreSQL bootstrap administrator (`replay`, using the existing
`POSTGRES_PASSWORD`) is used only by PostgreSQL and the provisioning job. The
separate `replay_owner` login owns the dedicated database/schema and named replay
objects, and applies migrations. It is not a superuser and cannot create roles
or other databases. Replay serves as `replay_runtime`, with database `CONNECT`,
schema `USAGE`, DML on live replay tables, version-table `SELECT`, and the required
audit sequence permissions. It cannot create/alter schema objects or rewrite the
migration version. See the [runtime grant contract](../operations/replay-service.md#separate-migration-and-runtime-database-roles).

The maintained [provisioning SQL](../../examples/production-local/database/provision.sql)
transfers ownership only for named current-schema objects; it never recreates
the database or clears data. [Runtime grants](../../examples/production-local/database/runtime-grants.sql)
are reapplied after migration. Update their explicit object list if a future
migration adds live tables or sequences. These scripts target this dedicated
local database; use deployment-specific role provisioning for a shared database.

Migration uses its [own configuration](../../examples/production-local/migration/replay.yaml)
and receives only owner credentials. The CLI currently validates HTTP settings
even for `migrate`, so its unused key/control fields derive from the job's own
secret. The migration command never serves HTTP or invokes core. Do not run
`serve` with this file. The real capture, operator, metrics and control credentials
remain confined to their actual runtime consumers.

An older local setup may lack the new role credentials. Extend it explicitly,
preserving the existing administrator password, copied configuration and data:

```sh
make prod-local-down
make prod-local-credentials-upgrade
make prod-local-validate
make prod-local-up
```

Pass the original `PROD_LOCAL_DIRECTORY` on every command for a custom setup.
The upgrade adds only missing owner/runtime credentials and URLs plus the
migration config, and is idempotent. Full startup gives an actionable error when
this step is required. It applies to the current generic-record database schema;
older failure-shaped databases remain outside its migration compatibility scope.

Compose binds replay's `REPLAY_DATABASE_URL` to `REPLAY_RUNTIME_DATABASE_URL`,
including when `.env` retains the earlier administrator URL. If your copied
`replay.yaml` hard-codes a database URL, point its `database.url` at the restricted
runtime connection yourself. Native invocations must likewise supply the runtime
URL rather than sourcing the retained legacy administrator alias. Operator edits
are never overwritten automatically.

Named volumes and `.env` contain plaintext application data and credentials.
This recipe establishes access boundaries; it does not encrypt disks or backups.
Use encrypted host storage and protected backups locally, and the target
platform's encrypted volumes/database storage and backup controls in deployment.
Authentication and private networking do not replace storage encryption.

### Optional localhost TLS termination

The maintained [TLS Compose overlay](../../examples/production-local/tls/docker-compose.yaml)
and [Caddyfile](../../examples/production-local/tls/Caddyfile) terminate TLS using
your local certificate. Caddy receives the certificate/key and no application
credentials. Its [manual certificate configuration](https://caddyserver.com/docs/caddyfile/directives/tls)
requires no public domain, certificate authority account or cloud service.

For a separate full TLS stack, use OpenSSL supporting `req -addext`:

```sh
HOOKWAY_HTTP_PORT=38080 REPLAY_HTTP_PORT=38081 CONSOLE_HTTP_PORT=38082 \
  RABBITMQ_HTTP_PORT=35673 REPLAY_API_URL=https://localhost:18444 \
  REPLAY_CONSOLE_ORIGIN=https://localhost:18445 make prod-local-bootstrap \
  PROD_LOCAL_DIRECTORY=examples/production-local/.local-tls \
  PROD_LOCAL_PROJECT=hookway-production-tls PROD_LOCAL_MODE=full
task_state=examples/production-local/.local-tls
umask 077
openssl req -x509 -newkey rsa:2048 -nodes -days 7 -subj /CN=localhost \
  -addext 'subjectAltName=DNS:localhost,IP:127.0.0.1' \
  -keyout "$task_state/localhost.key" -out "$task_state/localhost.crt"
export HOOKWAY_PROXY_UID="$(id -u)"
make prod-local-up PROD_LOCAL_DIRECTORY="$task_state" PROD_LOCAL_TLS=1
curl --cacert "$task_state/localhost.crt" https://localhost:18443/healthz
```

Generate the key once and keep it owner-readable only; regenerate deliberately
before certificate expiry. The proxy UID must match the key's owner so an
unprivileged container can read it. Ports `18443`, `18444` and `18445` front core,
replay and console respectively, all on loopback. Keep `PROD_LOCAL_TLS=1` on
lifecycle commands so shutdown/reset include the proxy. TLS ports can be changed
with `HOOKWAY_TLS_PORT`, `REPLAY_TLS_PORT`, and `CONSOLE_TLS_PORT`; update the exact
browser URLs/origin to match.

The local proxy has a 60-second graceful shutdown budget and a 70-second Compose
stop grace period. Keep the container budget longer than the proxy budget when
customizing them. Ordinary parallel Compose shutdown does not replace the
coordinated core-then-replay stop procedure for maintenance.

For browser use, explicitly trust this certificate in the browser/system trust
store you use, then open https://localhost:18445 and sign in with the normal
operator key. Remove that trust when the local setup is retired. Certificate
trust is independent of API authentication; verification bypass flags are not
needed. API calls still require their normal scopes and replay permits only the
exact configured console origin. The original local HTTP endpoints remain valid.
Core keeps forwarding headers untrusted by default; configure only the actual
proxy addresses/ranges if you need the original client IP for rate limits.

Standard Compose commands also work directly:

```sh
export HOOKWAY_LOCAL_DIRECTORY="$(pwd)/examples/production-local/.local"
docker compose --env-file "$HOOKWAY_LOCAL_DIRECTORY/.env" \
  -f examples/production-local/docker-compose.yaml ps
```

Published release images support Linux amd64. Local source builds use the Docker
daemon's architecture, allowing Apple Silicon Linux arm64 builds. An amd64 release
on Apple Silicon needs Docker emulation and a matching Compose platform override.
Emulation can be slower than native builds. See [releases](../operations/releases.md) for artifact verification.

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
   curl -sS -H "$auth" 'http://localhost:8081/v1/records?kind=failure&delivery_status=open&dismissed=false'
   ```

4. Copy the returned failure ID into `FAILURE_ID` and inspect it. The missed
   delivery has `node_id: notify` and `destination_id: notify`.

   ```sh
   FAILURE_ID='paste-the-failure-id-here'
   curl -sS -H "$auth" "http://localhost:8081/v1/records/$FAILURE_ID"
   curl -sS -H "$auth" "http://localhost:8081/v1/records/$FAILURE_ID/payload"
   ```

5. Restore the destination and request a retry:

   ```sh
   docker compose -f examples/docker-compose.yaml start notify-sink
   curl -sS -X POST -H "$auth" -H 'Idempotency-Key: my-first-retry' \
     "http://localhost:8081/v1/records/$FAILURE_ID/retries"
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
