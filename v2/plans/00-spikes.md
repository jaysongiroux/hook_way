# Chunk 0: Spikes

**Status:** not started
**Depends on:** nothing
**Unlocks:** Chunk 1 (gate only — spike findings may amend the port lattice)
**Verification gate:** two findings documents exist and each answers its questions with
evidence, not opinion. Any amendment they imply has been folded into `v2/02-design.md`.

**Goal:** Remove two pieces of uncertainty that would be expensive to discover later. These
are throwaway probes. **Their output is an answer, not code you keep.** Anything you build
gets deleted or clearly labelled as a spike artifact.

## Why these two, and why first

Spike S1 sits at the centre of the migration premise: if DigiFi's signature scheme is not
verifiable against raw bytes, one of the three "portable" webhook routers is not portable,
and the `auth` node's contract changes before Chunk 3 designs it. The gaps doc
(`docs/universal-gateway-migration-gaps.md` §6) calls it half a day and says it "changes
the cost of every option."

Spike S2 validates the port lattice against reality. Getting the lattice wrong means
rebuilding the type checker, and every chunk depends on it. An hour now.

---

## Task 0a: DigiFi signature compatibility (S1)

**Files:**
- Create: `v2/spikes/S1-digifi-signature.md`
- Create (throwaway): `v2/spikes/fixtures/` — **gitignore this or scrub it**
- Create (throwaway): `v2/spikes/cmd/digifi-probe/main.go`

### Background you need

`internal/auth/hmac_sha256.go` and the other verifiers in `internal/auth` all verify
against the **raw request bytes**. `docs/security.md:33` states Hookway "does not parse,
trim, normalize, or re-serialize" the body. That is invariant **I6**.

The DigiFi Lambda does this instead (`../universal-gateway/digifi_router_webhook_handler/`):

```js
Digifi.verifyWebhookSignature(JSON.stringify(body), endpointSecret, timestamp, signature)
```

`body` there is already-parsed JSON, so `JSON.stringify(body)` is a **re-serialization**.
If Node's `JSON.stringify` output differs from the bytes DigiFi actually signed — in key
order, unicode escaping, whitespace, or number formatting — then raw-byte verification
will reject signatures the current gateway accepts.

- [ ] **Step 1: Capture at least 20 real DigiFi payloads as raw bytes**

Byte fidelity is the entire point. Do **not** round-trip through `jq`, a browser, a log
viewer, or anything else that reformats JSON. Capture at the earliest point you can:
CloudWatch raw event, an API Gateway access log with the body, or a temporary passthrough.

For each, record three things: the raw body bytes, the timestamp header, and the signature
header. Store one payload per file under `v2/spikes/fixtures/`.

Add `v2/spikes/fixtures/` to `.gitignore` before you capture anything. These are real
customer payloads.

- [ ] **Step 2: Diff raw against re-serialized, using Node 20**

Node 20 matches the Lambda runtime; a different major version may canonicalize
differently, which would invalidate the finding.

```bash
cd v2/spikes
for f in fixtures/*.json; do
  node -e '
    const fs = require("fs");
    const raw = fs.readFileSync(process.argv[1], "utf8");
    const round = JSON.stringify(JSON.parse(raw));
    if (raw === round) { console.log("IDENTICAL", process.argv[1]); process.exit(0); }
    console.log("DIFFERS", process.argv[1], "raw:", raw.length, "round:", round.length);
    for (let i = 0; i < Math.max(raw.length, round.length); i++) {
      if (raw[i] !== round[i]) {
        console.log("  first diff at byte", i,
          JSON.stringify(raw.slice(Math.max(0,i-30), i+30)), "vs",
          JSON.stringify(round.slice(Math.max(0,i-30), i+30)));
        break;
      }
    }
  ' "$f"
done
```

Classify every difference as one of: **key reordering**, **unicode escaping**
(`\uXXXX` vs a literal character), **whitespace**, or **number formatting** (`1.0` vs `1`,
exponent notation). The classification matters because whitespace-only differences are
trivially fixable and key reordering is not.

- [ ] **Step 3: Determine which form the real signature verifies against**

Write a throwaway Go program at `v2/spikes/cmd/digifi-probe/main.go` that, for each
fixture, computes the DigiFi HMAC over **both** forms and compares each against the
captured `signature` header.

You need DigiFi's actual algorithm. It lives in a closed vendor package, so get it by
whichever of these is available: read the installed `node_modules` source in
`../universal-gateway`, or instrument the Lambda to log the string it signs. Record in the
findings doc **how** you obtained it and how confident you are.

Report a pass rate per form, for example "raw: 0/20, re-serialized: 20/20".

- [ ] **Step 4: Write the findings document**

`v2/spikes/S1-digifi-signature.md` must answer exactly these, with the evidence inline:

1. Do raw bytes and re-serialized bytes differ for real DigiFi traffic? Counts and a
   worked example.
2. Which form does the captured signature verify against?
3. If it is the re-serialized form: does the DigiFi endpoint need an exception to I6, and
   **what is the narrowest form that exception could take?** Propose a specific node
   contract — for example an `auth` node option `canonicalize: json_restringify`, scoped to
   one scheme, documented as a deliberate I6 exception rather than a general capability.

- [ ] **Step 5: Delete the throwaway code and scrub the fixtures**

```bash
rm -rf v2/spikes/cmd
```

Either delete `v2/spikes/fixtures/` or replace every real value in it with synthetic data
and re-verify the finding still reproduces. Do not leave real payloads on disk.

### Gate

**If the answer to (3) is that I6 needs an exception, stop and discuss with the user before
Chunk 3.** A scheme that must canonicalize before verifying is a genuinely different node
contract, and it weakens a documented security property. That is the user's call, not the
implementer's.

---

## Task 0b: Lattice validation against all seven handlers (S2)

**Files:**
- Create: `v2/spikes/S2-lattice-validation.md`

### What to read first

- `v2/02-design.md` §2 (port types) and §4 (node catalog) — the lattice you are testing
- `docs/universal-gateway-migration-gaps.md` §2 — what each handler does
- `../universal-gateway/*/app.js` — the actual behaviour, where the doc is ambiguous

- [ ] **Step 1: Write all seven handlers as V2 YAML**

One YAML block per handler, in the findings doc. Use only the port types and node types in
`02-design.md` §2 and §4 — the point is to find out where the catalog is insufficient, so
do not invent new nodes silently. When you need something that does not exist, write the
YAML you *wish* worked and flag it.

The seven: `digifi_router_webhook_handler`, `qualia_router_webhook_handler`,
`clearedge_router_webhook_handler`, `salesforce_router_proxy_handler`,
`salesforce_digifi_proxy_handler`, `digifi_router_proxy_handler`,
`docmagic_xml_proxy_handler`.

- [ ] **Step 2: Resolve the four known suspects explicitly**

These are already identified as likely lattice stress points. Give each a written answer:

**ClearEdge id-type discrimination.** It regex-matches to decide the id type *before*
looking up:
```js
const DIGIFI_ID_STRING_REGEX = /^[0-9a-fA-F]{24}$/;
const FRIENDLY_ID_STRING_REGEX = /^[A-Z]{2}\d{6}$/;
```
Does this need a `switch` node on `Envelope`, or a `lookup` node taking an ordered list of
candidate keys with a pattern each? Decide which, and say why.

**DocMagic XML.** Needs `body_format: xml` plus five ordered DOM rewrite rules
(`../universal-gateway/docmagic_xml_proxy_handler/pipeline.js`). Is that **one**
`transform` node configured with five rules, or **five chained** `transform` nodes? Note
that the handler logs before/after element counts, so whichever shape you pick must expose
per-rule counts.

**Qualia query-param auth.** `queryParams.authID !== endpointSecret`. Confirm
`RequestHead` carries query values (it does — `v2/02-design.md` §2) and specify the
`auth` node option that reads from there, e.g. `source: {kind: query, key: authID}`.

**Upstream OAuth on the three proxies.** They acquire a token before calling upstream.
Is that an `http_request` node option (`auth: {oauth2: ...}`), a separate node producing a
cached token, or out of scope for V2? Note that a separate node would need a port type it
does not currently have, so prefer an option.

- [ ] **Step 3: Write findings with proposed amendments**

`v2/spikes/S2-lattice-validation.md` has a table — handler, expressible yes/no, and for
each no, the **minimal** amendment that fixes it. Minimal matters: a new `PortType` is the
most expensive possible amendment and a new node option is the cheapest. Reach for the
cheapest that works.

- [ ] **Step 4: Fold amendments into the design**

Any amendment that changes a `PortType` or its assignability rule **must** be written into
`v2/02-design.md` §2 before Chunk 1 starts, because Chunk 1 Task 1 implements that table
verbatim. Amendments that only add node types or options go into §4 and can land later.

### Gate

**If any amendment changes a `PortType` or `Accepts`, stop and confirm with the user.**
Everything downstream depends on that table, and a change there invalidates Chunk 1's
tests.

---

## Done when

- [ ] `v2/spikes/S1-digifi-signature.md` answers its three questions with evidence
- [ ] No real customer payloads remain on disk
- [ ] Throwaway probe code is deleted
- [ ] `v2/spikes/S2-lattice-validation.md` covers all seven handlers with a yes/no and an
      amendment for each no
- [ ] Any `PortType` amendment is reflected in `v2/02-design.md` §2 and confirmed with the user
- [ ] `v2/README.md`'s decision table records the S1 outcome (does I6 need an exception?)
