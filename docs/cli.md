# CLI

Hookway ships one CLI entrypoint at `cmd/hookway`. It can serve traffic, validate config, and run a simple healthcheck for container or deployment probes.

## Serve

Start Hookway with `HOOKWAY_CONFIG`:

```bash
HOOKWAY_CONFIG=examples/config.yaml go run ./cmd/hookway
```

Or pass `--config`:

```bash
go run ./cmd/hookway --config examples/config.yaml
```

The process loads and validates config before connecting to RabbitMQ. `SIGINT` and `SIGTERM` trigger graceful HTTP shutdown using `server.shutdown_timeout`.

## Validate

Validate config without starting the server:

```bash
go run ./cmd/hookway validate --config examples/config.yaml
```

The repo wrapper is:

```bash
make validate
```

Validation rejects unknown YAML fields, missing required settings, invalid durations, duplicate or overlapping endpoint paths, unsupported backends, unsupported delivery failure policies, missing destination routes, unknown destination ids, duplicate destination/queue routes, conflicting queue options for shared queues, unsupported auth types, unsafe queue names, invalid header names, invalid IP allowlist entries, and missing endpoint secrets.

## Healthcheck

Check a running service:

```bash
go run ./cmd/hookway healthcheck --url http://127.0.0.1:8080/healthz --timeout 5s
```

The command exits `0` for any `2xx` response and `1` for connection failures or non-`2xx` responses. Use `/healthz` for process liveness and `/ready`, `/readyz`, or `/health` when the check must include RabbitMQ readiness.
