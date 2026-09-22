# Wishlist

Remaining:

- Standalone `lookup` provider binding. The node exists; CLI/serve reject it until an injected provider is configured.
- Shared (Redis) rate-limit and replay-guard stores (pod-local by design, D11).
- Per-publish replay snapshots for branch-specific transforms and object payloads.
- Split admin API keys
- invoking aws lambdas