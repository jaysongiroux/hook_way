# Wishlist

Remaining:

- Standalone `lookup` provider binding. The node exists; CLI/serve reject it until an injected provider is configured.
- Shared (Redis) rate-limit and replay-guard stores (pod-local by design).
- Split admin API keys
- allow for multi-file yml support -> split server out to server.yml
- Terminal devs can run to receive webhooks without having to configure a local env to connect to a queue (or any other destination). Terminal command can be configured how it forwards the request (like a POST request)
- JSON serialization node