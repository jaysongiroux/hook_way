# Wishlist

Remaining:

- Standalone `lookup` provider binding. The node exists; CLI/serve reject it until an injected provider is configured.
- Per-publish destination options: Temporal `workflow_type` / `workflow_id`, Kafka `partition_key`, SQS/SNS FIFO `group_id`, Redis `max_len` / `consumer_group` / `ttl` / `key_template`.
- Config hot reload / `SIGHUP` (secret overlap via `secret_ref` is already in).
- Shared (Redis) rate-limit and replay-guard stores (pod-local by design, D11).
- Split admin API keys, RBAC, LICENSE / SECURITY.md / CHANGELOG.

Shipped (dropped from this list):

- Header/body routing (`filter`, `switch`).
- Postgres-backed replay with `ttl` / `max_events` / `cleanup_interval`.
- Sync reverse proxy (`http_request` + `respond` passthrough).
- Query-parameter auth (`source.kind: query`).
- `secret_ref` file / AWS Secrets Manager with rotation overlap.
- Multipart bodies (`body_format: multipart` → `{fields, files}`).
- `publish.payload: object` on `s3` and `redis_kv` (decoded file bytes with vendor filename / Content-Type).
