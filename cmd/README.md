# Program entrypoints

Hookway provides two terminal programs: the core ingress service and the
optional replay service. Routing and registration execute inside core flows.

| Directory | Role | Build | First command |
|---|---|---|---|
| `hookway` | Ingress server and config tools. Routing, authentication, transforms, proxy calls and publication execute in configured flows. | `make build` | `bin/hookway help` |
| `hookway-replay` | Optional service for retaining and retrying failed ingress deliveries. Requires PostgreSQL and core replay configuration. | `make build` | `bin/hookway-replay help` |

The production Docker images contain their respective service binary.
`hookway-wasm` builds the browser builder's validator and schema as WebAssembly;
it is not a terminal program. Build that artifact with `make wasm`.

For a source-checkout invocation, use `go run ./cmd/hookway help` (replace
`hookway` with the desired terminal entrypoint). To run ingress, first check
your config with `bin/hookway validate --config config.yaml`, then start
`bin/hookway serve --config config.yaml`.

See [the CLI reference](../docs/cli.md) for flags, working examples, input
formats, environment variables and exit codes.
