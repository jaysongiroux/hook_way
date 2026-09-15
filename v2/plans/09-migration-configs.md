# Chunk 9: V2 Cutover and V1 Removal

> **Scope (locked 2026-09-14, restated 2026-09-15):** universal-gateway and
> DigiFi work is excluded. There is no captured-traffic harness, no seven
> handler YAMLs, and no IAM trust-domain split. The gate is Hookway V2 feature
> parity (existing destination types, proxy/transform, I1–I18), replacement of
> checked-in V1 examples, and removal of the V1 runtime.

**Status:** accepted
**Depends on:** Chunk 8
**Unlocks:** Chunk 10
**Verification gate:** `schema_version: 1` / `endpoints:` configs fail with a
migration error; V2 examples validate; I1–I18 pass; `handleWebhook` is gone;
`make test && make lint && make conformance && make validate-examples` green.

```bash
hookway validate --config examples/config.yaml          # V2, exit 0
hookway validate --config /tmp/v1.yaml                  # migration error, exit 1
go test ./internal/conformance/ -v                      # I1–I18, no skips
make test && make lint && make validate-examples
```

**Goal:** V2 is the only request path. Leaving two pipelines in one binary is
how a security fix lands on only one of them (D4, D5).

## Re-read before starting

- I1–I18 already live in `internal/conformance` on `FlowServer`. Do not delete
  a V1 test that is the only coverage of a behaviour the conformance suite
  does not exercise; port it onto `FlowServer` first.
- `FlowServer.v1()` reuses helper methods on `Server` (`sourceIP`,
  `requestID`, `captureEvent`, `handleLive`, metrics). Extract or keep those
  helpers; do not delete `server.go` wholesale.
- `flowconfig.ServingConfig()` still projects flows onto
  `config.EndpointConfig` so `queue.Factory` can provision targets. Keep the
  Go types. Stop loading user YAML `endpoints:`.
- Web/WASM still emit V1 `endpoints:` (Chunk 10). CLI/serve/validate must
  reject that YAML. Leave `web/src/graph/` for Chunk 10.

---

## Task 1: Reject V1 configs

**Files:** `internal/flowconfig/config.go`, `internal/configapi/configapi.go`,
`cmd/hookway/main.go`, tests

A `schema_version` other than 2 — including absent, which `DetectSchemaVersion`
still reports as 1 — is a **migration error**, not a V1 validate/serve path.

The message must mention `endpoints:`, `schema_version: 2`, `flows:`, and
`docs/configuration.md`.

- [x] **Step 1: Failing tests** — LoadBytes / CLI validate / configapi
  Validate / serve refuse `schema_version: 1`, absent version, and an
  `endpoints:` document, with the migration text.

- [x] **Step 2: Implement** — `runValidate` and `runServe` are V2-only.
  `editorAPIKey` loads V2 (or DecodeSharedYAML after the version check).
  `configapi.Validate` returns the migration issue instead of
  `config.ValidateBytes`.

- [x] **Step 3: Commit** (may land with Task 2)

---

## Task 2: V2 examples replace V1 examples

**Files:** `examples/*.yaml`, `examples/destinations/*.yaml`,
`examples/auth/*.yaml`, `Makefile` `CONFIG` / `validate-examples`,
`examples/README.md`

Every previously-V1 example becomes a `schema_version: 2` flow that still
demonstrates the same destination or auth surface. `examples/v2/` stays as
the github/proxy/invalid fixtures.

`make validate-examples` validates the converted files plus valid
`examples/v2/*.yaml`.

Keep `config.Load` and `endpoints.go` **as Go types / destination validation
fixtures** used by `internal/config` tests and `ServingConfig`. That is not
a user config path.

- [x] **Step 1: Convert examples and `make validate-examples`**

- [x] **Step 2: Commit** (may land with Task 1)

---

## Task 3: Remove the V1 request pipeline

**Files:** `internal/httpapi/server.go` and V1 handler tests; `cmd/hookway/main.go`

1. Confirm I1–I18 conformance tests exist and are not skipped.
2. Port any V1-only coverage the conformance suite does not have onto
   `FlowServer` (request-id / traceparent, metrics listener + API key,
   forwarded-for source IP, readiness cache tests).
3. Move shared test fakes (`fakePublisher`) out of `handlers_test.go`.
4. Delete `handleWebhook` and `NewServer*`. Keep helper methods `FlowServer`
   still calls.
5. Delete V1-only files: `handlers_test.go`, `hardening_test.go`,
   `filter_handlers_test.go`, `variables_handlers_test.go`,
   `loop_handlers_test.go`, `lookup_handlers_test.go`,
   `panic_recovery_test.go`, `replay_capture_test.go`, and V1-only helpers
   `variables.go` / `lookup.go` if unused.
6. `grep` production code for `httpapi.NewServer` / `handleWebhook`.

Do **not** delete `internal/config/endpoints.go` or the destination half of
`validation.go`.

- [x] **Step 1: Port unique tests, then delete V1 handler path**

- [x] **Step 2: Docs** — `README.md`, `docs/configuration.md` (node-based
  structure, not a search-and-replace of `endpoints.<name>`), `docs/cli.md`,
  `docs/security.md`, `docs/production-readiness.md`, `docs/dockerhub/README.md`.

- [x] **Step 3: Verify**

```bash
make test && make lint && make conformance && make validate-examples
GOOS=js GOARCH=wasm go build ./cmd/hookway-wasm
```

Optional if Docker is available: `make docker-build && make vuln-scan` and
`make test-all`.

---

## Done when

- [x] A `schema_version: 1` config produces a clear migration error
- [x] V1 examples are gone or rewritten as V2; `make validate-examples` green
- [x] Every invariant I1–I18 has a conformance test **before** V1 handler tests
      are deleted
- [x] V1 is gone from the serve path: no `handleWebhook`, no user `endpoints:`
      config path
- [x] Docs describe flows/nodes, not the V1 endpoint pipeline
- [x] `make test`, `make lint`, `make conformance`, `make validate-examples` green
- [x] Web graph export remains V1 until Chunk 10 (called out, not silently
      broken in CLI)
