# Hookway V2 — Working Directory

Scratch/working space for the V2 redesign: a typed-port node graph replacing V1's
hardcoded request pipeline.

**Branch:** `v2` (branched from `main` @ `242c358`)
**Status:** implementation preflight underway; all 11 chunks written. Chunk 0 is pending
source/traffic evidence; no V2 runtime chunk accepted yet. See
[`IMPLEMENTATION.md`](IMPLEMENTATION.md) for assignments, verification and plan corrections.

**Chunk sequence** (`plans/00-INDEX.md` has the dependency graph and per-chunk gates):

| # | Chunk | Gate |
|---|---|---|
| 0 | Spikes | Findings answer S1/S2 |
| 1 | Graph core | Illegal wiring fails to compile |
| 2 | Config + validation | `hookway validate` reports every problem |
| 3 | Minimal serving path | **first traffic** — `curl` gets a 202 |
| 4 | Gates + conformance | **I1-I18 suite passes** |
| 5 | Destinations + fan-out | All 13 types deliver |
| 6 | Sync proxy + transforms | `curl` returns the upstream's response |
| 7 | Code node (goja) | Each sandbox control C1-C7 provably fires |
| 8 | Secrets + observability | Rotation with no restart; per-flow `/ready` |
| 9 | Migration configs | All 7 handlers match the Lambdas on real traffic; **V1 deleted** |
| 10 | Web UI | Export passes `hookway validate` |

Chunk 3 is where V2 first serves traffic. Chunk 4 restores the invariant regression net the
clean break (D4) removed — it is the most skippable and most regretted item in the plan.

## Decisions locked so far

| # | Decision | Chosen | Date |
|---|---|---|---|
| D1 | What V2 optimizes for | Replace `../universal-gateway` **and** be a real production tool | 2026-09-14 |
| D2 | Graph topology model | **Typed ports** — every concern is a node, edges carry typed payloads, illegal wiring is a load-time type error | 2026-09-14 |
| D3 | Code node engine | **Sandboxed JS via goja** (new dependency, pure Go, no cgo) | 2026-09-14 |
| D4 | V1 config compatibility | **Clean break.** No converter, no dual loader. V2 is the only format. | 2026-09-14 |
| D5 | Code layout | **In-place on the `v2` branch.** One binary, `internal/graph` added, `handleWebhook` replaced. | 2026-09-14 |
| D6 | UI scope (Q7) | **Config generator only.** Retarget `web/src/graph/` to the node model, still export-to-YAML. No n8n-style live editor, no server-side flow CRUD. | 2026-09-14 |
| D7 | Secret indirection (Q8) | **In scope.** `secret_ref` with `env` / `file` / `aws_secrets_manager`, cached with timed refresh and rotation overlap. Closes W3 / P0-5. | 2026-09-14 |
| D8 | Execution durability (Q5) | **No per-node checkpointing.** In-memory per request; writes per event stay O(1), never O(nodes). Flows are not resumable. | 2026-09-14 |
| D9 | Readiness (Q6) | **Probe per unique destination; report per flow.** Fixes W1; probe load is O(destinations), not O(flows x destinations). | 2026-09-14 |
| D10 | Replay capture writes (Q9) | **Port `replaystore` to V2 unchanged.** Keep V1's INSERT + UPDATE. No batching, no WAL, no spool. Batching is written down as a deferred optimization with an explicit trigger. | 2026-09-14 |
| D11 | Shared state scope (Q10) | **Pod scope only.** No Redis on the hot path; `rate_limit` and `replay_guard` stay in-process and approximate. W5 persists, documented. | 2026-09-14 |
| D12 | I17 reservation release | **Optional `graph.Compensator` interface.** Executor collects compensations from nodes that succeeded; the **server** invokes them when `err != nil \|\| any delivery failed`, on a detached context. Rejected: context accumulator (a node unit-tested alone would silently no-op) and payload-carried callbacks (lost on the error path). Requires `Flow.Run` to return a populated `Terminal` on error. | 2026-09-14 |

Rejected alternatives for D2: *free DAG* (invariants become linter warnings) and
*constrained spine* (invariants unrepresentable, but auth can never branch and the
graph cannot touch the pre-body phase). See `01-topology-options.md`.

D4 consequence to manage: a clean break orphans V1's ~80 test files, which are currently
the only regression net for invariants I1-I18. Porting those *test cases* (not their
configs) into a V2 conformance suite is a first-class plan item, not a nice-to-have.

## Files

| File | Purpose |
|---|---|
| `00-v1-baseline.md` | What V1 is today, and the invariant inventory V2 must preserve |
| `01-topology-options.md` | The three topology models considered, and why typed ports won |
| `02-design.md` | The V2 design — port lattice, node catalog, fan-out/error semantics, goja sandbox |
| `03-scalability.md` | Write-amplification budget and the rules that keep it O(1) per event |
| `plans/` | **Implementation chunks 0-10.** Start at `plans/00-INDEX.md` |
| `IMPLEMENTATION.md` | Orchestration, dependency gates, verified progress and required plan corrections |
| `spikes/` | Spike findings (created by Chunk 0) |
| `OPEN-QUESTIONS.md` | Unresolved questions and pending spikes |

## Ground rules for this directory

- The user authorized Git writes on 2026-09-14. Stay on `v2`, leave `main` untouched,
  and never sign commits. See `IMPLEMENTATION.md` for the review workflow.
- Open questions live in `OPEN-QUESTIONS.md` and get promoted into decisions above once answered.
