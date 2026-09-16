# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

## Users

Primary user: a platform / infrastructure engineer at a mid-size company. They own the webhook gateway, the YAML that defines it, and the deploy. They sit in the config builder to draw and export a flow, then run Hookway themselves.

## Product Purpose

Hookway receives third-party webhook POST requests, verifies authentication, and publishes event envelopes to destinations the company already runs (queues, streams, databases, Temporal, or another HTTP webhook). Success is a verified event arriving at the right backend without glue code, a SaaS account, or a vendor-specific SDK.

## Positioning

One self-hosted binary, one YAML file. Verification is named by wire format (HMAC layout, JWT algorithm, static header), not by vendor. Destinations are declared once and referenced by publish nodes. The builder generates that YAML; it does not talk to a running process.

## Operating Context

Local compose or a static binary in the company’s own environment. Config is validated with `hookway validate` before serve. Prometheus metrics and a Grafana dashboard ship in-repo. Replay, when enabled, is a separate listener. Secrets are environment variables referenced as `${VAR}`.

## Capabilities and Constraints

Confirmed: HMAC / JWT / header_exact / no_auth; 13 destination types; typed graph edges; fail-closed unknown auth; process-local rate limits; optional in-memory replay guard; in-flight ceiling; webhook/Kafka/RabbitMQ retries then dead-letter. The public site must not claim hot reload, RBAC, SSO, OpenTelemetry, Helm, or encryption at rest.

## Brand Commitments

- Name: Hookway. Logo mark (hook into a waypoint) belongs in the navbar on both the overview and the builder.
- Voice: precise, operational, no hype. Numbers on the site are counts a reader can verify in the repo.
- Visual (user-pinned): corporate and professional; marketing and builder are one system. Typical AI landing-page chrome is out of bounds (glow orbs, candy accents, neon-on-navy palettes, decorative card bars).

## Evidence on Hand

Live YAML sample on the overview (`web/src/marketing/CodeBand.tsx`). Destination list is generated from the builder registry. Production figures live in `web/src/marketing/productionCopy.ts` and are guarded by tests. Docs live in `docs/` on GitHub; the site is a signpost, not a rewrite. No customer testimonials or third-party logos are on hand — do not invent them.

## Product Principles

- Fail closed. Unknown schemes and missing secrets reject traffic.
- Config is the interface. The builder writes YAML a human can still read.
- Claim only what the binary does. If it is not in the repo, it is not on the site.
- One visual system for the people who evaluate the product and the people who configure it.
- Self-hosted: nothing is stored that the operator did not configure.

## Accessibility & Inclusion

WCAG AA contrast is already enforced in `web/src/tokens.test.ts` for body text, UI boundaries, and graph chrome. No further product-specific standard was established.
