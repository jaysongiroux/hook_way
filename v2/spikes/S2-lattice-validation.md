# S2 — Lattice validation against seven gateway handlers

**Status: provisional; source verification pending.** 2026-09-14.

The five existing payload types can represent every **documented** handler topology.
None of the four known suspects requires a new `PortType` or a change to strict-equality
`Accepts`. Several node options need an explicit contract before implementation.
This is a paper graph review, not a claim that these configs run or match the Lambdas.

## Evidence and limits

Read for this investigation:

- [Design](../02-design.md) §§2, 4–8: payloads, node catalog, branching, example YAML.
- [Scalability](../03-scalability.md) R1–R4 and §4: no per-node persistence, bounded
  observations, cached reads and secret refresh.
- [Baseline](../00-v1-baseline.md) §2: I1–I18, especially raw-byte auth and filter ordering.
- [Gap analysis](../../docs/universal-gateway-migration-gaps.md) §§1–2 and §6: seven
  route paths, precise ClearEdge regexes, Qualia comparison, XML rule order, diff paths,
  SNS attribute type, dynamic lookup, proxy forwarding. Its upstream source citation is
  `universal-gateway` commit `3f8161f`; that is historical evidence, not a source checkout
  verified during this investigation.
- [Chunk 6](../plans/06-sync-proxy-and-transforms.md), [Chunk 9](../plans/09-migration-configs.md),
  and [Chunk 4](../plans/04-gates-and-processing.md): downstream requirements and proposed
  configuration semantics.
- [Existing DynamoDB provider](../../internal/lookup/dynamodb.go): supports a key plus a
  client-side equality filter, newest-first pagination and an environment value attribute.
  [Current condition documentation](../../docs/configuration.md) defines `regex` matching.

`test -d ../universal-gateway` returned false. No handler `app.js`, XML `pipeline.js`,
vendor package, SAM template, Jest cases, or captured request was available at that
location. Consequently this cannot settle exact event-to-id maps, queue/topic selection,
FIFO grouping/deduplication, upstream grant parameters, inbound credential names for most
handlers, XML predicates, or source-specific error responses. Obtain the source and check
these before marking S2 source verification complete. S1 separately controls DigiFi auth.

## Expressibility table

“Yes” means the **documented operation** fits the current catalog and existing option
concepts. It does not certify migration parity. “No” means an option/algorithm contract is
still missing or an explicit dependency is unresolved. All seven use the current lattice.

| Handler | Expressible now, yes/no | Minimal amendment or remaining qualification |
|---|---|---|
| `digifi_router_webhook_handler` | No | S1 must establish the `digifi` scheme. Specify `changed_fields` output binding and SNS `String.Array` attribute serialization; retain event-specific switching and existing lookup/destination nodes. No I6 exception assumed. |
| `qualia_router_webhook_handler` | Yes | Make existing `auth.source` concrete as `{kind: query, key: authID}` with timing-safe exact comparison. Source still needed for extraction/routing and FIFO details. |
| `clearedge_router_webhook_handler` | Yes | Existing ordered `switch` cases discriminate regexes before two existing `lookup` nodes. Expose existing DynamoDB `id_type` filter in lookup config; no candidate-key mechanism. Source still needed for actual filter values and unmatched behavior. |
| `salesforce_router_proxy_handler` | No | Add cached upstream OAuth configuration to `http_request`; existing forwarding and response slots suffice. Grant and credentials remain source-required. |
| `salesforce_digifi_proxy_handler` | No | Same `http_request.auth.oauth2` option; no token port/node. |
| `digifi_router_proxy_handler` | No | Same `http_request.auth.oauth2` option; no token port/node. |
| `docmagic_xml_proxy_handler` | No | Define ordered `xml_prune.rules`, per-rule counts and the transformed-body input to `http_request`. Existing XML envelope and response slot suffice. |

The three-OAuth-proxy requirement is explicit in [S2's task](../plans/00-spikes.md),
“Upstream OAuth on the three proxies.” Chunk 9's table mentions OAuth on only one row;
that omission must not silently narrow the task. Actual source must settle differences
between their grants, refresh behavior and destinations.

## Seven YAML graphs

These are **proposed paper configs**, one per handler. All node types are from design §4.
Every `__SOURCE_REQUIRED_*__` scalar is an unresolved source-dependent value, not a valid
deployment default. Unknown credentials deliberately use an invalid scheme placeholder;
do not replace it with `no_auth` just to make a config load. Body limits, cache TTLs and
fallbacks below are illustrative policy choices, not measured Lambda parity.

`cases`/`when`, `name`, `filter`, `rules`, typed `message_attributes`, `body`, and `oauth2`
show the **option spelling we wish worked**; their contracts are proposed below, not
claimed to be implemented. Template examples follow the design's `{route}` notation.
No global guards are shown because this spike examines handler shape; deployment guards
and executor inflight control still have to preserve I1–I4.

### 1. DigiFi webhook

Evidence: gaps A2/A3/A4/A7 and design §7. The example body key is documented in design
§7, but the real handler's per-event extraction map is unavailable. Its SNS/SQS selection
is modeled as an exclusive switch, **not** an invented unconditional fan-out.

```yaml
schema_version: 2
flows:
  digifi_router_webhook_handler:
    nodes:
      - {id: in, type: http_trigger, path: /webhooks/universal/digifi, methods: [POST]}
      - {id: body, type: read_body, max_bytes: 1048576}
      - {id: auth, type: auth, scheme: digifi, secret: "${DIGIFI_SECRET}"} # S1 blocked
      - {id: env, type: envelope, body_format: json}
      - id: route
        type: lookup
        provider: dynamodb
        table: "${ROUTING_TABLE}"
        name: route
        key: "__SOURCE_REQUIRED_EVENT_SPECIFIC_ID_TEMPLATE__"
        filter: {attribute: id_type, value: "__SOURCE_REQUIRED_ID_TYPE__"}
        cache: {ttl: 5m, max_entries: 10000}
        fallback: "__SOURCE_REQUIRED_FALLBACK_ENV__"
      - id: diff
        type: transform
        kind: changed_fields
        from: body.data.previousObject
        to: body.data.object
        name: changed_fields
      - id: select
        type: switch
        cases:
          - {port: sns, when: "__SOURCE_REQUIRED_SNS_EVENT_CONDITION__"}
          - {port: sqs, when: "__SOURCE_REQUIRED_SQS_EVENT_CONDITION__"}
        default: unsupported
      - id: sns
        type: sns
        topic: "{route}-__SOURCE_REQUIRED_TOPIC_SUFFIX__"
        message_attributes:
          fields_changed: {data_type: String.Array, value: "{changed_fields}"}
      - {id: sqs, type: sqs, queue: "{route}-__SOURCE_REQUIRED_QUEUE_SUFFIX__"}
      - {id: unsupported, type: respond, status: 400, body: Unsupported event type}
    edges: [in>body, body>auth, auth>env, env>route, route>diff, diff>select,
            "select:sns>sns", "select:sqs>sqs", "select:unsupported>unsupported"]
```

The unsupported-event response is a proposed explicit policy, not a known Lambda result.
After source review, expand event-specific extraction with existing `switch`/`field_map`
nodes if one lookup key cannot cover it. Do not add a network-capable `code` escape hatch.
Diffing may belong on only the SNS branch; source must establish which events contain
`previousObject`. SNS/SQS message body shape and FIFO parameters remain unverified.

### 2. Qualia webhook

Evidence: gaps A5 quotes `queryParams.authID !== endpointSecret`; A2 states all webhook
routers resolve environments. Reuse the exact-match verifier with a query source.

```yaml
schema_version: 2
flows:
  qualia_router_webhook_handler:
    nodes:
      - {id: in, type: http_trigger, path: /webhooks/universal/qualia, methods: [POST]}
      - {id: body, type: read_body, max_bytes: 1048576}
      - id: auth
        type: auth
        scheme: header_exact
        source: {kind: query, key: authID}
        secret: "${QUALIA_SECRET}"
      - {id: env, type: envelope, body_format: json}
      - id: route
        type: lookup
        provider: dynamodb
        table: "${ROUTING_TABLE}"
        name: route
        key: "__SOURCE_REQUIRED_QUALIA_ID_TEMPLATE__"
        filter: {attribute: id_type, value: "__SOURCE_REQUIRED_QUALIA_ID_TYPE__"}
        cache: {ttl: 5m, max_entries: 10000}
        fallback: "__SOURCE_REQUIRED_FALLBACK_ENV__"
      - {id: out, type: sqs, queue: "{route}-__SOURCE_REQUIRED_QUEUE_SUFFIX__"}
    edges: [in>body, body>auth, auth>env, env>route, route>out]
```

`header_exact` names the existing comparison scheme; `source` chooses the request value.
It must not secretly force a header read when the source is query. Naming it `exact` later
would be a naming choice, not a new payload type or new authentication algorithm.

### 3. ClearEdge webhook

Evidence: gaps A2 quotes both regexes and `client_file_number`; gaps C3 confirms a token
comparison but does not identify the request header. The precise body nesting and the
stored `id_type` values are source-required. The root-level field below is illustrative.

```yaml
schema_version: 2
flows:
  clearedge_router_webhook_handler:
    nodes:
      - {id: in, type: http_trigger, path: /webhooks/universal/clearedge, methods: [POST]}
      - {id: body, type: read_body, max_bytes: 1048576}
      - id: auth
        type: auth
        scheme: header_exact
        source: {kind: header, key: "__SOURCE_REQUIRED_CLEAREDGE_AUTH_HEADER__"}
        secret: "${CLEAREDGE_SECRET}"
      - {id: env, type: envelope, body_format: json}
      - id: classify
        type: switch
        cases:
          - port: digifi
            when: {source: body, path: client_file_number, operator: regex, value: '^[0-9a-fA-F]{24}$'}
          - port: friendly
            when: {source: body, path: client_file_number, operator: regex, value: '^[A-Z]{2}\d{6}$'}
        default: invalid
      - id: digifi_lookup
        type: lookup
        provider: dynamodb
        table: "${ROUTING_TABLE}"
        name: route
        key: "{body.client_file_number}"
        filter: {attribute: id_type, value: "__SOURCE_REQUIRED_DIGIFI_ID_TYPE__"}
        cache: {ttl: 5m, max_entries: 10000}
        fallback: "__SOURCE_REQUIRED_FALLBACK_ENV__"
      - id: friendly_lookup
        type: lookup
        provider: dynamodb
        table: "${ROUTING_TABLE}"
        name: route
        key: "{body.client_file_number}"
        filter: {attribute: id_type, value: "__SOURCE_REQUIRED_FRIENDLY_ID_TYPE__"}
        cache: {ttl: 5m, max_entries: 10000}
        fallback: "__SOURCE_REQUIRED_FALLBACK_ENV__"
      - {id: out, type: sqs, queue: "{route}-__SOURCE_REQUIRED_QUEUE_SUFFIX__"}
      - {id: invalid, type: respond, status: 400, body: Unsupported client file number}
    edges: [in>body, body>auth, auth>env, env>classify,
            "classify:digifi>digifi_lookup", "classify:friendly>friendly_lookup",
            "classify:invalid>invalid", digifi_lookup>out, friendly_lookup>out]
```

The two branches are exclusive; their shared destination is not a join waiting for both.
An invalid id makes no lookup. The 400 default is a proposed fail-closed behavior pending
source review, not preservation of the gateway's documented 200-on-error behavior.

### 4. Salesforce router proxy

Evidence: design §7 provides this route, raw envelope, header-exact auth and DigiFi base;
gaps A1/A6 supply response, method, path and query passthrough. OAuth is required by S2.

```yaml
schema_version: 2
flows:
  salesforce_router_proxy_handler:
    nodes:
      - {id: in, type: http_trigger, path: '/salesforce/proxy/{rest...}', methods: [ANY]}
      - {id: body, type: read_body, max_bytes: 10485760}
      - id: auth
        type: auth
        scheme: header_exact
        source: {kind: header, key: "__SOURCE_REQUIRED_SALESFORCE_AUTH_HEADER__"}
        secret: "${SF_TOKEN}"
      - {id: env, type: envelope, body_format: raw}
      - id: up
        type: http_request
        url: "${DIGIFI_BASE}/{rest}"
        forward_method: true
        forward_query: true
        body: raw
        auth:
          oauth2:
            token_url: "${SF_ROUTER_TOKEN_URL}"
            grant: "__SOURCE_REQUIRED_OAUTH_GRANT__"
            secret_ref: {provider: env, name: SF_ROUTER_OAUTH_CREDENTIALS}
      - {id: resp, type: respond, passthrough: true}
    edges: [in>body, body>auth, auth>env, env>up, up>resp]
```

### 5. Salesforce–DigiFi proxy

Evidence: gaps §1/A1/A6 and S2 OAuth requirement. Do not infer inbound auth or grant type
from the neighboring Salesforce proxy.

```yaml
schema_version: 2
flows:
  salesforce_digifi_proxy_handler:
    nodes:
      - {id: in, type: http_trigger, path: '/salesforce-digifi/proxy/{rest...}', methods: [ANY]}
      - {id: body, type: read_body, max_bytes: 10485760}
      - {id: auth, type: auth, scheme: "__SOURCE_REQUIRED_INBOUND_AUTH_SCHEME__",
         secret: "${SF_DIGIFI_INBOUND_SECRET}"}
      - {id: env, type: envelope, body_format: raw}
      - id: up
        type: http_request
        url: "${SF_DIGIFI_UPSTREAM_BASE}/{rest}"
        forward_method: true
        forward_query: true
        body: raw
        auth:
          oauth2:
            token_url: "${SF_DIGIFI_TOKEN_URL}"
            grant: "__SOURCE_REQUIRED_OAUTH_GRANT__"
            secret_ref: {provider: env, name: SF_DIGIFI_OAUTH_CREDENTIALS}
      - {id: resp, type: respond, passthrough: true}
    edges: [in>body, body>auth, auth>env, env>up, up>resp]
```

### 6. DigiFi router proxy

Evidence: gaps §1/A1/A6 and S2 OAuth requirement. Source must establish auth source,
grant, upstream URL, timeout and body limit; its Lambda resource settings are not limits
that can safely be copied without understanding the workload.

```yaml
schema_version: 2
flows:
  digifi_router_proxy_handler:
    nodes:
      - {id: in, type: http_trigger, path: '/proxy/{rest...}', methods: [ANY]}
      - {id: body, type: read_body, max_bytes: 10485760}
      - {id: auth, type: auth, scheme: "__SOURCE_REQUIRED_INBOUND_AUTH_SCHEME__",
         secret: "${DIGIFI_PROXY_INBOUND_SECRET}"}
      - {id: env, type: envelope, body_format: raw}
      - id: up
        type: http_request
        url: "${DIGIFI_PROXY_UPSTREAM_BASE}/{rest}"
        forward_method: true
        forward_query: true
        body: raw
        auth:
          oauth2:
            token_url: "${DIGIFI_PROXY_TOKEN_URL}"
            grant: "__SOURCE_REQUIRED_OAUTH_GRANT__"
            secret_ref: {provider: env, name: DIGIFI_PROXY_OAUTH_CREDENTIALS}
      - {id: resp, type: respond, passthrough: true}
    edges: [in>body, body>auth, auth>env, env>up, up>resp]
```

### 7. DocMagic XML proxy

Evidence: gaps §1/A3 and Chunk 6 Task 4 list the five ordered rules. Only path and pipeline
shape are established; POST and forwarding choices below are illustrative pending source.

```yaml
schema_version: 2
flows:
  docmagic_xml_proxy_handler:
    nodes:
      - {id: in, type: http_trigger, path: /digifi/docmagic, methods: [POST]}
      - {id: body, type: read_body, max_bytes: 10485760}
      - {id: auth, type: auth, scheme: "__SOURCE_REQUIRED_INBOUND_AUTH_SCHEME__",
         secret: "${DOCMAGIC_INBOUND_SECRET}"}
      - {id: env, type: envelope, body_format: xml}
      - id: prune
        type: transform
        kind: xml_prune
        rules:
          - remove_empty_self_closing_elements
          - remove_empty_parents
          - remove_hardcoded_only_parties
          - remove_blank_general_partners
          - prune_orphaned_relationships
      - id: up
        type: http_request
        url: "${DOCMAGIC_UPSTREAM_URL}"
        forward_method: true
        forward_query: true
        body: transformed
        headers: {Content-Type: application/xml}
      - {id: resp, type: respond, passthrough: true}
    edges: [in>body, body>auth, auth>env, env>prune, prune>up, up>resp]
```

The upstream content type, method, credentials and URL may differ in actual source; none
is certified here. Auth always sees original bytes. XML parsing, pruning and serialization
occur after verification and must not overwrite the authenticated original bytes.

## Four suspects: decisions and minimal amendments

### ClearEdge: use the existing switch

The regexes have disjoint lengths: 24 hex characters versus two uppercase letters and six
digits. First-match ordering therefore causes no overlap for these two patterns. Missing,
non-string or unmatched values take an explicit default branch. Match before lookup, then
make exactly one query using the selected static `id_type` filter. A missing database row
is a routing miss, not an invitation to reinterpret the id as another type.

Keep `switch: Envelope -> Envelope` and reuse the existing regex condition compiler. Keep
`lookup: Envelope -> Envelope`; expose the existing DynamoDB filter, output name and cache
options. An ordered candidate-key lookup would conflate classification with I/O and add a
second branching language. No extra node or type is justified by this example.

### DocMagic: one transform with five ordered rules

Use one `transform {kind: xml_prune, rules: [...]}`. Its five rules form one established
algorithm; the later relationship rule depends on earlier removals. Execute the list in
order against one parsed tree; serialize once for the upstream call. Five nodes are
possible in the current lattice but needlessly expose internal pipeline mechanics and
invite accidental rewiring.

Each rule must emit its configured rule name/index, before element count, after element
count and duration to structured logs/spans. Define counts against the same tree and
counting semantics as the JS once available. Fail the transform if a rule fails; do not
send a partially rewritten document upstream. These observations do not create database
rows or payload-derived metric labels (scalability R2/R3). Golden/differential tests against
real scrubbed XML remain mandatory; rule names alone cannot establish predicate parity.

Specify `http_request.body: raw|transformed` (or one equivalent unambiguous option).
Default raw for transparent proxies; XML flow explicitly chooses serialized transformed
content. Existing `Envelope` holds the parsed/transformed body and original `Verified`
request, so this needs a field/serialization contract, not another `PortType`.

### Qualia: query source on auth

`RequestHead` already includes query values; `RawRequest` carries that head. Configure
`source: {kind: query, key: authID}` on `auth`, using the exact-comparison scheme. Reject
missing and ambiguous repeated `authID` values, and compare with a timing-safe function.
Repeated-value behavior is an explicit proposed policy until checked against API Gateway
and the handler. Do not log the query credential. Parsing query metadata does not parse
the body, so I6 stays intact. No new type, query-to-body transform or pre-auth envelope.

### Three upstream OAuth proxies: option on http_request

Use `http_request.auth.oauth2` for token endpoint, source-verified grant-specific settings,
credential `secret_ref`, and applicable scopes/audience. The YAML deliberately leaves
grant undecided; “OAuth” does not prove client credentials, password, JWT bearer or refresh
token grants. Define supported grant schemas only after reading the handlers.

Cache tokens in memory, single-flight refresh, and refresh before expiry with a skew.
Cache identity must include upstream/token endpoint, credential identity/version, grant,
scope and audience as applicable; unrelated flows must not share credentials accidentally.
Honor request cancellation while obtaining a usable token, avoid a token request on every
event, and use refreshed credentials after secret rotation. Do not publish tokens through
envelope vars, logs, metrics or durable node state. Never silently send an unauthenticated
upstream request on token failure. Do not automatically retry a non-idempotent proxied
request merely because the token was refreshed.

This uses `Envelope -> Envelope` with the existing upstream response slot. A separate
token-producing node creates a new data type and secret exposure surface for no documented
benefit. Inbound credential verification remains a separate `auth` node; acquiring an
upstream OAuth token does not authenticate the inbound caller.

## Additional contract findings

1. **I8 is not enforced by equality alone.** `filter` and `lookup` both consume and produce
   `Envelope`; therefore `envelope > lookup > filter` passes the published type table,
   although a lookup error can now happen before the filter drops the request. Preserve
   I8 with a structural validation rule rejecting a `filter` reachable after `lookup`
   (and other variable-dependent work as that contract is finalized), or explicitly
   resolve the invariant before implementation. Do not add a sixth payload type just
   to rescue the claim that all I1–I9 are type errors. The design must distinguish type,
   topology and executor guarantees; I2 and I9 already require executor behavior.
2. **Changed-field attributes need explicit serialization.** Bind diff results in
   `Envelope.Vars` under the configured output name; preserve nested `status.*` promotion.
   The known SNS attribute is `fields_changed`, AWS `DataType: String.Array`, whose
   `StringValue` is the JSON array text. Treating it as an ordinary string attribute or
   double-encoding a template would change subscription matching. The destination owns
   final wire conversion; no new payload type is needed. Schema/example shorthand should
   not hide the AWS data type.
3. **Proxy failures differ from transport errors.** Upstream 4xx/5xx are responses to pass
   through by default; transport/timeouts produce gateway errors. Use existing `respond`
   and executor error handling, not an invented error payload type. Auth failures before
   `Envelope` remain server responses; these graphs do not route them into `respond`.
4. **Byte-identical parity excludes deliberate reliability changes.** The gap analysis
   documents 200-on-publish-failure; the approved Hookway behavior is a retriable 503.
   Chunk 9 differential tests must list this intentional difference explicitly, along
   with response header filtering and unresolved malformed-input policies. Do not claim
   unconditional byte-identical equivalence for error cases that deliberately change.

## Verification performed and handoff

- Read the existing contracts and the precise quoted behaviors above; verified upstream
  source checkout was unavailable.
- Audited every graph edge against the unchanged table: `RequestHead -> RawRequest ->
  Verified -> Envelope -> Terminal`, with Envelope-preserving processing in between.
  Switch branches are exclusive and each explicit leaf is Terminal. No token payload,
  pre-auth transform or body-reading gate is introduced.
- All seven fenced YAML graphs parse successfully with Ruby's `YAML.safe_load` (Psych).
  Labelled edge strings are quoted for parser portability.
- These are documentation artifacts. No V2 runtime, source parity, capture replay or
  compiled-schema validation was run or is claimed. YAML syntax checks do not imply
  configuration-schema validity.
- Proposed design amendments: formalize query source; switch regex syntax and lookup
  filter/output binding; ordered XML rules and counts; transformed outbound body choice;
  cached OAuth option; typed message attributes; I8 structural validation. Preserve §2
  payload types and strict equality. This document does not modify the design itself.
- S2 can support a provisional lattice decision. **It cannot close the all-seven-handler
  source-verification gate yet.** Resolve source-required fields against the handler
  checkout/tests, fold accepted option contracts into design §4, and retain S1's separate
  gate before declaring Chunk 0 complete.
