# Provider compatibility

Start with [`examples/providers/webhooks.yaml`](../../examples/providers/webhooks.yaml)
for GitHub JSON webhooks, Stripe snapshot events, Paddle Billing notifications,
and Slack Events API callbacks. Local tests check these recipes against the real
flow server with a test publisher. They do not replace testing your public URL,
provider account, broker, or application.

## Provider recipes

| Provider | Authentication | Response deadline |
| --- | --- | --- |
| GitHub JSON | `hmac_sha256_hex`, `X-Hub-Signature-256` | 2xx within 10 seconds |
| Stripe snapshot events | `hmac_sha256_ts_dot`, `Stripe-Signature`, 5-minute tolerance | Return 2xx promptly after publication |
| Paddle Billing | `hmac_sha256_ts_colon`, `Paddle-Signature`, 5-second tolerance | 200 within 5 seconds |
| Slack Events API | `hmac_sha256_slack_v0`, `X-Slack-Signature`, 5-minute tolerance | 2xx within 3 seconds |

### GitHub

The signature covers the exact body, with no signed timestamp. Preserve
`X-GitHub-Delivery` as the sender identity; manual redelivery keeps it. GitHub does
not automatically redeliver failures, so monitor missed deliveries and arrange
redelivery or reconciliation. See [best practices](https://docs.github.com/en/webhooks/using-webhooks/best-practices-for-using-webhooks)
and [failure handling](https://docs.github.com/en/webhooks/using-webhooks/handling-failed-webhook-deliveries).

### Stripe

The signature covers `timestamp + "." + raw_body`. Hookway accepts any matching
candidate among at most 16 `v1` values, allowing overlap during key rotation.
Deduplicate using the body event `id`; delivery order is not guaranteed.
Live automatic retries continue for up to three days. Manual resend is available
for up to 15 days in the Dashboard or 30 days through the CLI. See
[Stripe's webhook contract](https://docs.stripe.com/webhooks).

The recipe does not retrieve objects referenced by thin events or implement
synchronous authorization decisions. Those need application logic or a dedicated
flow with the appropriate deadline.

### Paddle

The signature covers `timestamp + ":" + raw_body`, with bounded repeated `h1`
values. The recipe explicitly returns 200 after publication. Use `event_id` for
business deduplication; `notification_id` identifies notification delivery.
Live retry behavior allows up to 60 attempts over three days; sandbox behavior
differs. See [signature verification](https://developer.paddle.com/webhooks/about/signature-verification/)
and [delivery handling](https://developer.paddle.com/webhooks/about/respond-to-webhooks/).

This recipe covers Paddle Billing, not Paddle Classic.

### Slack

The signature covers `v0:<timestamp>:<raw_body>` with a separate seconds timestamp
header. A signed `url_verification` request returns the challenge without
publishing. Preserve body `event_id` and retry headers. Slack retries failed
delivery up to three times; monitor disabled subscriptions and reconcile business
events separately. See [request verification](https://docs.slack.dev/authentication/verifying-requests-from-slack/)
and the [Events API](https://docs.slack.dev/apis/events-api/).

The recipe does not cover slash commands, interactions, or Socket Mode.

## Delivery, duplicates, and secrets

These flows acknowledge only after RabbitMQ publisher confirmation. Broker
persistence, replication, retention, and consumer processing still need your
configuration. A successful publish followed by a lost response can produce
a duplicate on sender redelivery. Consumers should record the sender event ID
atomically with their business effect.

A `replay_guard` can reduce duplicates during a bounded window, using memory for
one process or Redis/PostgreSQL across replicas. Expiry or store data loss can
admit repeats. Its default duplicate response is 409; test the response policy
against the provider's retry behavior. See [Protection stores](protection-stores.md).

Set `RABBITMQ_URL` and each provider's `*_SECRET_FILE` path. Each file holds only
that endpoint's signing secret. The example refreshes every minute and retains
the prior value for ten minutes. Local tests verify mounted-file refresh and
overlap for all four recipes. Coordinate rotation on every replica and keep
clocks synchronized, especially for Paddle's five-second window.

Verify signatures before rewriting bodies. Do not replay an old timestamped
request by widening the public auth window; request a fresh provider delivery or
use the authenticated replay interface for a captured failure. Capture remains
best effort. GitHub form-encoded delivery also needs a separate parsing recipe.

## Verify your integration

```sh
go test -short ./internal/conformance -run 'TestProvider|TestSlack' -count=1
go test -race -short ./internal/auth ./internal/conformance
```

Fixed synthetic signatures in `internal/conformance/testdata/provider-signatures.json`
were calculated independently with Python. They include whitespace, a newline,
and non-ASCII text. Tests also send fresh signatures through the recipe flows,
checking wrong keys, changed bytes, challenges, rotation, timeouts, and recovery.
The recipe uses a two-second publish budget; that is not a production latency SLO.

Before using a real provider, send its test delivery to the public URL. Check
invalid signatures, stale timestamps, duplicate events, secret rotation, and
broker outages. Measure the full response time, including upload, TLS, proxy,
and publication. Keep the image/configuration identity with those results.

For proxy and file workflows, see [Ingress adapters](ingress-adapters.md).
For test commands, see [Contributing](../../CONTRIBUTING.md#make-and-check-a-change).
