# MCP gateway

Expose MCP endpoints at paths you choose, for example `/services/mcp` and
`/utilities/mcp`. Create a separate flow with an `mcp_trigger` for each endpoint;
there is no single-endpoint restriction. Each endpoint owns its authentication,
tool catalogue and caller permissions. Tool names need to be unique within an
endpoint, and may be reused across endpoints. Each tool selects one named branch
of its trigger. Branches can call REST
operations with `http_request` or run native Hookway processing. Finish every
successful branch with `mcp_result`.

The builder's **Authenticated MCP gateway** template includes two tools: an
HTTP status request and a native greeting transform. It imports the complete
flow and outbound credential references, creating a separate flow when needed.
Insert it again to create another endpoint; the builder chooses an unused default
path. Set each trigger's `path` and matching `public_url` to your desired address.
See [multiple MCP endpoints](../../examples/mcp-endpoints.yaml) for a complete
configuration with `/services/mcp` and `/utilities/mcp` and separate API keys. Endpoint
paths must not overlap other MCP, webhook, health, metrics or metadata routes.
Configure the endpoint and tools in the MCP trigger inspector, connect its named
outputs, export YAML, validate it and deploy/restart Hookway.

## Authentication and permissions

Authentication is recommended. Configure `auth.api_keys`, `auth.oauth`, or both.
Public access requires explicit `auth.no_auth: true` and each public tool must
set `access.anonymous: true`. Invalid credentials never fall back to anonymous
access. Use HTTPS outside localhost. Set `allowed_origins` to exact browser
origins when using the builder's live tester; CORS is not authorization.

API keys are named secret references using `env`, `file`, or
`aws_secrets_manager`. Generate at least 32 random bytes and store them through
your secret provider. Resolved tokens must be 32–256 bytes. Tools grant access
with `access.api_keys: [reader]`. Optional `expires_at` is an RFC3339 timestamp.
Keys are checked on every request. Secret-provider refresh/overlap settings
control rotation; removing a key or changing grants requires deployment/restart
of every replica. Separate keys identify separate principals; shared keys grant
shared access and cannot identify individual humans.

### OAuth configuration

Hookway is the OAuth resource server. Your identity provider authenticates users
and issues access tokens; the MCP client owns login, consent, PKCE and refresh
tokens. Hookway does not host a login page, issue tokens or register OAuth clients.
Gateway access tokens are never forwarded to an upstream API. Outbound API
authentication belongs to a separate `credentials` profile on its HTTP node.

#### Set up the identity provider

1. Choose the endpoint's canonical public URL, such as
   `https://hookway.example.com/services/mcp`. Keep its path consistent with the
   MCP trigger and make both the MCP route and its metadata route reachable over
   HTTPS through your reverse proxy.
2. Register a protected API/resource with a dedicated audience. The example uses
   the canonical endpoint URL as the audience. If your provider uses a different
   API identifier, configure its exact value in Hookway and verify that tokens
   for unrelated APIs are rejected.
3. Define a permission such as `service:read` and grant it to the intended users
   or service principals. Arrange for access tokens to contain that permission
   in `scope` or `scp`; defining `discovery_scopes` in Hookway does not grant it.
4. Register the MCP client at the identity provider with its exact callback URI
   and authorization-code flow with PKCE. The callback belongs to the MCP client,
   not the Hookway endpoint. Public clients must not be given a shared client
   secret. Start with pre-registration when the client supports supplying its
   client ID; otherwise confirm a compatible registration mechanism with the
   provider before enabling the connection.

MCP clients request tokens for the canonical endpoint using the OAuth `resource`
parameter. Check the actual issued token's audience; do not assume every
provider maps `resource` to the audience in the same way. See the official
[MCP authorization requirements](https://modelcontextprotocol.io/specification/2026-07-28/basic/authorization).

#### Configure the trigger and tool grants

Start with the complete [OAuth example](../../examples/mcp-oauth.yaml). These
fields belong to its `mcp_trigger` node:

```yaml
path: /services/mcp
public_url: https://hookway.example.com/services/mcp
auth:
  oauth:
    issuer: https://identity.example.com
    audience: https://hookway.example.com/services/mcp
    discovery_scopes: [service:read]
```

All URLs and permission names here are placeholders for your deployment.
JWT validation needs no OAuth client secret in Hookway.

| `auth.oauth` field | How to configure it |
| --- | --- |
| `issuer` | Exact issuer string from the provider's metadata and access-token `iss` claim, including any issuer path or trailing slash. |
| `audience` | Dedicated API/resource identifier that must occur in the access-token `aud` claim. This is not the MCP client's client ID. |
| `discovery_scopes` | Minimum initial permissions advertised in resource metadata and 401 challenges. Per-tool access rules still enforce authorization. |
| `jwks_url` | Optional trusted signing-key URL. Omit it to use OIDC discovery at `<issuer>/.well-known/openid-configuration`. Set it explicitly if the provider hosts keys on another origin. |
| `roles_claim` | Optional top-level claim containing a whitespace-separated string or array of role strings. Nested objects and dotted claim paths are not supported. |
| `introspection_url` | Optional provider endpoint for validating opaque tokens. Setting it selects introspection for all OAuth tokens, including JWTs. |
| `client_id`, `client_secret` | Secret references for Hookway's confidential introspection client; required only when `introspection_url` is set. These are not the interactive MCP client's credentials. |
| `allow_private_targets` | Default `false`. Set `true` only when the trusted identity provider intentionally uses private addresses; it also permits HTTP for private-provider development. |

Configure each tool's `access.scopes` and/or `access.roles`. A token with
`scope: "service:read"` satisfies `access: {scopes: [service:read]}`. Every listed
scope is required. Roles match any listed role, and scope and role requirements
are combined when both are configured. Scope values come from `scope`, with
`scp` used when `scope` has no usable entries. Set `roles_claim` and map provider
roles to a top-level string/list before using role grants. A tool without OAuth
scope or role grants is not callable by an OAuth principal.

For a mutating tool, also set `read_only: false` and
`access.allow_mutation: true`, with a separate explicit write permission. Read
permissions do not imply write access. Discovery filters unauthorized tools and
every invocation checks the policy again. Identity is bound to issuer plus
subject; a role name alone does not identify a user.

API keys can coexist under `auth.api_keys`. A tool must separately list allowed
key names in `access.api_keys` to admit those callers. OAuth scope/role grants
apply to OAuth principals; an API-key caller is authorized by its named-key
grant. Invalid credentials never become anonymous access.

#### JWT validation and discovery

Hookway publishes protected-resource
metadata for each endpoint: `/services/mcp` uses
`/.well-known/oauth-protected-resource/services/mcp`. Its 401 challenge points to
that endpoint's metadata. JWT access tokens require RS256 or ES256, a `kid`,
`iss`, matching `aud`, nonempty `sub` and an unexpired `exp`. ID tokens must not
be used as gateway access tokens. OIDC discovery supplies the JWKS URL, or `jwks_url` sets an
explicit trusted URL. Discovery must echo the configured issuer; discovered JWKS
must stay on its origin. Unknown keys trigger a bounded refresh, at most once per
30 seconds; known keys are cached for five minutes. Failed validation is closed.

#### Opaque tokens and introspection

Replace the example's `auth.oauth` block with this shape when your provider
issues opaque access tokens:

```yaml
oauth:
  issuer: https://identity.example.com
  audience: https://hookway.example.com/services/mcp
  discovery_scopes: [service:read]
  introspection_url: https://identity.example.com/oauth/introspect
  client_id: {provider: env, name: MCP_INTROSPECTION_CLIENT_ID}
  client_secret: {provider: env, name: MCP_INTROSPECTION_CLIENT_SECRET}
```

Register a confidential client allowed to introspect this API's tokens, then
supply those environment variables to the Hookway process. The references also
support file and AWS Secrets Manager providers. Hookway sends a form-encoded
`token` and `token_type_hint=access_token` with HTTP Basic client authentication;
providers requiring a different client-authentication method are not supported
by this configuration.

The introspection response must contain `active: true`, the exact configured
`iss`, the intended `aud`, a nonempty `sub`, and a future numeric `exp`, plus the
scope/role claims needed by the tool. An `active` response alone is insufficient.
Hookway introspects every OAuth-authenticated request without caching the result;
an unavailable provider fails authentication. With introspection configured, JWTs
use this same path rather than local signature validation.

#### Connect and verify

Validate and deploy the complete example after changing its placeholders:

```sh
go run ./cmd/hookway validate --config examples/mcp-oauth.yaml --strict
```

Route the endpoint and protected-resource metadata through your public proxy.
The unauthenticated POST below should return 401 and a `WWW-Authenticate` header
pointing to that endpoint's metadata; it is an authentication probe, not a tool
call:

```sh
curl -i -X POST https://hookway.example.com/services/mcp \
  -H 'Content-Type: application/json' --data '{}'
curl --fail --silent --show-error \
  https://hookway.example.com/.well-known/oauth-protected-resource/services/mcp
```

The metadata's `resource` must match the public endpoint URL and
`authorization_servers` must name your configured issuer. Connect a compatible
MCP client to that URL, complete its provider login, request `service:read` for
the resource, list tools and invoke `get_service_status` with `{}`. The example
returns `{"status":"ok"}` through a native result, so this first check needs no
upstream API credentials. For another endpoint, configure its own URL, resource
audience and grants.

The builder's **Test & connect** accepts an existing OAuth access token in memory;
it does not perform the browser login or refresh tokens for you. Obtain the token
through your registered client/provider flow, set `allowed_origins` to the exact
builder origin, and use the deployed endpoint. Do not put access tokens in YAML,
query strings or connection screenshots. Authentication must accompany every
MCP HTTP request as an `Authorization: Bearer` header.

| Symptom | Check |
| --- | --- |
| Login cannot begin | Client registration/callback support, provider discovery, and public metadata routing. The gateway URL is not a login callback. |
| 401 with a token | Issuer/audience/expiry, signing algorithm and `kid`, JWKS reachability, or introspection credentials/required claims. Do not substitute an ID token. |
| Empty tool list or 403 | Token scope/role claims and each tool's grants. `discovery_scopes` advertises permissions; it does not add them to tokens. |
| Browser fails while a server client works | Exact `allowed_origins`, CORS/preflight routing, HTTPS and certificate trust. |
| Failures after key rotation | Provider JWKS publication, the five-minute known-key cache and bounded refresh for unknown keys. |

#### Identity-provider compatibility

For Zitadel, use pre-registered clients and a dedicated API audience. Its current
[DCR limitations](https://zitadel.com/docs/guides/integrate/dynamic-client-registration#limitations)
say resource indicators do not narrow the shared DCR-project audience. Hookway
does not enforce separate `client_id` or `azp` allowlists. Do not rely on this
shared audience to isolate gateways or weaken audience checks to make login work.
Confirm issued access-token claims and cross-resource rejection with your actual
provider/client versions. The example is a configuration recipe, not a claim of
live identity-provider certification.

## Tool contracts and mapping

Each tool requires `name`, `description`, `read_only`, `access`, `input_schema`
and `output_schema`. Input schemas declare `type: object`. Schemas compile at
load with local references only; no network or file schema fetching is permitted.
Schemas are limited to 64 KiB and normal JSON structural budgets. The gateway
accepts at most 64 tools per endpoint and bounds MCP requests, upstream replies and results to
1 MiB. It never exposes arbitrary caller URLs, commands or credential profiles.

Arguments become the native JSON body. Optional `request` maps select dotted
argument paths:

```yaml
request:
  variables: {record_id: id}
  query: {search: query}
  headers: {X-Operation: operation}
  body: {payload: data}
```

Variables remain plain inside the graph and are escaped at the HTTP boundary as path values for templates such as
`https://api.example.com/records/{record_id}`. Query values are encoded separately;
URL-template substitutions are confined to path segments; use `request.query`
and set `forward_query: true` on the HTTP node. Mapped headers must be application
headers and cannot replace authentication/identity/transport headers. Body maps
preserve JSON types; use `body: transformed` on an HTTP node. Without a body map,
the complete arguments remain available. Do not pass personal account selectors
unless the configured branch independently authorizes them.

`mcp_result.source` selects `body`, `upstream` (parsed upstream JSON), or
`constant` with `value`. Optional `field` selects one dotted value; `fields` builds
an object from named dotted selections. Output must satisfy the tool's schema.
Non-2xx upstream results and invalid JSON become sanitized tool errors. To handle
non-2xx responses explicitly, bind the HTTP status with `response_bindings` and
route it to a configured local result; `source: upstream` always requires 2xx.

Outbound credentials belong to the operator and remain in existing `credentials`
profiles. HTTP nodes use fixed origins and fixed credential profiles. Incoming
MCP bearer tokens and transport headers never enter the graph. Private adapters
require explicit private-target permission and origin restrictions. HTTP redirects
are disabled for MCP operations.

## Execution and operations

One call runs one branch. MCP uses caller cancellation and an overall deadline,
including admission and credential acquisition. Default timeout is 30 seconds,
configurable up to 120 seconds. Current Streamable HTTP disconnects cancel the
request. Older stateless protocol calls are deadline-bounded; cross-request
cancellation and resumable sessions are not provided.

Each MCP endpoint defaults to 32 active calls, four per tool, and 60 calls per minute per
principal/tool. Set trigger `max_inflight`, tool `max_inflight`, and
`requests_per_minute` to tune. Admission does not create a durable queue. Limits
are process-local; use deployment ingress limits for aggregate multi-replica
budgets. All endpoints also share the existing process admission limit. No new
database, broker or identity server is required.

HTTP operations default to one attempt. Read tools may explicitly enable
`allow_retries` and configure HTTP retries within their deadline. Mutating tools
cannot automatically retry, including managed-credential refresh/resend. An
HTTP mutation uses a fresh connection to prevent Go's transport from replaying
an idempotency-marked request on a reused connection. An
uncertain failure is not proof that the upstream operation did nothing. Where
supported, map a declared idempotency key to the upstream's idempotency header;
the upstream owns durable deduplication. JSON-RPC request IDs do not deduplicate
operations. Cancellation cannot roll back an accepted mutation.

Native publish branches currently support confirmed SQS, SNS and RabbitMQ
publication with publisher/SDK retries suppressed. The subsequent result may
report acceptance, never downstream completion. Other destination types are
rejected for MCP until their completion and retry behavior has an explicit safe
contract. Use HTTP request nodes for REST operations. Existing webhook delivery
and replay semantics are preserved. MCP flows do not capture/replay payloads;
set `capture_failures: false`. Native durable MCP Tasks are not advertised.

Metadata-only logs, node metrics and request IDs correlate calls. Results carry
`_meta["io.hookway/request_id"]`, also supplied in the HTTP `X-Request-ID` header.
Prometheus
`hookway_mcp_calls_total` and `hookway_mcp_duration_seconds` use bounded flow/tool
and outcome labels. Payloads and upstream response/error bodies are excluded.
An upstream API outage fails its tool without making unrelated tools unavailable.
The gateway does not cache tool results. Adapters returning cached data must
include observation time, age and explicit freshness; stale data must stay labeled.

## Test and connect

The trigger inspector distinguishes local validation from **Test & connect**.
The latter calls a deployed endpoint using the official TypeScript SDK. Supply a
named API key or an OAuth access token, discover permitted tools, enter arguments
and run the selected tool. Mutating calls require explicit acknowledgement in the
tester. Credentials remain in memory and clear on disconnect/reload/closing the
inspector; they are not included in drafts or exports. Browser testing requires
network access and an allowed browser origin.

Use a Streamable HTTP client that supports the configured authentication method.
Configure an API key as a bearer header where supported, or use the client's
OAuth login flow with your identity provider. API-key support varies by client;
check compatibility before deployment. The SDK supports current `2026-07-28` and tested older Streamable
HTTP revisions. Legacy HTTP+SSE, prompts, resources and sampling are not advertised.
Optional [MCP event subscriptions](mcp-events.md) use signed webhook delivery. Each HTTP request must contain one JSON-RPC message;
JSON-RPC batches are not accepted, including on older protocol revisions.
Real client/provider compatibility must be checked against
the deployed versions; automated SDK tests do not establish those connections.

Start with the [authenticated gateway template](../../examples/templates/mcp-gateway.yaml):
`get_service_status` calls a configured HTTP API with a separate outbound
credential, while `format_greeting` uses a native transform. Both require an
explicit caller grant and a schema-valid result. See the
[OAuth configuration](../../examples/mcp-oauth.yaml) and
[multiple endpoints](../../examples/mcp-endpoints.yaml) examples for other setups.

Sources: [MCP tools](https://modelcontextprotocol.io/specification/2026-07-28/server/tools),
[MCP authorization](https://modelcontextprotocol.io/specification/2026-07-28/basic/authorization),
[Streamable HTTP](https://modelcontextprotocol.io/specification/2026-07-28/basic/transports/streamable-http),
[Zitadel DCR](https://zitadel.com/docs/guides/integrate/dynamic-client-registration).

## Import API tools from OpenAPI

The [OpenAPI importer](openapi-import.md) generates native tool workflows with
explicit grants. `request.optional_query` and `request.optional_headers` omit
missing arguments while preserving supplied scalar values. Required and optional
mappings must not name the same destination (header names are case-insensitive).
`request.body_source` selects a complete JSON value instead of field mappings;
`body_optional: true` permits absence without sending a body. It requires
`body_source`, which excludes the existing `request.body` field-map form.

`http_request.body: none` explicitly suppresses request bytes. On `mcp_result`,
`wrap` places the selected value under a named object field; `empty_statuses`
permits an empty response only for listed 2xx statuses with `source: upstream`.
An accepted empty response becomes JSON null before wrapping. Without these
options, existing mapping and result behavior is unchanged.
