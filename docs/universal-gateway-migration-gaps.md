# Migrating `universal-gateway` → Hookway: Gap Analysis

> **Historical snapshot.** Written 2026-08-27 against Hookway `612e372` (endpoint-based config). Universal-gateway and DigiFi migration work was **removed from implementation scope** on 2026-09-14. Several Category A items were still built as general Hookway features. This file is a decision record, not current capability docs.

**Date:** 2026-08-27 (endpoint-based config). Status table added 2026-09-16.

**Sources:** `../universal-gateway` @ `3f8161f` (main), `hook_way` @ `612e372` (main)
**Question:** what breaks if we abandon the current SAM architecture in favour of Hookway?

### Status vs current Hookway

| Gap | Status |
|---|---|
| **A1** sync HTTP proxy | **Closed.** `http_request` + `respond` `passthrough`; `examples/showcase/proxy.yaml`. |
| **A2** dynamic dest / lookup | **Partial.** Path `{vars}` and `transform.field_map` work (`examples/variables.yaml`). The `lookup` node exists but CLI/serve reject it (`StandaloneIssues`: no standalone provider binding). |
| **A3** payload transform / XML prune | **Closed.** `transform` `changed_fields` / `field_map` / `xml_prune`; `envelope.body_format: xml`. |
| **A4** SNS/SQS `MessageAttributes` | **Closed.** `publish.message_attributes`. |
| **A5** query-parameter auth | **Closed.** `auth.source.kind: query`. |
| **A6** non-POST, greedy path, query forward | **Closed.** `methods: [ANY]`, `{rest...}`, `forward_method` / `forward_query`. |
| **A7** DigiFi re-serialized HMAC | **Still open.** Auth verifies **raw bytes**; no vendor-SDK exception (I6). |
| **B2** readiness AND-aggregation | **Closed** (D9 / per-flow `/ready`). |
| **B3** Secrets Manager + rotation overlap | **Partial.** `secret_ref.provider: aws_secrets_manager` with overlap. No live YAML reload. |

---

---

## 1. What `universal-gateway` actually is

An AWS SAM application: one API Gateway (custom domain, ACM cert, public + private
Route53 records) fronting **seven Node 20 Lambdas**, some VPC-attached to reach DigiFi.
`template.yaml` is 43 KB / ~1240 lines. Roughly 127 Jest test cases across the handlers.

Critically, it is **two different products sharing one gateway**:

| Handler | Path | Shape | Hookway equivalent? |
|---|---|---|---|
| `digifi_router_webhook_handler` | `/webhooks/universal/digifi` | Webhook → SNS/SQS | Partial |
| `qualia_router_webhook_handler` | `/webhooks/universal/qualia` | Webhook → SQS | Partial |
| `clearedge_router_webhook_handler` | `/webhooks/universal/clearedge` | Webhook → SQS | Partial |
| `salesforce_router_proxy_handler` | `/salesforce/proxy/{proxy+}` | **Sync HTTP proxy** | **No** |
| `salesforce_digifi_proxy_handler` | `/salesforce-digifi/proxy/{proxy+}` | **Sync HTTP proxy** | **No** |
| `digifi_router_proxy_handler` | `/proxy/{proxy+}` | **Sync HTTP proxy** | **No** |
| `docmagic_xml_proxy_handler` | `/digifi/docmagic` | **Sync proxy + XML transform** | **No** |

**Three of seven** are webhook ingestion, which is what Hookway does. **Four of seven**
are synchronous request/response proxies, which Hookway is not architected to do at all.

That split is the headline finding. "Replace universal-gateway with Hookway" is not one
migration — it is one migration plus one new product.

**Update (2026-09-16):** A1, A3, A5, and A6 shipped as general Hookway features (see the
status table). The 2026-08-27 rows and Category A write-ups below are the original
finding, not current capability docs.

---

## 2. Category A — Hard blockers (Hookway cannot do these today)

### A1. Synchronous request/response proxying

All four proxy handlers return the **upstream's** status code, headers, and body to the
caller. From `salesforce_router_proxy_handler/app.js`:

```js
return {
  statusCode: response.status,
  headers: response.headers,
  body: typeof response.data === 'string' ? response.data : JSON.stringify(response.data),
};
```

Hookway is fire-and-forget by design. Even its `proxy`-mode webhook destination — which
does forward the raw inbound body and headers — discards the upstream response and
returns a fixed envelope to the sender (`internal/httpapi/server.go:601-622`):

```go
status = http.StatusAccepted
body := map[string]any{"status": "accepted", "endpoint": ..., "destinations": published, ...}
```

**Update (2026-09-16):** `http_request` + `respond` `passthrough` is the sync proxy
(`examples/showcase/proxy.yaml`). The previous 202 envelope path cited above is gone.

Salesforce callers are doing synchronous API calls and consuming the DigiFi/DocMagic
response. There is no configuration that makes Hookway do this. It needs a new
"gateway mode" — response passthrough, streaming, response header filtering, and upstream
error mapping — which is a substantial architectural addition, not a feature flag.

### A2. Dynamic destination resolution from an external datastore

This is the single most important capability in the current system and Hookway has no
analogue. Every webhook router does the same three steps:

```js
const { id, idType } = extractIdAndIdType(body);              // pull id out of the payload
const foundEnvName = await queryDynamoDbForEnv(dynamoDb, id, idType);  // DynamoDB lookup
const queue = resolveQueueFromWebhookType(webhookType, foundEnvName);  // "${env}-applicationServiceQueue.fifo"
```

In sandbox, one DigiFi instance serves many ephemeral environments, so the destination
queue **cannot be known until runtime** — it depends on which environment owns that
application id. ClearEdge does the same thing keyed on `client_file_number`, with regex
discrimination to decide the id type first:

```js
const DIGIFI_ID_STRING_REGEX = /^[0-9a-fA-F]{24}$/;
const FRIENDLY_ID_STRING_REGEX = /^[A-Z]{2}\d{6}$/;
```

Hookway resolves `destinations[].target` to a **static string at config load**. There is
no lookup provider, no target templating from body content, and no runtime resolution
hook anywhere in the publish path. Without this, the entire ephemeral-environment routing
model — the reason this gateway exists — cannot be expressed.

### A3. Payload transformation

Two distinct transformations, both absent from Hookway:

**Changed-field diffing** (`digifi_router_webhook_handler/app.js`): `getChangedFieldsArray`
recursively diffs `data.previousObject` against `data.object` and emits dot-notation paths
(`['status', 'variables.borrower_first_name']`), with a special rule promoting any nested
`status.*` change to also include `status`. That array is published as an SNS message
attribute.

**XML rewriting** (`docmagic_xml_proxy_handler/pipeline.js`): a five-rule DOM pipeline over
`@xmldom/xmldom` — remove empty self-closing elements, remove empty parents, remove
hardcoded-only parties, remove blank general partners, prune orphaned relationships —
applied in a specific order, with before/after element counts logged.

Hookway publishes the envelope verbatim. It has *filters* (include/exclude a delivery) but
no transformation of any kind, and its body handling is JSON-only — the XML path has no
representation at all.

### A4. SNS/SQS `MessageAttributes` are not supported

`publishToSNS` attaches:

```js
params.MessageAttributes = {
  fields_changed: { DataType: 'String.Array', StringValue: JSON.stringify(changedFields) },
};
```

Hookway's `SNSPublisher.Publish` (`internal/queue/sns.go:180-183`) constructs:

```go
in := &sns.PublishInput{ TopicArn: aws.String(state.arn), Message: aws.String(string(body)) }
```

`MessageAttributes` appears nowhere in `sns.go` or `sqs.go` (verified by grep). **Any
downstream SNS subscription filter policy keyed on `fields_changed` silently stops
matching** — subscribers just stop receiving messages, with no error anywhere. Inventory
the subscription filter policies before touching this.

### A5. Query-parameter authentication

Qualia authenticates via the query string (`qualia-utils.js`):

```js
if (queryParams.authID !== endpointSecret) return 'Invalid signature';
```

Every Hookway verifier is fed from a header — `server.go:433`:

```go
s.verifiers[endpointName].Verify(r.Header.Get(endpoint.Auth.SignatureHeader), rawBody, time.Now())
```

There is no query-parameter auth source in `internal/auth` at all. The Qualia endpoint
cannot be migrated without adding one.

**Update (2026-09-16):** `auth.source.kind: query` reads a query parameter. The
header-only path cited above is gone.

### A6. Non-POST methods, greedy paths, query forwarding

The proxy routes use `x-amazon-apigateway-any-method` with `{proxy+}`, then strip the
route prefix and forward the remaining path, the method, and `queryStringParameters` to
the target. Hookway matches exact configured paths and rejects anything but POST
(`server.go:358-363`), and its webhook destination URL is `base + static target name`
(`joinWebhookURL`) with no inbound-path passthrough.

**Update (2026-09-16):** `methods: [ANY]`, `{rest...}`, and `forward_method` /
`forward_query` closed this (`examples/showcase/proxy.yaml`). The previous POST-only matcher cited
above is gone.

### A7. DigiFi's signature scheme is a vendor SDK, over a *re-serialized* body

```js
Digifi.verifyWebhookSignature(JSON.stringify(body), endpointSecret, timestamp, signature)
```

Two problems. First, the algorithm lives in a closed vendor package and would have to be
reverse-engineered and re-implemented as a scheme in `internal/auth`. Second — and more
subtly — it signs `JSON.stringify(body)`, the **re-serialized** payload, while Hookway
deliberately verifies against raw bytes and documents that it "does not parse, trim,
normalize, or re-serialize" the body (`docs/security.md:33`). If DigiFi's canonical form
differs from the bytes on the wire in any respect (key order, unicode escaping,
whitespace), Hookway will reject signatures the current gateway accepts. This needs a
byte-level compatibility test against real captured DigiFi traffic before anyone commits
to the migration.

---

## 3. Category B — Regressions (Hookway can do it, but worse)

### B1. Loss of per-endpoint IAM isolation ⚠️ most significant security regression

`template.yaml` defines **seven-plus distinct IAM roles** — `RouterLambdaRole`,
`SalesforceRouterLambdaRole`, `SalesforceDigifiProxyLambdaRole`, `ClearedgeLambdaRole`,
`QualiaLambdaRole`, `DocMagicXmlProxyLambdaRole`, `ProxyLambdaRole` — each with narrowly
scoped policies, and `SharedDigifi`/`DedicatedDigifi` conditionals that deliberately
*tighten* permissions in staging and production versus sandbox. The README calls this out
as an intentional design decision.

Hookway is one process with one IAM role, which must be the **union** of all of those.
A compromise or bug in any single endpoint's handling reaches every destination the
gateway can write to. For a shared gateway carrying multiple external partners' traffic,
this is a meaningful downgrade in blast radius.

*Mitigation:* deploy Hookway as several instances split by trust domain rather than one,
accepting the extra operational surface.

### B2. Loss of per-endpoint failure and scaling isolation

Lambda gives each handler an independent concurrency pool, memory, and timeout — and the
template uses that range aggressively: DocMagic and the DigiFi proxy at
`Timeout: 900 / MemorySize: 10240`, the webhook routers at 29s, Salesforce at
`Timeout: 30 / MemorySize: 256`. One handler saturating, leaking, or crashing cannot
affect another.

In Hookway everything shares one process, one goroutine budget, and one
`max_inflight_requests`. Worse, per the readiness AND-aggregation documented at
`docs/security.md:90`, one saturated destination pulls the **entire pod** out of
load-balancer rotation — including endpoints that never route to it. A slow DocMagic
would take DigiFi webhooks down with it.

### B3. Loss of working AWS Secrets Manager integration and rotation

The gateway reads secrets from Secrets Manager at runtime. `getLatestSecret` even handles
rotation explicitly — it lists secrets matching `${ENVIRONMENT_NAME}-lambdas`, paginates,
sorts by `LastChangedDate`, and takes the newest. Other handlers read a pinned ARN with a
cold-start cache.

Hookway only expands `${ENV}` at process start. No Secrets Manager, no `secret_file:`, no
rotation without a full restart, and only one secret per endpoint so there is no
old/new overlap window. This is already **P0-5** in the production-readiness review; here
it is a straight capability regression against something that works today.

### B4. Cost and idle profile

Sandbox traffic is bursty and low-volume; Lambda costs approximately nothing at idle.
Hookway is an always-on container — 2+ replicas per its own HA guidance
(`docs/security.md:135`) — plus a Postgres instance if you want the replay store. Not a
blocker, but a real delta to put in the decision.

### B5. Loss of the managed edge

API Gateway currently supplies TLS termination, the ACM certificate, the custom domain,
a WAF attachment point, throttling, and access logs. Hookway has **no in-process TLS at
all** (P1-3 in the readiness review) and would need an ALB or ingress controller to
replace every one of those.

---

## 4. Category C — Where Hookway is genuinely better (the case *for* moving)

### C1. The current system silently loses events ⚠️

This is the strongest argument for migrating, and it is a real correctness bug, not a
preference. Every error path returns HTTP **200**:

```js
// digifi_router_webhook_handler/app.js — catch block
return { statusCode: 200, body: JSON.stringify({ message: 'Message Processed' }) };
```

```js
// aws-utils.js — publishToSQS catch
return { success: false,
         response: { statusCode: 200, body: JSON.stringify({ message: 'Error sending message to SQS' }) } };
```

The comment says this is deliberate ("return to digifi a 200 response to avoid retries and
duplicate processing"), but the consequence is that **a failed SQS publish is lost
permanently** — DigiFi will not retry, there is no DLQ (the code carries a `TODO` about
adding one), no replay store, and no alert. `publishToSNS` at least retries twice with
backoff; `publishToSQS` does not retry at all.

Hookway returns 503 so the provider retries, records
`hookway_publish_total{status="failure"}`, and with the replay store enabled captures the
event for re-delivery. That is a direct, material reliability upgrade.

### C2. Observability

`console.log` and CloudWatch, versus 41 Prometheus metrics, a Grafana dashboard with a
test guarding it, structured logs with correlated request/trace/event ids, and W3C
traceparent propagation end to end.

### C3. Auth consistency

Four handlers implement four hand-rolled auth schemes, each with its own secret-fetch
code. One of them compares tokens with `===` (`clearedge-utils.js`
`validateAuth`) — a non-constant-time comparison and a small timing-attack surface.
Hookway centralizes this in a tested verifier registry that uses `hmac.Equal` /
`subtle.ConstantTimeCompare` throughout and fails closed on an unknown scheme.

### C4. Config over code

Adding a provider today means: a new Lambda directory, its own `package.json` and
lockfile, a copy of the shared layer, an IAM role, IAM policies, an API Gateway path in
the OpenAPI body, and CI wiring. In Hookway it is a YAML block. Note also that
`layer_external_services/` is **copied into each function directory** rather than shared —
seven near-duplicate copies of `aws-utils.js`, `errors.js`, and `responses.js` are checked
in, which is a maintenance hazard the migration would eliminate.

### C5. No 29-second ceiling, no cold starts

API Gateway caps integrations at 29s, and the VPC-attached Lambdas pay the worst cold-start
penalty. A long-running container has neither problem.

---

## 5. Timing: this is a pre-production system

`.gitlab-ci.yml` deploys to **sandbox only**. Both `deploy:sam:staging` and
`deploy:sam:production` are commented out, with `TODO: Add variable values and update
Gitlab CI/CD vars with staging and prod values` and blank parameter placeholders.

Two implications:

- **Migrating now is far cheaper than migrating later.** There is no production traffic to
  cut over, no live consumers to coordinate with, and no rollback plan to write.
- **There is no production baseline** to validate a migration against. Whatever you build,
  correctness has to be established from captured sandbox traffic and the handler tests,
  not from production parity.

---

## 6. Recommendation

**"Abandon the current architecture for Hookway" is the wrong framing.** Hookway is a
strong replacement for 3 of 7 handlers and a non-starter for the other 4.

### Option 1 — Hybrid (recommended)

Move the three webhook routers (DigiFi, Qualia, ClearEdge) to Hookway. Keep the four
synchronous proxies in SAM.

This captures the durability and observability wins exactly where they matter — async
ingestion, where events are currently being dropped silently — without forcing Hookway
into a reverse-proxy role it was not designed for. API Gateway continues to serve the
proxy paths; Hookway sits behind an ALB for the webhook paths.

Still requires building, in rough priority order:

1. **Dynamic target resolution** — a lookup provider plus target templating from body content.
   **Update (2026-09-16):** path `{vars}` and `transform.field_map` fill `{name}` in `publish.target` and webhook `url` (`examples/variables.yaml`). The `lookup` node exists but **standalone CLI/serve reject it** (`StandaloneIssues`). Endpoint `variables` / `source: lookup` are not YAML.
2. **SNS/SQS `MessageAttributes`** sourced from envelope fields.
   **Update (2026-09-16):** `publish.message_attributes` closed this.
3. **Query-parameter auth source** for Qualia.
   **Update (2026-09-16):** `auth.source.kind: query` closed this.
4. **AWS Secrets Manager secret source** with rotation (already P0-5).
   **Update (2026-09-16):** `secret_ref.provider: aws_secrets_manager` with overlap; no live YAML reload.
5. **A `digifi` auth scheme** byte-compatible with the vendor SDK, validated against
   captured real traffic.
6. **A transformation hook** — at minimum the `changedFields` diff.
   **Update (2026-09-16):** `transform` `changed_fields` / `field_map` / `xml_prune`.
7. **Deployment split by trust domain** to preserve IAM isolation (B1).

### Option 2 — Full migration

Additionally requires building synchronous gateway mode into Hookway: upstream response
passthrough, arbitrary methods, path and query forwarding, response header handling,
upstream OAuth token acquisition, and an XML transformation pipeline.

**Update (2026-09-16):** passthrough, methods, path/query forwarding, and XML prune
shipped. OAuth token acquisition and a vendor DigiFi HMAC scheme are still open.

### Option 3 — Status quo, patched

Keep SAM and fix the actual bug: stop returning 200 on publish failure, add a Lambda DLQ,
add retries to `publishToSQS`. Cheapest path to the main reliability win, forgoes
everything else.

### Suggested next step

Before committing either way, run one spike: **capture real DigiFi webhook traffic from
sandbox and verify Hookway's HMAC verification against it** (gap A7). If the vendor SDK's
canonicalization does not match raw-byte verification, that changes the cost of every
option above, and it is a half-day to find out.
