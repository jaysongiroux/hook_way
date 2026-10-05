# Extending Hookway

Use the existing module that owns the behavior you are changing. The builder
writes YAML; the console operates a replay service. Shared code handles
presentation, API types, and local validation. Servers handle credentials,
databases, permissions, and recovery actions.

## Code ownership

| Module | Use it for |
| --- | --- |
| `internal/flowconfig` | Node options, defaults, validation, and configuration catalogs |
| `internal/apicontract` | OpenAPI generated from Go API types and operation metadata |
| `internal/uicontract` | Pure input/sample validation shared by HTTP and WASM |
| `web/src/shared/ui` | Theme, branding, common fields, alerts, and loading/error states |
| `web/src/shared/contracts` | Generated API types and separate editor/replay clients |
| `web/src/shared/wasm` | Go validator loading, capabilities, and retry after load failure |
| `web/src/graph` | Builder graph, YAML conversion, workspace, and forms |
| `web/src/console` | Session, failure inbox, evidence, and recovery actions |

Shared UI components accept values and callbacks; they do not import the builder
store. Operator tokens stay in browser memory. Clients omit ambient browser
credentials, and each service has its own bearer-token callback.

## Contracts and generation

Change the Go source, then regenerate its browser contracts:

```sh
make node-schema                       # node and workspace catalogs
make api-schema                        # OpenAPI and TypeScript API types
make node-schema-check api-schema-check # compare without rewriting files
```

Specifications live in `docs/api/`; API types live in
`web/src/shared/contracts/generated/`. Do not edit generated files directly.
The checks include unstaged working-tree changes.

Keep WASM dependencies limited to pure configuration and validation code.
Dependency tests reject native database and queue SDKs. The public static builder
uses WASM; the optional Go-hosted editor can also use its editor API. The console
can explicitly use read-only API input validation if WASM is unavailable. None
of these previews grants permission or replaces server checks of live state.

`make console-ui` builds the standalone console and public builder, including
matching WASM assets. `web/Dockerfile` packages that bundle. It excludes marketing
code and external fonts. `make ui` builds the optional embedded local editor;
`make build` builds native binaries without requiring Node.

## Add or change a node

Start in `internal/flowconfig`: define typed options, validation/defaults and the
catalog/schema metadata together. Register through the existing node helpers;
preserve typed input/output stages and compiler placement checks. Put optional
schema customizations, effective form defaults, stage ports, body-format checks
and error-edge exceptions in that entry's `nodeMetadata`. Set a positive
`batchChildOrder` only for supported batch children; the Go admission check and
generated builder choices read the same value. No separate node allowlist is
needed in the batch editor. Inject native
providers using registry options rather than importing database SDKs into the
pure graph/configuration packages. Keep side effects and reservation compensation
explicit; a generic mutation interface cannot prove which effects occurred.

For data validation, reuse the Go engine and expose a bounded pure preview through
`internal/uicontract` and the WASM bridge only when useful. Do not expand runtime
environment secrets or open native stores during authoring validation. Keep
precision-sensitive JSON numbers in their original representation in the UI.

Run `make node-schema` to generate the node/workspace catalogs, then
`make node-schema-check api-schema-check`. Add tests for invalid options, stage
placement and observable behavior, plus native/WASM parity for pure logic. Use a
real builder form test when the change affects persisted drafts, mode switching,
field visibility or repair navigation. Avoid a second hand-maintained schema.

For the builder, add one record in `web/src/graph/nodeDescriptors.ts`. It owns
the node's label, icon, help, starting options, summary, field presentation and
editor choice. The record is checked against the generated Go catalog, so a
missing or misspelled node fails TypeScript compilation. Reuse an existing
editor family (`schema` renders the generated fields); add a dedicated editor
only for interactions those forms cannot express. Palette, canvas, defaults
and form routing read the descriptor without separate node-type lists.
Starting options are fresh authoring values, distinct from effective runtime
defaults in the Go-generated schemas.

## Add an API operation

Define named request/response DTOs and operation metadata beside the service
contract, then implement strict request decoding, scopes and authoritative live
checks in its handlers/store transactions. Generate via `make api-schema`; check
with `make api-schema-check`. Browser code uses the generated client under
`web/src/shared/contracts`, not duplicate fetch/response shapes.

Pure action validation may run in Go WASM or the explicitly advertised API
fallback. It does not grant permission or verify retained database state. Test
status/media-type/problem codes, scopes, idempotency and concurrent transitions.
Reuse `internal/problem` for RFC 9457 errors and its stable code member. The
editor's typed `validationHandler` shares bounded YAML transport; service-specific
validation and live mutation checks stay separate.
A lost mutating response is uncertain; preserve the original submission body and
idempotency key instead of silently creating fresh work.

## Add a UI component or error

Use `web/src/shared/ui` and its theme/tokens for elements used across builder and
console. Keep feature-specific forms and live operations in their respective
modules. Extract repeated presentation when both consumers need it; do not build
an external component package or a generic form framework for a single use.

Map structured validation issues to human-readable field labels and inline
errors, preserve entered values, and focus the first invalid field. Session/request
errors go through the console error formatter. Suppress only expected cancellation;
show interrupted reads and retain uncertain mutation state. Test desktop/mobile
widths, keyboard access and actual API/WASM behavior for the affected workflow.

## Add a native adapter

Reuse `tlsfiles`, `pgtls` and `redisconn` for shared connection primitives while
keeping consumer-specific operations, timeout/retry policy, schema and migration
ownership local. Do not implicitly migrate during serving. Validate/ping configured
protection providers before listeners start; after startup, fail closed, withdraw
readiness and preserve liveness during recoverable outages.

Extend shared protection conformance for every supported backend and algorithm.
Include ownership/expiry, capacity, uncertain writes and state-preserving policy
updates. Native acceptance must exercise independent processes where fleet-wide
behavior matters. Use the exact fixture-owned container for injected faults; never
search the host and pause unrelated services. Record the tested source commit, backend image versions, and test output.
Use [Contributing](../../CONTRIBUTING.md#make-and-check-a-change) to choose integration
checks. Rehearse failover and recovery against the topology you plan to deploy.
