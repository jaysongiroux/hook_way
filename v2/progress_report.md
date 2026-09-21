# Hookway V2 progress report

Merge-readiness hardening for the typed-port Go runtime on branch `v2`.
Typed-port `PortType.Accepts`, replay-capture I9/I15 (`BeforeDetached` /
best-effort capture), and metric names/label sets were not changed.

## Accepted SHA

- Commit: `38325c865e7f4af9c3f0e618dded6b70985cbd85` (unsigned)
- Parent hardening commit: `e5d21f90eb229ed1cd9302d4a02e1978c5ba4564`
- CI: https://github.com/jaysongiroux/hook_way/actions/runs/35574453706
- Coverage floor: **80.9%** (`coverage.floor`, `go test -short` statement coverage,
  rounded down to one decimal)

## CI jobs (all pass at `38325c8`)

| Job | Result |
| --- | --- |
| Checks / Go | pass |
| Checks / Go race | pass |
| Checks / Examples | pass |
| Checks / Web | pass |
| Checks / Web e2e | pass |
| Checks / RabbitMQ | pass |
| Checks / AWS | pass |
| Checks / Redis | pass |
| Checks / Postgres | pass |
| Checks / MongoDB | pass |
| Checks / Kafka | pass |
| Checks / Webhook | pass |
| Checks / Temporal | pass |
| Checks / Replay store | pass |
| Checks / Image | pass |
| Checks / golangci-lint | pass |
| Checks / govulncheck | pass |
| Checks / Coverage | pass |
| Checks / Formatting | pass |
| Checks / Observability | pass |
| Checks / All checks passed | pass |
| Vercel | pass |
| Vercel Preview Comments | pass |

Integration jobs run with `HOOKWAY_REQUIRE_INTEGRATION=1`. Unit/race use `-short`.

## Hardening landed

- Malformed templates fail validate/compile for `http_request`, `publish`,
  `respond`, `lookup`, and `replay_guard` (no empty-template fallback).
- Rendered HTTP targets require `http`/`https` and a host.
- Replay `key` stays optional (body SHA-256). A configured but unrenderable key
  is `NodeError` 400, not a body-hash fallback.
- `X-Trace-ID` is accepted only when it is 32 hex chars and not all zeros;
  Traceparent extraction is unchanged.
- Respond status is 200–599; 204/304 reject a configured body and write none.
- Secret startup deadline is 10s covering AWS config load and first fetch.
- Webhook and metrics servers set `ReadHeaderTimeout` to 5s.
- SIGINT/SIGTERM and listener-error paths drain every listener, wait in-flight,
  close replay then flow then publisher, join errors, and exit 1 on drain/close
  failure.
- Node panics become `graph.PanicError` and increment
  `hookway_panics_total{location="node"}` plus node outcome `"panic"`.
- `HOOKWAY_LOG_LEVEL` overrides `logging.level` (else info). Empty
  `logging.header_allowlist` logs no request headers (fail-closed).
- Publish HELP text and Grafana titles are generic V2 publish (same series).
- Prometheus 3.14.0 alert rules in `deploy/alerts.yaml`.
- golangci-lint v2.13.2, govulncheck v1.8.0, coverage floor, fmt/diff checks.
