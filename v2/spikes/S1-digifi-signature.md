# S1 — DigiFi signature compatibility

**Date:** 2026-09-14

**Status:** blocked on source and real captures; verification gate **not passed**.

**Decision:** preserve I6. No canonicalization option or DigiFi scheme has been approved
or implemented by this spike.

## Evidence available

| Evidence | Finding | Limit |
|---|---|---|
| `internal/auth/hmac.go:52-70`, `:85-86`, `:146-147`, `:195-196` | Existing HMAC verification feeds the original body into each scheme's signing payload, optionally preceded by a timestamp and delimiter. | Does not establish DigiFi's algorithm. |
| `docs/security.md:33`; `v2/02-design.md` §2 | Existing HMAC verification forbids parsing/normalizing before verification; `Verified` preserves the raw-byte contract. | This remains the contract until an explicit decision changes it. |
| `docs/universal-gateway-migration-gaps.md` §A7 | Records `Digifi.verifyWebhookSignature(JSON.stringify(body), endpointSecret, timestamp, signature)`. | Secondary repository documentation, dated 2026-08-27, citing gateway commit `3f8161f`; the SDK and handler were not inspected in this spike. A verifier call does not establish the sender's signing algorithm or exact wire representation. |
| Local source/capture inventory | No gateway checkout or DigiFi captures identified in the bounded search below. | Not a claim that none exist elsewhere. |

The expected sibling `../universal-gateway` is absent. A one-level inventory of
`/Users/jasongiroux/Desktop/git` and `/Users/jasongiroux/Documents` found no directory
whose name contained `gateway` or `digifi`. Candidate checkouts beneath `Developer`,
`code`, and `projects` were also absent. A filename search in this repository identified
only the migration gaps document for DigiFi/gateway; no real fixtures were found.
`git remote -v` identifies only Hookway's origin, not a gateway source location. No
cloud logs, deployment settings, secrets, or customer payloads were accessed.

The local Node runtime is **v22.22.1**, not the required Node 20 Lambda runtime. There
was no locally cached `node:20*` Docker image. The small synthetic demonstration below
used Node 22 and cannot satisfy the Node 20 gate.

## 1. Do raw and re-serialized bytes differ for real DigiFi traffic?

**Unknown. Real payloads examined: 0 of the required minimum 20.** Real identical count,
different count, and worked real example are unavailable.

To illustrate why a real comparison is necessary, these **invented** inputs were passed
through `JSON.stringify(JSON.parse(input))`. Lengths and first differences were measured
on UTF-8 `Buffer` values, not JavaScript UTF-16 string offsets. These are synthetic
serialization observations, not DigiFi compatibility evidence or captured traffic.

| Synthetic case | Input (JSON string notation) | Re-serialized result | Raw / result bytes | First differing byte (zero-based) |
|---|---|---|---|---|
| Compact control | `"{\"a\":1}"` | `{"a":1}` | 7 / 7 | none |
| Whitespace | `"{ \"a\": 1 }\n"` | `{"a":1}` | 11 / 7 | 1 |
| Unicode escape | `"{\"a\":\"\\u00e9\"}"` | `{"a":"é"}` | 14 / 10 | 6 |
| Number formatting | `"{\"a\":1.0}"` | `{"a":1}` | 9 / 7 | 6 |
| Integer-like key ordering | `"{\"2\":\"b\",\"1\":\"a\"}"` | `{"1":"a","2":"b"}` | 17 / 17 | 2 |

For example, the whitespace input is 11 bytes including its final newline; its
re-serialization is 7 bytes. The first mismatch is byte 1 (space versus a quote).
Identical JSON meaning therefore does not establish identical signed bytes.

## 2. Which form does the captured signature verify against?

**Unknown. Raw pass rate: not measured. Re-serialized pass rate: not measured.**
There are no captured timestamp/signature pairs, no matching sandbox verification
secret, and no installed vendor package available for this investigation. The actual
algorithm, timestamp units/encoding, delimiter, signature encoding, and treatment of
multiple signatures remain unverified. No generic HMAC scheme was substituted, and no
synthetic signature was presented as a DigiFi signature. No Go probe was written because
its signing contract is not yet known.

## 3. Does DigiFi require an I6 exception, and what is the narrowest contract?

**Undetermined; no exception proposed for adoption.** The documented Lambda call alone
does not prove one is needed. Its input may already match the transmitted bytes.

If real evidence later establishes verification only against Node 20 re-serialization,
bring a concrete, scheme-specific proposal to the user before Chunk 3, as required by
`v2/plans/00-spikes.md`. That proposal would have to specify the exact supported JSON
semantics and malformed-body behavior, preserve the original bytes for later processing,
and bind the exception to the proven DigiFi scheme. A general body-normalization switch
is not justified by the present evidence. No port type or invariant changed here.

## Inputs and procedure needed to finish

1. Supply an accessible `universal-gateway` checkout, including its lockfile and the
   installed vendor SDK (or the exact package/version from an authorized source). Inspect
   the handler's body parsing and the SDK's verifier implementation; record source
   revision, SDK version, exact signing bytes, and header handling without recording
   secrets. Logging signed input would not by itself reveal the complete algorithm.
2. Supply at least 20 authorized sandbox captures preserving original body bytes, exact
   timestamp and signature headers, and a secure local way to use the matching test
   secret. Do not paste payloads or secrets into this findings document or the task.
   Capture at a byte-preserving boundary; a parsed/reformatted CloudWatch record cannot
   establish the bytes received. Decode any transport/base64 envelope exactly once and
   record capture provenance. A newline added by an export tool invalidates byte fidelity.
3. Compare both forms under Node 20. Retain each original as bytes and produce the
   re-serialized form as UTF-8. Report byte lengths and offsets with buffers; the plan's
   string-index example measures UTF-16 positions and is not a byte-level diff for
   non-ASCII text. Keep real fragments out of terminal output and committed findings.
4. Implement the throwaway Go probe from the inspected algorithm. Check both forms against
   captured signatures and cross-check with the actual SDK. Separate cryptographic
   mismatch from SDK timestamp-expiry rejection for historical captures. Record real
   counts and a synthetic/scrubbed worked example that reproduces the relevant difference.
5. Delete the probe and real fixtures after the result is recorded. Update the S1 decision
   and gate only when the required evidence exists; discuss any I6 exception with the user.

## Capture hygiene and verification performed

- Added `v2/spikes/fixtures/` to `.gitignore` before any capture. Verified the rule using
  `git check-ignore -v v2/spikes/fixtures/probe-placeholder.json`; no file was created.
- No customer data or secrets were captured, written, or logged by this spike. No
  throwaway probe files were created. The synthetic demonstration ran through stdin.
- Verified existing verifier source and local runtime availability; no production code
  changed. Documentation existence and synthetic examples do **not** pass the S1 gate.
