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
- Chunk 2 configuration and validation is unlocked and ready for delegation.

## Orchestrator — current handoff

**Status:** Chunk 1 accepted; preparing Chunk 2 implementation slices.

**Accomplished:**

- Established the dependency and verification tracker in `v2/IMPLEMENTATION.md`.
- Re-scoped Chunk 0 and Chunk 9 around Hookway-only implementation per user direction.
- Settled D13 and aligned the design catalog to one shared `publish` node.
- Delegated Chunk 1, independently ran its race/coverage and repository gates, then
  delegated a separate code review.
- Before the review findings, graph tests passed under repeated shuffled race runs with
  91.3% statement coverage; `make test`, `make lint`, and diff checks passed.

**Resolved review findings:**

1. A terminal node can spoof delivery node/target metadata, and a non-detached `respond`
   can inject a delivery.
2. A detached node that reports `Delivery.Err` while returning a nil Go error does not
   stop later destinations under default `stop_on_failure`.
3. Multiple malformed edges appear as one `CompileError.Problems` entry.

The author added four failing-first tests for these findings in
`internal/graph/corrections_test.go`. The review agent implemented the fixes; the
orchestrator then added regressions proving destinations cannot inject responses or mark
an attempted delivery as unattempted.

**Final independent verification:** `go test -race -shuffle=on -count=5
./internal/graph/...`, 91.7% coverage, `make test`, `make lint`, and `git diff --check`
all passed. Review fixes are committed unsigned as `59975bd`.

**Exact next step:** implement Chunk 2 in dependency order. Begin with the shared V1/V2
config decoding seam and the disjoint node catalog/schema table; review those before the
V2 loader, lint, CLI, config API, examples, and final Chunk 2 gate.

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
