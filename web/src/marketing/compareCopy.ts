// Competitive comparison copy for /compare.
//
// Claims are feature and integration only — no pricing or subscription tiers.
// Sources are public docs checked while writing this page; re-verify before
// expanding a cell. Hookdeck Outpost is noted separately: it is an outbound
// event delivery product, not an inbound webhook gateway.

import { GITHUB_REPO } from "./links";

export type SupportLevel = "yes" | "partial" | "no" | "n/a";
type CompetitorId = "hookway" | "hookdeck" | "convoy";

export interface Competitor {
  id: CompetitorId;
  name: string;
  role: string;
  sourceHref: string;
  sourceLabel: string;
}

export interface CompareRow {
  id: string;
  capability: string;
  note?: string;
  values: Record<CompetitorId, { level: SupportLevel; detail: string }>;
}

export const COMPARE_INTRO = {
  title: "How Hookway compares",
  lead: "Feature and integration differences for inbound webhook gateways. Each claim cites public product documentation or this repository.",
} as const;

export const COMPETITORS: Competitor[] = [
  {
    id: "hookway",
    name: "Hookway",
    role: "Self-hosted inbound gateway: one binary, one YAML config, native publishes into your infrastructure.",
    sourceHref: GITHUB_REPO,
    sourceLabel: "hook_way repository",
  },
  {
    id: "hookdeck",
    name: "Hookdeck Event Gateway",
    role: "Managed inbound event gateway: ingest, queue, transform, and deliver to HTTP (plus CLI and Mock API destinations).",
    sourceHref: "https://hookdeck.com/docs/destinations",
    sourceLabel: "Hookdeck Destinations docs",
  },
  {
    id: "convoy",
    name: "Convoy",
    role: "Open-source webhook gateway for ingesting and delivering webhooks, with HTTP endpoints as the delivery target.",
    sourceHref: "https://www.getconvoy.io/docs/home/introduction",
    sourceLabel: "Convoy introduction",
  },
];

export const COMPARE_ROWS: CompareRow[] = [
  {
    id: "deploy",
    capability: "Deployment model",
    values: {
      hookway: { level: "yes", detail: "Self-hosted static binary / container you run" },
      hookdeck: { level: "yes", detail: "Managed SaaS Event Gateway" },
      convoy: { level: "yes", detail: "Self-hosted (API, workers, Postgres, Redis) or Convoy Cloud" },
    },
  },
  {
    id: "config",
    capability: "Primary config surface",
    values: {
      hookway: { level: "yes", detail: "YAML flows + optional visual builder export" },
      hookdeck: { level: "yes", detail: "Dashboard, API, Terraform" },
      convoy: { level: "yes", detail: "Dashboard and API" },
    },
  },
  {
    id: "verify",
    capability: "Inbound signature verification",
    values: {
      hookway: {
        level: "yes",
        detail: "Wire-format schemes (HMAC layouts, JWT, header exact); fails closed on unknown schemes",
      },
      hookdeck: { level: "yes", detail: "Vendor source types and generic webhooks with verification" },
      convoy: { level: "yes", detail: "Provider verification on incoming HTTP sources" },
    },
  },
  {
    id: "bodies",
    capability: "Body formats",
    values: {
      hookway: { level: "yes", detail: "JSON, XML element tree, multipart/form-data, raw" },
      hookdeck: {
        level: "partial",
        detail: "Preserves arbitrary request bodies; JavaScript transformations can convert formats such as XML to JSON",
      },
      convoy: { level: "partial", detail: "JSON event payloads; transforms available on ingest" },
    },
  },
  {
    id: "destinations",
    capability: "Native delivery destinations",
    note: "What the product writes to after verification — not brokers used only as ingest sources.",
    values: {
      hookway: {
        level: "yes",
        detail:
          "14 catalog types: RabbitMQ, SQS, SNS, S3, Lambda, Redis (stream/list/pubsub/kv), Postgres, MongoDB, Kafka, webhook, plus Temporal task-queue wiring whose workflow settings are not yet exposed in standalone YAML",
      },
      hookdeck: { level: "partial", detail: "HTTP, CLI (local), and Mock API only" },
      convoy: {
        level: "partial",
        detail: "HTTP/HTTPS webhook endpoints; Kafka/SQS/Pub/Sub/RabbitMQ are ingest sources for outgoing projects",
      },
    },
  },
  {
    id: "object-store",
    capability: "Store decoded file bytes in object storage",
    values: {
      hookway: {
        level: "yes",
        detail: "publish.payload: object on S3 (and redis_kv) after multipart parse",
      },
      hookdeck: { level: "no", detail: "Event Gateway destinations do not include S3" },
      convoy: { level: "no", detail: "Delivery targets are HTTP endpoints" },
    },
  },
  {
    id: "fanout",
    capability: "Fan-out to multiple destinations",
    values: {
      hookway: { level: "yes", detail: "Multiple publish nodes; stop_on_failure or skip_failed" },
      hookdeck: { level: "yes", detail: "One source to many connections/destinations" },
      convoy: { level: "yes", detail: "Fan-out by event type or endpoint matching" },
    },
  },
  {
    id: "transform",
    capability: "Payload transform",
    values: {
      hookway: { level: "yes", detail: "field_map, changed_fields, xml_prune, sandboxed JavaScript code node" },
      hookdeck: { level: "yes", detail: "JavaScript transformations on connections" },
      convoy: { level: "yes", detail: "Transform functions on ingest" },
    },
  },
  {
    id: "replay",
    capability: "Inspect and re-deliver",
    values: {
      hookway: {
        level: "partial",
        detail:
          "Optional replay service keeps events a destination missed after the sender got its 2xx; retry just that delivery from its API",
      },
      hookdeck: { level: "yes", detail: "Persisted event log, issues, bulk retry in the product UI" },
      convoy: { level: "yes", detail: "Delivery logs, retries, and dashboards" },
    },
  },
  {
    id: "ops",
    capability: "Operator observability",
    values: {
      hookway: { level: "yes", detail: "Prometheus metrics and in-repo Grafana dashboard" },
      hookdeck: { level: "yes", detail: "Managed observability in the Event Gateway product" },
      convoy: { level: "yes", detail: "Prometheus metrics; dashboards in the product" },
    },
  },
];

export const COMPARE_OUTPOST_NOTE = {
  title: "About Hookdeck Outpost",
  body: "Hookdeck also publishes Outpost, a separate open-source outbound event delivery service with native destinations such as webhooks, SQS, S3, RabbitMQ, and Kafka. Outpost solves sending events to your customers’ endpoints and brokers. Hookway solves receiving third-party webhooks into your own infrastructure. They are complementary problem spaces, not drop-in substitutes.",
  href: "https://hookdeck.com/docs/outpost/overview",
  label: "Hookdeck Outpost overview",
} as const;

export const COMPARE_SOURCES = [
  { href: "https://hookdeck.com/docs/destinations", label: "Hookdeck Destinations" },
  { href: "https://hookdeck.com/docs/transformations", label: "Hookdeck Transformations" },
  { href: "https://hookdeck.com/event-gateway", label: "Hookdeck Event Gateway" },
  { href: "https://hookdeck.com/docs/outpost/overview", label: "Hookdeck Outpost" },
  { href: "https://www.getconvoy.io/docs/home/introduction", label: "Convoy introduction" },
  { href: "https://www.getconvoy.io/docs/product-manual/sources", label: "Convoy Sources" },
  { href: "https://www.getconvoy.io/docs/product-manual/functions", label: "Convoy Functions" },
  { href: "https://www.getconvoy.io/docs/product-manual/metrics", label: "Convoy Metrics" },
  { href: GITHUB_REPO, label: "Hookway repository" },
] as const;
