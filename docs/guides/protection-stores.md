# Rate limits and duplicate protection

Use a protection store to enforce rate limits and reserve duplicate identities.
Choose the backend for the scope you need:

| Backend | Scope | Main consideration |
| --- | --- | --- |
| Memory | One core process | Restart loses its quota and duplicate history |
| Redis | Shared across core replicas | Configure no eviction, persistence, and failover |
| PostgreSQL | Shared across core replicas | Run migrations and size for policy-lock contention |

These stores hold admission state. They are separate from replay history,
routing mappings, lookups, and delivery destinations. The builder writes YAML;
the native server connects to stores and makes live admission decisions.

Each `rate_limit` and `replay_guard` node must name a store. There is no implicit memory fallback. Existing configurations must add the declaration and node references before upgrading.

```yaml
state_stores:
  cluster:
    namespace: production-us
    timeout: 250ms
    startup_timeout: 5s
    cleanup:
      interval: 30s
      batch_size: 100
      budget: 100ms
    postgres:
      url: ${PROTECTION_DATABASE_URL}
      max_conns: 10
```

Replace `postgres` with `memory: {}` for one process, or with:

```yaml
    redis:
      url: ${PROTECTION_REDIS_URL}
      pool_size: 10
```

Use exactly one backend. All replicas sharing protection need the same namespace and policy. Changing namespace, backend or node identity starts separate state; it is an operational change, not a quota update. Set database credentials through environment references or your deployment's secret mechanism. Pool sizes apply per process, so budget connections across all replicas.

Redis supports single server, Sentinel and Cluster (`mode`, `addrs`, `master_name`); Cluster uses database zero. Policy keys share a hash slot. Disable eviction for protection data and provision sufficient memory. Startup requires read access to `CONFIG GET maxmemory-policy` and `INFO replication`, plus the script/read/write capabilities used by protection. Services or ACLs that hide these checks are rejected until their capabilities can be verified. Redis persistence and failover settings determine whether acknowledged reservations can survive a restart or primary loss. Shared coordination is not an exactly-once delivery guarantee. Transport errors are not blindly retried, since the first operation may already have changed state.

PostgreSQL uses transactional updates with a policy-row lock to coordinate quota, reservations and capacity. A hot shared policy serializes its decisions, including different identities; consider this when choosing policy boundaries and sizing the database. Redis and PostgreSQL provide the same admission rules, with different latency and contention characteristics. Benchmark your actual workload.

## Choose an algorithm and identity

A token bucket admits bursts while continuously refilling:

```yaml
- id: quota
  type: rate_limit
  store: cluster
  strategy: token_bucket
  rpm: 600
  burst: 100
  key_by: source_ip
  max_keys: 10000
```

`rpm` is the refill rate; omitted/zero `burst` uses `rpm`. One token is consumed when the node admits the request, even if a later node fails. There are no automatic refunds. A full bucket can admit `burst` requests immediately. Memory applies this policy independently in each process; a shared backend coordinates the same policy across processes.

A sliding-window counter provides an approximate rolling quota:

```yaml
- id: quota
  type: rate_limit
  store: cluster
  strategy: sliding_window_counter
  limit: 100
  window: 1m
  key_by: header.X-Customer-ID
```

This combines the current window's count with a weighted previous count. It is not a strict maximum over every rolling interval. Do not configure `rpm` or `burst` for this strategy, or `limit`/`window` for a token bucket. A denied quota returns 429 with a conservative `Retry-After`.

The default stage operates on `RequestHead`, before reading the body or authenticating. `source_ip` uses Hookway's configured trusted-proxy rules. Header identities must be set by a trusted upstream: an attacker-controlled header can create fresh quota identities. Missing header identity returns 400.

For authenticated quotas, connect a `stage: verified` rate limit after an `auth` node and before parsing the envelope. Both ports then use `Verified`:

```yaml
- id: auth
  type: auth
  scheme: jwt_hs256
  credential_id: partner-api
  secret_ref: {provider: env, name: JWT_SECRET}
  source: {kind: header, key: Authorization}
  remove_bearer: true
  expected_issuer: https://issuer.example
  expected_audience: hookway
  body_hash_claim: body_sha256
  tenant_claim: tenant_id
- id: tenant_quota
  type: rate_limit
  store: cluster
  stage: verified
  key_by: jwt_tenant
  strategy: token_bucket
  rpm: 600
  burst: 100
```

`credential_id` quotas require a stable ID on the preceding auth node. `jwt_subject` and `jwt_tenant` additionally require configured expected issuer/audience; tenant quotas require `tenant_claim`. Public `no_auth` cannot produce these identities. Missing required verified identity rejects the request. Identity keys are hashed in stores and never used as metric labels.

Without `policy_id`, a policy is isolated by deployment namespace, flow and node. Set the same explicit `policy_id` on endpoints only when they intentionally share the same policy, stage, identity extraction and authentication trust. Conflicting definitions fail validation/startup. Do not change IDs to bypass exhausted quotas.

`max_keys` defaults to 10,000; duplicate protection's `max_entries` defaults to 1,024. Both accept up to 1,000,000 live identities. Capacity exhaustion returns 503 for a new identity; live protection is never evicted to make room. Capacity is separate from a customer's quota. Expired rows may occupy physical space until a cleanup batch removes them; cleanup lag can temporarily delay capacity reclamation, but it cannot extend an existing key's protection.

## Duplicate reservations and expiry

```yaml
- id: duplicates
  type: replay_guard
  store: cluster
  ttl: 5m
  key: '{header.X-Event-ID}'
  max_entries: 100000
```

A guard atomically reserves an identity for the configured window and returns 409 for duplicates. Omitted `key` hashes the original body. Prefer a stable provider event ID when identical bodies represent different events. TTL starts at admission; duplicate requests do not extend it. Owner tokens prevent an old request's compensation from deleting a newer reservation.

The guard releases a reservation only when processing is known to have produced no delivery side effect. It remains after successful, partial or uncertain delivery, including earlier outbound HTTP calls. This prevents an inbound retry from repeating already attempted effects. Destination-only replay is the intended recovery path when a retained record exists. The current capture contract retains eligible failures only when the final inbound response is 2xx; a failed 503 attempt can therefore retain a guard without creating a replay record. Guard retention does not depend on enabling replay; if capture fails or replay is disabled, recovery may require downstream idempotency, investigation or waiting for expiry. Neither expiry nor replay guarantees exactly-once effects.

PostgreSQL does not need a native TTL feature: every decision compares `expires_at` with database time. An expired reservation immediately stops blocking that identity even if its row remains on disk. An indexed, bounded background job removes expired rows. Autovacuum reclaims database space separately and must remain properly configured. The optional GC command can run extra bounded cleanup passes. Redis and memory also use logical expiry; cleanup does not define correctness.

## Startup, readiness and outages

Hookway verifies every configured store, even an unused one, before serving. It exits on connection, permissions, schema or policy conflicts. PostgreSQL migrations are explicit and never run during `serve`. Use a separate migration role/job when runtime credentials should not have DDL privileges.

After startup, a store failure leaves Hookway running and makes `/readyz` return 503. `/healthz` remains a liveness probe. Protected requests fail closed with 503 until their store recovers. Independent flows without a protection node can still execute if addressed directly, but global readiness withdraws the instance from normal service. Health probes retry with bounded exponential backoff and jitter. Probes verify existing policy state; they do not recreate lost policies. Store deadlines and bounded cleanup work are tied to shutdown cancellation.

Protection infrastructure failures cannot be recovered through a node's `on_error` branch. Ordinary quota or duplicate denials may have a custom response branch, but the compiler rejects recovery paths that perform outbound effects or child execution. A custom response must not bypass admission.

## Operator commands

```sh
hookway state-store status --config core.yaml --store cluster
hookway state-store migrate --config core.yaml --store cluster
hookway state-store gc --config core.yaml --store cluster
```

`migrate` applies the embedded versioned PostgreSQL schema under an advisory migration lock. Run it before starting the new server version. `status` reports the schema and stored policy fingerprints without creating policies. `gc` removes expired state only. These commands do not open destinations or start listeners. A separate CLI process cannot inspect or change a running process's memory store.

To change an existing shared policy, first prepare the desired config, inspect the current fingerprint, and run:

```sh
hookway state-store policy-update --config desired-core.yaml --store cluster \
  --policy customer-quota --expected-fingerprint CURRENT_HASH
```

This is an explicit compare-and-swap operation. Compatible numeric changes retain existing state: old-rate refill is settled at the update timestamp and available tokens are clamped to the new burst. Sliding counters retain their counts. Lowering capacity does not evict existing live identities. An update below retained occupancy is rejected; allow expiry and run cleanup before lowering that bound. Existing duplicate reservations retain their original expiry; a changed TTL applies to new reservations. Changes to algorithm, window, identity rules or auth trust require a new policy and a planned state transition.

Updates enter fail-closed maintenance and process entries in bounded batches. While updating, neither old nor new definitions can admit traffic. After completion, old server configurations remain unready and reject traffic; deploy the matching config. If interrupted, inspect status and resume the same explicit target with the same expected fingerprint. Do not blindly retry an uncertain admission or reservation write. Schedule changes for a maintenance window and account for temporary unavailability.

## Observability and verification

`hookway_protection_operations_total` and `hookway_protection_operation_duration_seconds` report store, configured policy, kind, strategy, operation and bounded outcome. Outcomes include allowed, denied, capacity, policy_conflict and unavailable. `hookway_protection_store_ready` tracks store health. Cleanup passes, deleted entries and cleanup latency have separate metrics. Pool connections and cumulative connection-acquisition wait expose contention per configured store; Redis Cluster statistics aggregate its node pools. No event IDs, customer IDs, IPs, JWT subjects, secrets or DSNs appear in these labels.

The conformance tests exercise the same policy semantics against memory, Redis and PostgreSQL, including expiry, ownership and policy updates. Real backend integration tests and independent-process tests are required for shared-store evidence. Local benchmark figures are workload-specific and are not a production capacity claim. Rehearse failover and state recovery against your actual deployment.

The complete authoring example is [examples/protection/shared.yaml](../../examples/protection/shared.yaml).
For Docker or AWS ECS, run
`hookway state-store migrate --config core.yaml --store cluster` in a one-shot core container or task before starting or upgrading core.
Use the matching store configuration and database connection, and require successful
completion before updating the service. Migration credentials can differ from
core's runtime credentials.

Local performance evidence and its limits are recorded in [Protection benchmarks](../evidence/protection-benchmarks.md).

## Runtime grants and state-loss recovery

Use a migration owner separately from the runtime role. After migration, the
runtime role needs database `CONNECT`, schema `USAGE`, read access to the version
table, and DML on the two live tables. For the dedicated protection schema:

```sql
GRANT USAGE ON SCHEMA hookway_protection TO protection_runtime;
GRANT SELECT ON hookway_protection.goose_db_version TO protection_runtime;
GRANT SELECT, INSERT, UPDATE, DELETE
  ON hookway_protection.policies, hookway_protection.entries TO protection_runtime;
```

Provision the login/secret separately. Do not grant schema ownership, `CREATE`,
or writes to the migration-version table. The integration test verifies startup,
reservation writes, and denial of DDL/version rewrites with this restricted role.
Run migrations as the owner; re-check required grants whenever tables change.

Route all PostgreSQL protection operations to the writable primary. Read replicas
cannot provide authoritative admission decisions. Configure durability, HA and
failover using your deployment's requirements; local transaction tests do not
prove managed failover or replica promotion preserves acknowledged writes.

Distinguish connection outages from state loss. Outage recovery uses existing
policies and entries; probes do not recreate missing policy state. Redis key loss
fails closed until explicit recovery, but missing individual entries with an
otherwise intact policy can allow duplicates. Sentinel/Cluster asynchronous
replication can lose acknowledged state; no-eviction alone does not prevent this.
Stop protected traffic during uncertain recovery, restore/reconcile the expected
state, inspect policy fingerprints, then restart matching configurations. Startup
can create a missing policy as fresh state, so restarting blindly after data loss
can clear the duplicate/quota history you intended to preserve.

Back up protection along with replay when their recovery windows are coupled.
Restoring an old backup rewinds quotas and reservations; reconcile downstream
business receipts and outstanding retries before resuming. The native acceptance
suite restores both schemas into a fresh local PostgreSQL database and verifies
quota/reservation retention plus destination-only replay. Off-host disaster
recovery and primary failover remain target-deployment checks.
