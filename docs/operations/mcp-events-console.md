# MCP event operations

The existing replay console includes **MCP events → Subscriptions / Deliveries**.
The browser uses its replay operator session. Replay calls core through its
configured private control connection; it never opens the event database or
receives the event encryption key. Core scopes every operation to its configured
namespace and rechecks eligibility when accepting a mutation.

## Permissions and availability

Console sign-in still requires `read`. Grant event permissions explicitly:

| Scope | Permission |
| --- | --- |
| `events_read` | Inspect event runtime status, subscriptions and delivery metadata |
| `events_payload` | Explicitly load retained occurrence JSON |
| `events_retry` | Retry one eligible failed delivery |
| `events_manage` | Revoke and restore subscription identities |

Existing `payload` and `retry` scopes do not grant event access. Native MCP
credentials cannot authenticate operator requests. Operator keys are namespace
administrators: they can inspect other subscriber identities in that namespace
when granted event scopes. Native MCP ownership rules remain unchanged.

A disabled, unsupported or unreachable event runtime is shown separately from
normal replay availability. Upgrade core before replay/console to enable the new
operations; older replay versions simply omit the event capability.

## Investigation and recovery

Filter by endpoint, event, owner, subscription, state, or time. Subscription time
filters apply to expiry; delivery time filters apply to durable acceptance.
Pagination defaults to 50 records and permits at most 200. Reuse the same filters
with a cursor. Delivery IDs and versions are strings; filter JSON is displayed
without converting large numbers through JavaScript floating-point values.

Delivery detail shows the latest 200 attempt and action entries. A missing
historical attempt is reported as unrecorded. An interrupted attempt has an
unknown outcome: lack of a receipt does not prove the callback did not run.
`Receipt accepted` means the callback returned `2xx`, not that an agent finished
its action. Callback paths, query strings, signing material and authentication
snapshots are absent from operator metadata. Payload loading is explicit.

**Retry delivery** starts a new bounded ten-attempt cycle for the original
subscriber with the original event ID and exact body. Lifetime attempt counts
and earlier cycles remain visible. Acknowledge duplicate risk. The delivery must
be failed, retained, authorized, unleased, and in the original active monitoring
interval. Expiry/unsubscribe followed by re-subscription does not make old work
eligible. `410` and `413` failures cannot be retried.

**Revoke subscription** clears stored signing keys, cancels pending work, and
persistently blocks subscribe/refresh for that exact identity. Its marker survives
cleanup. Already-started callbacks may complete. Changing the subscription's
identity is not a principal-wide ban; remove configured grants for broader denial.

**Restore subscription** removes the block but leaves monitoring inactive. The
agent must subscribe again and pass authentication and callback verification.
Cancelled deliveries never reactivate. Revocation and restoration require a reason.

Every action requires the displayed version and an `Idempotency-Key`. Retain the
same key and body after losing a response; a new key could request a second
action. Conflicting versions reject stale decisions. Successful receipts record
the operator identity forwarded by replay, action, time and resulting version.
Attempt/action retention follows the parent's 24-hour terminal-delivery retention;
revocation markers remain until restored.

## Migration and verification

Run `hookway event-store migrate --config <core-config>` explicitly before serving
the upgraded core. Migration 2 adds lifecycle generations, retry cycles, attempt
records and action receipts. It changes only the event schema and uses the existing
migration lock. Pause/drain old core replicas before migration: old writers do not
understand the new lifecycle and history fields. Upgrade all core replicas before
resuming traffic; serving requires the exact schema version. Do not run old core
binaries against the upgraded namespace.

See the [combined demo](../../examples/mcp-events/demo/README.md) and
[durable subscription guide](../guides/mcp-events.md). Run
`make event-integration-test` and `make console-e2e` for store/race and browser
coverage. Live hosted-agent verification remains separate from protocol tests.
