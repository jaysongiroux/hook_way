# Chunk 5: All Destinations and Fan-out Policies

**Status:** implemented
**Depends on:** Chunk 4 (`d7316df`)
**Unlocks:** Chunk 6
**Verification gate:** every one of the 13 destination types delivers under V2, and the
existing testcontainers integration suites pass against V2 flows.

```bash
make test
make integration-test        # RabbitMQ
make aws-integration-test    # SQS/SNS/S3 via LocalStack
make redis-integration-test  # Redis/Valkey
make test-all                # everything
go test ./internal/conformance/ -v
```

**Goal:** Make `publish` work for all 13 destination types, implement both fan-out failure
policies, add `on_error` ports, and add SNS/SQS `MessageAttributes` (gaps doc A4).

## Re-read before starting

- Confirm Q11's resolution. This chunk assumes **one `publish` node** referencing a shared
  `destinations:` entry. If it resolved to one node type per destination, Task 1 becomes
  "register 13 specs sharing one implementation" and the option structs differ.
- Confirm Chunk 3's `PublisherResolver` interface still exists and is how `publish` gets
  its publisher.
- Check whether Chunk 4's `Brancher` optional interface landed as designed — `on_error`
  in Task 3 uses the same labelled-port mechanism.

## Why this is one chunk and not thirteen

The 13 destination types are already implemented, tested, and integration-tested in
`internal/queue`. The V2 work is **one adapter** plus per-type option plumbing. Splitting
per destination would be thirteen chunks of near-identical review.

---

## Task 1: `publish` across all 13 destination types

**Files:** modify `internal/flowconfig/node_publish.go`; create `node_publish_types_test.go`

The 13 types, from `README.md` and `examples/destinations/`: `rabbitmq`, `sqs`, `sns`, `s3`,
`redis_stream`, `redis_list`, `redis_pubsub`, `redis_kv`, `postgres`, `mongodb`, `kafka`,
`webhook`, `temporal`.

The shared `destinations:` block is **reused from V1 unchanged** (Chunk 2 Task 1 already
embeds `config.DestinationConfig`), so all connection config, the `create: true` declaration
behaviour, and the AWS `on_existing_conflict` drift handling (`reconcile` / `warn` / `error`)
come along for free. **Do not reimplement any of it.**

- [x] **Step 1: Write a table-driven test across every type**

One subtest per destination type, asserting `publish` resolves a publisher and delivers.
Use the existing fakes and helpers in `internal/queue`'s tests where they exist — read
`internal/queue/*_test.go` first to find them rather than writing new ones.

```go
func TestPublishSupportsEveryDestinationType(t *testing.T) {
	types := []string{"rabbitmq", "sqs", "sns", "s3", "redis_stream", "redis_list",
		"redis_pubsub", "redis_kv", "postgres", "mongodb", "kafka", "webhook", "temporal"}
	for _, dt := range types {
		t.Run(dt, func(t *testing.T) { /* ... */ })
	}
	// Guard against a new destination type being added to internal/queue
	// without V2 support: assert the list above matches what the queue
	// package actually registers.
}
```

That last guard matters — otherwise a future destination type silently has no V2 support.

- [x] **Step 2: Implement, then pass**

- [x] **Step 3: Commit**

```bash
make test && make lint
git add internal/flowconfig/node_publish.go internal/flowconfig/node_publish_types_test.go
git commit -m "feat(flowconfig): publish node across all 13 destination types"
```

---

## Task 2: Fan-out failure policies

**Files:** modify `internal/graph/executor.go`; `executor_policy_test.go`

Chunk 1 shipped `stop_on_failure` only. Add `skip_failed`, carrying V1's semantics forward
(`server.go:732`–`806`):

- **`stop_on_failure`** (default) — publish routes sequentially until one fails. Response is
  503. `published` lists what already succeeded; `failed` lists the failing route **plus
  every remaining unattempted route**, so replay has a complete destination list even on a
  503.
- **`skip_failed`** — attempt every route. Return **202** when at least one destination
  succeeded, with `failed_destinations` listing the failures.

Where the policy lives: it is a property of the **fan-out point**, not the flow. Put it on
the node whose default port fans out — in practice the last `Envelope -> Envelope` node
before the destinations — or on the flow as a default with per-node override. Decide and
write it in the config schema; the per-node form is more flexible and costs nothing.

Two V1 subtleties that must survive:
- **I14** — a branch filtered out by a `filter` node is **not** a failure. It is removed from
  the fan-out set before the policy applies.
- **I17** — under `skip_failed`, a *partial* failure must release the replay reservation, so
  an identical sender retry is accepted and can redeliver to the destinations that failed.
  V1 does this at `server.go:344`. The retry also re-delivers to destinations that already
  succeeded; consumers dedupe on `idempotency_key`, which is the documented contract.

- [x] **Step 1: Write the failing tests**

Cover: `stop_on_failure` stops at the first failure and reports unattempted routes as
failed; `skip_failed` attempts all and returns 202 with one success and one failure;
`skip_failed` with *zero* successes returns 503; a filtered branch never appears in
`failed`; partial failure under `skip_failed` releases the reservation.

- [x] **Step 2: Implement, then pass**

`skip_failed` attempts every detached successor instead of aborting on the first
error. Fan-out is sequential; `FanoutConcurrency` remains reserved for later
parallel dest workers. Outcomes match V1: 202 with `failed_destinations` on
partial success, 503 when every destination fails.

- [x] **Step 3: Commit**

```bash
make test && make lint
git add internal/graph/executor.go internal/graph/executor_policy_test.go
git commit -m "feat(graph): skip_failed fan-out policy alongside stop_on_failure"
```

---

## Task 3: `on_error` ports

**Files:** modify `internal/graph/executor.go`, `internal/graph/compile.go`; tests

Per `v2/02-design.md` §5: any node may declare an optional `on_error` out-port. If present,
the error payload routes there; if absent, the error propagates per the fan-out policy.

Reuse Chunk 4's labelled-port mechanism — `on_error` is a reserved port label, not a new
concept. So `edges: [lookup:on_error>fallback_dest]` just works if `on_error` is treated as
a declared port on every node.

Compile-time checks to add: an `on_error` edge's target must accept the same payload type
the failing node consumes (the error path carries the node's *input*, since its output never
materialized). That is a genuine subtlety worth a comment and a test — the intuitive
"same as output" is wrong.

- [x] **Step 1: Write the failing tests, then implement, then pass**

Cover: a node with `on_error` routes failures there and the flow succeeds overall; without
`on_error`, the failure propagates; an `on_error` edge with an incompatible target type is a
compile error.

- [x] **Step 2: Commit**

```bash
make test && make lint
git add internal/graph/ && git commit -m "feat(graph): on_error output ports"
```

---

## Task 4: SNS/SQS `MessageAttributes` (gaps doc A4)

**Files:** modify `internal/queue/sns.go`, `internal/queue/sqs.go`;
`internal/flowconfig/node_publish.go`; tests

**This is a silent-failure gap, which is why it is called out separately.** From the gaps
doc A4: universal-gateway attaches `fields_changed` as an SNS message attribute, and
downstream subscription **filter policies** key on it. Without attributes, subscribers just
stop receiving messages — no error anywhere.

`MessageAttributes` appears nowhere in `internal/queue/sns.go` or `sqs.go` today (verified
by grep in the gaps doc).

- [x] **Step 1: Inventory the existing filter policies first**

Universal-gateway live accounts are out of scope. The documented contract from
gaps A4 is `fields_changed` with AWS `DataType: String.Array` (JSON array of
changed field names). That is what V2 implements.

- [x] **Step 2: Write the failing tests**

Before writing code, list the SNS subscription filter policies in the target AWS accounts.
The gaps doc says to do this and it is right: you need to know which attribute names and
`DataType`s are actually required. Record the inventory in the commit message or a note —
it is the acceptance criteria.

- [x] **Step 2: Write the failing tests**

Cover: string, number, and `String.Array` data types; attributes sourced from envelope
fields via templating (`message_attributes: {fields_changed: "{diff}"}`); attribute values
over the AWS size limit produce a clear error; LocalStack integration test asserting a
subscription filter policy actually matches.

The `String.Array` case is the one universal-gateway needs —
`{DataType: 'String.Array', StringValue: JSON.stringify(changedFields)}`.

- [x] **Step 3: Implement, then pass**

- [x] **Step 4: Commit**

```bash
make test && make aws-integration-test && make lint
git add internal/queue/sns.go internal/queue/sqs.go internal/queue/*_test.go \
        internal/flowconfig/node_publish.go
git commit -m "feat(queue): SNS/SQS MessageAttributes sourced from envelope fields"
```

---

## Done when

- [x] All 13 destination types deliver under a V2 flow, with a test each
- [x] A guard test fails if a new destination type is added without V2 support
- [x] `stop_on_failure` and `skip_failed` both match V1's documented semantics
- [x] A filtered branch is never reported as a delivery failure (I14)
- [x] Partial failure under `skip_failed` releases the replay reservation (I17)
- [x] `on_error` ports route failures, and an incompatible `on_error` target is a compile error
- [x] SNS `String.Array` attributes work and a real subscription filter policy matches them
- [x] `make test-all` green; `make conformance` still green
