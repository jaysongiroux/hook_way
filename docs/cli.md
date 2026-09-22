# CLI

Hookway ships two commands.

- **`cmd/hookway`** — the binary you deploy. With no subcommand it serves
  traffic; otherwise it dispatches one of five subcommands: `validate`,
  `healthcheck`, `schema`, `editor`, and `version`. Counting the bare serve
  mode, that is six invocations, each documented below.
- **`cmd/hookway-wasm`** — the same validator and schema compiled to
  WebAssembly for the browser. It is not a terminal command; it is built by
  `make wasm` and exposes three JS globals. See
  [WASM validator](#wasm-validator).

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

Set `logging.level` (`debug`, `info`, `warn`, or `error`; default `info`) in config to control log verbosity. `HOOKWAY_LOG_LEVEL` overrides the config value at process start.

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

Validation rejects unknown YAML fields, missing required settings, invalid durations, duplicate or overlapping flow paths, unsupported backends, unsupported delivery failure policies, missing publish destinations, unknown destination ids, invalid IP allowlist entries, and missing secrets. On `http_request` it also rejects a `{variable}` in the `url` host that is not followed by a literal dotted domain suffix, and an `allow_private_targets: true` on a node whose host is literal (where it would be a no-op) — see [Configuration § `http_request` outbound egress policy](configuration.md#http_request-outbound-egress-policy). Unknown auth schemes fail closed at runtime. JWT requires body binding. Auth may read a query parameter via `source.kind: query`. Standalone lookup nodes are rejected until provider binding is implemented.

Plain `validate` expands `${VAR}` references against the process environment (same as serving) and hard-fails — nonzero exit, no other output — if any referenced variable is unset.

### `--no-env`

Skip `${VAR}` expansion entirely and check syntax/structure only:

```bash
go run ./cmd/hookway validate --config examples/config.yaml --no-env
```

Useful for a pure syntax/structure check with no environment dependency at all — e.g. linting a config template that intentionally references variables that are never set in the linting environment. Works with both plain `validate` and `validate --json`.

### `--strict`

Treat lint warnings as errors:

```bash
go run ./cmd/hookway validate --config examples/config.yaml --strict
```

By default a config whose only issues are `"warning"`-severity exits `0` — the
warnings are printed (or listed in the JSON envelope) but do not fail the
command. `--strict` makes any warning blocking, so the exit code is `1`.

This is the only way to make the five policy lint warnings gate a build:

- a flow with no `rate_limit` node (it is unthrottled)
- a flow containing a `no_auth` node (every request is accepted unverified)
- a `code` node with no explicit `timeout` (the 1s default applies, and goja
  has no memory cap)
- a `read_body.max_bytes` above `server.max_body_bytes`
- a `lookup` node with no `cache` block

`--strict` composes with both output modes and with `--no-env`:
`validate --strict`, `validate --json --strict`, and
`validate --json --no-env --strict` all behave as you would expect. Use it in
CI; leave it off for interactive iteration.

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
  envelope as **`validate --json --no-env`**.

  The envelope *shape* is identical to plain `validate --json`, but the
  semantics are not: the editor never expands `${VAR}`. `handleValidate` calls
  `configapi.Validate`, the non-expanding entry point, not
  `configapi.ValidateWithEnv(data, true)`. So a `${VAR}` reference in a posted
  config is an opaque literal, and you will never see a
  `"config.env.<NAME>"` warning from this endpoint.

  This is deliberate and load-bearing, not an oversight. The request body is
  untrusted client input, and the editor may be reachable by any origin (see
  the CORS note below). If it expanded environment references, a client could
  post a config full of `${AWS_SECRET_ACCESS_KEY}`-style probes and infer
  server-side secrets and internal URLs from which fields come back populated
  or which validation errors change shape. `internal/configapi` says so at the
  function it exports for exactly this path.

  If you want the env-expanding behaviour, run the CLI where the environment
  actually lives — `validate --json` — rather than reaching for the HTTP API.

Both endpoints allow any origin (CORS `*`); they expose no secrets.

The same server also serves the embedded web bundle, when one was staged into
the binary with `make ui`. On a clean clone run `make web-install` first —
`make ui` runs a Vite build and nothing else installs `web/node_modules`, so
without it the target fails with `vite: not found`:

```bash
make web-install
make ui
```

- `/` → the project overview, which links to these docs and the examples.
- `/builder` → the config builder. It is a **config generator**: drag nodes,
  export the config YAML, and deploy that file yourself. It does not
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

## WASM validator

`cmd/hookway-wasm` is the second command in the repo: the same config validator
and schema, compiled to WebAssembly so the config builder validates
client-side instead of round-tripping to a server.

Build it:

```bash
make wasm
```

That runs `GOOS=js GOARCH=wasm go build ./cmd/hookway-wasm`, writes
`hookway.wasm` and the matching `wasm_exec.js` into `internal/editor/assets/`,
and copies both into `web/public/` where Vite serves them. Both artifacts are
generated and gitignored. `make ui` and `make web-e2e` depend on `wasm`;
**`make web-dev` does not** — start the dev server without it and
`hookway.wasm` 404s, the builder's validation call silently falls back to a
`POST /validate` that is not running, and every in-browser validation appears
to pass. Run `make wasm` before `make web-dev`.

### Browser globals

`main` installs three functions on `globalThis` and then blocks forever so
they stay callable:

| Global | Arguments | Returns |
|---|---|---|
| `hookwayValidate(yaml)` | one string: candidate config YAML | JSON string `{"issues":[...]}` — the same envelope as `validate --json --no-env` |
| `hookwaySchema()` | none | JSON string: the config JSON Schema (`hookway schema`) |
| `hookwayNodeSchemas()` | none | JSON string: the per-node catalog (`hookway schema --nodes`) |

An internal failure (a marshalling error, or a bad argument type) returns a
JSON string with a top-level `"error"` key instead, which is how the JS side
distinguishes it from a validation response's `"issues"`.

All three delegate to `cmd/hookway-wasm/bridge`, a pass-through of
`internal/configapi` that adds **no** validation logic of its own. That is the
point: an in-browser result is byte-identical to the CLI's and to the HTTP
editor's, because all three share one `configapi` entry point. `bridge` has no
`syscall/js` import, so it is unit-testable on the host.

Like the HTTP editor, `hookwayValidate` uses the non-expanding path —
browser-supplied bytes are untrusted, so `${VAR}` is never expanded. See the
[Editor API](#editor-api) note above for why.

### Standalone harness

A single static page (`cmd/hookway-wasm/harness/index.html`, no npm
dependencies) that loads the module and gives you a YAML textarea plus
`hookwayValidate()` and `hookwaySchema()` buttons — useful for exercising the
validator without running Vite or the editor:

```bash
make wasm-harness
```

This rebuilds the WASM (it depends on `wasm`), copies `hookway.wasm` and
`wasm_exec.js` next to `cmd/hookway-wasm/harness/index.html`, and serves that
directory on **http://localhost:8091**. Ctrl-C stops it.

The harness uses `python3 -m http.server`, so **`python3` must be on your
PATH** — it is the one undeclared toolchain dependency in the Makefile, and
the only target that needs it. A plain `file://` open will not work: browsers
refuse to instantiate WebAssembly from a `file:` origin, which is why the
target serves over HTTP at all.
