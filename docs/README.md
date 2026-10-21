# Documentation

Start with the [project overview](../README.md), then choose the task you need.

## Get started

- [Run locally](development/local-development.md): disposable dev stack, persistent
  production-style setup, signed requests and a failure/retry walkthrough.
- [Examples](../examples/README.md): runnable YAML grouped by feature.
- [Docker images](dockerhub/README.md): core, replay and frontend images.
- [Contributing](../CONTRIBUTING.md): development setup and checks.

## Configure Hookway

- [Configuration](reference/configuration.md): YAML fields, defaults and destinations.
- [CLI](reference/cli.md): commands, flags, environment variables and exit codes.
- [Security](reference/security.md): signatures, credentials and network access.
  Report vulnerabilities through [SECURITY.md](../SECURITY.md).
- [Ingress adapters](guides/ingress-adapters.md): routing, credentials, HTTP relays,
  SQS/SNS formats, batches and file handling.
- [MCP gateway](guides/mcp-gateway.md): configurable endpoints, tools and
  [OAuth configuration](guides/mcp-gateway.md#oauth-configuration), including
  identity-provider setup and per-tool permissions.
- [OpenAPI importer](guides/openapi-import.md): generate MCP tools from explicitly selected API operations.
- [Provider recipes](guides/provider-compatibility.md): GitHub, Stripe, Paddle and Slack.
- [Named events](guides/named-events.md): shared payload contracts and event construction before delivery.
- [Validate JSON](guides/json-validation.md): schemas, conversions and sample checks.
- [Lookups](guides/lookups.md): HTTP, PostgreSQL and DynamoDB routing values.
- [Feature flags](guides/feature-flags.md): LaunchDarkly evaluation, fallbacks, typed routing and replay compatibility.
- [Rate limits and duplicate protection](guides/protection-stores.md): stores,
  quotas, expiry and policy changes.

## Operate Hookway

- [Delivery guarantees](operations/delivery-guarantees.md): what a success response
  means, fan-out and duplicate handling.
- [Replay service](operations/replay-service.md): setup, API, retention and maintenance.
- [Replay console](operations/replay-console.md): inspect payloads/files and recover
  failures; [view-only inspections](operations/replay-console.md#view-only-inspections)
  let you spot-check webhooks.
- [Uncertain delivery](operations/replay-investigation.md): check the destination
  before confirming or repeating an unknown outcome.
- [Monitoring and incidents](operations/observability.md): dashboards, alerts and troubleshooting.
- [Resource sizing](operations/resource-limits.md): body, memory, queue and time limits.
- [Upgrades and rollback](operations/upgrades.md): coordinated shutdown and migrations.
- [Backup and restore](operations/recovery.md): recover data without repeating delivered work.
- [Releases](operations/releases.md): manual publication and image verification.

## Develop and measure

- [Extending Hookway](development/extending-hookway.md): module layout, generated
  contracts, node/API changes and UI conventions.
- [Pipeline invariants](development/invariants.md): ordering and delivery rules.
- [Performance](evidence/performance.md): dated ingress, replay and protection measurements.

API specifications: [editor OpenAPI](api/editor.openapi.json) and
[replay OpenAPI](api/replay.openapi.json). Regenerate them using the
[contributor instructions](development/extending-hookway.md#contracts-and-generation).

- [Durable MCP event subscriptions](guides/mcp-events.md): authenticated subscriptions and signed callbacks.
