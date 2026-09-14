# Chunk 10: Web UI — Node-Based Config Generator

**Status:** not started
**Depends on:** Chunk 9 (config format must be final)
**Unlocks:** nothing — this is last
**Verification gate:** the UI exports a config that `hookway validate` accepts, and can
import a hand-written V2 config and round-trip it without loss.

```bash
cd web && npm test && npm run build
# then, by hand:
#  1. build a flow in the UI
#  2. export the YAML
#  3. hookway validate --config <exported>   -> exit 0
#  4. re-import that same YAML -> the canvas matches what you built
```

**Goal:** Retarget `web/src/graph/` from V1's fixed endpoint→destination view to the V2 node
model. **Per decision D6 this stays a config generator** — it produces YAML for a human to
deploy. It is explicitly **not** an n8n-style live editor: no server-side flow CRUD, no
execution view, no running-instance connection.

## Re-read before starting

This chunk is the **most likely in the whole plan to be stale**, because it depends on the
final shape of everything before it. Before starting, verify:

- The node catalog in `v2/02-design.md` §4 against what `hookway schema --nodes` actually
  emits. The schema output is the contract — trust it over the design doc.
- Whether Q11 resolved to one `publish` node or one node type per destination. The palette
  and the destination property panel differ substantially.
- Whether Chunk 4's `switch` node uses labelled ports as designed — the canvas needs to
  render multiple labelled output handles, which is a different React Flow shape than a
  single output.

## Why last

The UI generates configs, so it needs the config format final. Chunk 9 is the first point
where every node has been exercised against real traffic and the format has stopped moving.
Building this earlier means rebuilding it.

---

## What exists today

`web/src/graph/` (about 50 files) renders V1 config as a canvas:

| File | Today | Fate |
|---|---|---|
| `fromConfig.ts` (10.6 KB) | V1 config → nodes/edges | **Rewrite** for `flows:` |
| `model.ts` (6.3 KB) | Node/edge model | **Rewrite** — one node type per catalog entry |
| `destTypes.ts` (9 KB) | Hand-coded per-destination field lists | **Delete** — replaced by `hookway schema --nodes` |
| `EndpointForm.tsx`, `DestinationForm.tsx`, `FilterForm.tsx`, `VariablesSection.tsx` (12.7 KB) | Hand-coded forms per config section | **Delete** — replaced by one schema-driven property panel |
| `Canvas.tsx`, `nodes.tsx`, `RouteEdge.tsx` | Rendering | **Keep**, extend for labelled ports |
| `Palette.tsx` | Node palette | **Extend** — driven by the schema |
| `Inspector.tsx`, `InspectorSections.tsx` | Property inspector shell | **Keep**, gut the contents |
| `persistence.ts`, `ImportModal.tsx`, `ExportModal.tsx`, `fromYaml.ts` | Import/export | **Keep**, retarget |
| `connection.ts` | Edge validation | **Rewrite** — this is where port types go |

The big win is deleting `destTypes.ts` plus the four hand-coded form components — roughly
25 KB of hand-maintained field definitions — and replacing them with one generic
schema-driven panel. That is the payoff for Chunk 2's `hookway schema --nodes`.

---

## Task 1: Consume `hookway schema --nodes`

**Files:** `web/src/graph/nodeSchema.ts`, `nodeSchema.test.ts`; build wiring

The schema is the single source of truth for node types, their ports, and their options.
Hand-maintaining a copy in TypeScript is what `destTypes.ts` does today and why it drifts.

Two options for getting it into the browser, and the choice matters:
- **Build-time**: a `make` step runs `hookway schema --nodes > web/src/graph/nodeSchema.json`.
  Simple, no runtime dependency, and it can drift if someone forgets to regenerate.
  Mitigate with a CI check that regenerates and fails on a diff.
- **Runtime**: the editor (`internal/editor`) serves it. Always current, but the UI then
  needs a server — and there is already `cmd/hookway-wasm` compiling config validation to
  WASM for the browser, so check what that already exposes.

**Recommended: build-time JSON plus a CI drift check**, because it keeps the exported-config
workflow fully static. But read `cmd/hookway-wasm/main.go` and `internal/editor/embed.go`
first — if the WASM build already ships the registry, runtime is nearly free and strictly
better.

- [ ] **Step 1: Write failing tests** — every node type in the schema produces a palette
      entry; each option renders an input of the right kind (string, number, boolean, enum,
      array, nested object); a `required` option is marked required.
- [ ] **Step 2: Implement, then pass.**
- [ ] **Step 3: Add the CI drift check** if you chose build-time.

---

## Task 2: Port types in the canvas

**Files:** `web/src/graph/connection.ts`, `connection.test.ts`, `nodes.tsx`

This is the highest-value UI work: **make illegal wiring impossible to draw.** The backend
rejects it at load, but catching it at drag time is the entire reason a typed graph is nicer
to author than YAML.

- [ ] **Step 1: Write failing tests**

Mirror the backend's rule exactly — **strict equality**, no assignability:

```ts
describe('isValidConnection', () => {
  it('allows RequestHead -> RequestHead', () => { /* ... */ });
  it('allows RequestHead -> RawRequest (read_body)', () => { /* ... */ });

  // The load-bearing case, mirroring TestRawRequestIsNotARequestHead.
  it('REJECTS RawRequest -> RequestHead (a gate after read_body)', () => {
    expect(isValidConnection({ from: 'read_body', to: 'rate_limit' })).toBe(false);
  });

  it('REJECTS RawRequest -> Envelope (auth bypass)', () => {
    expect(isValidConnection({ from: 'read_body', to: 'envelope' })).toBe(false);
  });
});
```

- [ ] **Step 2: Implement, then pass**

Wire it into React Flow's `isValidConnection` so an illegal drag is refused visually. Colour
the handles by port type and show the type name on hover — that is what teaches the model
without documentation.

- [ ] **Step 3: Render labelled output ports** for `switch` and `on_error`.

---

## Task 3: Schema-driven property panel

**Files:** `web/src/graph/NodeInspector.tsx` + tests; delete `destTypes.ts`,
`EndpointForm.tsx`, `DestinationForm.tsx`, `FilterForm.tsx`, `VariablesSection.tsx`

One generic component rendering a form from a node's JSON Schema, replacing five hand-coded
ones.

- [ ] **Step 1: Write failing tests** covering each JSON Schema type, `required` marking,
      defaults shown as placeholders, enums as selects, and validation messages on blur.
- [ ] **Step 2: Implement, then pass.**
- [ ] **Step 3: Delete the five old files** and confirm nothing imports them:
      `grep -rn 'destTypes\|EndpointForm\|DestinationForm\|FilterForm\|VariablesSection' web/src/`

---

## Task 4: Import, export, and round-trip

**Files:** `web/src/graph/fromConfig.ts`, `toConfig.ts`, `fromYaml.ts`, tests

- [ ] **Step 1: Write the round-trip test first** — it is the real acceptance criterion:

```ts
// Round-tripping must be lossless. If it is not, the UI silently destroys
// hand-written config, which makes it unsafe to use on a real file.
it.each(EXAMPLE_CONFIGS)('round-trips %s without loss', (yaml) => {
  const model = fromConfig(parseYaml(yaml));
  const out = toConfig(model);
  expect(normalize(out)).toEqual(normalize(parseYaml(yaml)));
});
```

Feed it every file in `examples/v2/`. Losing a comment is acceptable; losing a field is not.

- [ ] **Step 2: Implement, then pass.**
- [ ] **Step 3: Validate exports in-browser** using the existing `cmd/hookway-wasm` build,
      so the UI can show validation errors before the user ever downloads a file. Check what
      `internal/editor/embed.go` already wires up.
- [ ] **Step 4: Layout** — `layout.ts` needs to handle deeper graphs than V1's two columns.
      A simple layered DAG layout keyed on port type reads well, since the port types are
      already an ordering.

---

## Task 5: Verify by hand and update docs

- [ ] **Step 1: Run the full gate**

```bash
cd web && npm test && npm run build
```

Then by hand: build a flow, export, `hookway validate` it, re-import, confirm the canvas
matches.

- [ ] **Step 2: Round-trip every example** in `examples/v2/` through import and export.

- [ ] **Step 3: Update docs** — `docs/cli.md` for the `editor` command, `README.md`, and
      `web/`'s own README if it has one. State plainly that the UI **generates config** and
      does not manage a running instance, so nobody expects n8n.

- [ ] **Step 4: Commit**

```bash
make test && make lint
git add web/ docs/ README.md
git commit -m "feat(web): node-based config generator for schema_version 2"
```

---

## Done when

- [ ] The palette is generated from `hookway schema --nodes`, not hand-maintained
- [ ] `destTypes.ts` and the four hand-coded form components are **deleted**
- [ ] An illegal connection cannot be drawn; `RawRequest -> RequestHead` is refused
- [ ] Handles are coloured by port type and show the type name
- [ ] `switch` and `on_error` render labelled output ports
- [ ] Every `examples/v2/` config round-trips without field loss
- [ ] An exported config passes `hookway validate`
- [ ] In-browser validation shows errors before download
- [ ] Docs state this is a config generator, not a live editor
- [ ] `npm test` and `npm run build` clean; `make test && make lint` clean
