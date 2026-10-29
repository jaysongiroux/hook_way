# Send Sentry issue alerts to Discord

Use a Sentry **internal integration** to send signed webhooks to Hookway. Sentry
alert rules select when to notify; Hookway verifies the request, filters issue
alerts, formats a summary and queues delivery to a Discord webhook.

Start with [the complete configuration](../../examples/sentry-discord.yaml).
For another destination, use the builder's **Sentry webhook** template or
[the ingress template](../../examples/templates/sentry-ingest.yaml), then connect
its envelope output to your own nodes. An inserted template requires an explicit
protection-store choice. Complete configurations are also in the builder's
Examples tab.

## Configure Sentry and Discord

1. Create a webhook in your Discord channel's integration settings and copy its
   complete URL. Treat the URL as a credential.
2. Expose Hookway over HTTPS, with `/webhooks/sentry` routed to its webhook port.
   Keep the separate metrics port private. A development tunnel can expose your
   local webhook port for testing.
3. In Sentry, open **Settings → Developer Settings → Custom Integrations** and
   create an internal integration named Hookway. Set its Webhook URL to
   `https://YOUR-HOOKWAY-HOST/webhooks/sentry`, enable **Alert Rule Action**, and
   grant **Issue & Event: Read**. Navigation labels can vary by Sentry version.
4. Save and copy the integration's **Client Secret** into `SENTRY_CLIENT_SECRET`.
   Use the Client Secret, not the generated API token or a project DSN. Hookway
   does not need a Sentry API token for this flow.
5. Set `DISCORD_WEBHOOK_URL` to the complete Discord webhook URL and set
   `HOOKWAY_API_KEY` for metrics. Start Hookway using the example:

   ```sh
   # Set the three environment variables securely before running these commands.
   go run ./cmd/hookway validate --config examples/sentry-discord.yaml
   go run ./cmd/hookway --config examples/sentry-discord.yaml
   ```

6. Create an issue alert rule in Sentry, for example **A new issue is created**,
   optionally filtered to production. Add **Send a notification via an integration**
   and select Hookway. Choose the rule's notification frequency in Sentry. Issue
   lifecycle or raw error subscriptions are unnecessary for this recipe.

See Sentry's [internal integration documentation](https://docs.sentry.io/integrations/integration-platform/internal-integration/)
and [issue alert payload](https://docs.sentry.io/integrations/integration-platform/webhooks/issue-alerts/).
This setup uses an internal integration within one organization; there is no
public-app installation or OAuth exchange.

## Try a signed fixture locally

With Hookway running and `SENTRY_CLIENT_SECRET` available in this terminal, run
from the repository root. This posts the synthetic fixture to Hookway and, if
accepted, sends a real message to the configured Discord channel:

```sh
python3 - <<'PY'
import hashlib
import hmac
import os
from pathlib import Path
import urllib.request

body = Path('examples/fixtures/sentry-issue-alert.json').read_bytes()
signature = hmac.new(os.environ['SENTRY_CLIENT_SECRET'].encode(), body, hashlib.sha256).hexdigest()
request = urllib.request.Request(
    'http://localhost:8080/webhooks/sentry', data=body, method='POST',
    headers={'Content-Type': 'application/json',
             'Sentry-Hook-Resource': 'event_alert',
             'Sentry-Hook-Signature': signature},
)
with urllib.request.urlopen(request, timeout=5) as response:
    print(response.status, response.read().decode())
PY
```

The message resembles:

```text
Sentry: ReferenceError: checkout is not defined
Rule: New production issue
Project: 42 · Environment: production · Level: error
https://sentry.io/organizations/example/issues/123/events/0123456789abcdef0123456789abcdef/
```

Sentry's documented event payload includes a project ID, so the recipe shows
that ID without making an enrichment API call. Environment comes from
`event.environment` or its `environment` tag. Missing optional fields use readable
fallbacks, and a missing human-facing `web_url` omits the link. Field limits keep
the message below Discord's 2,000-character content limit, markdown in values is
escaped, and `allowed_mentions.parse` is empty. The flow sends selected summary
fields only, excluding user records, request contents and stack traces.

## Authentication, acknowledgements and failures

- `hmac_sha256_raw_hex` verifies the exact raw body against the unprefixed digest
  in `Sentry-Hook-Signature`, using constant-time comparison. Existing
  `hmac_sha256_hex` requires GitHub's `sha256=` prefix and is a different scheme.
- Sentry's signature does not cover `Sentry-Hook-Timestamp`, `Request-ID`, or the
  resource header. The resource header is a routing hint, not authenticated
  identity. This example also checks body `action: triggered`; its default
  replay guard hashes the signed raw body, rather than unsigned headers.
- The guard rejects identical bodies for 24 hours with **409**, without queueing
  another message. Changing the timestamp or request ID does not bypass it.
  Retrying the fixture unchanged therefore returns 409. Memory history resets
  on restart and is not shared between processes; select Redis/PostgreSQL for
  shared protection. This is bounded duplicate protection, not exactly-once
  Discord delivery.
- Authenticated requests outside `event_alert` / `triggered` return **200** and
  do not notify Discord. Missing or invalid signatures return **401**.
- Matching alerts return **202** after entering the existing bounded async
  webhook queue, before Discord responds. The flow uses a 500ms publish budget;
  measure public response latency including upload, TLS and proxies against
  [Sentry's one-second deadline](https://docs.sentry.io/integrations/integration-platform/webhooks/).
- Async delivery retries connection failures, 429 and 5xx up to five attempts,
  honoring `Retry-After` within the configured maximum backoff. Other 4xx
  responses are terminal. Watch delivery logs and metrics for failures after
  Sentry has already received its acknowledgement. The async queue is held in
  memory: a crash can lose acknowledged work. Replay cannot restore a vanished
  queue; see [delivery guarantees](../operations/delivery-guarantees.md).
- The publish node uses `wire_body: json` and `target: ""` so the formatted
  Discord JSON is sent to the exact configured URL, including any query string.
  No source credentials are forwarded. Destination authentication is `none`
  because Discord's credential is already in its URL.

The secret reference uses the existing refresh/rotation-overlap support.
Environment changes require updating the running process's environment (normally
by restarting it). Use a mounted-file secret reference for rotation without a
process restart. Neither the secret nor Discord URL belongs in committed YAML.

## Verify a live delivery

After deploying this version and configuring the integration, send a Sentry test
notification if available, or generate a fresh test issue that matches your rule.
Confirm the Sentry webhook log shows a timely 202 and exactly one message reaches
the intended Discord channel. Check its title, rule, project, environment and
clickable event link. A synthetic fixture's example link is not a live issue.

If no message arrives, check Sentry's delivery log first, then Hookway's
signature failures, filter outcomes, queue/delivery metrics and Discord response
status. A 202 proves queue admission, not Discord receipt. Local stub tests do
not verify your public routing, account permissions or channel configuration.
