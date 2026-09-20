# Wishlist

Remaining:

- convert XML -> JSON, JSON -> XML
- Standalone `lookup` provider binding. The node exists; CLI/serve reject it until an injected provider is configured.
- Per-publish destination options: Temporal `workflow_type` / `workflow_id`, Kafka `partition_key`, SQS/SNS FIFO `group_id`, Redis `max_len` / `consumer_group` / `ttl` / `key_template`.
- Config hot reload / `SIGHUP` (secret overlap via `secret_ref` is already in).
- Shared (Redis) rate-limit and replay-guard stores (pod-local by design, D11).
- Per-publish replay snapshots for branch-specific transforms and object payloads.
- Split admin API keys, LICENSE / SECURITY.md / CHANGELOG.
