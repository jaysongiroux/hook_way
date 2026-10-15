# Hookway

Hookway is a webhook intake service that forwards verified third-party events to RabbitMQ, AWS SQS, AWS SNS, AWS S3, AWS Lambda, Redis / Valkey, Postgres, MongoDB, Kafka, Temporal, or another HTTP webhook.

It is designed for production webhook ingress where the receiving service should validate signatures, normalize event envelopes (JSON, XML, multipart, or raw), and hand work off to consumers through a queue, topic, object store, or HTTP webhook. Configuration is YAML: `flows` of typed nodes wired by `edges`.

## Capabilities

Signature verification, typed routing/transforms, bounded request protection,
destination fan-out and optional failure recovery are configured in YAML.
See the [project overview](https://github.com/jaysongiroux/hook_way/blob/main/README.md)
and [configuration reference](https://github.com/jaysongiroux/hook_way/blob/main/docs/reference/configuration.md)
for supported backends, fields and limits. A successful acknowledgement follows
the configured [delivery contract](https://github.com/jaysongiroux/hook_way/blob/main/docs/operations/delivery-guarantees.md).

## Image Tags

- latest: latest stable GitHub release (a `vX.Y.Z` tag, not every commit on main)
- vX.Y.Z: semantic release tags, for example v1.0.0
- Pre-release tags such as vX.Y.Z-rc.1 are published but do not move latest

## Runtime

Hookway reads YAML configuration from a file path supplied by the HOOKWAY_CONFIG environment variable or the --config flag.

The core container entrypoint is /usr/local/bin/hookway. Published images contain
only the deployable runtime and do not require Node.js or the optional config
builder. The image includes a built-in healthcheck command, so the runtime image
does not need shell utilities.

The matching `<image>-replay` image runs the optional `hookway-replay` service. It uses PostgreSQL for captured failed deliveries; run `hookway-replay migrate --config replay.yaml` before starting or upgrading it. See the [replay service guide](https://github.com/jaysongiroux/hook_way/blob/main/docs/operations/replay-service.md).

## Links

- Source: https://github.com/jaysongiroux/hook_way
- Docs: https://github.com/jaysongiroux/hook_way/blob/main/docs/README.md

## Standalone operations frontend

Matching releases publish `hookway-console` separately from the core and replay
Go images. Run it with `REPLAY_API_URL=https://replay-api.example.com` on port
8080, with `/healthz` for health checks. It serves the operations console at `/`
and the public YAML builder at `/builder`; it includes matching Go WASM and
public build identity at `/build-info.json`. Set replay
`server.cors.allowed_origins` to the frontend origin, without a path. The browser
calls the API directly; this container never proxies API requests or needs keys.
Pin all three images to verified immutable release digests and scale frontend
and replay services independently. TLS, ALBs/ingress, WAF and access policies are
deployer-owned. See [console deployment](https://github.com/jaysongiroux/hook_way/blob/main/docs/operations/replay-console.md).
