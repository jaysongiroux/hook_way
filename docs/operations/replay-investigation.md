# Investigating uncertain delivery

The replay API separates delivery evidence from operator decisions. An `unknown`
record means Hookway cannot determine whether the retry was accepted. Resolving
it records your independent confirmation; it does not call core, deliver a
payload, alter the original run result, or verify a receipt for you.

## Choose the next action

| Evidence | Record / latest run | Next step |
|---|---|---|
| A confirmed attempt failure | `open` / `failed` | Inspect the error and destination before requesting another retry. |
| Current configuration prevents execution | `open` / `blocked` | Read the block reason, repair configuration and reassess the captured target. Nothing was sent by that blocked run. |
| Response lost, timeout after possible execution, or expired lease | `unknown` / `unknown` | Investigate the destination before sending again. |
| Successful retry execution | `resolved`, `resolution_source: retry_succeeded` | Review child failures if present; they remain separate work. |
| You independently confirmed delivery | `resolved`, `resolution_source: operator_confirmed` | Read the resolution note and original unknown run; this is operator confirmation. |
| Operator discards recovery work | `dismissed_at` set; previous `delivery_status` retained | Artifacts were deleted. This does not confirm delivery. |

1. Use `GET /v1/me` to confirm the key identity and scopes. Give individual
   responders distinct named keys; a shared key identifies that credential,
   not the human who happened to use it. `read` inspects metadata; `payload`
   downloads bytes; `retry` permits retry, dismissal and operator resolution.
2. Fetch `GET /v1/records/{id}`. Match the flow, destination, rendered target,
   event/request/trace IDs, timestamps, parent/child IDs and current configuration
   hashes. Review every run's result and the chronological audit history.
3. Query the destination's receipts, logs, object IDs or consumer-side deduplication
   records. Correlate the stable sender event identity as well as Hookway's IDs.
   A missing search result alone does not prove nondelivery. An expired worker
   lease does not cancel a core execution already in progress.
4. If you confirmed acceptance, resolve the unknown record with a note describing
   the evidence and an optional incident/receipt reference. If delivery remains
   uncertain, keep it unknown, or request retry with explicit duplicate-risk
   acknowledgement after assessing downstream idempotency. Dismiss only when
   intentionally abandoning recovery and deleting artifacts.
5. Review the result and audit history. A later worker response is retained as a
   `late_result` event with its returned result, without replacing the original
   unknown run or operator resolution.

The same workflow is available in the [operations console](replay-console.md).
Filter by **Unknown outcome**, inspect the latest run and audit history, then
choose **Confirm outcome** only when independent evidence confirms receipt.
Unknown retries require explicit duplicate-risk acknowledgement. Dismissal deletes
artifacts and does not confirm delivery.

## Identity and capabilities

```sh
curl -sS -H "Authorization: Bearer $REPLAY_OPERATOR_TOKEN" \
  "$REPLAY_URL/v1/me"
```

Example response for a named operator key:

```json
{"key_name":"incident-ops","scopes":["read","payload","retry"],"contract_version":1,"capabilities":["record_api","inspection_records","failure_inspection","payload_download","destination_retry","batch_retry","unknown_resolution","action_input_validation","action_eligibility"]}
```

Capabilities describe implemented API features; they do not grant scopes or
assert that any particular record can be acted on. The server always checks the
current state and permissions when an action arrives.

## Record independent confirmation

```sh
curl -sS -X POST -H "Authorization: Bearer $REPLAY_OPERATOR_TOKEN" \
  -H 'Content-Type: application/json' -H 'Idempotency-Key: incident-42-resolution' \
  -d '{"note":"Destination audit confirms event evt-42 was accepted; matched receipt req-9.","reference":"INC-42"}' \
  "$REPLAY_URL/v1/records/$FAILURE_ID/resolve"
```

Example receipt:

```json
{
  "delivery_status":"resolved",
  "resolution_source":"operator_confirmed",
  "resolution":{
    "record_id":"01926d3c-8d6f-7b3a-9c1e-2f4a5b6c7d8e",
    "actor":"incident-ops",
    "idempotency_key":"incident-42-resolution",
    "note":"Destination audit confirms event evt-42 was accepted; matched receipt req-9.",
    "reference":"INC-42",
    "resolved_at":"2026-10-04T20:00:00Z"
  }
}
```

A note is required, nonblank valid UTF-8, at most 4096 bytes. It may contain
line breaks and tabs; other control characters are rejected. An optional
reference is printable UTF-8, at most 1024 bytes; omit it when unavailable.
These are byte limits, so multibyte characters use more than one byte. Evidence
is preserved exactly, including whitespace. References are opaque text, never
fetched or executed. Do not include tokens, passwords or unnecessary personal
information in a note: it is stored in the resolution and audit history.

Only an unexpired `unknown` record with no queued/running retry may be resolved.
A running lease whose deadline passed remains active until the lease-expiry
transaction fences it and sets the run unknown. This avoids racing an active
worker. Resolution does not change snapshot/payload bytes, unknown run results,
lineage, expiry or capacity usage; those remain until ordinary retention GC.
Even metadata-only unknown records may be resolved with independent evidence.
Resolved records cannot be dismissed or retried with a new request.

## Idempotency and competing actions

`Idempotency-Key` must appear once and contain 1–255 printable ASCII characters
without spaces. Keep it with the exact body after a timeout or lost HTTP reply.
An identical repeat by the same named key returns the original 200 receipt and
creates no additional audit event. Reusing that key for changed evidence or
another failure returns 422 `idempotency_key_mismatch`. A different request/key
for an already operator-resolved record returns 409 `resolution_conflict`.

Resolution keys are scoped to the named actor and resolution operation; retry
keys have their existing separate scope. Reusing the same text across retry and
resolution does not equate the two actions. Retry, dismissal and resolution
serialize on the failure row: one competing transition succeeds, the others
receive their applicable state/conflict error. No action sends again implicitly.

| Error | Status | Meaning |
|---|---:|---|
| `unauthorized` / `forbidden` | 401 / 403 | Missing credential or required scope. |
| `invalid_resolution` | 400 | Invalid note or reference. |
| `idempotency_key_required` / `invalid_request` | 400 | Missing/invalid/repeated key header or malformed/unknown JSON fields. |
| `request_too_large` | 413 | Request exceeds 64 KiB. |
| `not_found` | 404 | Malformed ID, missing record, or record already removed by retention. |
| `run_active` | 409 | A queued/running retry or retrying record prevents resolution. |
| `not_resolvable` | 422 | Record is not unknown. |
| `resolution_conflict` | 409 | Different resolution already recorded. |
| `idempotency_key_mismatch` | 422 | Actor/key already used with different input. |
| `record_expired` | 410 | A new resolution cannot be created after expiry, even before GC. |

Previously successful receipts remain repeatable while retained, including
between logical expiry and GC. After GC, records, resolutions, runs and history
are removed together and the receipt returns 404. Idempotency is not indefinite.
A repeated previously accepted retry returns its original run ID; it never
queues a second run even after resolution.

### A submit reply disappeared

First read the record and its runs/history; the action may have committed even
though your client saw a timeout. Repeat a retry or resolution request using the
same named actor, exact body and original `Idempotency-Key`. Another replay
replica can return the retained original receipt. Do not substitute a new key,
change the duplicate-risk acknowledgement, or edit the resolution note while
trying to recover that reply. Changed input is a new decision, not a transport
retry, and a reused key with changed input is rejected.

Dismissal has no equivalent idempotency receipt. If its reply is lost, re-read
the record before doing anything else. A conflict from a competing action is
also a reason to refresh state, not to automatically submit another action.
An expired worker lease leaves the original execution uncertain; neither a new
worker nor the operator should infer that the destination received nothing.

Keep the accepted run ID or resolution receipt with the incident evidence, and
correlate it with the stable provider identity and independent destination
receipts. Idempotency here fences retained replay actions; it is not an
exactly-once business guarantee or a permanent deduplication archive.

## Shared validation and migration

WASM advertises `replay_resolution_input` and uses the Go-owned action validator.
A read-scoped fallback is available without executing an action:

```sh
curl -sS -X POST -H "Authorization: Bearer $REPLAY_OPERATOR_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"action":"resolve","idempotency_key":"incident-42-resolution","note":"Confirmed downstream receipt","reference":"INC-42"}' \
  "$REPLAY_URL/v1/actions/validate"
```

HTTP 200 with `{"issues":[]}` means input syntax is valid, not that evidence is
true or resolution is currently permitted. Auth, expiry, active runs and lifecycle
transitions are checked only by the action endpoint. Retry inputs use `action:
retry` (or omit action); batch validation accepts up to 100 unique `record_ids`.
There is no batch resolution.

Run `hookway-replay migrate --config replay.yaml` before serving. The generic-
record model uses one initial database migration (version 1), including operator
resolutions, and API/WASM contract version 1. These numbers describe different
contracts. Use a fresh database for this baseline; existing failure-shaped
schemas are not upgraded by it. Preserve existing databases and backups rather
than deleting volumes as an implicit setup step. Start matching core, replay API,
console and WASM assets together. Legacy failure routes are unsupported; the
current API uses `/v1/records`. Serving never migrates. See
[Replay service](replay-service.md#running-it).

The API returns JSON with `Cache-Control: no-store`. The investigation features
are available through the API and the [operations console](replay-console.md). These operations provide no exactly-once delivery guarantee.
