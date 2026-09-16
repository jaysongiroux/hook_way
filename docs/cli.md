# CLI

Hookway ships one CLI entrypoint at `cmd/hookway`. It can serve traffic, validate config, and run a simple healthcheck for container or deployment probes.

## Serve

Start Hookway with `HOOKWAY_CONFIG`:

```bash
HOOKWAY_CONFIG=examples/config.yaml go run ./cmd/hookway
```

Or pass `--config`:

```bash
go run ./cmd/hookway --config examples/config.yaml
```

The process loads and validates config before connecting to its destinations. `SIGINT` and `SIGTERM` trigger graceful HTTP shutdown using `server.shutdown_timeout`. Pass `--version` to print the version and exit without serving.

The dedicated `metrics.addr` listener (when configured) is treated the same as the main listener: if it fails to bind (e.g. the port is already in use), the whole process exits nonzero instead of silently running with no metrics.

Set `logging.level` (`debug`, `info`, `warn`, or `error`; default `info`) in config to control log verbosity.

## Version

Print the build version and commit and exit:

```bash
go run ./cmd/hookway version
```

Prints `hookway <version> (<commit>)`. Both default to `dev`/`none` for a plain `go build`; release builds inject real values via `-ldflags` (see the Makefile's `build`/`docker-build` targets and the Dockerfile).

## Validate

Validate config without starting the server:

```bash
go run ./cmd/hookway validate --config examples/config.yaml
```

The repo wrapper is:

```bash
make validate
```

Validation rejects unknown YAML fields, missing required settings, invalid durations, duplicate or overlapping flow paths, unsupported backends, unsupported delivery failure policies, missing publish destinations, unknown destination ids, invalid IP allowlist entries, and missing secrets. Unknown auth schemes fail closed at runtime. JWT requires body binding. Auth may read a query parameter via `source.kind: query`. Standalone lookup nodes are rejected until provider binding is implemented. A `schema_version: 1` / `endpoints:` document is a migration error (exit 1) pointing at `docs/configuration.md`.

Plain `validate` expands `${VAR}` references against the process environment (same as serving) and hard-fails — nonzero exit, no other output — if any referenced variable is unset.

### `--no-env`

Skip `${VAR}` expansion entirely and check syntax/structure only:

```bash
go run ./cmd/hookway validate --config examples/config.yaml --no-env
```

Useful for a pure syntax/structure check with no environment dependency at all — e.g. linting a config template that intentionally references variables that are never set in the linting environment. Works with both plain `validate` and `validate --json`.

## Healthcheck

Check a running service:

```bash
go run ./cmd/hookway healthcheck --url http://127.0.0.1:8080/healthz --timeout 5s
```

The command exits `0` for any `2xx` response and `1` for connection failures or non-`2xx` responses. `/healthz` and `/health` are process liveness checks only; use `/ready` or `/readyz` when the check must include destination readiness.

## Schema

Print the JSON Schema describing the config YAML:

```bash
go run ./cmd/hookway schema > hookway.schema.json
```

The schema is generated from the config structs, so it never drifts from the
code. Point your editor at it for autocomplete and inline validation while
writing YAML, e.g. at the top of a config file:

```yaml
# yaml-language-server: $schema=./hookway.schema.json
```

Print the per-node catalog (ports plus option JSON Schema) used by the config
builder:

```bash
go run ./cmd/hookway schema --nodes
go run ./cmd/hookway schema --destinations
```

`make node-schema` writes those catalogs to `web/src/graph/nodeSchema.json` and
`web/src/graph/destinationSchema.json`. `make node-schema-check` fails if the
committed files drifted from the CLI.

## Validate (JSON output)

Emit validation results as a JSON envelope instead of human-readable text:

```bash
go run ./cmd/hookway validate --config examples/config.yaml --json
```

Output is `{"issues": [ ... ]}` where each issue has `field` (a dotted config
path), `message`, optional `suggestion`, and `severity` (`"error"` or
`"warning"`). An empty array means the config is valid. Exit code is `1` when
any `"error"`-severity issue is present, `0` otherwise — a config with only
`"warning"` issues exits `0`.

Unlike plain `validate`, the JSON path expands `${VAR}` references but does
**not** hard-fail on a missing one: a referenced-but-unset variable is
reported as a `"config.env.<NAME>"` issue at severity `"warning"` (not
`"error"`), and validation otherwise proceeds as if that reference had
resolved to a non-empty placeholder. This is deliberate — CI commonly runs
`validate --json` without production secrets configured, and a missing
secret shouldn't fail the build the way a genuine structural error should.
Add `--no-env` (see above) to skip expansion entirely instead.

## Editor API

Serve the config API over HTTP for a config UI or other client:

```bash
go run ./cmd/hookway editor --addr :8090
```

- `GET /schema` → the JSON Schema above.
- `GET /node-schema` → the per-node catalog (`hookway schema --nodes`).
- `POST /validate` → body is candidate YAML; returns the same `{"issues": [...]}`
  envelope as `validate --json`.

Both endpoints allow any origin (CORS `*`); they expose no secrets.

The same server also serves the embedded web bundle, when one was staged into
the binary with `make ui`:

- `/` → the project overview, which links to these docs and the examples.
- `/builder` → the config builder. It is a **config generator**: drag nodes,
  export `schema_version: 2` YAML, and deploy that file yourself. It does not
  connect to a running Hookway instance, edit live flows, or show executions.
  The canvas refuses illegal port wiring. Export validates in-browser (WASM)
  and falls back to `POST /validate`.

Without a staged bundle both paths return a page explaining that the UI was not
built; the API keeps working either way.

Pass `--config` (or set `HOOKWAY_CONFIG`) so the editor reads `security.api_key`
and requires `Authorization: Bearer <key>` on `GET /schema`, `GET /node-schema`,
and `POST /validate`:

```bash
go run ./cmd/hookway editor --addr :8090 --config examples/config.yaml
```

Without a config, the editor runs unauthenticated. `OPTIONS` preflight is always
allowed so browser CORS works.
