# Hookway V2 progress report

This is the shared implementation ledger for the V2 overhaul. The orchestrator and every
subagent must update it before handing work off. Keep entries concrete enough that another
agent can resume without reading chat history.

## Working rules

- Branch: `v2`. Never modify `main`.
- Commits must be cryptographically unsigned. The orchestrator owns staging and commits
  unless an assignment explicitly says otherwise.
- Preserve unrelated work. Record every changed file and every command actually run.
- A worker report is not acceptance. The orchestrator records independent review and gate
  results below.
- Each active assignment gets a named section containing: scope, status, files changed,
  decisions/API changes, verification, unresolved risks, and exact next step.

## Current state

- Accepted graph implementation commits: `0d4cdd3` plus review fixes `59975bd`.
  Git reports both as cryptographically unsigned.
- External gateway/provider migration work is excluded by user direction. Raw-byte auth
  invariant I6 remains unchanged.
- D13 is settled: one `publish` node references the shared top-level destinations map.
- Chunk 1 graph core is independently accepted at 91.7% statement coverage.
- Chunk 2 configuration and validation is accepted at unsigned `9db929f`.
- Chunk 3 minimal serving path is accepted at unsigned `a468a52`.
- Chunk 4 gates and I1-I18 conformance is accepted at unsigned `d7316df`.
- Chunk 5 destinations, fan-out, `on_error`, and SNS/SQS MessageAttributes is
  accepted at unsigned `a722076`.
- Chunk 6 sync proxy and transforms (`http_request`, `respond`, `xml_prune`,
  `body_format: xml`) is accepted at unsigned `cd0288c`.
- Chunk 7 goja `code` node (C1–C7) is accepted at unsigned `2b9b94d`.
- Chunk 8 secrets, per-node metrics, per-flow readiness, and V2 replay (I9/I15)
  is accepted at unsigned `d97b40e`.

## Orchestrator — current handoff

**Status:** Chunk 8 accepted at unsigned `d97b40e`. Next chunk is 9 (V2 cutover
and V1 removal). Do not push.

**Accomplished in this window (Cursor, 2026-09-15):**

- Added `internal/secretref` (env/file/AWS) with rotation overlap and last-good
  refresh failure. Serving injects resolvers; flowconfig stays WASM-safe.
- Per-node Prometheus metrics, Grafana Node Duration panel, correlated logs,
  and OTel spans. Cardinality bounded by config (R3).
- FlowServer `/ready` is per-flow (D9). Default 200 if any flow is ready;
  `readiness_require_all` restores V1 AND-aggregation.
- Replay capture INSERT in `BeforeDetached` (I9); best-effort (I15); redelivery
  republishes recorded destinations without re-entering the graph.
- Marketing Prometheus count updated 42 → 45.

**Chunk 8 gate (local, 2026-09-15):**

- `make test`, `make lint`, `make conformance`: PASS (I9/I15 no longer skipped)
- `go test -race ./internal/secretref ./internal/httpapi ./internal/flowconfig
  ./internal/graph ./internal/metrics ./internal/conformance`: PASS
- WASM `GOOS=js GOARCH=wasm go build ./cmd/hookway-wasm`: PASS
- `make aws-integration-test`: PASS including `./internal/secretref`
- Full `make test-all` not re-run (broker packages unchanged)

**Exact next step:** Chunk 9 (`v2/plans/09-migration-configs.md`). Do not push.

## Orchestrator — Chunk 7 gate (retained)

**Status:** Chunk 7 accepted at unsigned `2b9b94d`. Next chunk is 8 (secrets
and observability). Do not push.

**Accomplished in this window (Cursor, 2026-09-15):**

- Added `internal/codenode` sandboxed goja runtime with named C1–C7 tests.
- Wired `code` node: compile at load, `{body, vars, headers}` copies, no
  `Verified`/secrets/XML, return `{body, vars}`.
- Documented memory gap, interrupt gap, and trusted-operator model (D3 pin,
  D15). Lint timeout warning points at `docs/security.md#code-node`.

**Chunk 7 gate (local, 2026-09-15):**

- `make test`, `make lint`, `make conformance`: PASS (I9/I15 SKIP)
- `go test -race ./internal/codenode ./internal/flowconfig`: PASS
- WASM `GOOS=js GOARCH=wasm go build ./cmd/hookway-wasm`: PASS
- `CGO_ENABLED=0 go build ./cmd/hookway`; goja `CgoFiles` empty
- Docker `FROM scratch` image `hookway:chunk7` built (binary + CA bundle only)
- Trivy HIGH/CRITICAL: 2 pre-existing (amqp091-go CVE-2026-79921, grpc
  CVE-2026-84445). goja introduced no new HIGH/CRITICAL findings.

**Exact next step:** Chunk 8 (`v2/plans/08-secrets-and-observability.md`). Do not push.

## Orchestrator — Chunk 6 gate (retained)

**Status:** accepted at unsigned `cd0288c`.

**Chunk 6 gate (local, 2026-09-15):**
- `go test -race` graph/flowconfig/httpapi/transform/conformance: PASS
- WASM `GOOS=js GOARCH=wasm go build ./cmd/hookway-wasm`: PASS
- `make validate-examples`: PASS (`examples/v2/proxy.yaml` valid with no-rate_limit warning)
- `make test-all`: PASS (queue 337s including LocalStack)
- httptest: proxy PUT returns upstream 418/body; Authorization not forwarded;
  hops incremented; XML prune proxy sends transformed body and returns upstream 202

**Chunk 6 files:** `internal/transform/{xml_dom,xml_prune,changed_fields}.go` (+ tests),
`internal/flowconfig/{node_envelope,node_http_request,node_transform,node_respond,nodes,headers,node_auth}.go`,
`internal/httpapi/{flowserver,flowserver_proxy_test}.go`, `internal/auth/registry.go`,
`examples/v2/proxy.yaml`, `Makefile`,
`v2/README.md` (D14), `v2/plans/06-sync-proxy-and-transforms.md`.

**Risks / skipped:** UG JS differential tests for `changed_fields` and
`xml_prune` (UG excluded). `http_request.auth.oauth2` not in this chunk.
I9/I15 still skipped until Chunk 8.

**Exact next step:** Chunk 7 (`v2/plans/07-code-node-goja.md`). Do not push.

## Orchestrator — Chunk 5 gate (retained)

**Status:** accepted at unsigned `a722076`.

**Chunk 5 gate (local, 2026-09-15):**

- `make test`, `make lint`, `make conformance`: PASS (I9/I15 SKIP until Chunk 8)
- `go test -race` graph/flowconfig/httpapi/queue/conformance: PASS
- WASM `GOOS=js GOARCH=wasm go build ./cmd/hookway-wasm`: PASS
- `make test-all`: PASS (queue 391s including LocalStack SNS filter-policy test)
- httptest: skip_failed partial **202** with `failed_destinations`; all-fail **503**;
  partial success then retry **202 not 409** (I17); full success then resend **409**

**Files:** `internal/graph/{compile,executor,model,payload,clone}.go`,
`internal/graph/executor_policy_test.go`, `internal/flowconfig/node_publish.go`,
`internal/flowconfig/message_attributes.go`, `internal/flowconfig/node_publish_types_test.go`,
`internal/httpapi/flowserver.go`, `internal/queue/{publisher,sns,sqs,message_attributes}.go`,
`internal/queue/aws_integration_test.go`.

## Subagent — Chunk 1 implementation (`spike_s2`)

**Status:** Implementation committed at `0d4cdd3`; follow-up interrupted by usage limit.

**Files:** `internal/graph/**`, `v2/plans/HANDOFF.md`, plus the roadmap/design files listed
in that commit.

**Accomplished:** typed ports with strict equality; node registry; aggregated compiler;
sequential default execution; detached destination contexts; terminal responses and
delivery outcomes; branch payload cloning; panic isolation; executor-owned compensation;
label/reconvergence rejection; failing-first tests and handoff documentation.

**Verification before review:** graph race tests passed with 91.3% coverage; repository
tests and lint passed. Those results predate the four uncommitted review regressions.

## Subagent — Chunk 1 review (`graph_review`)

**Status:** Complete and accepted in unsigned commit `59975bd`.

**Files:** `internal/graph/executor.go`, `internal/graph/compile.go`,
`internal/graph/corrections_test.go`.

**Accomplished:** fixed the three accepted review findings. Only detached nodes can now
contribute delivery outcomes; the executor replaces node-supplied delivery identity and
target with compiled config metadata while preserving error/unattempted outcome fields.
A detached delivery outcome error is promoted to the default run error, stops sequential
successors, and reaches `OnNode`. Joined edge-parser diagnostics are flattened into separate
`CompileError.Problems` entries with accurate counts and bullets. Updated the older terminal
metadata regression to source its target through `DeliveryTarget`.

**Verification:** the four focused regressions first failed for the intended reasons and
then passed. `go test -race -cover ./internal/graph/` passed at 91.7% statement coverage;
`make test` passed; `make lint` passed; `git diff --check -- internal/graph progress_report.md`
passed.

**Risks:** `DeliveryTarget` remains optional, so an unattempted destination without it has
an empty target by design. Chunk 5 must make delivery-outcome error promotion conditional
when it adds `skip_failed`; the current behavior is the required default `stop_on_failure`.

**Exact next step:** orchestrator reviews this diff, reruns the Chunk 1 gate, updates the
control record, and creates the unsigned Chunk 1 commit if accepted.

## Subagent — Chunk 2 preflight (`config_preflight`)

**Status:** Complete; no files changed.

**Accomplished:** mapped the safe Chunk 2 slices and current API differences. Key findings:

- Extract shared YAML raw decoding/defaulting/validation rather than decoding runtime
  config structs directly.
- Add severity to `config.ValidationIssue` while treating empty as error for V1.
- Retain server, security, metrics, logging, replay, and shared destinations in V2; omit
  the V1 global rate limit.
- Use one 17-type node definition table to drive registration and node schemas.
- Route both config API validation paths and CLI schema arguments by `schema_version`.
- Emit node schema metadata as `{input, output, detached, schema}` and implement `--strict`.

**Exact next step after Chunk 1 acceptance:** delegate the shared config extraction and
node catalog/schema as disjoint slices, review both, then implement the V2 loader/lint and
CLI/config API surfaces in dependency order.

## Earlier investigation agents

The S1/S2 external migration findings are retained under `v2/spikes/` as historical design
notes. They no longer block implementation. `plan_audit` supplied the corrections recorded
in `v2/IMPLEMENTATION.md`; its later implementation assignment did not start because that
agent hit its usage limit.

## Subagent — Chunk 2 shared config (`config_preflight`)

**Status:** Complete; review findings fixed; used by the V2 loader.

**Implemented files:** `internal/config/shared.go`, `internal/config/shared_test.go`, and
focused edits to `internal/config/config.go`, `internal/config/validation.go`, and
`internal/config/schema.go`.

**Result:** Added `SharedConfig`, `DecodeSharedYAML`, shared raw YAML DTO conversion and
defaults, `SharedConfig.ValidationIssues`, severity constants/default compatibility, and
`NewSchemaReflector`. V1 `convert` now uses the same shared conversion path while retaining
its global rate limit, endpoint parsing, and destination/endpoint presence requirements.
The shared V2 seam excludes the global rate limit and permits zero destinations for
respond-only flows. Endpoint validation now sorts names before checking shared target
settings, making fallback conflict diagnostics deterministic.

**Tests:** Added coverage for V1/shared conversion equivalence, snake_case YAML, all shared
sections, duration conversion failures and defaults (including inflight `0` to `256`),
destination conversion/defaults/validation, strict nested and top-level unknown fields,
allowed version-specific top-level fields, zero destinations, V1 presence requirements,
and empty-severity compatibility. The fallback regression passed 20 shuffled package runs.

**Commands:** `go test ./internal/config -count=20 -shuffle=on`; focused config/config API/
CLI/wasm tests and vet; focused race tests; `make test`; `make lint`; and
`git diff --check` all pass.

**Risk:** `DecodeSharedYAML` deliberately does not inspect the contents of caller-approved
top-level extras; the V2 loader must strictly decode `schema_version` and `flows` itself.
The shared raw DTOs use unnamed struct aliases so the existing V1 schema remains inlined.

**Exact next step:** done. `flowconfig.LoadBytes` now calls
`DecodeSharedYAML(data, source, "schema_version", "flows")` and combines shared
validation with flow compile/lint issues.

## Subagent — Chunk 2 node catalog (`graph_review`)

**Status:** Complete; review findings fixed; used by the V2 loader.

**Implemented files:** `internal/flowconfig/nodes.go`,
`internal/flowconfig/nodes_test.go`, `internal/flowconfig/schema.go`, and
`internal/flowconfig/schema_test.go`.

**Result:** Added one private 17-entry definition table that drives both registry creation
and node schema metadata. The table covers the exact V2 catalog and strict port lattice;
`publish` is the only detached type and references shared destinations. Every type has a
future-facing option struct and a strict decoder. The node constructors enforce settled
required and exclusive options, and every runtime stub returns a node-specific
not-implemented error. Generated metadata exposes `input`, `output`, `detached`, and
`schema`, reuses `config.NewSchemaReflector`, and keeps authored durations as strings.

**Tests:** Catalog exactness, ports, detached boundary, per-type options/schema coverage,
YAML option round trips, unknown-field rejection, representative required/exclusive
contracts, schema JSON round trips, required schema fields, secret-ref shape, and duration
field types are covered. The failing-first run initially failed on the absent catalog API.

**Commands:** `go test ./internal/flowconfig ./internal/graph`;
`go test -race ./internal/flowconfig ./internal/graph`; `make test`; `make lint`; and
`git diff --check` all pass.

**Risks:** Runtime behavior intentionally remains stubbed for later chunks. Generic
`message_attributes` values are settled in Chunk 5 (String / Number / String.Array).

**Exact next step:** done. The V2 loader decodes flow nodes through this registry.

## Subagent — Chunk 2 shared config review (`shared_config_review`)

**Status:** Findings fixed in this window; seam used by `flowconfig.LoadBytes`.

**Reviewed files:** `internal/config/shared.go`, `internal/config/shared_test.go`, and the
focused diffs in `internal/config/config.go`, `internal/config/validation.go`, and
`internal/config/schema.go`. Review snapshot hash for those diffs:
`ef4b24490999d1fb30b295bb4ead22a09b63d3e443d55804708ac92220b245fa`.

**Findings:**

1. **High — `internal/config/shared.go:112-116`: strict decoder errors short-circuit issue
   aggregation.** `yaml.Decoder.Decode` can populate the known fields while returning a
   `*yaml.TypeError` for unknown fields or scalar type mismatches, but this branch discards
   that partial raw config and returns immediately. A document containing a nested typo
   plus invalid durations or other semantic errors therefore omits the conversion and
   shared-validation problems. This conflicts with Chunk 2's `invalid-many` requirement to
   list all problems. Continue conversion for recoverable `*yaml.TypeError` values, append
   their issues, and reserve the immediate return for unrecoverable YAML syntax/shape
   errors. Add a regression combining a nested unknown field, an invalid duration, and an
   independent semantic shared-config error.
2. **Medium — `internal/config/shared.go:119-124` and
   `internal/config/shared_test.go:144-166`: nested strict-decode failures are anchored to
   `Field: "config"`.** The V2 plan requires paths precise enough for the editor to click;
   `server.tyop` and `destinations.outgoing.webhook.tyop` currently both become a generic
   config issue. The test asserts only that some issue exists, so it does not protect the
   field-path contract. Emit one issue per strict-decode problem with its dotted field path
   and assert the exact path for nested server and destination typos.
3. **Medium — `internal/config/validation.go:38-45,73-75,105-107`: shared V2 issues expose
   empty `Severity` values.** `EffectiveSeverity()` interprets empty as error, but
   `DecodeSharedYAML` and `SharedConfig.ValidationIssues()` return the structs directly and
   no API enforces that downstream callers normalize them. The V2 loader contract explicitly
   requires structural/compile issues with `Severity: "error"`; inspecting or serializing
   these shared issues directly yields `""`. Normalize issues at the shared V2 boundary (or
   make the issue constructor explicit while preserving any required V1 compatibility) and
   add tests that assert the returned `Severity` field itself.

**V1 and schema assessment:** No V1 loading regression or JSON Schema drift was found in
the shared conversion extraction. The only V1 validation behavior change is deterministic
endpoint iteration in `validation.go`; it keeps acceptance/rejection semantics and removes
map-order-dependent diagnostics.

**Verification:** `go test ./internal/config -count=20 -shuffle=on`; focused schema/shared
tests with verbose output; 20 shuffled runs of the V1 load/validate/config/fallback tests;
`go test -race ./internal/config ./internal/configapi ./cmd/hookway
./cmd/hookway-wasm/bridge`; focused `go vet`; `make validate-examples`; `make test`;
`make lint`; and `git diff --check` all passed. Every shipped V1 example validated.

**Unresolved risks:** None remaining from the three review findings. `DecodeSharedYAML`
still does not inspect caller-approved extras (`schema_version`, `flows`); the V2 loader
owns those.

**Exact next step:** done. Aggregation, dotted paths, and explicit error severity are
covered by `TestDecodeSharedYAMLAggregatesUnknownDurationAndSemanticIssues` and
`TestSharedValidationIssuesSetExplicitErrorSeverity`.

## Subagent — Chunk 2 node catalog review (`node_catalog_review`)

**Status:** Review started in Codex and finished in this Cursor window.

**Findings (from the adversarial probe left in
`internal/flowconfig/review_adversarial_test.go`):**

1. **High — schema vs runtime gap.** Generated schemas advertised `minimum` / enum
   constraints that constructors did not enforce: negative `max_hops`, `burst`,
   `max_entries`, `max_output_bytes`, `max_call_stack`; `secret_ref.provider: vault`;
   `source.kind: cookie`; empty `source.key`.
2. **Medium — exclusive contracts missing from JSON Schema.** Runtime already required
   exactly one of `secret`/`secret_ref` and `status`/`passthrough`, but `NodeSchemas()`
   omitted `oneOf`, so the editor contract was weaker than compile-time validation.

**Fixes:** constructors reject the invalid options; `NodeSchemas` adds `oneOf` for `auth`
and `respond`. The adversarial tests now pass.

**Exact next step:** done; catalog is the registry used by LoadBytes/Compile.

## Subagent — Chunk 2 loader, lint, and CLI (`cursor_chunk2`)

**Status:** Complete; local Chunk 2 gate passed.

**Files:** `internal/flowconfig/config.go`, `config_test.go`, `lint.go`, `lint_test.go`,
plus CLI/config API/examples/Makefile listed in the orchestrator handoff.

**Result:**
- `DetectSchemaVersion` / `LoadBytes` / `ValidateBytes` decode shared config, compile
  every flow through the 17-node registry, aggregate compile problems, and reject
  duplicate trigger paths and unknown `publish.destination` ids.
- `Lint` implements the presence rules (missing rate_limit, no_auth, code timeout, body
  cap vs server, lookup cache, missing terminal, parallel respond, missing destination).
  Labelled switch branches are not treated as parallel fan-out.
- CLI `validate` routes on `schema_version`; `--strict` promotes warnings; `--json` keeps
  the existing response shape and preserves V2 warning severity. `schema --nodes` emits
  the catalog. V1 `validate` is unchanged.
- Examples: `examples/v2/github.yaml` (valid), `invalid-gate-order.yaml`,
  `invalid-many.yaml`. Makefile `validate-examples` covers the valid V2 file.

**Risks:** Node `Run` bodies are still stubs (`errNotImplemented`); this chunk cannot serve
traffic. Missing-destination diagnostics can appear twice (LoadBytes + Lint). Labelled
edges still fail compilation until Chunk 4 switch semantics exist.

**Exact next step:** Chunk 3 serving path.

