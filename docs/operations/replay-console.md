# Replay operations console

Use the console to inspect captured failures and request recovery from a running
replay service. It shows payloads, delivery history, and evidence for uncertain
outcomes. Inspect nodes provide separate view-only observations in **Inspections**. The public builder writes configuration separately; it does not manage
core. Accepting a retry only queues work, so check its result before concluding
that delivery succeeded.

## Build and deploy

From the repository root:

```sh
docker build -f web/Dockerfile -t hookway-console:local .
docker run --rm -p 8082:8080 \
  -e REPLAY_API_URL=https://replay-api.example.com \
  hookway-console:local
```

Replace the example API URL with your replay service's browser-reachable URL.
The image serves the console at `/` and the public builder at `/builder`, with
matching Go WASM assets. `/healthz` checks the static host; `/build-info.json`
identifies its build. Core and replay run in separate Go-only images.

The Docker image reads `REPLAY_API_URL` at startup, so one image can be used in
several environments. The value is public configuration, not a secret. The browser
calls that API directly; the frontend does not proxy requests and needs no API key
in its environment. Container DNS names usually do not work in an operator's browser.

For another static host, build with `make console-ui`, serve `web/dist-console`,
map `/` and `/builder` to `console.html`, and provide `/runtime-config.json` with:

```json
{"replayApiUrl":"https://replay-api.example.com"}
```

Apply the [browser security headers](#browser-and-server-boundaries) on that host.
Use `make ui` and `hookway editor` only for the optional Go-hosted local editor;
`make build` builds native binaries without Node.

Configure replay's exact frontend origin separately:

```yaml
server:
  addr: ":8080"
  cors:
    allowed_origins: ["https://console.example.com"]
```

Origins include scheme, host, and optional port, with no path or trailing slash.
The [local Compose example](../../examples/docker-compose.yaml) uses
`http://localhost:8082` for the frontend and `http://localhost:8081` for the API.
There is no `console.enabled` setting. CORS permits browser access; each API
operation still requires a scoped key.

Run replay migrations before serving. For production, use HTTPS and your
operator network/access policy, preserve Authorization headers and no-store
responses, and keep core control/metrics listeners private. The Docker host's
CSP restricts connections to the configured API origin.

The [replicated Compose example](../../examples/replay/docker-compose.yaml) keeps
frontend and API services separate. They can scale independently on your chosen
platform; the browser must reach the API endpoint as well as the frontend.

## Sign in and scopes

Sign in with a named token from replay `keys`. Use a separate credential per
operator if audit history needs individual attribution.

| Scope | Allows |
| --- | --- |
| `read` | Browse records and history |
| `payload` | Preview and download captured bytes |
| `retry` | Retry, confirm an unknown outcome or dismiss records |

Capture and metrics scopes are not needed for the console. These are bearer
keys, not SSO accounts. Keys load at startup; restart replay to apply revocation.

The token and displayed records stay in browser memory. Reload, leaving the app
or signing out clears the session. A 401 signs you out; a 403 reports a missing
permission. Navigation to `/builder` retains the session but pauses polling.

The console checks API compatibility before querying records. If WASM input
validation is unavailable, you can select read-only API validation; it does not
perform an action or grant permission. The server checks current state and scopes
when you submit.

After a lost recovery-action reply, keep the displayed submission identity
before reload, sign-out or handover. See [uncertain responses](#uncertain-submission-responses).

## Inspect and recover

1. The inbox starts with **Open failures**. State tabs switch immediately. Enter
   an exact flow and choose **Apply filters**; **Filters** reveals exact node/
   destination, captured retry capability and local-time date bounds. Unapplied
   changes are marked. The lower time bound is inclusive, upper exclusive.
   Cursor pages contain at most 50 records; selection applies only to explicit
   IDs on the current page and clears on filter/page changes.
2. Inspect a failure for its captured destination, retry scope, error, expiry,
   attempts and latest retry result in **Overview**. **Activity** contains retry
   and audit history, with raw identifiers and diagnostics available on demand.
   **Related** navigates captured parent/root/child relationships. Resume retries start at the
   captured node; publish-only failures retry their captured destination.
3. For an ordinary failure, correct the destination problem first. For a
   **blocked** run, inspect its result for changed configuration, credential or
   control requirements. For **unknown**, inspect destination receipts/audit
   evidence: a lost response or expired worker lease does not prove nondelivery.
4. **Retry delivery** or **Retry selected** confirms exactly the displayed IDs.
   The API accepts up to 100 unique IDs per batch; the current console page caps
   selection at 50. Batch results show accepted run IDs and individual rejection
   reasons. A stale record can reject independently of other selected records.
   Unknown retries require duplicate-risk acknowledgement. Queued/running work
   cannot be resolved or dismissed.
5. **Confirm outcome** requires an evidence note (4096 UTF-8 bytes maximum) and
   accepts an optional receipt/incident reference (1024 bytes). It displays
   **Resolved — operator confirmed**, preserving unknown run results, history,
   artifacts and original expiry. Reference text isn't an automatically opened
   link. Hookway records an operator conclusion, not an independently verified
   destination receipt.
6. **Dismiss failure** explicitly confirms deletion of stored payload/snapshot.
   It stops recovery but retains metadata/audit history until retention expires.
   Dismissal never establishes whether delivery happened.

The page refreshes while visible. A failed refresh marks the displayed data as
potentially stale. Another operator or retention expiry can change whether an
action is allowed; refresh the record after a rejection.

For unknown outcomes, use the [investigation guide](replay-investigation.md)
before confirming or retrying. On mobile, **Back to inbox** preserves filters.

## View-only inspections

**Failures** shows missed deliveries; **Inspections** shows view-only observations.
Use **Available**, **Dismissed** or **All inspections** to filter observations.
They cannot be retried or confirmed as deliveries. An inspection's HTTP status is
the response to the webhook sender, not a delivery result.

**Load body and context** requires `payload` scope. It shows the rendered body,
method/path, redacted headers and variables. The preview reads up to 1 MiB of the
inspection artifact and displays up to 64 KiB of body text. Use **Download
inspection JSON** for the full capture; binary bodies remain base64 in that file.
Oversized or unsupported captures may contain metadata only.

**Files** is available for inspections and failures. **Load file information**
lists names, sizes, content types and multipart fields. **Download file** saves
the decoded bytes without opening a viewer. With `multipart.include_content:
false`, metadata is available but file contents are not. Invalid base64 or a size
mismatch prevents an individual download; the stored payload can still be
downloaded for investigation. Leaving the record discards loaded previews/files.

**Dismiss inspection** requires `retry` scope and deletes its stored body/context.
Metadata and audit history remain until expiry. Inspections share failure
retention/storage limits and are best effort; collection is independent of
`capture_failures`.

To run the [sampling example](../../examples/inspect.yaml) locally, start
`make dev-up` (Docker), then in another terminal:

```sh
export REPLAY_CAPTURE_TOKEN=dev-capture-token-0123456789abcdef
export REPLAY_CONTROL_TOKEN=dev-control-token-0123456789abcdef
go run ./cmd/hookway serve --config examples/inspect.yaml
```

Send a set of local requests from a third terminal:

```sh
for i in $(seq 1 100); do
  curl -fsS -o /dev/null -H 'Content-Type: application/json' \
    --data "{\"event\":\"spot-check\",\"sequence\":$i}" \
    http://localhost:8083/webhooks/inspect
done
```

Open `http://localhost:8082`, sign in with the default local operator token
`dev-operator-token-0123456789abcde`, select **Inspections**, and filter flow
`inspected_events`. About five observations are expected; random selection can
produce a different count. RabbitMQ receives all 100 requests in the
`inspected_events` queue (management UI at `http://localhost:15672`, guest/guest).
For a deterministic demonstration, change `rate` to `1` to inspect every request
or `0` to inspect none. This example's public access and demo credentials are for
local use. The replay stack's configured core control URL is irrelevant to these
view-only records, which can never be redriven.

## Uncertain submission responses

A network failure or 5xx after submission can follow a committed transaction.
The dialog identifies this uncertainty. **Check current state** refreshes history;
**Resubmit same request** preserves the exact IDs/body and submission key for
retry/confirmation. Closing to inspect history retains that pending request in
memory and exposes **Continue uncertain request**. Another console action cannot
replace it until its result is confirmed or definitively rejected. Mixed batch
submissions reuse the same key for all selected IDs after an uncertain response.

Dismissal has no submission-idempotency contract. Its uncertain dialog checks
current record state instead of automatically reissuing deletion.

Reload/sign-out ends the in-memory session and loses pending submission state.
Record the displayed submission key if handing an incident over, and inspect run
or resolution history before issuing new work. Never treat a fresh key as a safe
way to retry an uncertain submission. Retention GC eventually removes both the
record and its idempotency evidence; no perpetual exactly-once promise is made.

## Captured payloads

Opening details never loads artifact bytes. **Preview payload** is explicit and
requires `payload` scope. It reads at most 64 KiB into an escaped UTF-8 text view;
large previews say they are truncated. JSON is displayed as original text, so
large integers and precise decimals are not parsed or rounded. Supplied HTML,
XML or script text isn't rendered as active content. Binary/non-UTF-8 payloads
require download. Closing the preview discards its displayed text.

**Download original bytes** explicitly retrieves the full payload after the
server's digest check and audit event. The response and browser Blob use
`application/octet-stream`; downloaded files are not opened by the console.
Full downloads use a browser-memory Blob, so provision retention artifact limits
for the expected operator machines. Preview/download errors never imply empty
or successful artifacts. Dismissed, expired and metadata-only records have no
available byte controls. Captured metadata isn't a new payload capture.

## Browser and server boundaries

Static console responses use `Cache-Control: no-store`, `nosniff`, `no-referrer`,
frame denial and a CSP restricting scripts to local assets and connections to
the configured replay API origin. WASM
compilation is specifically permitted; arbitrary JavaScript eval is not. Mantine
requires inline styling. Directory listings, hidden paths and unknown assets
return 404, without falling back to an HTML application for missing scripts.
There is no service worker or persistent failure cache. Browser credentials are
omitted and API redirects are rejected. Apply equivalent no-cache policy at the
proxy; HTTPS and origin isolation remain deployment requirements.

Code ownership and shared validation are covered in
[Extending Hookway](../development/extending-hookway.md).

## Local verification

Run `make console-ui` to build the frontend and matching WASM, and
`make console-e2e` to test against a disposable PostgreSQL-backed API.
Docker is required. Other checks are listed in
[Contributing](../../CONTRIBUTING.md#make-and-check-a-change).
