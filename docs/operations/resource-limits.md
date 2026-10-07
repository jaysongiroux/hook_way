# Resource limits and sizing

Size Hookway for the traffic and failure backlog you actually expect. The same
configuration and production containers can run locally; there is no separate
security mode or required workstation size. Start small, keep finite budgets,
then raise the relevant limits for legitimate workloads.

## What a limit means

These are separate budgets, not interchangeable memory guarantees:

- Wire bytes bound a buffered request or response, not its parsed object tree.
- Structural depth/count bounds parser expansion; it is not a byte-to-heap ratio.
- Async queue bytes conservatively account retained jobs, not live Go heap.
- Replay retention counts stored artifacts, not PostgreSQL disk usage including
  indexes, history, WAL, replication or backups.
- A container memory limit bounds the entire process. Hitting it may kill the
  process and lose acknowledged volatile jobs. It is containment, not graceful
  rejection or isolation between flows.

## Budget matrix

Defaults below describe the implementation, not a throughput or latency promise.
The [configuration reference](../reference/configuration.md) owns field syntax;
the linked guides explain operational tradeoffs.

| Boundary | Default and adjustment | Enforcement and failure |
| --- | --- | --- |
| Inbound wire body | Required positive `read_body.max_bytes`, per flow. `server.max_body_bytes` is only a lint reference. | Admission precedes body reading; over cap returns `413`. The whole accepted body is buffered. |
| HTTP sockets and headers | Core header read deadline 5s; Go HTTP default header bound. Read/write/idle defaults 5s/10s/60s, configurable positive durations. | Read timeout ends a slow body; write timeout bounds socket writes, **not** all graph execution. Execution admission is not a TCP connection cap; proxy/OS limits are separate. |
| JSON envelope | Depth 128, 1,000,000 nodes; `json.max_depth` up to 1024 and `json.max_nodes` up to 1,000,000. | Valid JSON is structurally checked before tree allocation, including duplicate-key input. Over structure is `413`. Malformed JSON retains raw-base64 fallback. |
| JSON validation | Body 20 MiB/depth 128/1,000,000 nodes; schema 256 KiB/depth 64/10,000 nodes. | Independent fixed validation limits; invalid/over-limit data is `400` or follows configured `on_error`. Raising envelope depth does not raise these. See [JSON validation](../guides/json-validation.md). |
| XML envelope | 1 MiB/depth 256/100,000 nodes; explicit maxima 64 MiB/1024/1,000,000. | Over limit `413`, malformed or prohibited directive `400`. Namespace frames retain local declarations instead of repeatedly copying inherited maps. |
| Multipart | 16 parts, 1 MiB per decoded part; optional decoded aggregate up to 64 MiB. | Read at most the smaller of part cap and remaining aggregate plus one byte; no draining an over-limit part. Limits `413`, malformed `400`. Omitted aggregate still has wire/count/part caps. Metadata-only skips retained base64, not the temporary part buffer. |
| Raw/base64 and file objects | Original wire cap still applies; base64 length is `4 * ceil(n / 3)`. | Raw, encoded and decoded object representations can coexist. Destination payload limits apply independently; projecting or using object storage can be necessary. There is no streaming intake. |
| HTTP response body | `http_request.max_response_bytes`: 10 MiB default, configurable up to 64 MiB. | Cap-plus-one read, `502` on excess and `504` on timeout. Automatic decompression disabled; compressed response wire bytes preserved. |
| HTTP JSON response bindings | 64 KiB default selection budget, configurable up to 1 MiB; fixed depth 128/1,000,000 nodes. | Preflight before independently decoding selection data. Failure sets selection-error metadata; original status/body remain available for passthrough. |
| Request admission | 256 executions; positive override. Explicit `-1` remains supported but removes this protection. Pending queue disabled; enable with positive count and positive wait timeout. | Full/expired/closing admission returns `503` with `Retry-After: 1`, no flow body read. HTTP/1 rejected unread bodies close the connection to avoid slow-drain delay. Health/readiness bypass admission. |
| Publication fan-out | 8 concurrent publishes per request for `skip_failed`, adjustable `server.fanout_concurrency`. | Not a process-wide publish cap. Multiply by active requests; immutable branch copies still cost memory. Publish successors share the same detached publish budget. |
| Batch children | 1,000 children (max 10,000); 64 in-flight per node, process ceiling 1,024; dedicated concurrency default 8; timeout 30s (positive override up to 2m). | Over child count `413`; bounded admission and cancellation; no nested batches. Shared accounting responds `200`, dedicated eligible failures `502`. Parent wire/tree, child/branch copies and publication all count. See [batches](../guides/ingress-adapters.md#batches-and-http-responses). |
| Async webhook jobs | Per destination: 1,000 queued jobs, 8 workers by default, **64 MiB accounted queued-plus-active bytes**. Both limits adjustable; bytes omitted/0 uses default, positive finite override only. | Either count or bytes rejects before async acceptance; one job can exceed byte budget. Readiness at ≥90% of either. Hold accounting through retries, dead-letter publication and terminal callbacks; release on recovered worker panic too. Sync/inline bypass these queue caps. |
| Async byte accounting | Fixed message/value overhead plus conservative retained strings/byte-slices and JSON/base64 headroom; includes headers, raw/object/frozen payloads. | Bounded structural traversal, aliases charged again, cycles refused. Not exact wire bytes, allocator cost or hard heap. Observe `hookway_webhook_queue_accounted_bytes` against its capacity gauge. |
| Destination sockets/pools | RabbitMQ 8 publisher-confirm channels, configurable; PostgreSQL/MongoDB driver defaults unless explicitly capped. Shared state and lookup pools are separate. | Requests share finite operation deadlines, including pool/connect waits where supported. Sum pools across destinations, providers and replicas; no universal process-wide connection cap. |
| Dynamic target cache | 10,000 cached targets, FIFO eviction. | Same-target provisioning shares a flight; each waiter honors its own cancellation, while the shared operation retains its initiating deadline. |
| Lookup/cache | Provider timeout 2s (max 30s); cache 10,000 entries (max 1,000,000), positive values TTL 5m (max 24h). | Backend failure `503`, missing value without fallback `400`; errors not cached. Same-key requests share work. See [lookups](../guides/lookups.md). |
| Protection state | Rate identity capacity 10,000; replay reservations 1,024 with 5m TTL; adjustable. Backend deadline 250ms default, explicit per-process pools. | Quota exhaustion `429`; identity-capacity/store failure `503` and readiness withdrawal. No blind retry of uncertain mutations. Hot PostgreSQL policies serialize even different identities. See [protection stores](../guides/protection-stores.md). |
| Capture buffer | 128 records / 64 MiB estimated record bytes, 4 upload workers. | Best-effort drop metrics on pressure; does not change inbound success/readiness. Snapshot building and temporary serialization can allocate outside buffer accounting. |
| Replay artifacts/retention | Artifact 16 MiB; retention 100,000 records/10 GiB/168h, adjustable. Database pool 10 per replica. | Oversized artifact `413`, new captures at capacity `507`; core counts drops. Storage accounting is not total database disk. Child recovery records can still add storage. See [replay configuration](replay-service.md#configuration). |
| Replay runs/control | 4 worker slots (max 256), execution 30s, lease 60s; lease at least execution +15s. | Core execution budget subtracts 5s; worker reserves final 10s of lease for finalization. No automatic resend after a possibly written request or lost result; inspect `unknown` outcomes first. |
| JavaScript | Timeout 1s, input/output 1 MiB, stack 1000; configurable. | Statement interruption and input/output checks do **not** cap VM heap or guarantee interruption of native work. Trusted authors only; see [Code node security](../reference/security.md#code-node). |

## Time budgets during failures

Leave room for admission waiting, body reading, authentication, lookups,
transforms and publication inside the sender's acknowledgement deadline. The
HTTP write deadline begins before all that work has completed. Validation of
individual durations does not prove the sum fits; do not set publication equal
to the entire response budget and assume the response always arrives in time.

Normal publication deliberately survives inbound cancellation, but shares the
finite `server.publish_timeout` (10s default), including continuation nodes and
fan-out. Keep the configured publish budget no greater than write/shutdown
budgets. Never add a resend because a client disconnected: an effect may already
have occurred, and uncertain duplicate reservations must remain.

RabbitMQ's destination publish budget defaults to 5s and includes retries,
connection waiting, TCP/TLS/AMQP handshake, setup, channel operations, writes and
confirmation. Canceling a stalled shared socket can also make other concurrent
sends uncertain. Existing at-least-once backend retries remain possible within
their budgets; cancellation does not prove a message was never sent.

`http_request.timeout` is per attempt, not a total retry budget. Multiply attempts
by timeout and add bounded backoff before choosing sender and server deadlines.
The async webhook default is three attempts with 10s per attempt, initial 500ms
backoff and a 30s backoff cap (including `Retry-After`); dead-letter forwarding
can add another 10s. Ignored webhook response bodies drain at most 64 KiB before
closing, while retaining the HTTP attempt deadline. Async retry lifetime can
exceed an inbound publish deadline because queue acceptance has already happened.

Capture shares 5s across at most three upload attempts and allows a final 6s
drain. `server.shutdown_total_timeout` bounds the complete core termination
sequence, including sequential destination drains. An expired async drain does
not cancel retained workers or safely free their dependent resources; shutdown
must exit unsuccessfully. Set platform termination grace above this total.
See [shutdown and upgrades](upgrades.md#core-shutdown-contract).

## Adjustable sizing recipes

These are starting points, not measured capacity claims. Apply overrides to a
[runnable local production configuration](../development/local-development.md),
validate it, and measure the same configuration before promotion. Private local
destinations and production TLS remain supported; raising budgets needs no code
fork or developer-only security bypass.

### Ordinary small events

For a constrained single replica, start with 8 active requests, no pending
admission queue, a 1 MiB per-flow wire cap, four fan-out/async workers and a
32 MiB accounted async queue cap with 128 queued jobs. A 256 MiB container is a
**trial allowance**, not guaranteed sufficient for all parsed inputs. Inspect
peak RSS/Go heap and rejects before changing throughput. Lower count and byte
limits together if the process cannot maintain headroom. Prefer immediate
backpressure to retaining a large failure backlog in memory.

### Legitimate large files

For 12 MiB files, start with two active requests and a raw flow (no unnecessary
JSON, multipart or JS conversion). Set its `read_body.max_bytes` to `12582912`;
for parsed XML, also raise `envelope.xml.max_bytes`. For passthrough replies set
`http_request.max_response_bytes` above the actual wire response, for example
16 MiB. Keep async count low (for example two queued jobs) and use a finite
256 MiB accounted byte allowance with a trial container allowance of at least
512 MiB. Conservative accounting may permit only one such job at a time; raw,
base64 and object copies vary by path. Raise the byte cap only with measured
memory headroom, or use sync/confirmed object-storage publication instead.

Multipart users must budget framing and decoded content separately. A 12 MiB
file exceeds both the default 1 MiB part cap and the default code input cap;
changing only the ingress cap cannot make that path valid. Never use the
base64 4/3 factor as a complete peak-memory estimate.

### Shared protection and lookup traffic

Start with small explicit pools (for example two connections per shared provider
per core), a 250ms protection deadline, conservative identity capacity and no
extra backend retry layer. Measure same-key and many-key traffic separately;
policy contention can dominate even with different identities. Budget the
aggregate connection count across every replica and provider. Increase a pool
only if dependency capacity and pool-wait measurements support it.

### Failure capture and replay

Size capture for best-effort failure/inspection bursts; its buffer is not a
durable acceptance guarantee. Alert on buffer, upload and replay-capacity drops.
Begin replay with one or two workers and an explicit small database pool on a
constrained machine. Each worker can hold roughly `2 * max_record_bytes +
64 MiB` in snapshot/request/result data; ingest adds roughly twice the artifact
cap. Add runtime and database headroom, and increase worker concurrency only
after measuring. Storage expiry does not eliminate WAL/index/backup growth.

## Validate containment, then capacity

Declare the workload and thresholds **before** each trial: image/source hashes,
CPU/memory/PID limits, body mix, scheduled rates and duration, dependency faults,
allowed rejection statuses, probe deadlines, recovery and termination bounds.
Use open-loop scheduled traffic; include skipped/late requests in the denominator
instead of reporting only completed successes. Record HTTP statuses, latency,
queue/capture occupancy and drops, CPU, RSS, Go heap/goroutines, dependency pool
waits, readiness, drain results and recovery. Keep failed trials and explain any
repair before rerunning. Old [performance measurements](../evidence/performance.md)
do not establish a later build's capacity.

Run pathological scripts only in isolated, hard-limited subprocess/container
fixtures with no restart and an external kill deadline, never in the agent or
developer's host process. A native allocation can OOM-kill core; a native regex
can require external termination after the configured JS timeout. Until a
separate execution-isolation design is approved, avoid those constructs and
treat one core as one trusted scripting domain.
