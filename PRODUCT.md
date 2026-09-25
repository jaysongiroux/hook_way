# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

## Users

Primary user: a platform / infrastructure engineer at a mid-size company. They own the webhook gateway, the YAML that defines it, and the deploy. They sit in the config builder to draw and export a flow, then run Hookway themselves.

## Product Purpose

Hookway receives third-party webhook POST requests, verifies authentication, and publishes to destinations the company already runs (queues, streams, object storage, databases, Lambda, Temporal, or another HTTP webhook). Default publishes are JSON envelopes; `s3` and `redis_kv` can store decoded file bytes with `publish.payload: object`, and S3 preserves supplied filename / Content-Type metadata. Success is a verified event arriving at the right backend without glue code, a SaaS account, or a vendor-specific SDK.

## Positioning

One self-hosted binary, one YAML file. Verification is named by wire format (HMAC layouts, HTTP Basic, exact header, JWT algorithms — 11 schemes), not by vendor. Destinations are declared once and referenced by publish nodes; shared credentials are declared once as a named connection and referenced by destinations. The builder generates that YAML; it does not talk to a running process.

## Operating Context

Local compose or a static binary in the company’s own environment. Config is validated with `hookway validate` before serve. Prometheus metrics and a Grafana dashboard ship in-repo. Secrets are environment variables referenced as `${VAR}`, and shared destination credentials live in a top-level `connections` block.

## Capabilities and Constraints

Confirmed: 11 verification schemes (HMAC layouts, HTTP Basic, exact header, JWT HS256/RS256/ES256, and no_auth on its own node); JSON / XML / multipart / raw bodies; filter, if/else, and switch; field_map, changed_fields, xml_prune, a sandboxed code node, and http_request; 14 destination types, including Lambda, into queues, streams, databases, object storage, Temporal, and webhook; `publish.payload: object` on S3 and redis_kv; named connections for shared destination credentials; typed graph edges; fail-closed unknown schemes; process-local rate limits; optional in-memory replay guard; in-flight ceiling; Webhook, Kafka, and RabbitMQ retry with backoff; only webhook honours a 429 or dead-letters a failed publish; an optional replay service keeps the deliveries a destination missed after the sender already got its 2xx, for an operator to retry through an API (no UI, no automatic re-drive; 7-day default retention). The public site must not claim hot reload, RBAC, SSO, OpenTelemetry, Helm, encryption at rest, a replay UI, automatic re-drive, or exactly-once delivery.

## Brand Commitments

- Name: Hookway. Logo mark (hook into a waypoint) belongs in the navbar on both the overview and the builder.
- Voice: precise, operational, no hype. Numbers on the site are counts a reader can verify in the repo. Describe behaviour in the reader's terms, not the code's: "the sender already got its 2xx", not "an acknowledged failure"; "retry that delivery", not "redrive the record".
- Visual (user-pinned): corporate and professional; marketing and builder are one system. Typical AI landing-page chrome is out of bounds (glow orbs, candy accents, neon-on-navy palettes, decorative card bars).

## Evidence on Hand

Live YAML sample on the overview (`web/src/marketing/CodeBand.tsx`). Destination list is generated from the builder registry. Production figures live in `web/src/marketing/productionCopy.ts` and are guarded by tests. Use-case and compare pages (`/use-cases`, `/compare`) cite committed examples and public peer docs; claims are feature-only (no pricing). Docs live in `docs/` on GitHub; the site is a signpost, not a rewrite. No customer testimonials or third-party logos are on hand — do not invent them.

## Product Principles

- Fail closed. Unknown schemes and missing secrets reject traffic.
- Config is the interface. The builder writes YAML a human can still read.
- Claim only what the binary does. If it is not in the repo, it is not on the site.
- One visual system for the people who evaluate the product and the people who configure it.
- Self-hosted: nothing is stored that the operator did not configure.

## Accessibility & Inclusion

WCAG AA contrast is already enforced in `web/src/tokens.test.ts` for body text, UI boundaries, and graph chrome. No further product-specific standard was established.
