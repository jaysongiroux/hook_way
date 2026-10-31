---
title: Run your first signed webhook
description: Run Hookway locally, verify a signed webhook, and inspect its delivery to RabbitMQ with the disposable Docker Compose walkthrough.
---

Last revised: October 31, 2026.
# Run your first signed webhook

Run a local Hookway stack, send a signed request, and inspect the resulting event
in RabbitMQ. The walkthrough uses synthetic data and disposable development
credentials. It does not require a hosted account.

## Prerequisites

Install Git, Docker with Docker Compose, Make, curl and OpenSSL. Start Docker and
make sure ports 8080, 8081, 8082, 5672 and 15672 are available. Image builds need
network access; the first start can take several minutes.

## Start Hookway

```sh
git clone --branch main https://github.com/jaysongiroux/hook_way.git
cd hook_way
make dev-up
```

Leave that terminal running. This builds core, the optional replay service, and
the frontend, and starts RabbitMQ and the walkthrough’s other dependencies.
Open a second terminal in the same checkout. Check core readiness:

```sh
curl --fail http://localhost:8080/readyz
```

A successful readiness response is HTTP 200. A new stack can return 503 while
RabbitMQ finishes starting; wait a few seconds and retry until it is ready. Open the
[local builder](http://localhost:8082/builder) to inspect and export configurations
without signing in. The optional replay console at http://localhost:8082 uses the
local operator token documented in [Local development](development/local-development.md).

## Send a signed webhook

The local example uses `change-me` as its disposable webhook secret. Use a separate
random secret for any real deployment. Run:

```sh
body='{"action":"opened","issue":{"number":1}}'
signature=$(printf '%s' "$body" | openssl dgst -sha256 -hmac 'change-me' -r | cut -d' ' -f1)
curl -i http://localhost:8080/webhooks/github \
  -H 'Content-Type: application/json' \
  -H "X-Hub-Signature-256: sha256=$signature" \
  --data-binary "$body"
```

Expect **202 Accepted**. The [configuration](../examples/dev/core.yaml) verifies
the original body before creating an envelope and attempting delivery to
RabbitMQ and a local HTTP destination. The flow uses `skip_failed`: a 202 means
at least one configured delivery succeeded. It does not mean business processing
finished or that every destination received the event.

## Inspect the queued event

Open http://localhost:15672 and sign in with the local RabbitMQ credentials
`guest` / `guest`. Open **Queues and Streams → github-webhooks**, then use
**Get messages** to inspect the envelope. Its body contains the synthetic issue
number. Retrieving a message can acknowledge or requeue it depending on the
selected mode; choose requeue if you want to keep it for another inspection.

## When something goes wrong

- **Connection refused or readiness fails:** wait for the initial build and
  RabbitMQ health check. Inspect the terminal logs. Resolve port conflicts before
  starting another stack.
- **401:** sign the exact bytes sent by curl. Adding whitespace or a trailing
  newline after signing changes the signature. If you exported a different
  `GITHUB_WEBHOOK_SECRET`, use that same secret when signing.
- **No queue:** check core logs and RabbitMQ readiness. The queue appears when the
  configured publish resource is provisioned. A 202 under `skip_failed` can come
  from the other destination; inspect both delivery outcomes.

## Stop and take the next step

In a second terminal:

```sh
make dev-down
```

Use [Webhooks to queues](guides/webhooks-to-queues.md) to understand routing,
acknowledgements and consumer responsibilities. For persistent deployment with
fresh credentials, follow the production-style recipe in
[Local development](development/local-development.md#persistent-local-production-setup).
For APIs and AI clients, [import selected OpenAPI operations as MCP tools](guides/openapi-import.md).
