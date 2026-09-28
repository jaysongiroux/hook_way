# CLI

Hookway ships two commands you run, and a third compiled for the browser.

| Command | What it is | Image |
|---|---|---|
| [`hookway`](#hookway) | Core. It serves webhooks, and it checks configs, prints the config schema, and serves the config builder. | `hookway` |
| [`hookway-replay`](#hookway-replay) | The optional replay service. It keeps the deliveries core missed after the sender got its 2xx, and retries them when you ask. | `hookway-replay` (Docker Hub: `<image>-replay`) |
| [`hookway-wasm`](#hookway-wasm) | The config validator and schema compiled to WebAssembly for the builder. Not a terminal command. | — |

## Getting help

Both commands document themselves:

```bash
hookway help                  # every command, the environment, exit codes
hookway help validate         # one command: what it does, its flags, examples
hookway validate -h           # the same page
hookway-replay help migrate
```

Help goes to stdout and exits `0`. A mistake exits `2` and prints the error
and a pointer to the help on stderr: an unknown command, a flag the command
does not have, or an argument it does not take. That last one catches
`hookway validate config.yaml`, which is refused instead of quietly
validating `$HOOKWAY_CONFIG`.

Flags take one dash or two (`-config` and `--config` are the same). A value
follows a space or `=` (`--config x.yaml`, `--config=x.yaml`), except for
on/off flags: `--strict` alone turns one on, and an explicit value needs `=`
(`--strict=false`).

| Exit code | Meaning |
|---|---|
| `0` | Success, including printing help. |
| `1` | The command ran and failed: an invalid config, an unhealthy target, an unreachable database. |
| `2` | The command line was wrong. Nothing ran. |

## Building and running

```bash
make build                            # bin/hookway and bin/hookway-replay
go run ./cmd/hookway help             # or run straight from the checkout
docker run --rm hookway:local help    # each image's entrypoint is its binary
```

The core image runs `hookway` with no command (serve). The replay image runs
`hookway-replay serve` by default. Any other command works in either image:
`docker run --rm hookway-replay:local migrate --config /etc/hookway-replay/replay.yaml`.

## Recipes

| Task | Command |
|---|---|
| Fail CI on any config problem, lint warnings included, without secrets | `hookway validate --config config.yaml --json --no-env --strict` |
| Get completion and inline errors in your editor | `hookway schema > hookway.schema.json`, then `# yaml-language-server: $schema=./hookway.schema.json` at the top of the config |
| Draw a flow and export its YAML | `hookway editor`, then open http://localhost:8090/builder |
| Check a container from inside it | `hookway healthcheck --url http://127.0.0.1:8080/readyz` |
| Upgrade the replay service | `hookway-replay migrate`, then roll out `hookway-replay serve` |
| Free replay storage now, after shortening retention | `hookway-replay gc` |
| See every missed delivery and retry one | the replay API; [local-development.md](local-development.md#catch-a-missed-delivery-and-retry-it) walks through it with `curl` |

## hookway

| Command | What it does |
|---|---|
| [`serve`](#hookway-serve) | Serve webhooks. The default: `hookway` alone runs it. |
| [`validate`](#hookway-validate) | Check a config without starting anything. |
| [`schema`](#hookway-schema) | Print the config JSON Schema, or the builder's per-type catalogs. |
| [`editor`](#hookway-editor) | Serve the config builder and the config API. |
| [`healthcheck`](#hookway-healthcheck) | Probe a running server. |
| [`version`](#hookway-version) | Print the version and commit. |

| Variable | Effect |
|---|---|
| `HOOKWAY_CONFIG` | The config file when `--config` is not given (`serve`, `validate`, `editor`). |
| `HOOKWAY_LOG_LEVEL` | Overrides `logging.level` at start: `debug`, `info`, `warn`, or `error`. |

Values inside the config come from the environment too, as `${VAR}`
references; see [configuration.md](configuration.md).

### hookway serve

```bash
hookway [--config FILE]
hookway serve [--config FILE]
```

| Flag | Meaning |
|---|---|
| `--config FILE` | The config file. Default: `$HOOKWAY_CONFIG`. |
| `--version` | Print the version and exit without serving. |

Loads and validates the config before connecting to any destination, then
serves every flow on `server.addr`. A config that does not validate exits `1`
and names each problem, exactly as `hookway validate` would.

- **Listeners.** `server.addr` carries the flows and the probes.
  `metrics.addr`, when set, gives `/metrics` its own listener. A `replay:`
  block adds the control listener (`replay.control.addr`) that the replay
  service retries through. If any of them fails to bind (the port is taken,
  say), the process exits `1` rather than run without it.
- **Probes.** `GET /healthz` and `GET /health` are liveness only.
  `GET /readyz` and `GET /ready` also check every destination the flows
  publish to, and report per flow.
- **Shutdown.** `SIGINT` or `SIGTERM` stops taking requests and drains the
  ones in flight for `server.shutdown_timeout`. With replay on, the control
  listener waits for retries in progress before the destinations close.
- **Logs.** JSON on stdout. `logging.level` in the config sets the level
  (default `info`); `HOOKWAY_LOG_LEVEL` overrides it.

```bash
HOOKWAY_CONFIG=examples/config.yaml go run ./cmd/hookway
go run ./cmd/hookway --config examples/config.yaml
make run                                              # the same, with examples/config.yaml
```

### hookway validate

```bash
hookway validate [--config FILE] [--json] [--no-env] [--strict]
```

| Flag | Meaning |
|---|---|
| `--config FILE` | The config file to check. Default: `$HOOKWAY_CONFIG`. |
| `--json` | Print `{"issues": [...]}` on stdout instead of text. An unset `${VAR}` becomes a warning. |
| `--no-env` | Leave `${VAR}` unexpanded: check syntax and structure only. |
| `--strict` | Fail on lint warnings too. |

Checks the config the way `serve` would, without connecting to anything:
YAML syntax, unknown fields, required settings, durations, duplicate or
overlapping flow paths, unknown destinations, node wiring, auth settings,
and the rest of the rules in [configuration.md](configuration.md). `make
validate` runs it on `examples/config.yaml`; `make validate-examples` runs it
on every shipped example.

A valid config prints `config valid: FILE` and exits `0`. An invalid one
prints every issue on stderr and exits `1`.

**Environment.** Plain `validate` expands `${VAR}` references against the
process environment, like `serve`, and fails with exit `1` if any referenced
variable is unset. `--json` expands too, but reports a missing variable as a
`config.env.<NAME>` issue at severity `warning` and carries on as if it had a
value: CI can validate without production secrets and still fail on a real
structural error. `--no-env` skips expansion in either mode, for a pure
syntax and structure check (a config template, say).

**Warnings.** A config whose only issues are warnings exits `0`; the
warnings are printed, or listed in the JSON envelope. `--strict` makes them
fail with exit `1`. It is the only way to gate a build on the policy lints:

- a flow with no `rate_limit` node (it is unthrottled)
- a flow with a `no_auth` node (every request is accepted unverified)
- a `code` node with no explicit `timeout` (the 1s default applies, and goja
  has no memory cap)
- a `read_body.max_bytes` above `server.max_body_bytes`
- a `lookup` node with no `cache` block
- `replay.tls.insecure_skip_verify: true`

**JSON output.** `{"issues": [ ... ]}`, where each issue has `field` (a
dotted config path), `message`, an optional `suggestion`, and `severity`
(`"error"` or `"warning"`). An empty array means valid. The exit code is `1`
when any issue is an error, or any issue at all under `--strict`; `0`
otherwise.

`--json`, `--no-env`, and `--strict` combine freely:

```bash
hookway validate --config examples/config.yaml
hookway validate --config config.yaml --strict                  # lint warnings fail too
hookway validate --config config.yaml --json                    # CI: secrets optional
hookway validate --config config.template.yaml --json --no-env --strict
```

### hookway schema

```bash
hookway schema [--nodes | --destinations | --connections]
```

| Flag | Meaning |
|---|---|
| `--nodes` | Print each node type's ports and option JSON Schema. |
| `--destinations` | Print each destination type's option JSON Schema. |
| `--connections` | Print each connection type's option JSON Schema. |

With no flag, prints the JSON Schema for the whole config file. It is
generated from the config structs, so it cannot drift from the code. Point
your editor at it for completion and inline validation:

```yaml
# yaml-language-server: $schema=./hookway.schema.json
```

The three flags print the per-type catalogs the config builder uses instead.
Pass one; if several are set, the first in the order above wins.
`make node-schema` writes them to `web/src/graph/{node,destination,connection}Schema.json`,
and `make node-schema-check` fails if the committed files drifted from the
binary.

```bash
hookway schema > hookway.schema.json
hookway schema --nodes
```

### hookway editor

```bash
hookway editor [--addr ADDR] [--config FILE]
```

| Flag | Meaning |
|---|---|
| `--addr ADDR` | The address to listen on. Default: `:8090`. |
| `--config FILE` | A config whose `security.api_key` protects the API. Default: `$HOOKWAY_CONFIG`; with neither, the API is open. |

Serves the config builder and the API behind it:

- `/` is the project overview, which links to these docs and the examples.
- `/builder` is the config builder. It is a **config generator**: drag nodes,
  export the YAML, and deploy that file yourself. It does not connect to a
  running Hookway, edit live flows, or show executions. The canvas refuses
  illegal port wiring. Export validates in the browser (WASM) and falls back
  to `POST /validate`.
- `GET /schema` returns the JSON Schema (`hookway schema`).
- `GET /node-schema` returns the per-node catalog (`hookway schema --nodes`).
- `POST /validate` takes candidate YAML as the body and returns the same
  `{"issues": [...]}` envelope as `validate --json --no-env`.

**`POST /validate` never expands `${VAR}`.** The body is untrusted client
input, and the API allows any origin (CORS `*`). If it expanded environment
references, a client could post a config full of
`${AWS_SECRET_ACCESS_KEY}`-style probes and infer server-side secrets and
internal URLs from which fields come back populated. So a `${VAR}` in a posted
config is an opaque literal, and you never see a `config.env.<NAME>` warning
here. For env-expanding validation, run `validate --json` where the
environment lives.

With a config that sets `security.api_key`, `GET /schema`,
`GET /node-schema`, and `POST /validate` require
`Authorization: Bearer <key>`. `OPTIONS` preflight is always allowed, so
browser CORS works. A config given but invalid exits `1`.

The builder UI is embedded only in a locally built binary after `make ui`
(run `make web-install` first on a clean clone). Published core and replay
images are runtime-only and do not include the builder bundle. Without the
bundle, `/` and `/builder` explain that the UI was not built; the API works
either way.

```bash
hookway editor                                    # http://localhost:8090/builder
hookway editor --addr :9000 --config config.yaml
```

### hookway healthcheck

```bash
hookway healthcheck [--url URL] [--timeout DURATION]
```

| Flag | Meaning |
|---|---|
| `--url URL` | The URL to GET. Default: `http://127.0.0.1:8080/healthz`. |
| `--timeout DURATION` | Give up after this long. Default: `5s`. |

Exits `0` for any `2xx` and `1` for a connection failure, a timeout, or
anything else. The container image runs it as its `HEALTHCHECK`, which is why
a scratch image with no shell or curl can still be probed. `/healthz` is
liveness only; point `--url` at `/readyz` to include the destinations.

### hookway version

```bash
hookway version
```

Prints `hookway <version> (<commit>)`. A plain `go build` reports
`dev (none)`; `make build`, `make docker-build`, and the release workflow
inject the real values from `git describe` and `git rev-parse`.
`hookway --version` prints the same line.

## hookway-replay

The replay service is optional; core runs the same without it. Its API,
configuration, outcomes, and runbook are in
[replay-service.md](replay-service.md). Every command takes its config from
`--config`, or from `HOOKWAY_REPLAY_CONFIG`, and `${VAR}` references in the
file are expanded from the environment.

| Command | What it does |
|---|---|
| [`serve`](#hookway-replay-serve) | Run the API, the retry workers, and the retention sweep. |
| [`migrate`](#hookway-replay-migrate) | Apply database migrations. |
| [`gc`](#hookway-replay-gc) | Delete expired records once. |
| [`validate`](#hookway-replay-validate) | Check a config without connecting to anything. |
| [`healthcheck`](#hookway-replay-healthcheck) | Probe a running service. |
| [`version`](#hookway-replay-version) | Print the version and commit. |

| Variable | Effect |
|---|---|
| `HOOKWAY_REPLAY_CONFIG` | The config file when `--config` is not given. |
| `HOOKWAY_LOG_LEVEL` | Overrides `logging.level` at start: `debug`, `info`, `warn`, or `error`. |

A deployment runs `validate` in CI, `migrate` once per release (a Job, an
init container, or the one-shot `migrate` service in
`examples/docker-compose.yaml`), and then any number of `serve` replicas on
one database.

### hookway-replay serve

```bash
hookway-replay serve [--config FILE]
```

| Flag | Meaning |
|---|---|
| `--config FILE` | The config file. Default: `$HOOKWAY_REPLAY_CONFIG`. |

Runs, until `SIGINT` or `SIGTERM`:

- **The API** on `server.addr`: `GET /v1/failures` (filter by `status`,
  `flow`, `node_id`, `destination_id`, `kind`, `retryable`; page with `limit`
  and `cursor`), `GET /v1/failures/{id}`,
  `GET /v1/failures/{id}/payload`, `POST /v1/failures/{id}/retries`,
  `POST /v1/failures/{id}/dismiss`, `POST /v1/retries` (a batch), and
  `GET /v1/retries/{run_id}`, plus `POST /v1/failures`, where core uploads.
  Each route needs a key with the matching scope (`capture`, `read`,
  `payload`, `retry`). `/healthz` and `/readyz` are open.
- **Metrics** on `metrics.path`, with its own listener on `metrics.addr` when
  set and on the API listener otherwise. Once any key has the `metrics`
  scope, a scrape needs one; with none, the endpoint is open and `validate`
  warns.
- **The workers**, `workers.concurrency` per replica, which claim queued
  retries and send each one to core's control listener (`core.control_url`).
- **The retention sweep** every `retention.gc_interval`. One replica sweeps at
  a time.

It refuses to start, with exit `1`, when the database is unreachable or its
schema is behind this binary: run `migrate` first. Shutdown drains the API
for up to 30s and waits for running retries to finish; give the container a
stop grace period longer than `workers.execution_timeout`. Logs are JSON on
stdout.

```bash
hookway-replay serve --config /etc/hookway-replay/replay.yaml
HOOKWAY_REPLAY_CONFIG=replay.yaml hookway-replay serve
```

### hookway-replay migrate

```bash
hookway-replay migrate [--config FILE]
```

| Flag | Meaning |
|---|---|
| `--config FILE` | The config file. It must validate, though only `database` is used. Default: `$HOOKWAY_REPLAY_CONFIG`. |

Applies every pending migration and prints `applied migrations [...]; schema
is at version N`, or `schema is current at version N` when there was nothing
to do. Replicas may run it at the same time: a PostgreSQL advisory lock
serializes them. Exits `1` when the database is unreachable or a migration
fails.

```bash
hookway-replay migrate --config replay.yaml
```

### hookway-replay gc

```bash
hookway-replay gc [--config FILE]
```

| Flag | Meaning |
|---|---|
| `--config FILE` | The config file. Default: `$HOOKWAY_REPLAY_CONFIG`. |

Deletes the records past `retention.ttl` once, prints `gc deleted N records
and freed M bytes`, and exits. `serve` already sweeps on a timer, so this is
for cron, or for freeing space right after shortening the retention. When
another replica holds the sweep lock, it prints `gc skipped: another replica
is sweeping` and exits `0`.

```bash
hookway-replay gc --config replay.yaml
```

### hookway-replay validate

```bash
hookway-replay validate [--config FILE]
```

| Flag | Meaning |
|---|---|
| `--config FILE` | The config file to check. Default: `$HOOKWAY_REPLAY_CONFIG`. |

Checks the config without connecting to the database or to core: unknown
fields, durations, limits, key names and scopes, token lengths (32
characters at least), and URLs. `${VAR}` references are expanded, and an
unset variable fails. Warnings (metrics enabled with no `metrics`-scoped key,
so the endpoint is open) are printed on stderr but pass. Prints `config valid: FILE` and
exits `0`, or lists the issues and exits `1`.

```bash
hookway-replay validate --config examples/dev/replay.yaml
```

### hookway-replay healthcheck

```bash
hookway-replay healthcheck [--url URL] [--timeout DURATION]
```

| Flag | Meaning |
|---|---|
| `--url URL` | The URL to GET. Default: `http://127.0.0.1:8080/healthz`. |
| `--timeout DURATION` | Give up after this long. Default: `5s`. |

Exits `0` for any `2xx` and `1` otherwise. The container image runs it as its
`HEALTHCHECK`. `/healthz` is liveness only; `/readyz` also checks that the
database answers and its schema is current.

### hookway-replay version

```bash
hookway-replay version
```

Prints `hookway-replay <version> (<commit>)`. A plain `go build` reports
`dev (none)`.

## hookway-wasm

`cmd/hookway-wasm` is the same config validator and schema, compiled to
WebAssembly so the config builder validates client-side instead of
round-tripping to a server.

Build it:

```bash
make wasm
```

That runs `GOOS=js GOARCH=wasm go build ./cmd/hookway-wasm`, writes
`hookway.wasm` and the matching `wasm_exec.js` into `internal/editor/assets/`,
and copies both into `web/public/` where Vite serves them. Both artifacts are
generated and gitignored. `make ui`, `make web-dev`, and `make web-e2e` all
build it first: without it the dev server 404s `hookway.wasm`, and the
builder's validation would fall back to a `POST /validate` that is not
running.

### Browser globals

`main` installs three functions on `globalThis` and then blocks forever so
they stay callable:

| Global | Arguments | Returns |
|---|---|---|
| `hookwayValidate(yaml)` | one string: candidate config YAML | JSON string `{"issues":[...]}`: the same envelope as `validate --json --no-env` |
| `hookwaySchema()` | none | JSON string: the config JSON Schema (`hookway schema`) |
| `hookwayNodeSchemas()` | none | JSON string: the per-node catalog (`hookway schema --nodes`) |

An internal failure (a marshalling error, or a bad argument type) returns a
JSON string with a top-level `"error"` key instead, which is how the JS side
tells it apart from a validation response's `"issues"`.

All three delegate to `cmd/hookway-wasm/bridge`, a pass-through of
`internal/configapi` that adds **no** validation logic of its own. That is the
point: an in-browser result is byte-identical to the CLI's and to the HTTP
editor's, because all three share one `configapi` entry point. `bridge` has no
`syscall/js` import, so it is unit-testable on the host.

Like the HTTP editor, `hookwayValidate` never expands `${VAR}`:
browser-supplied bytes are untrusted. See [hookway editor](#hookway-editor)
for why.

### Standalone harness

A single static page (`cmd/hookway-wasm/harness/index.html`, no npm
dependencies) that loads the module and gives you a YAML textarea plus
`hookwayValidate()` and `hookwaySchema()` buttons, for exercising the
validator without running Vite or the editor:

```bash
make wasm-harness
```

This rebuilds the WASM (it depends on `wasm`), copies `hookway.wasm` and
`wasm_exec.js` next to `cmd/hookway-wasm/harness/index.html`, and serves that
directory on **http://localhost:8091**. Ctrl-C stops it.

The harness uses `python3 -m http.server`, so **`python3` must be on your
PATH**. It is the one undeclared toolchain dependency in the Makefile, and
the only target that needs it. A plain `file://` open will not work:
browsers refuse to instantiate WebAssembly from a `file:` origin, which is
why the target serves over HTTP at all.
