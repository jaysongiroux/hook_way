# V2 implementation control

## Authority and workflow

The user authorized implementation orchestration and Git writes on 2026-09-14.
Work stays on `v2`; never modify `main`, and never sign commits. This supersedes
older plan text requiring permission for each commit. Use explicit unsigned commits
after verification, stage only reviewed files, and leave unrelated work intact.

The orchestrator assigns bounded chunks to Codex subagents, reviews their changes,
runs the verification gate independently, and records the result here before
unlocking dependencies. An implementer's report alone does not close a gate.
Workers share the checkout: assign disjoint file ownership, serialize overlapping
edits, and reserve Git mutations for the orchestrator.

Before dispatch: reread the chunk against current interfaces and locked decisions,
identify acceptance tests and excluded work, and supply relevant predecessor findings.
Before acceptance: inspect the diff, verify failure cases and applicable invariants,
run the chunk gate plus `make test` and `make lint`, and record any skipped checks.
Missing evidence means pending, never passed. Plans require tests to fail for the
intended reason before implementation; workers must report that evidence.

## Dependency schedule

| Chunk | State | Unlock condition |
|---|---|---|
| 0 Spikes | Closed for current scope; external migration work excluded by user | Existing Hookway design constraints retained |
| 1 Graph core | Accepted through `59975bd` | Race tests, 91.7% coverage, illegal gate wiring rejected |
| 2 Config and validation | Accepted through `9db929f` | Valid configs accepted; all invalid-config problems reported |
| 3 Minimal serving | Accepted through `a468a52` | Signed live request delivered with 202; invalid signature rejected |
| 4 Gates and conformance | Accepted through `d7316df`; I9/I15 skipped until Chunk 8 | Applicable invariant tests and gate rejection tests pass; track deferred I9/I15 explicitly |
| 5 Destinations and fan-out | Accepted through `a722076` | All 13 types deliver through V2; integration gates pass |
| 6 Proxy and transforms | Implemented; local unsigned commit after gate | Upstream response, method, path, query and transforms verified |
| 7 Code node | Waiting for 6 | C1-C7 individually exercised; race and dependency checks pass |
| 8 Secrets and observability | Waiting for 6 | Rotation, per-flow readiness, bounded metrics; I9/I15 pass |
| 9 V2 cutover and V1 removal | Waiting for 7 and 8 | V2 feature/conformance gates pass before V1 removal |
| 10 Web UI | Waiting for 9 | Export validates; import/export round-trip and visual checks pass |

S1 and S2 are independent. Chunks 4 and 5 may overlap only after shared interfaces
are settled and file ownership is separated; acceptance of 5 still requires 4.
Chunks 7 and 8 can overlap with coordinated registry changes. UI remains last.

## Initial verification

- Starting commit: `40d2d5c` (`feat: v2`); branch `v2`; clean worktree.
- Local Go and Docker available.
- `make test && make lint`: PASS on the starting implementation, 2026-09-14.
  This is the unit/example target and Go vet, not the container integration suite.
- No V2 runtime implementation accepted yet.

## Open evidence and plan reconciliation

1. The user removed universal-gateway and DigiFi work from the implementation scope on
   2026-09-14. Their source, captured-traffic, parity, and migration-config gates no
   longer block Hookway V2. Preserve Hookway's raw-byte authentication invariant I6;
   do not add a provider-specific exception.
2. Q11 is resolved by D13: one `publish` node references shared destinations. The design
   catalog and open-question record now match the later implementation plans.
3. Chunk 4 explicitly skips I9 and I15 until Chunk 8. Report this honestly; the full
   I1-I18 gate cannot be called complete while those tests are skipped.
4. Design section 9 calls Q5-Q8 unresolved, although README decisions D6-D9 and
   `OPEN-QUESTIONS.md` settle them. The locked decisions take precedence.
5. No I6 exception is in scope. A port-type change still requires design review because
   all later chunks depend on strict equality.

## Corrections required before dispatching implementation

The plans contain illustrative implementations, not verified code. Preserve the
intended contracts rather than copying a defective example. Add regression cases
for these independently inspected issues:

- **Chunk 1, terminal results:** the sample `execution.node` records only node id
  and error, discarding a node's returned `Terminal`; `Flow.Run` never returns its
  response. Preserve actual delivery metadata and explicit responses, do not count
  `respond` as a delivery, and handle branch termination without losing compensation.
  See `plans/01-graph-core.md` Task 7, especially the sample `node` and `Run`.
- **Chunk 1, labelled edges:** edge parsing supports labels before execution does.
  Reject unsupported labelled wiring at compile time until its semantics are
  implemented; never execute all labelled successors as ordinary fan-out.
- **Chunks 1 and 5, failure policy:** the proposed parallel `stop_on_failure`
  executor conflicts with V1's sequential stop and Chunk 5's required behavior.
  Preserve sequential stop with succeeded, failed and unattempted outcomes;
  reserve bounded parallel delivery for `skip_failed`. Test that later publishes
  are not called after the first failure under `stop_on_failure`.
- **Chunk 1, payload ownership:** siblings currently receive shared maps/slices.
  Specify immutable inputs with copy-on-write or safe branch cloning before
  implementing concurrent processing; race-test sibling mutations and nested data.
- **Chunks 1 and 2, diagnostic aggregation:** the supplied edge parser stops at
  the first malformed edge. Collect independent malformed-edge and node problems
  so one validation invocation reports all actionable problems.
- **Chunk 2, shared config:** V1 runtime structs are not its YAML decoding contract.
  `internal/config/config.go` uses private `rawConfig` and `convert` to parse
  durations, apply defaults, and convert destinations. Extract/reuse that machinery
  without changing V1 behavior. Test snake_case options, duration errors, default
  inflight limits and destination validation through the V2 loader.
- **Chunk 4, branch selection:** `SelectedPort()` on a shared node instance cannot
  safely hold per-request selection. Return selection as part of an individual
  execution's result, and test concurrent requests selecting different branches.
- **Chunk 4, I8:** `filter` and `lookup` both use `Envelope` ports, so strict equality
  alone cannot prevent a lookup before a filter. Add explicit structural validation
  for the invariant and tests showing a filtered request performs no lookup or
  variable resolution. Keep the port equality rule unchanged.
- **Chunk 4, inflight test:** a configured zero defaults to 256; it does not reject
  all requests. Saturate a capacity-one server and assert a second request is
  rejected without reading its body, using the existing V1 hardening test pattern.

The Chunk 1 header references unavailable `superpowers:*` skills. Use the available
Codex subagent workflow described above to perform its task-by-task TDD and review;
do not claim that an unavailable skill was invoked.

## Delegations

- `spike_s1`: DigiFi signature evidence and fixture handling; owns S1 findings and
  fixture ignore entry, no production code.
- `spike_s2`: seven provisional graphs and lattice findings; owns S2 findings,
  proposes design amendments to the orchestrator.
- `plan_audit`: read-only dependency/interface audit of Chunks 1-4.

## Preflight review outcome — 2026-09-14

- S1 findings reviewed against the current raw-body verifier. Independently reproduced
  its five synthetic UTF-8 comparisons and verified the fixture ignore rule. Actual
  DigiFi sample count remains **0/20**; no signature compatibility conclusion.
- S2's seven proposed graphs reviewed against the design and gap analysis. Existing
  payload types cover the documented shapes; source-dependent values are placeholders.
  No new port type, changed assignability, or I6 exception adopted.
- Independently parsed all seven S2 YAML blocks and checked node references, port
  equality and terminal leaves after the worker fixed unquoted labelled edges.
  This validates the paper graphs' syntax and wiring, not a V2 configuration schema
  or live handler equivalence. `git diff --check` also passed.
- Plan-audit findings above checked against actual code and plan examples. These are
  acceptance requirements for future workers, not fixes already implemented.
- The user excluded universal-gateway and DigiFi work after this preflight. Chunk 1 is
  now dispatched. The findings remain historical design notes and are not implementation
  dependencies. No V2 production code had been written at dispatch time.

## Chunk 1 acceptance — 2026-09-15

- Implemented typed ports, payloads, registry, compiler, sequential default executor,
  detached destination contexts, outcome aggregation, panic isolation, payload cloning,
  and D12 compensation collection.
- Independent review found and fixed delivery-metadata spoofing, non-detached delivery
  injection, outcome-only failures that did not stop successors, and under-counted edge
  parse diagnostics. Acceptance added executor ownership of response/delivery boundaries
  and attempted state.
- `go test -race -shuffle=on -count=5 ./internal/graph/...`: PASS.
- `go test -cover ./internal/graph/...`: PASS, 91.7% statement coverage.
- Illegal gate-after-body wiring tests: PASS and name `RequestHead` and `RawRequest`.
- `make test`, `make lint`, and `git diff --check`: PASS after final changes.
- Implementation is in `0d4cdd3`; review fixes are in unsigned commit `59975bd`.

## Chunk 2 local gate — 2026-09-15

- Shared YAML seam continues conversion after recoverable type errors, reports nested
  unknown fields with dotted paths, and returns explicit `Severity: "error"`.
- 17-node catalog constructors match advertised schema bounds/enums; `auth`/`respond`
  schemas encode exclusive contracts.
- `hookway validate` routes by `schema_version`. Valid V2 example exits 0. Illegal
  gate-after-body names `RequestHead` and `RawRequest`. `invalid-many` lists unknown type,
  duplicate id, dangling non-Terminal leaf, and missing destination. `schema --nodes`
  lists 17 types. V1 `examples/config.yaml` still validates.
- `make test`, `make lint`, `make validate-examples`, `git diff --check`: PASS.
- Implementation is in unsigned commit `9db929f`.

## Chunk 3 local gate — 2026-09-15

- Five runtime nodes: `http_trigger`, `read_body`, `auth`/`no_auth`, `envelope`,
  `publish`. `rate_limit` is an identity pass-through so `examples/v2/github.yaml`
  can serve until Chunk 4 implements actual limiting.
- `FlowServer` is a new HTTP path (V1 `server.go` unchanged). `runServe` routes on
  `schema_version`. Publish failures map to 503 via `graph.NodeError`. Compensation
  is invoked on the error path even though no Compensator exists yet.
- Live curl against `examples/v2/github.yaml` (with `GITHUB_WEBHOOK_SECRET=testsecret`
  and `WEBHOOK_URL=https://example.com/hooks`): valid GitHub HMAC → HTTP 202
  `{"status":"accepted",...}`; `sha256=deadbeef` → HTTP 401. httptest covers 413,
  404, 405+Allow, panicking node 500, and publish-failure 503.
- `make test`, `make lint`, WASM validator build: PASS. V1 handler tests still pass.

## Chunk 4 local gate — 2026-09-15

- Gate nodes are real: `loop_guard`, `ip_allowlist`, `rate_limit` (pod-scoped, D11),
  `replay_guard` with `graph.Compensator`, `filter`, `lookup` (injected resolver; no
  `internal/lookup` import from flowconfig), `switch` with labelled ports.
- Branch selection lives on `graph.Envelope.Branch`, not on the shared node.
  Compile validates labelled edges via `PortDeclarer`. The executor follows only
  matching `FromPort` edges; a missing port is an error, not a silent drop.
- FlowServer maps 400/401/403/409/413/422/429/503, releases reservations on
  publish failure (I17), keeps them on filter silent drop, and still registers a
  compile-broken flow's path as 500 not 404 (I13).
- `internal/conformance` has one named test per I1-I18. I9 and I15 are skipped
  with pointers to Chunk 8. `make conformance` runs in CI next to `make test`.
- `make test`, `make lint`, `make conformance`, race on graph/flowconfig/httpapi/
  conformance, WASM validator build: PASS. V1 still serves.
- Next after Chunk 5: Chunk 6 synchronous proxy and transforms
  (`v2/plans/06-sync-proxy-and-transforms.md`).

## Chunk 6 local gate — 2026-09-15

- `http_request` is Envelope→Envelope (not Terminal/Detached). Shared pooled
  client; no redirect follow. Templated `{rest}`, `forward_method`/`forward_query`,
  timeout→504, non-2xx passthrough, `error_on_status: [5xx]`→502. Increments
  `X-Hookway-Hops`. Never forwards inbound `Authorization` unless listed in
  `forward_headers`. `body: raw|transformed` (default raw).
- `respond` passthrough copies `Envelope.Upstream` and strips hop-by-hop plus
  `Set-Cookie`. Missing upstream is 502.
- `methods: [ANY]` skips 405. `header_exact` aliases `exact`. `auth.NewVerifier`
  infers AuthType from the scheme when empty.
- `transform` kinds: `changed_fields` (status promotion), `field_map`, `xml_prune`.
- `body_format: xml` stores a cloneable `transform.Document` on `Envelope.Body`
  and keeps raw bytes on `Verified` (I6). Invalid XML is 400.
- `xml_prune` runs the five ordered rules from S2. DOM is in-tree over
  `encoding/xml` (D14). Universal-gateway JS/fixtures are not in this repo;
  predicates are documented and tested with synthetic MISMO-shaped XML that
  exercises empty leaves, empty parents, hardcoded parties, blank general
  partners, and orphaned relationships. Per-rule element counts are logged.
- `examples/v2/proxy.yaml` is the curl-gate flow; `{rest...}` paths must be quoted.
- OAuth on `http_request.auth.oauth2` remains S2/Chunk-later; not required for
  the proxy gate.
- `make test`, `make lint`, `make conformance`, race on graph/flowconfig/httpapi/
  transform/conformance, WASM validator build, `make validate-examples`,
  `make test-all`: PASS. V1 still serves. UG JS differential tests skipped.

## Chunk 5 local gate — 2026-09-15

- One `publish` node still references shared `destinations:` (D13). A catalog
  guard fails if `internal/queue` grows a type V2 does not list.
- `delivery_failure_policy: skip_failed` attempts every detached successor;
  HTTP 202 with `failed_destinations` when at least one succeeds, 503 when none
  do. Partial success releases the replay reservation (I17). Filter drops stay
  202 without `failed` (I14). `stop_on_failure` still stops and records
  unattempted destinations.
- Reserved `on_error` ports route the failing node's **input** (compile-checked
  against `from.In`, not `from.Out`). A node with only `on_error` successors is
  a compile error.
- SNS/SQS `MessageAttributes` are rendered from `publish.message_attributes`
  (string / number / `String.Array`, including `{data_type, value}`). Oversized
  values fail closed. Universal-gateway live filter-policy inventory is out of
  scope; the implemented contract is gaps A4's `fields_changed` String.Array.
- `skip_failed` fan-out is sequential; `FanoutConcurrency` remains a reserved
  bound for later parallel dest fan-out.
- `make test`, `make lint`, `make conformance`, race on graph/flowconfig/httpapi/
  queue/conformance, WASM validator build, `make test-all`: PASS. V1 still serves.

