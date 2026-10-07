# Documentation

Hookway's [README](../README.md) is the feature overview. Use this index to find
setup instructions, configuration details, or help with a specific task.

## Start here

| I want to… | Read |
| --- | --- |
| Run Hookway locally and retry a missed delivery | [Local development](development/local-development.md) |
| Find a runnable flow | [Example catalog](../examples/README.md) |
| Write or check YAML | [Configuration](reference/configuration.md) and [CLI](reference/cli.md) |
| Choose what a success response means | [Delivery guarantees](operations/delivery-guarantees.md) |
| Deploy containers | [Docker images](dockerhub/README.md), [security](reference/security.md), and [upgrades](operations/upgrades.md) |
| Spot-check webhooks | [Inspect and Sample](reference/configuration.md#inspecting-webhooks) and [view-only console records](operations/replay-console.md#view-only-inspections) |
| Investigate a failure | [Replay console](operations/replay-console.md) and [uncertain deliveries](operations/replay-investigation.md) |
| Change the code | [Contributing](../CONTRIBUTING.md) and [extending Hookway](development/extending-hookway.md) |

## Configuration and features

- [Configuration reference](reference/configuration.md): YAML fields, defaults,
  destinations, event envelopes, logging, and probes.
- [CLI reference](reference/cli.md): commands, flags, environment variables, and exit codes.
- [Security](reference/security.md): sender verification, credentials, proxy controls,
  and trust boundaries. Report vulnerabilities through [SECURITY.md](../SECURITY.md).
- [Ingress adapters](guides/ingress-adapters.md): SQS/SNS wire formats, environment
  routing, registration, credentials, HTTP relays, and batches.
- [Provider compatibility](guides/provider-compatibility.md): GitHub, Stripe, Paddle,
  and Slack recipes, with their tested boundaries.
- [JSON validation](guides/json-validation.md): schemas, sample checks, conversions,
  and numeric precision.
- [Lookups](guides/lookups.md): HTTP, PostgreSQL, and DynamoDB providers and caching.
- [Protection stores](guides/protection-stores.md): memory or shared quotas,
  duplicate reservations, outages, and maintenance.

## Operations

- [Delivery guarantees](operations/delivery-guarantees.md): acknowledgements,
  partial fan-out, duplicates, and who owns recovery.
- [Replay service](operations/replay-service.md): service configuration, capture,
  retry API, retention, and metrics.
- [Replay console](operations/replay-console.md): frontend setup, view-only inspections, file downloads, and recovery actions.
- [Replay investigation](operations/replay-investigation.md): check downstream evidence
  before retrying an uncertain delivery.
- [Upgrades](operations/upgrades.md): graceful shutdown, migrations, backup, and rollback.
- [Releases](operations/releases.md): published images, signing, and verification.

## Development

- [Local development](development/local-development.md): ports, defaults, and walkthroughs.
- [Pipeline invariants](development/invariants.md): ordering and execution rules tied to tests.
- [Extending Hookway](development/extending-hookway.md): code ownership, generated
  contracts, shared UI, and how to add nodes or APIs.
- [Design system](development/design-system.md): shared visual and accessibility rules.

Generated HTTP contracts: [editor OpenAPI](api/editor.openapi.json) and
[replay OpenAPI](api/replay.openapi.json). See the
[generation instructions](development/extending-hookway.md#contracts-and-generation)
before changing them.

## Measurements and distribution

- [Performance](evidence/performance.md) and [protection benchmarks](evidence/protection-benchmarks.md):
  dated measurements with workload and environment details.
- [Docker Hub description](dockerhub/README.md): the image summary published by the release workflow.

## Maintaining documentation

Write for someone trying to use or change Hookway. Start with the task, explain
terms when needed, and give runnable examples. Keep field tables in the reference
and link to them from guides. Add a page only when it serves a distinct task.

When code changes, update the relevant guide and its incoming links, including
marketing links. Keep this index current. Preserve the date, workload, and build
context of benchmark results; they do not prove a later build's performance.
