# Durable MCP event subscriptions

Expose named events alongside tools on an authenticated MCP endpoint. Hookway
stores active subscriptions and accepted pending deliveries in PostgreSQL, then
sends matching occurrences to subscribers through signed HTTPS callbacks. No
model runs inside Hookway.

This implementation follows the [OpenAI MCP Events webhook profile](https://developers.openai.com/plugins/build/mcp-events)
reviewed on 2026-10-21 and the [draft design sketch at revision 13e0a597](https://github.com/modelcontextprotocol/experimental-ext-triggers-events/blob/13e0a59749cde947374aaa8e3a65f0b424468d5a/docs/design-sketch-proposal.md).
The compatibility profile uses protocol `2026-07-28`, `events/list`,
`events/subscribe`, `events/unsubscribe`, and `capabilities.events` in
`server/discover`. Callback errors use OpenAI's `-32015` profile. Events are not
advertised to older protocol clients. Existing tool protocols remain supported.

## Configure and initialize

Start with [the complete example](../../examples/mcp-events.yaml). Configure
`event_delivery.postgres_url`, a stable `namespace`, and `encryption_key` using an
existing env/file/AWS secret reference. The encryption secret must be base64
encoding of exactly 32 random bytes. Keep it stable across restarts and identical
on all replicas sharing the namespace. Changing it makes the namespace unusable;
key rotation and historical catch-up are outside this release.

The connection must permit creation of the `hookway_events` schema during the
explicit migration. It may share a database with protection or replay; its tables
and lifecycle are independent.

```sh
hookway validate --config examples/mcp-events.yaml
hookway event-store migrate --config examples/mcp-events.yaml
hookway event-store status --config examples/mcp-events.yaml
hookway --config examples/mcp-events.yaml
```

Set the variables named in the example before running these commands. Migration
creates only the event schema/tables and takes a PostgreSQL advisory migration
lock. Serving never migrates and refuses missing/incompatible schemas or a
namespace encryption-key mismatch. CLI status does not print subscriptions,
callbacks, secrets or caller credentials.

Optional limits default to four workers, 1,000 active subscriptions, 10,000
pending deliveries, and 256 MiB of retained serialized event bodies per namespace.
`workers`, `max_subscriptions`, `max_pending`, and `max_bytes` can be authored under
`event_delivery`. Subscription metadata is bounded by schema/transport limits;
`max_bytes` accounts for occurrence bodies, not total PostgreSQL disk usage.
Replicas must use the same configuration, namespace, and encryption key.

## Expose and publish events

Shared `events` definitions still describe payloads, and Create event still has
no delivery side effects. Add an `events` list to `mcp_trigger`:

```yaml
events:
  - event: issue.opened
    input_schema:
      type: object
      additionalProperties: false
      required: [repository]
      properties: {repository: {type: string}}
    filters: {repository: repository}
    access: {api_keys: [reader]}
```

Each argument must have one filter binding to a dotted path inside occurrence
`data`. Arguments are scalar strings, booleans, or numbers; comparisons preserve
numeric precision and do not coerce types. All supplied arguments must match.
Exposed MCP event names use 1–128 letters, digits, dots, underscores or hyphens.
Omitted optional arguments do not filter. Use explicit API-key/scope/role grants;
anonymous subscriptions are unsupported. Grants use the same semantics as tools:
all scopes, any configured role, or an explicitly named API key. Filters do not
prove access to a resource in an upstream system.

A destination selects the MCP flow, and its Publish node selects the event:

```yaml
destinations:
  agents:
    type: mcp_events
    mcp_events: {endpoint: agent_tools}
# In a normal authenticated webhook flow, after Create event:
# - {id: send, type: publish, destination: agents, target: issue.opened}
```

Targets must be literal event names exposed by that endpoint. Broker options,
object payloads, and alternate wire formats are rejected. Publication revalidates
the occurrence after any transformations and limits its final JSON body to
256 KiB, including `cursor: null`. Existing destinations retain their behavior.

In the builder, use **Config → Events** for definitions and durable delivery
settings. Select the MCP trigger's **Events** tab for subscriptions and grants,
then select **MCP events** when adding a destination. The builder exports
configuration; it does not manage live subscriptions. Live subscriptions and deliveries are managed in the operator console, separately from builder configuration.

## Client lifecycle

Use the same authenticated MCP endpoint as tools. MCP 2.0 requests also require
the SDK's request metadata and matching `Mcp-Method` header; use a compatible SDK
rather than sending only a legacy JSON-RPC envelope. Discover events and send:

```json
{
  "name": "issue.opened",
  "arguments": {"repository": "acme/app"},
  "delivery": {
    "mode": "webhook",
    "url": "https://receiver.example/mcp-events",
    "secret": "whsec_<base64-of-24-to-64-random-bytes>"
  },
  "cursor": null,
  "ttlMs": 3600000
}
```

These are the `events/subscribe` parameters. Callback URLs must use HTTPS without
embedded credentials or fragments. Verification and deliveries block non-public
IP addresses at connection time, validate TLS, and never follow redirects.
Before activation, Hookway sends a signed `verification` control object with a
fresh random challenge. Return a successful JSON response containing the same
`challenge`. Successful verification is cached for five minutes by principal,
URL, and signing-key fingerprint; no application event is sent before verification.

Subscribe returns a deterministic opaque `id`, `refreshBefore`, `cursor: null`,
and `truncated: false`. Identity includes namespace, endpoint, authenticated
principal, URL, event, and canonical JSON arguments. Reordered object keys and
equivalent numeric spellings do not create new identities. Call subscribe again
before expiry to refresh; requests for no expiry still receive a finite grant.
The default is one hour, clamped between one minute and 24 hours, and further
capped by the authenticated token or API-key expiry. Refreshing a signing secret
retains the previous key for a five-minute dual-signature overlap.

OAuth bearer tokens are never persisted. Delivery uses the verified principal's
scope/role snapshot until its token deadline, checking current configured grants
before every attempt. Renew with a fresh authenticated request to update that
snapshot. JWT issuer-side revocation/role changes are not continuously observable
without a new authentication; the token expiry bounds that authorization lease.
Removing an API-key grant/key, losing the key resolver, expiry, or unsubscribe
stops new attempts. Config changes take effect on restart; roll replicas with
matching settings. Already-started HTTP requests may finish.

Unsubscribe takes the original name, arguments, and `delivery: {mode: webhook,
url: ...}`. It is idempotent and can affect only the authenticated principal's
subscription. Subscription IDs cannot be used to bypass ownership.

## Delivery and recovery boundaries

A successful Publish commits an occurrence and matching pending recipients in
one transaction. With no matching active subscribers, publication is a successful
no-op. Capacity/database failures fail publication; existing caller acknowledgement
policies still apply. Hookway does not introduce a durable spool before this
publication boundary.

The retained key is `(namespace, endpoint, event name, event ID)`. Repeated
acceptance preserves the first committed body and recipient set; different data
under the same key fails. Use stable sender IDs for sender redelivery. Rebuilding
an event with a generated UUID creates a new identity. The retention window is
bounded; this is not an exactly-once guarantee.

Workers use leased PostgreSQL claims and fenced completion updates. Pending
work survives process restarts and can be recovered by another replica after
lease expiry. Each callback receives the original event ID, timestamp, data,
and null cursor, signed with Standard Webhooks headers. Retries use the same
body bytes with a fresh signing timestamp and signature. `2xx` acknowledges
receipt, not completion of an agent action.

Deliveries are at least once and unordered. Network errors, `408`, `429`, and
`5xx` retry with jittered exponential backoff, starting at one second and capped
at five minutes, for at most ten delivery attempts. Other statuses, including
`410` and `413`, are terminal. Expired or unsubscribed work is cancelled.
Terminal delivery metadata is retained for 24 hours, then removed with
unreferenced bodies by periodic cleanup or `hookway event-store gc`.

This release has no historical subscription catch-up, non-null cursors, poll,
stream, `gap`, or `terminated` delivery. Recovery applies only to already accepted
pending deliveries for subscriptions that remain active. Inspect metrics or the operator console for failures. The console supports scoped inspection and eligible manual retries.

Metrics include `hookway_event_delivery_total{operation,outcome}`,
`hookway_event_pending`, `hookway_event_retained_bytes`, and
`hookway_event_store_ready`. No principal IDs or callback URLs are metric labels.
Database availability affects readiness whenever event delivery is configured.

## Verification

Run `make event-integration-test` for real PostgreSQL, signed TLS callbacks,
restart/lease recovery, ownership boundaries, retries, capacity rejection, and the
authenticated webhook → MCP event path. The deterministic test receiver verifies
signatures and echoes challenges; private-address bypass exists only in the test
client, never as a production callback option.

For a local receiver outside tests, [callback_receiver.py](../../examples/mcp-events/callback_receiver.py)
uses the Python standard library and your own TLS certificate/private-key files.
It validates timestamps and signatures, echoes challenges, and prints received
occurrences. Run it on a publicly reachable HTTPS hostname with a trusted
certificate when connecting an external agent; a localhost receiver alone does
not satisfy production callback policy.

Hosted-agent verification is separate: connect the deployed HTTPS endpoint through
a plugin in a supported ChatGPT Work/Cloud environment, discover events, subscribe,
confirm challenge verification, trigger matching and nonmatching webhooks, observe
an agent turn and a tool call, then unsubscribe. Automated protocol tests do not
establish that hosted-agent wake-up happened. Record the client, protocol and
server revision when performing that check.

## Operator console

The [MCP event operations guide](../operations/mcp-events-console.md) covers
subscription inspection, per-attempt delivery evidence, scoped payload access,
manual retry cycles, and persistent revocation/restoration. The
[combined issue demo](../../examples/mcp-events/demo/README.md) includes its own
Docker test identity provider and an automated OAuth/PKCE check.
