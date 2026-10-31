---
title: Route verified webhooks to RabbitMQ, Kafka or SQS
description: Follow a working signed-webhook-to-RabbitMQ example, then choose Kafka or SQS with explicit acknowledgement, retry and consumer boundaries.
---

Last revised: October 31, 2026.
# Route verified webhooks to RabbitMQ, Kafka or SQS

Hookway can receive a signed webhook, verify its original body, wrap the verified
JSON in an envelope, and publish it into a queue or stream you operate. Your
workers consume that event and own business processing. This guide uses RabbitMQ
for a complete local path; the linked Kafka and SQS configurations show the
adapter-specific alternatives.

## Prerequisites and a working request

Complete [Run your first signed webhook](../get-started.md). Keep its Docker
Compose stack running, send the signed synthetic request, and inspect the
`github-webhooks` queue. Git, Docker Compose, Make, curl and OpenSSL are sufficient
for that walkthrough.

The complete runnable configuration is [examples/dev/core.yaml](../../examples/dev/core.yaml),
served by [examples/docker-compose.yaml](../../examples/docker-compose.yaml).
It declares a durable RabbitMQ exchange named `hookway.webhooks`. The publish
node names `github-webhooks` as its target and requests queue creation.

## Follow the verified event

The relevant graph is:

```yaml
nodes:
  - {id: in, type: http_trigger, path: /webhooks/github, methods: [POST]}
  - {id: body, type: read_body, max_bytes: 1048576}
  - {id: auth, type: auth, scheme: hmac_sha256_hex, secret: "${GITHUB_WEBHOOK_SECRET}", signature_header: X-Hub-Signature-256}
  - {id: env, type: envelope, body_format: json}
  - {id: out, type: publish, destination: primary, target: github-webhooks, create: true}
edges: [in>body, body>auth, auth>env, env>out]
```

This is an excerpt, not a complete configuration. The linked example supplies
server settings, destination connections, and a second HTTP delivery branch.
Signature verification uses the raw bytes. Parsing or changing the body before
verification can invalidate a provider’s signature.

Open RabbitMQ’s local management UI at http://localhost:15672. Inspect a message
in `github-webhooks`: the envelope’s body contains the issue from the signed
request. A successful publish establishes broker acceptance under the adapter’s
contract; it does not prove a worker processed the message.

## Add routing without duplicating verification

After the verified envelope, add filters, `if_else` or `switch` nodes to select
publication branches. The [filter example](../../examples/filters.yaml) shows
conditions, and [environment admission](../../examples/routing/admission.yaml)
checks a requested environment before using it in a queue target. Keep untrusted
request values inside the routing profile’s allowed set.

Several publish nodes can send one verified event to different systems. Choose
the flow’s failure policy explicitly. In the local example, `skip_failed`
attempts the RabbitMQ and HTTP branches and can acknowledge the sender when one
succeeds. See [delivery guarantees](../operations/delivery-guarantees.md) before
choosing a contract for production.

## Choose a destination

| Destination | Starting configuration | What to configure and verify |
| --- | --- | --- |
| RabbitMQ | [RabbitMQ example](../../examples/destinations/rabbitmq.yaml) | Broker URL, exchange, queue and binding. Your consumer owns acknowledgement and business retries. |
| Kafka | [Kafka example](../../examples/destinations/kafka.yaml) | Bootstrap brokers, TLS/SASL, topic provisioning and producer acknowledgements. Your consumers own groups, offsets and processing. |
| SQS | [SQS example](../../examples/destinations/sqs.yaml) | AWS region and credentials or role, queue permissions and resource attributes. Consumers own visibility, deletion and processing. |

For Kafka and SQS, start with the complete linked YAML and provision the real
broker or queue service. These are alternatives to the local RabbitMQ path, not
services secretly included in that Compose stack. Validate configuration before
starting a deployment:

```sh
make build
bin/hookway validate --config examples/destinations/kafka.yaml --no-env
bin/hookway validate --config examples/destinations/sqs.yaml --no-env
```

These destination examples produce lint warnings about missing rate-limit nodes.
Add an appropriate inbound rate limit before using them in production.
Offline validation checks configuration, not broker connectivity, IAM permission
or successful live delivery. FIFO SQS requires additional explicit identity
settings; use the [FIFO example](../../examples/destinations/fifo-sqs.yaml).

## Failures and recovery

- **Invalid signature:** compare the provider’s signing format, header and secret
  with the [security reference](../reference/security.md). Never transform signed
  bytes first.
- **Destination unavailable:** inspect readiness, credentials, TLS and delivery
  errors. A successful sender response must be interpreted using the selected
  flow policy.
- **Repeated sender delivery:** use stable provider event IDs for duplicate
  protection. Envelope IDs generated for new requests are not a substitute for
  those IDs.
- **Worker outage:** broker persistence, replication, retention and consumer
  acknowledgement settings determine what survives. Hookway is not your worker.

The optional [replay service](../operations/replay-service.md) can retain eligible
missed deliveries after an early success response. Capture is best effort and
recovery is operator initiated. Async HTTP delivery has a process-local queue;
this is not an exactly-once or durable-ingress promise.

Stop the local walkthrough with `make dev-down`. For deployment, continue with
[shared protection stores](protection-stores.md) and
[observability](../operations/observability.md).
