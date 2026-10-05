# Protection benchmark evidence

## Local sample: 2,000 admissions per variant (2026-10-04)

Run on the same Apple M1 Pro/macOS host with Go 1.26.8, Docker Redis 7 and
PostgreSQL 17. The declared profile expands each variant to 2,000 operations,
with one or four workers, one hot identity or distinct identities, generous
quotas, ten-connection pools and a two-second test deadline. Any backend error
fails the benchmark. These are local samples; no production throughput threshold
was asserted. Some frontend tests ran concurrently on the host.

```sh
HOOKWAY_REQUIRE_INTEGRATION=1 go test ./internal/protectionstore -run '^$' \
  -bench '^BenchmarkProtection$' -benchtime=2000x -count=1
HOOKWAY_REQUIRE_INTEGRATION=1 go test ./internal/protectionstore -run '^$' \
  -bench '^BenchmarkProtectionMaintenance$' -benchtime=1x -count=1
```

| Backend | Strategy | Identities | Workers | Elapsed/op ms | p95 ms | p99 ms | Go allocation bytes/op |
|---|---|---|---:|---:|---:|---:|---:|
| memory | token_bucket | hot_key | 1 | 0.0029 | 0.0045 | 0.0069 | 1714 |
| memory | token_bucket | hot_key | 4 | 0.0038 | 0.0191 | 0.0378 | 1716 |
| memory | token_bucket | many_keys | 1 | 0.0029 | 0.0048 | 0.0067 | 1975 |
| memory | token_bucket | many_keys | 4 | 0.0037 | 0.0198 | 0.0487 | 1978 |
| memory | sliding_window_counter | hot_key | 1 | 0.0024 | 0.0041 | 0.0066 | 1762 |
| memory | sliding_window_counter | hot_key | 4 | 0.0033 | 0.0175 | 0.0280 | 1763 |
| memory | sliding_window_counter | many_keys | 1 | 0.0026 | 0.0034 | 0.0071 | 2023 |
| memory | sliding_window_counter | many_keys | 4 | 0.0035 | 0.0210 | 0.0591 | 2026 |
| redis | token_bucket | hot_key | 1 | 0.4043 | 0.7091 | 2.4210 | 2875 |
| redis | token_bucket | hot_key | 4 | 0.1159 | 0.6805 | 1.0440 | 2989 |
| redis | token_bucket | many_keys | 1 | 0.2404 | 0.3302 | 0.4035 | 2885 |
| redis | token_bucket | many_keys | 4 | 0.1018 | 0.5711 | 0.7570 | 3000 |
| redis | sliding_window_counter | hot_key | 1 | 0.2322 | 0.4821 | 0.6473 | 2923 |
| redis | sliding_window_counter | hot_key | 4 | 0.0901 | 0.6654 | 0.8347 | 3035 |
| redis | sliding_window_counter | many_keys | 1 | 0.2390 | 0.3174 | 0.6870 | 2932 |
| redis | sliding_window_counter | many_keys | 4 | 0.1074 | 0.6166 | 0.7490 | 3044 |
| postgres | token_bucket | hot_key | 1 | 1.6499 | 2.3850 | 3.8090 | 8231 |
| postgres | token_bucket | hot_key | 4 | 1.2449 | 5.8570 | 7.1920 | 8362 |
| postgres | token_bucket | many_keys | 1 | 2.0926 | 2.6160 | 3.5220 | 8111 |
| postgres | token_bucket | many_keys | 4 | 1.9294 | 9.5330 | 13.4300 | 8262 |
| postgres | sliding_window_counter | hot_key | 1 | 1.5009 | 1.8180 | 1.9790 | 8239 |
| postgres | sliding_window_counter | hot_key | 4 | 1.2841 | 6.7330 | 11.4100 | 8383 |
| postgres | sliding_window_counter | many_keys | 1 | 1.9947 | 2.6950 | 3.7710 | 8137 |
| postgres | sliding_window_counter | many_keys | 4 | 1.7363 | 8.9350 | 12.4700 | 8289 |

In this run PostgreSQL p99 ranged from 1.979–13.43ms; Redis from
0.4035–2.421ms. A smaller preliminary sample had much longer PostgreSQL tails;
host contention varies, and local runs cannot establish universal capacity.
Admission wall-time throughput includes concurrent workers; it is not individual
latency. PostgreSQL four-worker variants reported connection-acquisition wait
means of 7.4–28.1 microseconds/op, alongside their serialized policy updates.
Go allocation bytes/op are temporary allocations, not retained heap, process RSS
or a prediction of a full production fleet's memory requirement.

The repeated 1,000-identity maintenance sample measured:

| Backend | Operation | Elapsed ms | Backend bytes after operation | WAL bytes/op |
|---|---|---:|---:|---:|
| Redis | policy update | 13.506 | 657352 key bytes | not measured |
| Redis | cleanup (100 rows) | 1.124 | 588552 key bytes | not measured |
| PostgreSQL | policy update | 317.140 | 974848 table bytes | 888776 |
| PostgreSQL | cleanup (100 rows) | 5.304 | 1384448 table bytes | 11472 |

A single maintenance observation does not estimate tail latency or sustained
autovacuum/disk growth. Policy update remains an explicit maintenance operation;
its total runtime can exceed a request's admission deadline. Table growth after
cleanup reflects retained pages/dead tuples, not extended logical protection.
Physical reclamation and vacuum need production monitoring.

Retain raw benchmark logs and hashes with the [release verification](../operations/releases.md)
record. Sustained shared-protection
load, steady-state RSS, production contention and failover budgets remain target
deployment gates. The older 5,000 requests/s ingress report measured a different
configuration before these shared-store features and cannot size them.

PostgreSQL coordinates capacity and updates with a policy lock, so decisions for
different identities within a shared policy also serialize. The production store
deadline defaults to 250ms; benchmark operations use two seconds and fail on any
backend error. Select policy boundaries and deadlines from deployment measurements.
Memory, Redis and PostgreSQL remain supported choices. Redis key bytes and
PostgreSQL table/WAL bytes are different measurements, not equivalent memory costs.
