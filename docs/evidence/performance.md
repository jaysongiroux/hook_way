# Performance results and methodology

Measured on **2026-10-01**, using a local Docker deployment of the V2 ingress
runtime. Two Hookway replicas, each limited to **2 vCPU and 4 GiB**, passed
three **5,000 requests/s, 20-second bursts** and a continuous **500 requests/s,
30-minute mixed workload** against a synthetic destination. These are measured
results for the recorded build and workloads, not an AWS capacity claim or a
production availability guarantee.

## Recorded build and environment

- Go **1.26.8**, Linux/arm64 application containers on an Apple M1 Pro host.
- Docker Desktop VM: 10 CPUs and 8,217,059,328 bytes of available memory;
  application, nginx, destination and load generators shared this host.
- Application image: `sha256:e38601d585d77b2db96f748d74c08622fb8de067fff1a4411a98edb81de58027`.
- Runtime input fingerprint: `2549bcfe32fb1ec8f8b660e05397b52fae421ddc6c4702cba5cdce54cdccebca`;
  base Git revision `a2d9d92c9c6b4c90673019b946eb0b7ce295e983`, with local changes.
  The fingerprint includes runtime inputs and synthetic fixtures; it is not
  a Git commit or an assertion that later working-tree changes were measured.
- nginx round-robin proxy, HTTP/1.1 upstream reuse, request/response buffering
  disabled and upstream retries disabled. A failed request was not replayed
  automatically to another replica.
- Synthetic destination implemented SQS publication and routing-lookup response
  protocols, adding **10 ms per send** and **10 ms per routed lookup**. It
  acknowledged requests and counted receipts without durable queue storage.
  LocalStack was not used as the destination for these capacity measurements.

Application bindings per replica were `GOMAXPROCS=2`, `GOGC=100`,
`GOMEMLIMIT=3GiB`, 256 executing requests, 6,144 bounded waiting handlers and a
four-second admission wait. Publication timeout was five seconds; the client
and body-read timeouts were 30 seconds, and the HTTP write timeout was 35 seconds.
These are explicit small-webhook test settings. The default waiting queue is
disabled; these settings are not recommended sizing for maximum-size documents.

The measured signed and ordinary bodies were 6,532 bytes. Timestamped signatures
were refreshed by the generator, preserving the configured five-minute validity
window. Repeated event identities make this a publication-operation workload,
not a test of distinct FIFO messages, deduplication or consumer delivery.

## Burst results

Each repetition planned and dispatched 100,000 signed requests over 20 seconds
through nginx. Every request returned its expected HTTP 200 before five seconds,
and acknowledged publication counts matched the destination receipts.

| Repetition | Offered rate | Timely successes / planned | Highest driver p99 histogram bound | Mean consumed CPU cores per replica | Largest sampled application memory |
|---|---:|---:|---:|---|---:|
| 1 | 5,000/s | 100,000 / 100,000 | 500 ms | 1.28 / 1.30 | 212 MiB |
| 2 | 5,000/s | 100,000 / 100,000 | 1,000 ms | 1.34 / 1.36 | 304 MiB |
| 3 | 5,000/s | 100,000 / 100,000 | 1,000 ms | 1.62 / 1.65 | 395 MiB |

Across these three bursts: **300,000 / 300,000 timely successes**, with zero
503s, transport errors, publication errors, generator skips or over-budget
responses. One replica was not required to handle the whole 5,000/s workload.
Six before/after echo calibrations each dispatched and passed 100,000 requests.

## Continuous mixed workload

One uninterrupted 30-minute run offered 500 requests/s across three paths.
All **900,000 / 900,000 planned requests** completed with expected HTTP 200
responses below five seconds. Publication counts matched all 900,000 destination
receipts; routing lookups matched 298,800.

| Path | Requests/s | Timely successes / planned | p50 / p95 / p99 histogram upper bounds |
|---|---:|---:|---|
| Fixed publication | 167 | 300,600 / 300,600 | 25 / 25 / 25 ms |
| Timestamped signature | 167 | 300,600 / 300,600 | 25 / 25 / 25 ms |
| Lookup then publication | 166 | 298,800 / 298,800 | 25 / 50 / 50 ms |

Each replica averaged about 0.25 consumed CPU cores. Sampled container memory
peaks were 60.13 and 73.93 MiB. Executing and waiting admission gauges drained to
zero, and goroutines settled at 20 per replica in the post-run idle observation.
This was a continuous run, not an aggregation of restarted short segments.

The archived soak proxy counted two extra upstream observations that could not
be classified retrospectively. They are excluded from the workload denominator.
Generator responses and application/destination publication counts reconcile
exactly. A subsequent 5,000-request check retained method/path access records
and reconciled every generated request without unclassified observations.

## Additional workload checks

On the same application image and two-replica allocation:

- Three additional 20-second bursts with a 50% native / 50% JavaScript-fallback
  mix passed **300,000 / 300,000** planned requests. The highest per-driver p99
  histogram bounds were 1,000, 500 and 1,000 ms. JavaScript remains available
  when the native canonicalization path cannot prove identical signing bytes.
- Shared sequential and dedicated concurrent batch profiles passed **42,000
  parent requests and 420,000 child publications**. Profiles varied between
  10 and 100 children per parent. A parent request and its child operations
  are different capacity units; this does not establish 5,000 batch parents/s.
- Decimal **12 MB = 12,000,000 bytes**: raw binary, DOCX-shaped ZIP, multipart,
  XML and JSON/base64 profiles passed 26 requests each at up to eight concurrent
  clients, alongside 500 ordinary webhooks/s. All **130 document requests** and
  **60,000 coexistence webhooks** passed byte/digest and response checks.
  Formats ran sequentially; there were at most eight total document clients.
- Sixteen 12,000,000-byte binary responses passed exact size and SHA256 checks
  at eight concurrent clients. This response test ran separately from the
  coexistence stream. Sampled memory in the document coexistence run reached
  approximately 476.3 MiB per replica; it is not a hard peak-memory bound.

The transparent JSON relay has a wire-size cap, not a decoded base64 file-size
validator. Its upstream owns the decoded-document limit. Raw/XML/multipart
oversize checks returned 413 before forwarding. PDF/DOCX-shaped fixtures test
transport, not document format validation.

## Methodology and interpretation

1. Record the exact application image, configuration, payload sizes and
   allocation of every role. Use realistic neutral payloads and independent
   signing/body-digest expectations.
2. Calibrate the generator, proxy and synthetic destination before and after
   load at the offered rate. Monitor their CPU, memory and OOM state separately
   from Hookway; fast echo calibration alone cannot qualify a slow-backend test.
3. Schedule load by time, independently of response completion. Keep every
   planned request in the denominator. A missed dispatch, capacity skip, wrong
   response, transport error or response taking at least five seconds fails
   the timely-success gate. Use a 30-second per-request timeout.
4. Reconcile generator counts with per-replica publication metrics and
   independently counted destination receipts. For batch traffic, reconcile
   both parent requests and child publication operations.
5. Repeat bursts and run an uninterrupted sustained workload. Sample CPU,
   throttling, memory, heap, goroutines and admission gauges during load and
   after drain. Resource samples do not establish exact peaks; histogram
   bounds do not establish exact percentiles.
6. Preserve failed trials. Earlier trials exposed admission refusal logging
   amplification, canonicalization cost, and generator/proxy resource limits.
   A fallback trial that skipped 5,044 planned requests failed; increasing only
   generator outstanding capacity produced the three full fallback passes.

The signed-handler component benchmark fell from approximately 181–184 µs/op
and 227 KB allocated/op to 91–96 µs/op and 113 KB allocated/op on the recorded
host. The native path proves when JSON bytes already match the JavaScript
representation and avoids constructing a VM for those inputs; other inputs
fall back to JavaScript. This component comparison does not predict an equal
end-to-end throughput improvement. Reusable broker clients/connections and
bounded, nonblocking overload logging address separate sources of contention.

These finite samples observed 100% timely success; they do not establish a
99.99% or 99.999% production SLA. They also do not establish real AWS durability,
FIFO ordering/deduplication, SNS filtering, TLS behavior, autoscaler response,
a workday-long leak-free lifetime, or 5,000/s bursts mixed with maximum-size files.
The deployment must validate its actual traffic mix and downstream behavior.

## Repeating measurements

Use an existing load generator and load balancer against your deployment; no
performance-specific deployment stack or load client is shipped. Use fixtures
whose repetition is safe, and report planned/completed work, dispatch skips and
latency failures according to the methodology above. Go hot-path benchmarks
remain available for profiling:

```bash
go test ./internal/httpapi -run '^$' -bench BenchmarkRepresentativeSignedFlow -benchmem
go test ./internal/jsoncanon -run '^$' -bench . -benchmem
```

The original raw traces, neutral fixtures and helper sources are retained as
local evidence outside source control. This report summarizes them; it is not
an archived runnable benchmark suite. Collect a fresh baseline for a new build
or changed destination, workload, hardware, proxy or admission configuration.

## Replay capture and ingest benchmarks

This is a separate historical replay sample, not part of the ingress workload or
build recorded above. Keep its environment and sizing limits with the results.

Measured with `make bench-replay` on an Apple M4 Pro (arm64, 12 cores), Go
1.25.4, PostgreSQL 16 in a local container — single-process measurements to
size a deployment, not throughput claims, from one run each. Core's figures
are the request path of a `skip_failed` flow with one of two destinations down
and a raw body (`BenchmarkCaptureOverhead`, 200 requests each; p99 is the
198th-fastest); replay's are ingests of one snapshot and one payload of the
given size each (`BenchmarkIngest`, 30 records each, one ingester and
GOMAXPROCS ingesters).

| Payload | Core p99 without capture | Core p99 with capture | Record uploaded | Replay ingest, 1 ingester | 12 ingesters |
|---|---|---|---|---|---|
| 1 KiB | 0.29 ms | 0.46 ms | full | ~480 records/s | ~175 records/s |
| 256 KiB | 3.3 ms | 4.0 ms | full | ~135 records/s | ~145 records/s |
| 4 MiB | 49 ms | 46 ms | full | ~12 records/s | ~12 records/s |
| 16 MiB | 177 ms | 175 ms | metadata-only (`too_large`) | ~3 records/s | ~3.5 records/s |

Capture adds bookkeeping, not work, to a request: the failure is recorded and
the response is written; the record is built and uploaded afterwards on
background workers, so above small payloads the difference is run-to-run
noise. A failure whose retained bodies alone exceed the 64 MiB upload buffer —
here a 16 MiB raw body, whose envelope copies and base64 form add up past it —
is uploaded as its metadata-only `too_large` record instead of being held.

Replay ingest is serialized by the usage row every ingest updates: more
ingesters (or more replicas) do not raise it, and at small sizes contention
lowers it. The figures are therefore limits for the whole cluster, bounded by
writing the artifacts to PostgreSQL (roughly 70–115 MB/s here, per-transaction
latency at 1 KiB); size `retention` and the database for the capture rate you
expect.
