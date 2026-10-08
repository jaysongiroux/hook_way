# Named lookup providers

Use a lookup to turn an incoming ID into a routing value, such as a tenant's
environment. Define the provider under `lookups`, then reference its name from
a `lookup` node.

| Provider | Choose it when |
| --- | --- |
| HTTP | A directory API returns the value |
| PostgreSQL | A SQL query can resolve the ID |
| DynamoDB | A table/index holds the mapping |

The server opens providers before listening, shares their bounded cache across
flows in that process, and closes them on shutdown. Here is an HTTP example:

```yaml
lookups:
  tenant_environment:
    timeout: 2s
    cache:
      ttl: 5m
      negative_ttl: 0s
      max_entries: 10000
    http:
      url: https://directory.example.com/tenants/{key}
      value_path: environment
      secret_headers:
        Authorization:
          prefix: 'Bearer '
          secret_ref: {provider: env, name: DIRECTORY_TOKEN}
```

Add a node that references that provider:

```yaml
- id: find_environment
  type: lookup
  provider: tenant_environment
  key: '{body.tenant_id}'
  name: environment
```

Later nodes can use `{environment}`. `fallback` on the node supplies a value for a clean miss; backend errors and timeouts return HTTP 503 by default so the sender can retry. A missing key without a fallback returns HTTP 400. Provider error details are not returned or logged by the node. An explicit `lookup_error` response or `on_error` branch can override these defaults; preserve a non-success status when the sender should retry.

Provider timeout defaults to 2 seconds and cannot exceed 30 seconds. Construction is bounded by the greater of 5 seconds and the configured timeout. Successful values expire after `cache.ttl` (default 5 minutes, maximum 24 hours); `cache.negative_ttl` optionally caches misses (default zero, disabled). Errors are never cached. `max_entries` defaults to 10,000 and must be 1–1,000,000. Concurrent lookups of the same key share one request. Cache state is process-local and is lost on restart. Lower the TTL when routing must reflect changes quickly.

Cache and database-table settings belong to the named provider. Legacy node-level `table`, `cache`, and `negative_ttl` are embedding-only and produce an actionable standalone validation error rather than being ignored.

## HTTP

HTTP supports `GET` (default) and `POST`. The URL must contain exactly one `{key}` in the path or query. The key is percent-encoded; it cannot select the host or inject query parameters. POST also sends `{"key":"..."}` as JSON. `value_path` selects a scalar from the JSON response. HTTP 404 is a miss; other non-2xx responses, malformed JSON, or a missing path are errors. Responses are capped at 1 MiB.

Public HTTPS targets are the default. DNS is checked at connection time and connections use the vetted address. Private addresses and HTTP require an explicit `allow_private_targets: true` on the provider. This opt-in is appropriate for a deliberately internal directory service. Redirects are always refused, including redirects to the same host; credentials never follow a redirect. Configure the final URL.

Use `headers` for non-secret static headers and `secret_headers` for credentials. Secret references support the existing environment, file, and AWS Secrets Manager providers. They are resolved before startup and refresh according to the reference settings; each request uses the current secret. Resolved headers are never put into the graph envelope or captured payload. Cache hits do not contact the provider and therefore do not acquire credentials. Do not put secrets in URL queries.

## PostgreSQL

```yaml
lookups:
  tenant_environment:
    postgres:
      url: ${LOOKUP_DATABASE_URL}
      query: SELECT environment FROM tenants WHERE tenant_id = {key}
```

The query must contain exactly one unquoted `{key}` placeholder. Hookway binds it as `$1`; it never interpolates the request value into SQL. The first scalar column in the first row is the result; no row or an empty value is a miss. Use a read-only database role and a `LIMIT 1` or unique key. The connection URL uses the existing strict environment expansion; keep credentials out of exported YAML. Changing the database URL requires a server restart. The server verifies the database connection before listening.

## DynamoDB

```yaml
lookups:
  tenant_environment:
    dynamodb:
      table: tenant-routes
      region: us-east-1
      key_attribute: tenant_id
      value_attribute: environment
```

Credentials come from the standard AWS credential chain, including workload roles; no access keys are stored in the lookup definition. Optional fields are `index`, `filter_attribute` with `filter_value`, `consistent_read`, `page_limit` (default 25, maximum 1000), and `endpoint` for a deliberately configured emulator or endpoint. Queries walk pages newest-first, applying the equality filter until a matching scalar value is found or the timeout expires. Use AWS-supported consistency options for the chosen table/index. Startup loads AWS configuration; it does not prove table permissions or reachability.

## Replay and verification

A failed lookup is captured as metadata only when replay capture applies. Its provider configuration and current external data are not frozen in the replay snapshot, so it cannot be safely resumed automatically. A later delivery failure captures the already resolved envelope variables; ordinary downstream replay does not repeat a lookup that already succeeded. These cache and replay rules do not supply exactly-once delivery.

Test provider permissions, routing changes and failure responses against your
deployment. Contributor test commands are in
[Contributing](../../CONTRIBUTING.md#make-and-check-a-change).
