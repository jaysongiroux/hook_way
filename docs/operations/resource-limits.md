# Resource limits and sizing

Start with finite limits and size them for your payloads, concurrency and failure
backlog. The same settings work locally and in production; there is no minimum
workstation size or special security mode.

For field syntax, see [Configuration](../reference/configuration.md).
For diagnosing overload, see [Saturation](observability.md#saturation).

## What a limit means

A body-size limit is not a process-memory limit. Hookway buffers accepted bodies;
parsing, base64 encoding, branch copies and capture can hold additional copies.
Async queue accounting is conservative, not a measurement of Go heap.

Likewise, replay artifact usage excludes database indexes, WAL and backups.
Monitor actual process memory and database disk space as well as application
limits. Container memory limits can kill the process and lose volatile work;
they do not guarantee graceful rejection.

## Budget matrix

These defaults help identify which limit to adjust. They are not throughput targets.

| Work | Main limits | When exceeded |
| --- | --- | --- |
| Incoming body | Set positive `read_body.max_bytes` on every flow; `server.max_body_bytes` is only a lint reference | 413 |
| Request admission | 256 active requests by default; pending queue disabled | 503 with `Retry-After: 1`, before reading the body |
| Parsed JSON | Depth 128 and 1,000,000 nodes; independently configurable | 413 for excessive structure; malformed JSON retains raw-base64 fallback |
| Parsed XML | 1 MiB, depth 256 and 100,000 nodes by default | 413 for limits; 400 for malformed/prohibited input |
| Multipart | 16 parts and 1 MiB per decoded part by default; optional aggregate cap | 413 for limits; 400 for malformed input |
| JSON validation | Body 20 MiB; schema 256 KiB; separate depth/work limits | 400 or the configured error path; see [validation limits](../guides/json-validation.md#limits-and-api-contract) |
| HTTP response | `http_request.max_response_bytes`: 10 MiB by default, up to 64 MiB | 502 for excess, 504 for timeout; JSON response bindings have separate selection limits |
| Batch processing | 1,000 children by default (max 10,000); bounded child concurrency and timeout | Over-size input returns 413; see [batch responses](../guides/ingress-adapters.md#batches-and-http-responses) |
| Async webhook queue | Per destination: 1,000 queued jobs and 64 MiB accounted queued-plus-active bytes | Publication fails before acceptance; readiness withdraws at 90% of either limit |
| Failure/inspection capture | Shared buffer: 128 records and 64 MiB estimated bytes | Best-effort uploads can drop; the sender's response is unchanged |
| Replay storage | Artifact 16 MiB; retention 100,000 records, 10 GiB and 168h by default | 413 for oversized ingest, 507 at capacity; see [replay settings](replay-service.md#configuration) |
| Replay execution | 4 workers, 30s execution, 60s lease by default | A lost result/expired lease can leave delivery unknown |
| JavaScript | 1s timeout and 1 MiB input/output by default | Checks do not cap VM heap; use trusted, bounded scripts |

Wire, decoded and destination limits apply independently. Raising an ingress cap
does not raise JSON validation, multipart, code-node or broker limits. For large
files, publish an object reference or use raw/synchronous forwarding where appropriate.

Publication fan-out defaults to eight concurrent sends **per request**, not per
process. Connection pools also multiply across destinations, providers and
replicas. Size the database and downstream services for that combined load.
See [lookups](../guides/lookups.md) and [protection stores](../guides/protection-stores.md)
for their deadlines, caches and identity limits.

## Time budgets during failures

Leave room for admission waiting, upload, authentication, lookups, transforms and
publication before the sender's deadline. The HTTP write timeout does not cancel
all graph execution. Normal publication survives caller cancellation within
`server.publish_timeout` (10s default); a disconnected caller can still have
caused a delivery.

`http_request.timeout` is per attempt. Allow for retries and backoff when setting
the overall response deadline. Async webhook delivery continues after queue
acceptance and can outlast the inbound request; its default three attempts and
dead-letter forwarding need their own shutdown allowance.

Set container termination grace above `server.shutdown_total_timeout` and
replay's `max(30s, workers.lease)`, with margin. Follow the
[shutdown procedure](upgrades.md#core-shutdown-contract). Forced termination can
lose acknowledged async work.

## Adjustable sizing recipes

These are trial configurations, not measured capacity guarantees. Apply them to
the [local production recipe](../development/local-development.md#persistent-local-production-setup),
validate, then measure memory and rejection rates.

### Ordinary small events

On a constrained single replica, try eight active requests, no waiting queue,
a 1 MiB body cap, four fan-out/async workers, 128 queued jobs and a 32 MiB async
byte cap. A 256 MiB container is a starting allowance; reduce concurrency if
parsed inputs or failures leave too little headroom.

### Legitimate large files

For a 12 MiB raw file, set `read_body.max_bytes: 12582912` and start with two
active requests. Parsed XML, multipart, HTTP replies and code nodes need their
own limits raised where applicable. Avoid unnecessary parsing/base64 copies.
If using async delivery, start with a small queue and measure retained memory;
sync delivery or object storage may be simpler.

### Shared protection and lookup traffic

Start with small explicit connection pools, then measure backend waits and
same-key versus distinct-key traffic. PostgreSQL protection decisions serialize
within one policy, including different identities. Increasing pools cannot
remove that contention.

### Failure capture and replay

Start with one or two replay workers and a small database pool. A worker can hold
roughly `2 * max_record_bytes + 64 MiB`; ingest can hold roughly twice the
artifact cap. Leave additional runtime/database headroom. Alert on capture drops
and disk growth; successful retries do not immediately free artifacts.

## Validate containment, then capacity

Measure your actual payload mix and acknowledgement mode. Count planned,
completed, rejected and late requests, and inspect latency, CPU, memory, queue
usage, capture drops and destination receipts during load and after draining.
Use [performance measurements](../evidence/performance.md) as examples of reporting,
not as capacity for another build or workload.

Run pathological-script tests only in disposable processes/containers with hard
memory limits and an external kill deadline. JavaScript allocations can exhaust
memory, and native work such as a regular expression can outlast the script
timeout. Do not run such tests inside your working server.
