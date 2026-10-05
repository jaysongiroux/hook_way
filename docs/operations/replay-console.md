# Replay operations console

Use the console to inspect captured failures and request recovery from a running
replay service. It shows payloads, delivery history, and evidence for uncertain
outcomes. The public builder writes configuration separately; it does not manage
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

Use a **named** operator token defined in replay `keys`. The key name identifies
its actions in audit history. Give each operator a separate credential when
individual attribution matters. Read-only responders need `read`; recovery
operators also need `retry`; captured bytes additionally require `payload`.
Capture and metrics scopes are unnecessary for the console. These are scoped
bearer credentials, not SSO or user/role administration.

The password input is cleared after sign-in. The session token lives only in
browser memory, never cookies, local/session storage, IndexedDB, URL parameters,
or application logs. Navigation between the console and `/builder` retains the
session and investigation context in memory, while builder visits pause polling.
Reload, leaving the application or signing out clears the session and requires
authentication again. A 401 clears the active session; 403 reports
the missing permission without interpreting it as success. Sign-out aborts
requests and removes displayed failures, payload previews and pending actions.
The menu named after the current API key shows its scopes and **Sign out**.

A rejected or expired key clears the session and explains how to sign in again.
Routine polling cancellations during navigation or sign-out are silent; an
unexpected interruption or connection failure includes recovery guidance.
Service errors preserve previously loaded inbox data with a stale-data warning.
Resolution input errors appear beside their fields and in a readable summary,
focus the first invalid field and preserve entered evidence. Network errors after
submitting a recovery action still require investigation and reuse of the same
submission key; an error message does not establish that the action was rejected.

`GET /v1/me` verifies the contract version and advertised API capabilities,
including `action_eligibility`. Incompatible API assets cannot become a usable
session. Local Go/WASM checks input structure. Missing or incompatible WASM is
visible; an operator can explicitly select the API's **read-only input validation**
path. It performs no recovery operation and grants no permissions. State,
retention and scopes are always rechecked by the server for the actual action.

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

Desktop keeps the inbox and evidence independently scrollable, with recovery
controls at the top of the detail panel. Mobile opens a focused detail view;
**Back to inbox** preserves filters and restores focus to the inspected record.

The page refreshes current list and selected details every three seconds while
visible, without overlapping polls. Hidden pages stop polling; returning resumes
refresh. A failed refresh identifies potentially stale data. Action eligibility
in list/detail responses is state-based and independent of caller scopes; API
transactions remain authoritative if another operator acts or retention expires.
New retries and confirmations reject expired records even before physical GC.
Already accepted idempotent requests can still return their prior result while
that retained record exists. Retryability at capture alone is not eligibility now.

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

```sh
make console-ui              # Go WASM and independent Vite static bundle
make console-e2e             # real API/store + disposable Docker PostgreSQL
make api-schema-check node-schema-check
```

Console browser tests require Docker and fail rather than silently substituting
a mock store. A test-only Go service owns `/test/reset`; it is compiled only into
its test executable and never into Hookway binaries. Synthetic credentials,
records, destination URLs and receipts are used in screenshots. Tests run with
one browser worker so resets remain isolated. This verifies the operational
interface and database-backed API, not end-to-end destination execution or an
actual production rollout. Run `make replay-acceptance-test` separately to check
delivery, shared protection, and recovery against disposable backends.
