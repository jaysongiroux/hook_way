# Chunk 2: Config Format and Validation

**Status:** not started
**Depends on:** Chunk 1 (`internal/graph` compiler and registry)
**Unlocks:** Chunk 3
**Verification gate:**
```bash
go test ./internal/flowconfig/ ./internal/graph/
hookway validate --config examples/v2/github.yaml          # exit 0, "config valid"
hookway validate --config examples/v2/invalid-gate-order.yaml  # exit 1, names BOTH port types
hookway validate --config examples/v2/invalid-many.yaml    # exit 1, lists ALL problems, not just the first
hookway validate --config examples/v2/github.yaml --json | jq '.issues'
hookway schema --nodes | jq 'keys'                          # every registered node type
```

**Goal:** Define the `schema_version: 2` config format, wire every real node type into the
registry with its port signature and config schema (but **stub** `Run` bodies), and make
`hookway validate` fully exercise all three validation layers. After this chunk you can
author and validate V2 configs; you cannot yet serve traffic.

## Why validation ships before behaviour

Every validation layer is testable with stub nodes. Landing it separately means the config
format and its error messages get reviewed on their own, and when Chunk 3 starts producing
runtime failures you already know the config was well-formed. It also gives Chunk 10's UI a
stable `hookway schema --nodes` contract to build against.

## Re-read before starting

- Confirm Chunk 1 actually produced `graph.Compile(name string, fc FlowConfig, reg *Registry) (*Flow, error)`
  and `graph.CompileError{Flow string, Problems []string}`. If the signatures drifted,
  fix this chunk's code blocks before starting.
- Check whether Chunk 0's S2 spike amended `v2/02-design.md` §4's node catalog. If it added
  node types or options, add them to the registry in Task 3 below.

## Existing machinery you must reuse, not replace

- `config.ValidationIssue` (`internal/config/validation.go`) — the issue struct with
  `Field`, `Message`, `Suggestion`, `Severity`. The CLI's human output, the `--json`
  output, and the editor API all render it. **V2 issues must be `ValidationIssue` values**
  or three surfaces break at once.
- `config.ConfigValidationError{Issues []ValidationIssue}` — the aggregating error type.
- `configapi.ValidateWithEnv(data []byte, expandEnv bool)` (`internal/configapi/configapi.go`)
  — what `validate --json` and the editor call. Must learn to route by `schema_version`.
- `config.expandEnvStrict` / `ExpandEnvLenient` and `config.MissingEnvIssues` — `${VAR}`
  expansion. Reuse verbatim; do not write a second expander.
- `configapi.Schema()` and `internal/config/schema.go`'s `jsonschema.Reflector` — the
  existing `invopop/jsonschema` setup. Per-node schemas use the same reflector.
- `yaml.NewDecoder(...).KnownFields(true)` — V1 rejects unknown fields. V2 must too: a
  typo'd node option that silently does nothing is the worst kind of config bug.

## Decision needed before Task 1 — see OPEN-QUESTIONS Q11

V1 keeps connection config in a shared top-level `destinations:` map keyed by id, and
endpoint routes reference it by id. **That exists so one RabbitMQ or Kafka connection
serves many endpoints.** If a V2 destination node embedded its own connection config, N
nodes pointing at the same broker would open N connections.

This chunk assumes the shared block is **kept**, and that there is **one** destination node
type — `publish` — which references a destination id:

```yaml
destinations:
  primary:
    type: rabbitmq
    rabbitmq: {url: "${RABBITMQ_URL}", exchange: hookway.webhooks}

flows:
  github:
    nodes:
      - {id: out, type: publish, destination: primary, target: github-webhooks, create: true}
```

This differs from `v2/02-design.md` §4, which lists `sns`, `sqs`, etc. as separate node
types. One `publish` node preserves connection sharing and keeps the registry small; the UI
resolves the icon and label from the referenced destination's `type`. **Confirm with the
user before implementing**, and update §4 either way.

---

## File Structure

| File | Responsibility |
|---|---|
| `internal/flowconfig/config.go` | Top-level V2 `Config` struct, `Load`, `LoadBytes` |
| `internal/flowconfig/config_test.go` | Load/parse tests, `schema_version` routing |
| `internal/flowconfig/nodes.go` | `Register(*graph.Registry)` — every node type's spec and option struct |
| `internal/flowconfig/nodes_test.go` | Every registered type has ports, a schema, and an option struct that round-trips |
| `internal/flowconfig/lint.go` | Layer 3 policy lint |
| `internal/flowconfig/lint_test.go` | One subtest per lint rule |
| `internal/flowconfig/schema.go` | `NodeSchemas()` for `hookway schema --nodes` |
| `internal/flowconfig/schema_test.go` | Schema generation covers every registered type |
| `examples/v2/github.yaml` | Minimal valid flow |
| `examples/v2/invalid-gate-order.yaml` | Gate after `read_body` — must fail |
| `examples/v2/invalid-many.yaml` | Several distinct problems — must report all |
| `cmd/hookway/main.go` | Modified: `validate` routes on `schema_version`; `schema --nodes` |

A new package rather than extending `internal/config`: V1's `validation.go` is 1,391 lines
of fixed-shape endpoint rules that V2 replaces entirely (decision D4). Entangling them
would make both harder to reason about, and V1's config package gets deleted in Chunk 9.
**Do not modify `internal/config`'s existing behaviour in this chunk** — V1 must keep
working until Chunk 9.

---

## Task 1: Top-level V2 config struct and loading

**Files:**
- Create: `internal/flowconfig/config.go`, `internal/flowconfig/config_test.go`

**Interfaces produced:**
```go
type Config struct {
    SchemaVersion int
    Server        config.ServerConfig              // reused from V1
    Security      config.SecurityConfig            // reused from V1
    Replay        config.ReplayConfig              // reused from V1
    Destinations  map[string]config.DestinationConfig // reused from V1
    Flows         map[string]graph.FlowConfig
}

func LoadBytes(data []byte, source string, expandEnv bool) (Config, []config.ValidationIssue)
func Load(path string) (Config, []config.ValidationIssue)
func DetectSchemaVersion(data []byte) (int, error)
```

`Server`, `Security`, `Replay`, and `Destinations` are **reused from `internal/config`
unchanged**. Only `endpoints:` is replaced by `flows:`. Reusing them means the 13
destination types, their drift handling, and their validation all keep working without a
rewrite.

- [ ] **Step 1: Write the failing tests**

```go
package flowconfig

import (
	"strings"
	"testing"
)

func TestDetectSchemaVersion(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want int
	}{
		{"explicit 2", "schema_version: 2\nflows: {}\n", 2},
		{"explicit 1", "schema_version: 1\nendpoints: {}\n", 1},
		{"absent means 1", "endpoints: {}\n", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := DetectSchemaVersion([]byte(tc.src))
			if err != nil {
				t.Fatalf("DetectSchemaVersion() = %v", err)
			}
			if got != tc.want {
				t.Errorf("= %d, want %d", got, tc.want)
			}
		})
	}
}

func TestLoadBytesValidFlow(t *testing.T) {
	const src = `
schema_version: 2
server:
  addr: ":8080"
destinations:
  primary:
    type: webhook
    webhook:
      base_url: "https://example.test"
flows:
  github:
    nodes:
      - {id: in,   type: http_trigger, path: /webhooks/github, methods: [POST]}
      - {id: body, type: read_body,    max_bytes: 1048576}
      - {id: auth, type: auth,         scheme: hmac_sha256_hex, secret: "shh",
                                       signature_header: X-Hub-Signature-256}
      - {id: env,  type: envelope,     body_format: json}
      - {id: out,  type: publish,      destination: primary, target: github-webhooks}
    edges: [in>body, body>auth, auth>env, env>out]
`
	cfg, issues := LoadBytes([]byte(src), "test", false)
	if len(issues) > 0 {
		t.Fatalf("LoadBytes() issues = %+v, want none", issues)
	}
	if cfg.SchemaVersion != 2 {
		t.Errorf("SchemaVersion = %d, want 2", cfg.SchemaVersion)
	}
	if _, ok := cfg.Flows["github"]; !ok {
		t.Fatal("flow github missing")
	}
	if len(cfg.Flows["github"].Nodes) != 5 {
		t.Errorf("got %d nodes, want 5", len(cfg.Flows["github"].Nodes))
	}
}

// TestLoadBytesRejectsGateAfterReadBody is the invariant test at the config
// layer: the same wiring Chunk 1 rejects in Compile must be rejected by a
// `hookway validate` run, with an error naming both port types.
func TestLoadBytesRejectsGateAfterReadBody(t *testing.T) {
	const src = `
schema_version: 2
server: {addr: ":8080"}
destinations:
  primary: {type: webhook, webhook: {base_url: "https://example.test"}}
flows:
  bad:
    nodes:
      - {id: in,   type: http_trigger, path: /w, methods: [POST]}
      - {id: body, type: read_body,    max_bytes: 1024}
      - {id: rl,   type: rate_limit,   rpm: 60}
      - {id: auth, type: auth,         scheme: hmac_sha256_hex, secret: s, signature_header: X-S}
      - {id: env,  type: envelope,     body_format: json}
      - {id: out,  type: publish,      destination: primary, target: t}
    edges: [in>body, body>rl, rl>auth, auth>env, env>out]
`
	_, issues := LoadBytes([]byte(src), "test", false)
	if len(issues) == 0 {
		t.Fatal("LoadBytes() = no issues; a gate after read_body must be rejected (I1-I4)")
	}
	joined := issuesText(issues)
	if !strings.Contains(joined, "RequestHead") || !strings.Contains(joined, "RawRequest") {
		t.Errorf("issues %q must name both port types so the operator sees the mismatch", joined)
	}
}

// TestLoadBytesAggregatesAcrossFlows keeps one validate run useful: problems in
// flow B must not be hidden by problems in flow A.
func TestLoadBytesAggregatesAcrossFlows(t *testing.T) {
	const src = `
schema_version: 2
server: {addr: ":8080"}
destinations:
  primary: {type: webhook, webhook: {base_url: "https://example.test"}}
flows:
  a:
    nodes: [{id: in, type: http_trigger, path: /a, methods: [POST]}, {id: x, type: nonexistent}]
    edges: [in>x]
  b:
    nodes: [{id: in, type: http_trigger, path: /b, methods: [POST]}, {id: y, type: alsomissing}]
    edges: [in>y]
`
	_, issues := LoadBytes([]byte(src), "test", false)
	joined := issuesText(issues)
	if !strings.Contains(joined, "nonexistent") || !strings.Contains(joined, "alsomissing") {
		t.Errorf("issues %q must report problems from BOTH flows", joined)
	}
}

func TestLoadBytesRejectsUnknownField(t *testing.T) {
	const src = `
schema_version: 2
server: {addr: ":8080"}
flows:
  a:
    nodes: [{id: in, type: http_trigger, path: /a, methods: [POST]}]
    edges: []
    typo_field: 1
`
	_, issues := LoadBytes([]byte(src), "test", false)
	if len(issues) == 0 {
		t.Fatal("unknown field typo_field must be rejected: a silently-ignored option is a config bug")
	}
}

func TestLoadBytesRejectsDuplicateTriggerPaths(t *testing.T) {
	const src = `
schema_version: 2
server: {addr: ":8080"}
destinations:
  primary: {type: webhook, webhook: {base_url: "https://example.test"}}
flows:
  a:
    nodes: [{id: in, type: http_trigger, path: /same, methods: [POST]}, {id: r, type: respond, status: 200}]
    edges: [in>r]
  b:
    nodes: [{id: in, type: http_trigger, path: /same, methods: [POST]}, {id: r, type: respond, status: 200}]
    edges: [in>r]
`
	_, issues := LoadBytes([]byte(src), "test", false)
	if !strings.Contains(issuesText(issues), "/same") {
		t.Error("two flows claiming the same trigger path must be rejected; routing would be ambiguous")
	}
}

func issuesText(issues []config.ValidationIssue) string {
	var b strings.Builder
	for _, i := range issues {
		b.WriteString(i.Field)
		b.WriteString(": ")
		b.WriteString(i.Message)
		b.WriteString("\n")
	}
	return b.String()
}
```

Add `"github.com/jasongiroux/hookway/internal/config"` to the test imports.

- [ ] **Step 2: Run the tests and confirm they fail to build**

Run: `go test ./internal/flowconfig/ -v`
Expected: build failure, `undefined: DetectSchemaVersion`.

- [ ] **Step 3: Implement `config.go`**

Structure it as: detect version → expand env → decode with `KnownFields(true)` → compile
every flow through `graph.Compile` → convert `graph.CompileError.Problems` into
`config.ValidationIssue` values → check cross-flow rules (duplicate trigger paths,
`destination:` references that name no entry in `destinations:`).

Key implementation notes:

- **`DetectSchemaVersion`** decodes only `struct{ SchemaVersion int \`yaml:"schema_version"\` }`
  with `KnownFields(false)`, so it tolerates the rest of the document. Absent or `0` means 1.
- **Env expansion** reuses `config.MissingEnvIssues`. When `expandEnv` is false, leave
  `${VAR}` as literals — `validate --no-env` and the editor rely on that (see the memory
  note in `v2/README.md`: the validate path does not expand env).
- **Issue `Field` paths** must be precise enough to click: `flows.github.edges[3]`,
  `flows.github.nodes[2].secret`. Vague fields make the editor useless.
- **Converting `CompileError`**: one `ValidationIssue` per entry in `Problems`, with
  `Field: "flows." + flowName` and `Severity: "error"`.
- **Cross-flow checks** cannot live in `graph.Compile` (it sees one flow), so do them here:
  duplicate trigger paths across flows, and every `publish` node's `destination:` naming a
  real entry in the `destinations:` map.

- [ ] **Step 4: Run the tests until they pass**

Run: `go test ./internal/flowconfig/ -v`
Expected: PASS, all six tests.

- [ ] **Step 5: Verify and commit**

```bash
make test && make lint
git add internal/flowconfig/config.go internal/flowconfig/config_test.go
git commit -m "feat(flowconfig): schema_version 2 config loading and flow compilation"
```

---

## Task 2: Register every node type with ports, options, and stub behaviour

**Files:**
- Create: `internal/flowconfig/nodes.go`, `internal/flowconfig/nodes_test.go`

**Interfaces produced:**
```go
func Register(reg *graph.Registry) error   // registers every V2 node type
func NewRegistry() (*graph.Registry, error) // graph.NewRegistry() + Register()
```

Plus one exported option struct per node type, e.g. `HTTPTriggerOptions`,
`ReadBodyOptions`, `AuthOptions`, `PublishOptions`. These are exported because Chunk 10's
`hookway schema --nodes` reflects over them and the UI renders forms from the result.

**Every `Run` in this chunk returns `errNotImplemented`.** Behaviour arrives in Chunks 3-7.
The point of this task is that the *port signatures and config schemas* are complete and
validated, so `hookway validate` is meaningful before anything runs.

- [ ] **Step 1: Write the failing tests**

```go
package flowconfig

import (
	"testing"

	"github.com/jasongiroux/hookway/internal/graph"
)

// wantedSpecs is the node catalog from v2/02-design.md §4. If the S2 spike
// amended the catalog, update BOTH this table and §4 -- they must not drift.
var wantedSpecs = map[string]struct {
	in, out  graph.PortType
	detached bool
}{
	"http_trigger": {graph.PortNone, graph.PortRequestHead, false},
	"loop_guard":   {graph.PortRequestHead, graph.PortRequestHead, false},
	"ip_allowlist": {graph.PortRequestHead, graph.PortRequestHead, false},
	"rate_limit":   {graph.PortRequestHead, graph.PortRequestHead, false},
	"read_body":    {graph.PortRequestHead, graph.PortRawRequest, false},
	"auth":         {graph.PortRawRequest, graph.PortVerified, false},
	"no_auth":      {graph.PortRawRequest, graph.PortVerified, false},
	"replay_guard": {graph.PortVerified, graph.PortVerified, false},
	"envelope":     {graph.PortVerified, graph.PortEnvelope, false},
	"filter":       {graph.PortEnvelope, graph.PortEnvelope, false},
	"lookup":       {graph.PortEnvelope, graph.PortEnvelope, false},
	"transform":    {graph.PortEnvelope, graph.PortEnvelope, false},
	"code":         {graph.PortEnvelope, graph.PortEnvelope, false},
	"switch":       {graph.PortEnvelope, graph.PortEnvelope, false},
	"http_request": {graph.PortEnvelope, graph.PortEnvelope, false},
	"publish":      {graph.PortEnvelope, graph.PortTerminal, true},
	"respond":      {graph.PortEnvelope, graph.PortTerminal, false},
}

func TestRegisterCoversTheCatalog(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatalf("NewRegistry() = %v", err)
	}
	for name, want := range wantedSpecs {
		spec, ok := reg.Lookup(name)
		if !ok {
			t.Errorf("node type %q is not registered", name)
			continue
		}
		if spec.In != want.in || spec.Out != want.out {
			t.Errorf("%s ports = %s->%s, want %s->%s", name, spec.In, spec.Out, want.in, want.out)
		}
		if spec.Detached != want.detached {
			t.Errorf("%s Detached = %v, want %v", name, spec.Detached, want.detached)
		}
	}
	for _, got := range reg.Types() {
		if _, expected := wantedSpecs[got]; !expected {
			t.Errorf("node type %q is registered but not in the catalog; add it to v2/02-design.md §4", got)
		}
	}
}

// TestPublishIsTheOnlyDetachedNode pins invariant I10's boundary: publish runs
// on a detached context, respond must NOT (it writes to the caller's
// connection), and no gate or processor may be detached.
func TestPublishIsTheOnlyDetachedNode(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatalf("NewRegistry() = %v", err)
	}
	for _, name := range reg.Types() {
		spec, _ := reg.Lookup(name)
		if spec.Detached && name != "publish" {
			t.Errorf("node %q is Detached; only publish may be (I10)", name)
		}
	}
	spec, _ := reg.Lookup("respond")
	if spec.Detached {
		t.Error("respond must not be Detached: it writes to the caller's connection")
	}
}

func TestNodeOptionsRejectUnknownFields(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatalf("NewRegistry() = %v", err)
	}
	spec, _ := reg.Lookup("read_body")
	nc := nodeConfigFromYAML(t, "id: body\ntype: read_body\nmax_bytes: 1024\nnot_a_field: 1\n")
	if _, err := spec.New(nc); err == nil {
		t.Error("read_body accepted not_a_field; a silently-ignored option is a config bug")
	}
}

func TestNodeOptionsRequireMandatoryFields(t *testing.T) {
	cases := []struct{ nodeType, yaml, wantIn string }{
		{"http_trigger", "id: in\ntype: http_trigger\n", "path"},
		{"auth", "id: a\ntype: auth\nscheme: hmac_sha256_hex\n", "secret"},
		{"publish", "id: p\ntype: publish\ntarget: t\n", "destination"},
		{"respond", "id: r\ntype: respond\n", "status"},
	}
	reg, err := NewRegistry()
	if err != nil {
		t.Fatalf("NewRegistry() = %v", err)
	}
	for _, tc := range cases {
		t.Run(tc.nodeType, func(t *testing.T) {
			spec, ok := reg.Lookup(tc.nodeType)
			if !ok {
				t.Fatalf("%s not registered", tc.nodeType)
			}
			_, err := spec.New(nodeConfigFromYAML(t, tc.yaml))
			if err == nil {
				t.Fatalf("%s accepted config missing %q", tc.nodeType, tc.wantIn)
			}
		})
	}
}
```

Write the `nodeConfigFromYAML(t *testing.T, src string) graph.NodeConfig` helper in the
test file: `yaml.Unmarshal` into a `graph.NodeConfig` and `t.Fatal` on error.

- [ ] **Step 2: Confirm the tests fail**

Run: `go test ./internal/flowconfig/ -run TestRegister -v`
Expected: build failure, `undefined: NewRegistry`.

- [ ] **Step 3: Implement `nodes.go`**

One option struct and one `graph.NodeSpec` per catalog entry. The pattern, shown once —
repeat it for every type:

```go
package flowconfig

import (
	"errors"
	"fmt"

	"github.com/jasongiroux/hookway/internal/graph"
)

// errNotImplemented is returned by every node's Run in this chunk. Behaviour
// arrives in Chunks 3-7; this chunk exists to make port signatures and config
// schemas complete so `hookway validate` is meaningful first.
var errNotImplemented = errors.New("flowconfig: node behaviour not implemented yet")

// ReadBodyOptions configures the read_body node. Exported because
// `hookway schema --nodes` reflects over it for the config UI.
type ReadBodyOptions struct {
	// MaxBytes caps the buffered body. Required and must be positive: an
	// unbounded read is how a flood exhausts memory (invariant I2).
	MaxBytes int64 `yaml:"max_bytes" json:"max_bytes" jsonschema:"required,minimum=1"`
}

type readBodyNode struct {
	id   string
	opts ReadBodyOptions
}

func (n *readBodyNode) Run(_ context.Context, _ graph.Payload) (graph.Payload, error) {
	return nil, errNotImplemented
}

func readBodySpec() graph.NodeSpec {
	return graph.NodeSpec{
		Type: "read_body",
		In:   graph.PortRequestHead,
		Out:  graph.PortRawRequest,
		New: func(nc graph.NodeConfig) (graph.Node, error) {
			opts, err := decodeOptions[ReadBodyOptions](nc)
			if err != nil {
				return nil, err
			}
			if opts.MaxBytes <= 0 {
				return nil, fmt.Errorf("max_bytes must be positive")
			}
			return &readBodyNode{id: nc.ID, opts: opts}, nil
		},
	}
}
```

`decodeOptions` is the shared strict decoder, and it is the thing that makes
`TestNodeOptionsRejectUnknownFields` pass. It must ignore the `id` and `type` keys (every
node has them) while rejecting anything else unknown:

```go
// decodeOptions strictly decodes a node's type-specific options. Unknown
// fields are an error: a typo'd option that silently does nothing is the
// worst kind of config bug. `id` and `type` are stripped first because every
// node carries them and no option struct declares them.
func decodeOptions[T any](nc graph.NodeConfig) (T, error) {
	var zero T
	var raw map[string]any
	if err := nc.Options.Decode(&raw); err != nil {
		return zero, err
	}
	delete(raw, "id")
	delete(raw, "type")
	remarshalled, err := yaml.Marshal(raw)
	if err != nil {
		return zero, err
	}
	dec := yaml.NewDecoder(bytes.NewReader(remarshalled))
	dec.KnownFields(true)
	var out T
	if err := dec.Decode(&out); err != nil {
		return zero, fmt.Errorf("node %q: %w", nc.ID, err)
	}
	return out, nil
}
```

Then `Register` adds every spec:

```go
func Register(reg *graph.Registry) error {
	for _, s := range []graph.NodeSpec{
		httpTriggerSpec(), loopGuardSpec(), ipAllowlistSpec(), rateLimitSpec(),
		readBodySpec(), authSpec(), noAuthSpec(), replayGuardSpec(), envelopeSpec(),
		filterSpec(), lookupSpec(), transformSpec(), codeSpec(), switchSpec(),
		httpRequestSpec(), publishSpec(), respondSpec(),
	} {
		if err := reg.Register(s); err != nil {
			return err
		}
	}
	return nil
}

func NewRegistry() (*graph.Registry, error) {
	reg := graph.NewRegistry()
	if err := Register(reg); err != nil {
		return nil, err
	}
	return reg, nil
}
```

Option-struct requirements, so later chunks do not have to change the format:

| Node | Required | Notable optional |
|---|---|---|
| `http_trigger` | `path` | `methods` (default `[POST]`), supports `{seg}` and `{rest...}` |
| `loop_guard` | — | `max_hops` (default from `security.loop_max_hops`) |
| `ip_allowlist` | `cidrs` | — |
| `rate_limit` | `rpm` | `burst`, `key_by` (default source IP) |
| `read_body` | `max_bytes` | — |
| `auth` | `scheme`, `secret` or `secret_ref` | `signature_header`, `source` (header/query — gap A5), `timestamp_tolerance`, `remove_bearer`, `expected_issuer`, `expected_audience`, `body_hash_claim` |
| `no_auth` | — | — |
| `replay_guard` | — | `ttl`, `max_entries`, `key` |
| `envelope` | — | `body_format` (json/xml/raw, default json) |
| `filter` | `conditions` | `on_no_match` (drop/reject) |
| `lookup` | `provider`, `key` | `table`, `cache`, `fallback`, `negative_ttl` |
| `transform` | `kind` | kind-specific fields |
| `code` | `script` | `timeout` (default 1s), `max_output_bytes` |
| `switch` | `cases` | `default` |
| `http_request` | `url` | `forward_query`, `forward_method`, `timeout`, `headers` |
| `publish` | `destination`, `target` | `create`, `message_attributes` |
| `respond` | `status` | `headers`, `body`, `passthrough` |

Mirror `auth`'s option names on `internal/auth.Spec` exactly, so Chunk 3's implementation
is a field copy with no translation layer.

- [ ] **Step 4: Run tests until green**

Run: `go test ./internal/flowconfig/ -v`

- [ ] **Step 5: Verify and commit**

```bash
make test && make lint
git add internal/flowconfig/nodes.go internal/flowconfig/nodes_test.go
git commit -m "feat(flowconfig): register V2 node catalog with ports and option schemas"
```

---

## Task 3: Policy lint (validation layer 3)

**Files:**
- Create: `internal/flowconfig/lint.go`, `internal/flowconfig/lint_test.go`

**Interfaces produced:**
```go
func Lint(cfg Config) []config.ValidationIssue   // Severity "warning" unless strict
```

This layer catches what types cannot: **presence**, not ordering. Per
`v2/01-topology-options.md`, typed ports enforce ordering for free and presence only where
a type has exactly one producer. So "auth exists" is free; "rate limit exists" is not.

- [ ] **Step 1: Write the failing tests — one subtest per rule**

Rules to implement, each with a test asserting it fires and a test asserting it does not
fire on a clean config:

| Rule | Severity | Message must say |
|---|---|---|
| Flow has no `rate_limit` node | warning | which flow, and that it is unthrottled |
| Flow has a `no_auth` node | warning | which flow, and that every request is accepted unverified |
| `code` node with no explicit `timeout` | warning | that the 1s default applies and goja has no memory cap |
| `read_body.max_bytes` above `server.max_body_bytes` | warning | both values |
| Flow has no `publish` and no `respond` node | **error** | that the flow can never deliver or reply |
| Two `respond` nodes reachable through parallel fan-out | **error** | both node ids; only one response may be written |
| `publish.destination` names no entry in `destinations:` | **error** | the bad id and the known ids |
| `lookup` node with no `cache` block | warning | that reads will hit the provider per event (rule R4) |

The last one matters more than it looks: an uncached `lookup` turns steady-state read load
from `unique_keys / TTL` into `events/sec`, which is exactly the DB-scaling failure the
whole design avoids.

The two-`respond` check needs graph reachability, so it must run after `graph.Compile`
succeeded. Walk from the root, and for any node with more than one successor on its default
port, collect the `respond` nodes reachable from each successor; if two different branches
of the *same* fan-out both reach a `respond`, that is the error. Two `respond` nodes on
different `switch` branches is **legal** — only one branch fires.

- [ ] **Step 2: Confirm they fail, then implement, then confirm they pass**

Run: `go test ./internal/flowconfig/ -run TestLint -v`

- [ ] **Step 3: Verify and commit**

```bash
make test && make lint
git add internal/flowconfig/lint.go internal/flowconfig/lint_test.go
git commit -m "feat(flowconfig): policy lint for presence rules types cannot express"
```

---

## Task 4: CLI and example configs

**Files:**
- Modify: `cmd/hookway/main.go` — `runValidate`, `runSchema`
- Modify: `internal/configapi/configapi.go` — `ValidateWithEnv` routes on version
- Create: `examples/v2/github.yaml`, `examples/v2/invalid-gate-order.yaml`, `examples/v2/invalid-many.yaml`
- Modify: `Makefile` — `validate-examples` covers `examples/v2/`
- Modify: `internal/flowconfig/schema.go` — `NodeSchemas()`

- [ ] **Step 1: Route `validate` on `schema_version`**

In `runValidate` (`cmd/hookway/main.go:88`), call `flowconfig.DetectSchemaVersion` on the
file bytes first. Version 1 keeps the existing `config.NewService()` path **unchanged**.
Version 2 goes to `flowconfig.LoadBytes` plus `flowconfig.Lint`.

Preserve all three existing behaviours exactly: `--json` emits the same response shape,
`--no-env` skips expansion, and the exit code is 1 if any issue has a severity other than
`warning`. The `--json` shape is consumed by the editor UI — changing it breaks Chunk 10.

- [ ] **Step 2: Add `hookway schema --nodes`**

`runSchema` currently prints the whole-config schema. Add a `--nodes` flag that emits a
JSON object mapping node type name to that type's option JSON Schema, built with the same
`jsonschema.Reflector` configured in `internal/config/schema.go`. This is Chunk 10's
contract — the UI renders a property panel per node type from it, instead of the
hand-coded forms in `web/src/graph/*Form.tsx`.

- [ ] **Step 3: Write the three example configs**

`examples/v2/github.yaml` is the minimal valid flow from Task 1's test, with real env
placeholders. `examples/v2/invalid-gate-order.yaml` wires `rate_limit` after `read_body`.
`examples/v2/invalid-many.yaml` has at least four distinct problems: an unknown node type,
a duplicate node id, a dangling non-Terminal leaf, and a `publish` naming a missing
destination.

Add a header comment to each invalid file saying **it is expected to fail and why**, so
nobody "fixes" them. Then make sure `make validate-examples` skips `examples/v2/invalid-*`
or asserts they fail — check the Makefile target and wire it deliberately either way.

- [ ] **Step 4: Run the verification gate**

```bash
go test ./internal/flowconfig/ ./internal/graph/
go build -o /tmp/hookway ./cmd/hookway

/tmp/hookway validate --config examples/v2/github.yaml
echo "exit=$?  # want 0"

/tmp/hookway validate --config examples/v2/invalid-gate-order.yaml
echo "exit=$?  # want 1, and output must name RequestHead and RawRequest"

/tmp/hookway validate --config examples/v2/invalid-many.yaml
echo "exit=$?  # want 1, and output must list all four problems, not just the first"

/tmp/hookway validate --config examples/v2/github.yaml --json | jq '.issues'
/tmp/hookway schema --nodes | jq 'keys'   # want all 17 node types

/tmp/hookway validate --config examples/config.yaml
echo "exit=$?  # want 0 -- V1 configs must still validate unchanged"

make validate-examples && make test && make lint
```

The last V1 check is not optional. V1 must keep working until Chunk 9.

- [ ] **Step 5: Commit**

```bash
git add cmd/hookway/main.go internal/configapi/configapi.go internal/flowconfig/schema.go \
        examples/v2/ Makefile
git commit -m "feat(cli): validate and schema support for schema_version 2"
```

---

## Done when

- [ ] `hookway validate` accepts `examples/v2/github.yaml` with exit 0
- [ ] `hookway validate` rejects `examples/v2/invalid-gate-order.yaml`, naming both port types
- [ ] `hookway validate` reports **all** problems in `examples/v2/invalid-many.yaml`
- [ ] `hookway validate --json` emits the unchanged response shape
- [ ] `hookway schema --nodes` lists all 17 node types with option schemas
- [ ] **V1 configs still validate and V1 still serves** — `examples/config.yaml` passes
- [ ] `make test`, `make lint`, `make validate-examples` all clean
- [ ] Q11 (one `publish` node vs one node type per destination) is resolved and
      `v2/02-design.md` §4 matches what was built
