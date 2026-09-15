# Chunk 9: V2 Cutover and V1 Removal

> **Scope update (2026-09-14):** the user excluded universal-gateway and DigiFi work.
> Ignore the external-handler configuration and captured-traffic tasks below. Before this
> chunk starts, rewrite its gate around Hookway's V2 feature parity, I1–I18 conformance,
> existing destination integrations, proxy/transform behavior, configuration examples,
> and removal of the V1 runtime. No external source or provider fixture is required.

**Status:** not started
**Depends on:** Chunk 8 (and Chunk 7 if any handler needs a `code` node)
**Unlocks:** Chunk 10
**Verification gate:** all seven universal-gateway handlers run as V2 flows and produce
byte-identical downstream output to the Lambdas on captured traffic.

```bash
hookway validate --config examples/v2/universal-gateway.yaml   # exit 0
go test ./internal/conformance/ -v
go test ./v2/replay/ -v        # differential tests against captured traffic
make test-all
```

**Goal:** Turn Chunk 0's paper graphs into real, validated, traffic-tested configs — then
delete V1.

## Re-read before starting

- Re-read `v2/spikes/S2-lattice-validation.md`. Its seven YAML blocks are the starting
  point for this chunk. Where the implementation diverged from the spike, the spike is
  wrong — update it.
- Re-read `v2/spikes/S1-digifi-signature.md`. If DigiFi needed an I6 exception, confirm it
  landed and is documented as a scoped exception rather than a general capability.
- Check `docs/universal-gateway-migration-gaps.md` §2's table of seven handlers against
  what `../universal-gateway` actually contains today. That doc is dated 2026-08-27 and
  the source may have moved.

---

## Task 1: Write all seven flows

**Files:** `examples/v2/universal-gateway.yaml` (or one file per handler — prefer one file
per handler for reviewability, plus a combined one for the real deployment)

Work from the spike, the gaps doc §2, and the handler source. The seven, with the gap each
one exercises:

| Flow | Shape | Exercises |
|---|---|---|
| `digifi_router` | webhook → SNS | A2 dynamic target, A3 changed-field diff, A4 MessageAttributes, A7 vendor HMAC |
| `qualia_router` | webhook → SQS | A5 query-param auth |
| `clearedge_router` | webhook → SQS | A2 with regex id-type discrimination |
| `salesforce_router_proxy` | sync proxy | A1 response passthrough, A6 any-method + greedy path |
| `salesforce_digifi_proxy` | sync proxy | A1, A6, upstream OAuth |
| `digifi_router_proxy` | sync proxy | A1, A6 |
| `docmagic_xml_proxy` | sync proxy + XML | A1, A3 XML prune, `body_format: xml` |

- [ ] **Step 1: Write each flow and validate it**

```bash
for f in examples/v2/ug-*.yaml; do
  echo "== $f"; hookway validate --config "$f" || echo "FAILED: $f"
done
```

Every one must reach exit 0 before moving on. A flow that does not validate is not a flow.

- [ ] **Step 2: Resolve the IAM blast-radius question (gaps doc B1)**

This is a **deployment** decision this chunk must not skip. universal-gateway has seven-plus
narrowly-scoped IAM roles, deliberately tightened in staging and production versus sandbox.
Hookway is one process with one role, which must be the **union** — a bug in any one flow's
handling reaches every destination the gateway can write to.

The mitigation the gaps doc recommends is deploying **several Hookway instances split by
trust domain** rather than one. Decide the split, write it down in
`v2/09-deployment.md`, and make the configs match it (one config file per instance). Do not
leave this as an implicit single-instance deployment.

- [ ] **Step 3: Commit**

```bash
git add examples/v2/ v2/09-deployment.md
git commit -m "feat(examples): all seven universal-gateway handlers as V2 flows"
```

---

## Task 2: Differential testing against captured traffic

**Files:** `v2/replay/` — a test harness plus scrubbed fixtures

This is the real verification and the reason this chunk exists. Per the gaps doc §5 there is
**no production baseline** — both staging and production SAM deploys are commented out — so
correctness has to be established from captured sandbox traffic and the 127 Jest handler
tests, not from production parity.

- [ ] **Step 1: Capture traffic for each of the seven paths**

Record request bytes, headers, and the **downstream effect** (the SQS/SNS message actually
published, including message attributes; or the proxy response actually returned). Scrub
PII before anything lands on disk, and gitignore the raw captures.

- [ ] **Step 2: Build a differential harness**

For each captured request: replay it against a V2 Hookway with a recording destination
fake, then compare the published message against what the Lambda produced.

Compare **byte-for-byte where possible**. Where it is not (timestamps, generated event ids,
`request_id`), normalize those specific fields explicitly and list them in the harness — a
fuzzy comparison that ignores whole objects will hide real divergence.

- [ ] **Step 3: Port the Jest cases that encode real behaviour**

There are roughly 127 Jest test cases across the handlers. Do not port them mechanically —
most are framework plumbing. Port the ones that encode **behaviour you would otherwise
lose**: id extraction edge cases, the regex discrimination boundaries, the `status.*`
promotion rule, XML prune ordering.

- [ ] **Step 4: Commit**

```bash
make test && make lint
git add v2/replay/
git commit -m "test(migration): differential harness comparing V2 flows to the Lambdas"
```

---

## Task 3: Remove V1

**Files:** delete `internal/httpapi/server.go` and its tests; delete V1-only parts of
`internal/config`; modify `cmd/hookway/main.go`, `examples/`, `docs/`

Decision D4 is a clean break, and D5 is in-place replacement. V1 has been kept alive
through Chunks 2–8 specifically so it could keep serving while V2 was built. Once Task 2
proves V2 equivalence, V1 is dead weight — and leaving two request paths in one binary is
how you get a security fix applied to only one of them.

- [ ] **Step 1: Confirm nothing still depends on V1**

```bash
grep -rn 'httpapi.NewServer\|config.Load(' --include='*.go' . | grep -v _test.go
```

Every remaining call site must be either deleted or migrated.

- [ ] **Step 2: Delete, in this order**

1. `internal/httpapi/server.go` and `handlers_test.go`, `hardening_test.go`,
   `replay_capture_test.go`, `panic_recovery_test.go`, `filter_handlers_test.go`,
   `variables_handlers_test.go`, `loop_handlers_test.go`, `lookup_handlers_test.go`
2. V1-only config: `internal/config/endpoints.go`, the endpoint half of `validation.go`,
   and their tests. **Keep** `ServerConfig`, `SecurityConfig`, `ReplayConfig`,
   `DestinationConfig`, `duration.go`, and the env expansion — `flowconfig` uses all of them
3. `schema_version` routing in `cmd/hookway/main.go` — version 2 becomes the only path, and
   a version 1 config gets a clear error pointing at the migration
4. `examples/config.yaml` and the other V1 examples, replaced by V2 equivalents

**Before deleting any test file, check whether it covers an invariant the conformance suite
does not.** That is the actual risk of this task. Work through
`v2/00-v1-baseline.md` §2 and confirm each of I1–I18 has a conformance test; anything only
covered by a V1 test must be ported first.

- [ ] **Step 3: Update all documentation**

`README.md`, `docs/configuration.md` (94 KB — the endpoint sections all change),
`docs/cli.md`, `docs/security.md`, `docs/production-readiness.md`, `docs/dockerhub/README.md`.

`docs/configuration.md` is the big one. Budget real time for it; a node-based config needs a
different structure, not a search-and-replace.

- [ ] **Step 4: Verify the whole suite**

```bash
make test-all && make conformance && make lint && make validate-examples && make docker-build && make vuln-scan
```

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "refactor: remove V1 endpoint pipeline, V2 is the only path"
```

---

## Done when

- [ ] All seven flows validate with exit 0
- [ ] The differential harness shows byte-identical downstream output on captured traffic,
      with every normalized field explicitly listed
- [ ] The IAM trust-domain deployment split is decided and written in `v2/09-deployment.md`
- [ ] Every invariant I1–I18 has a conformance test **before** any V1 test is deleted
- [ ] V1 is gone: no `handleWebhook`, no `endpoints:` config path
- [ ] A `schema_version: 1` config produces a clear migration error
- [ ] All docs updated, including the 94 KB `docs/configuration.md`
- [ ] `make test-all`, `make conformance`, `make validate-examples`, `make docker-build`,
      `make vuln-scan` all green
