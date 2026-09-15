# V2 Implementation Chunks — Index and Dependency Graph

Waterfall. Each chunk depends on the one before it and ends at a **verification gate** you
can run yourself. No chunk is "done" until its gate passes.

**Grounding docs (read before any chunk):** `v2/02-design.md` (the design),
`v2/03-scalability.md` (write budget), `v2/00-v1-baseline.md` (invariants I1-I18,
weaknesses W1-W5), `v2/README.md` (decisions D1-D11).

## Sequence

| # | Chunk | Verification gate | Traffic? |
|---|---|---|---|
| 0 | [Spikes](00-spikes.md) | Two findings docs answer their questions; any lattice amendment is folded into `02-design.md` | no |
| 1 | [Graph core](01-graph-core.md) | `go test -race ./internal/graph/`; a gate wired after `read_body` fails to compile | no |
| 2 | [Config + validation](02-config-and-validation.md) | `hookway validate --config <v2.yaml>` accepts valid flows and reports **every** problem in invalid ones | no |
| 3 | [Minimal serving path](03-minimal-serving-path.md) | `curl` a signed webhook at a running V2 binary; it is delivered and returns 202 | **yes — first traffic** |
| 4 | [Gates, processing, conformance](04-gates-and-processing.md) | The I1-I18 conformance suite passes; every gate node demonstrably rejects what it should | yes |
| 5 | [Destinations + fan-out](05-destinations-and-fanout.md) | All 13 destination types deliver under V2; `make test-all` green | yes |
| 6 | [Sync proxy + transforms](06-sync-proxy-and-transforms.md) | `curl` through a proxy flow returns the **upstream's** status/headers/body | yes |
| 7 | [Code node (goja)](07-code-node-goja.md) | Each sandbox control C1-C7 has a test proving the control actually fires | yes |
| 8 | [Secrets + observability](08-secrets-and-observability.md) | Secret rotation works with no restart; `/ready` reports per-flow; per-node metrics present | yes |
| 9 | [V2 cutover](09-migration-configs.md) | Hookway V2 feature/conformance gates pass; V1 is removed | yes |
| 10 | [Web UI](10-web-ui.md) | UI exports a config that `hookway validate` accepts | yes |

```
0 spikes
  └─> 1 graph core
        └─> 2 config + validation
              └─> 3 minimal serving path   <-- first traffic
                    ├─> 4 gates + conformance
                    │     └─> 5 destinations + fan-out
                    │           └─> 6 sync proxy + transforms
                    │                 ├─> 7 code node (goja)
                    │                 └─> 8 secrets + observability
                    │                       └─> 9 V2 cutover
                    │                             └─> 10 web UI
                    └─> (4 and 5 may overlap if two people are working)
```

## Why the boundaries fall here

- **1 | 2** — the graph engine is pure logic with no config or I/O. Reviewable alone, and
  it is where the invariant-bearing type checker lives.
- **2 | 3** — validation is fully testable with **stub** node implementations. Splitting it
  out means the config format and all three validation layers land and get reviewed before
  any runtime behaviour exists to confuse a failure.
- **3 | 4** — chunk 3 proves one narrow path end to end (trigger -> body -> auth ->
  envelope -> one destination). Everything after it is additive, which makes regressions
  easy to attribute.
- **4** carries the **I1-I18 conformance suite**, because chunk 4 is where the
  invariant-bearing nodes (gates, replay guard, filter) arrive. This is the regression net
  that decision D4's clean break removed, and it is the single item most likely to be
  skipped and most regretted. Do not defer it.
- **6 | 7** — the sync proxy is what makes V2 able to replace universal-gateway at all
  (gaps doc A1/A6). It ships before goja because it is the higher-value capability and has
  no security surface.
- **10 last**, per instruction. The UI generates configs, so it needs the config format
  final — which it is not until chunk 9 has completed the V2 cutover checks.

## Staleness warning

Chunks 0-4 are written against code that exists today and are safe to implement as
written. **Chunks 5-10 assume interfaces that chunks 1-4 will define, and some of those
assumptions will be wrong.** Before starting any chunk from 5 onward, re-read it against
what actually got built, and update it in place. Each of those files carries a
`## Re-read before starting` section listing exactly which assumptions to check.

## Ground rules for implementers

- **Do not commit, stage, branch, or push** unless the user asks in the current
  conversation. Leave work in the `v2` branch working tree. Read-only git is fine.
- **TDD.** Write the failing test, run it, see it fail for the right reason, implement,
  see it pass. A test that has never failed has proven nothing.
- **Never weaken an invariant to make a test pass.** If a test in the conformance suite
  fails, the implementation is wrong, not the test. Invariants I1-I18 are in
  `v2/00-v1-baseline.md` §2 with the V1 line numbers that establish each one.
- **`PortType.Accepts` is strict equality.** It will look like it should be widened to an
  assignability relation. Widening it silently destroys I1-I4. See the comment on the
  function and `TestRawRequestIsNotARequestHead`.
- **Metric and log labels are bounded by config, never by data** (scalability rule R3).
  Node ids and configured templates are safe labels; a rendered target is not.
- **No per-node persistence, ever** (decision D8). Per-node observability goes to metrics,
  logs, and spans. If you find yourself adding a database write inside the executor's node
  loop, stop.
- Run `make test && make lint` before declaring any step done.
