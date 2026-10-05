// Feature comparison, checked against linked vendor documentation on 2026-10-05.
// Compare Event Gateway separately from the outbound Outpost product.

import { GITHUB_REPO, repoBlob } from "./links";

export const COMPARE_CHECKED_ON = "2026-10-05";

export type SupportLevel = "yes" | "partial" | "no" | "n/a";
type CompetitorId = "hookway" | "hookdeck" | "convoy";

export interface Competitor {
  id: CompetitorId;
  name: string;
  role: string;
  sourceHref: string;
  sourceLabel: string;
  /** Short label when this column is the product the page is about. */
  mark?: string;
}

export interface CompareRow {
  id: string;
  capability: string;
  note?: string;
  sources: Record<CompetitorId, string>;
  values: Record<CompetitorId, { level: SupportLevel; detail: string; count?: number }>;
}

export const COMPARE_INTRO = {
  title: "How Hookway compares",
  lead: "Compare how these gateways receive, verify, and deliver webhooks. Hookway runs in your infrastructure and sends events directly to queues, streams, databases, and object storage. Its optional replay service lets you inspect and retry missed deliveries through a scoped operations console or API. The table explains different delivery boundaries and links to the docs behind each claim.",
} as const;

export const COMPARE_DIFFERS = {
  title: "Where Hookway differs",
  points: [
    {
      title: "Native destinations",
      detail:
        "Publishes into queues, streams, databases, object storage, Lambda, Temporal, and webhooks — 14 catalog types. Configure Temporal workflow type, input, IDs and timeouts in YAML or the builder.",
    },
    {
      title: "Object and file publish",
      detail:
        "S3 and redis_kv accept publish.payload: object, so multipart file bytes are stored in infrastructure you run.",
    },
    {
      title: "Optional hookway-replay",
      detail:
        "Inspect missed deliveries in the scoped console or API. Retry from the failed node or confirm an uncertain outcome with evidence. Capture is best effort; recovery is operator initiated.",
    },
  ],
} as const;

export const COMPETITORS: Competitor[] = [
  {
    id: "hookway",
    name: "Hookway",
    role: "Self-hosted inbound gateway. Core is one binary configured in YAML; replay and the frontend are optional separate services.",
    sourceHref: GITHUB_REPO,
    sourceLabel: "hook_way repository",
    mark: "This product",
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
    sources: { hookway: repoBlob("docs/dockerhub/README.md"), hookdeck: "https://hookdeck.com/event-gateway", convoy: "https://www.getconvoy.io/docs/home/introduction" },
    capability: "Deployment model",
    values: {
      hookway: {
        level: "yes",
        detail: "Self-hosted static binary / container you run",
      },
      hookdeck: { level: "yes", detail: "Managed SaaS Event Gateway" },
      convoy: {
        level: "yes",
        detail: "Self-hosted API/workers with PostgreSQL and Redis, or Convoy Cloud",
      },
    },
  },
  {
    id: "config",
    sources: { hookway: repoBlob("docs/reference/configuration.md"), hookdeck: "https://hookdeck.com/docs/connections", convoy: "https://www.getconvoy.io/docs/home/quickstart" },
    capability: "Primary config surface",
    values: {
      hookway: {
        level: "yes",
        detail: "YAML flows + optional visual builder export",
      },
      hookdeck: { level: "yes", detail: "Dashboard, API, Terraform" },
      convoy: { level: "yes", detail: "Dashboard and API" },
    },
  },
  {
    id: "verify",
    sources: { hookway: repoBlob("docs/reference/security.md#auth-schemes"), hookdeck: "https://hookdeck.com/docs/sources", convoy: "https://www.getconvoy.io/docs/product-manual/sources" },
    capability: "Inbound signature verification",
    values: {
      hookway: {
        level: "yes",
        detail:
          "14 authentication formats: HMAC, ECDSA P-256, HTTP Basic, exact tokens, and JWT HS256/RS256/ES256. Unknown schemes are rejected; unauthenticated flows use an explicit no_auth node",
      },
      hookdeck: {
        level: "yes",
        detail: "Vendor source types and generic webhooks with verification",
      },
      convoy: {
        level: "yes",
        detail: "HMAC, Basic auth, API keys, and provider-specific incoming source verification",
      },
    },
  },
  {
    id: "bodies",
    sources: { hookway: repoBlob("docs/reference/configuration.md#envelope-and-publish"), hookdeck: "https://hookdeck.com/docs/destinations", convoy: "https://www.getconvoy.io/docs/product-manual/sources" },
    capability: "Body formats",
    values: {
      hookway: {
        level: "yes",
        detail: "JSON, ordered XML element tree, multipart/form-data, raw",
      },
      hookdeck: {
        level: "partial",
        detail:
          "Preserves arbitrary request bodies; JavaScript transformations can convert formats such as XML to JSON",
      },
      convoy: {
        level: "partial",
        detail: "JSON event payloads and JavaScript transformations; no equivalent ordered XML or multipart-file parser is documented",
      },
    },
  },
  {
    id: "destinations",
    sources: { hookway: repoBlob("docs/reference/configuration.md#destinations"), hookdeck: "https://hookdeck.com/docs/destinations", convoy: "https://www.getconvoy.io/docs/product-manual/sources" },
    capability: "Native delivery destinations",
    note: "Counts are catalog destination types, not provider brands. Hookway counts Redis modes separately; Event Gateway includes CLI and Mock API. Configure Temporal workflow type, input, IDs and timeouts in YAML or the builder.",
    values: {
      hookway: {
        level: "yes",
        count: 14,
        detail:
          "14 catalog types: RabbitMQ, SQS, SNS, S3, Lambda, Redis (stream/list/pubsub/kv), Postgres, MongoDB, Kafka, webhook, and Temporal workflow starts with configurable input, IDs and timeouts",
      },
      hookdeck: {
        level: "yes",
        count: 3,
        detail: "HTTP, CLI for local testing, and Mock API. Hookdeck Outpost is a separate outbound product with 11 destination types",
      },
      convoy: {
        level: "yes",
        count: 1,
        detail:
          "HTTP/HTTPS webhook endpoints; Kafka/SQS/Pub/Sub/RabbitMQ are ingest sources for outgoing projects",
      },
    },
  },
  {
    id: "object-store",
    sources: { hookway: repoBlob("docs/reference/configuration.md#publish-payload-modes"), hookdeck: "https://hookdeck.com/docs/destinations", convoy: "https://www.getconvoy.io/docs/product-manual/retention-policy" },
    capability: "Multipart / decoded file → object storage",
    values: {
      hookway: {
        level: "yes",
        detail:
          "publish.payload: object writes decoded multipart file bytes to S3 or redis_kv",
      },
      hookdeck: {
        level: "no",
        detail:
          "HTTP delivery can call your own upload handler; no native decoded-file publish. Outpost is a separate product",
      },
      convoy: {
        level: "no",
        detail:
          "HTTP endpoints can implement uploads themselves. An S3 retention archive stores webhook history, not decoded multipart files",
      },
    },
  },
  {
    id: "fanout",
    sources: { hookway: repoBlob("docs/operations/delivery-guarantees.md#fan-out-and-acknowledged-failures"), hookdeck: "https://hookdeck.com/docs/connections", convoy: "https://www.getconvoy.io/docs/product-manual/subscriptions" },
    capability: "Fan-out to multiple destinations",
    values: {
      hookway: {
        level: "yes",
        detail: "Multiple publish nodes; stop_on_failure or skip_failed",
      },
      hookdeck: {
        level: "yes",
        detail: "One source to many connections/destinations",
      },
      convoy: {
        level: "yes",
        detail: "Fan-out by event type or endpoint matching",
      },
    },
  },
  {
    id: "protection",
    sources: { hookway: repoBlob("docs/guides/protection-stores.md"), hookdeck: "https://hookdeck.com/docs/deduplication", convoy: "https://www.getconvoy.io/docs/product-manual/rate-limits" },
    capability: "Traffic controls",
    note: "Inbound admission and downstream delivery pacing protect different parts of a system.",
    values: {
      hookway: { level: "yes", detail: "Inbound token buckets, approximate rolling quotas, and duplicate reservations; memory, Redis, or PostgreSQL. Verified caller/tenant identities are supported" },
      hookdeck: { level: "yes", detail: "Queued delivery pacing per destination and configurable deduplication rules" },
      convoy: { level: "yes", detail: "Redis-backed ingest/API rate limits and per-endpoint delivery rate limits" },
    },
  },
  {
    id: "transform",
    sources: { hookway: repoBlob("docs/guides/ingress-adapters.md"), hookdeck: "https://hookdeck.com/docs/transformations", convoy: "https://www.getconvoy.io/docs/product-manual/functions" },
    capability: "Payload transform",
    values: {
      hookway: {
        level: "yes",
        detail:
          "Field mapping, JSON changes and schema validation, explicit conversions, ordered XML edits, sandboxed JavaScript, and bounded batches",
      },
      hookdeck: {
        level: "yes",
        detail: "JavaScript transformations on connections",
      },
      convoy: { level: "yes", detail: "JavaScript functions on incoming subscriptions and outgoing broker sources" },
    },
  },
  {
    id: "replay",
    sources: { hookway: repoBlob("docs/operations/replay-service.md"), hookdeck: "https://hookdeck.com/docs/retries", convoy: "https://www.getconvoy.io/docs/api-reference/event-deliveries/retry-event-delivery" },
    capability: "Inspect and retry failed deliveries",
    values: {
      hookway: {
        level: "yes",
        detail:
          "Optional hookway-replay: inspect, download, retry individually or in batches, dismiss, and investigate via scoped operations console or API. Best-effort recovery of eligible misses after the sender’s 2xx; resumes at the failed node. No automatic re-drive",
      },
      hookdeck: {
        level: "yes",
        detail:
          "Stored requests/events with dashboard and API retry, including bulk replay of requests. Automatic retries and manual recovery are both available",
      },
      convoy: {
        level: "yes",
        detail:
          "Event/delivery logs and dashboard/API resend. Automatic retries use configurable linear or exponential backoff",
      },
    },
  },
  {
    id: "payload-retention",
    sources: { hookway: repoBlob("docs/operations/replay-service.md#what-gets-captured"), hookdeck: "https://hookdeck.com/docs/limits", convoy: "https://www.getconvoy.io/docs/guides/best-practices-for-generating-webhook-events" },
    capability: "Payload limits",
    note: "Ingress size, retained artifact size, and searchable size are separate limits; these numbers are not durability guarantees.",
    values: {
      hookway: {
        level: "yes",
        detail:
          "Ingress is bounded by each read_body.max_bytes. Replay max_record_bytes defaults to 16 MiB per artifact; larger artifacts become metadata-only and cannot be retried",
      },
      hookdeck: {
        level: "partial",
        detail:
          "Default Developer-plan inbound limit: 10 MiB; oversize requests get 413. Body search limit: 2.5 MB, separate from storage and delivery. Larger ingest limits need arrangement",
      },
      convoy: {
        level: "n/a",
        detail:
          "The linked guidance does not state an enforced payload-size ceiling. Check the configuration and deployment you plan to use",
      },
    },
  },
  {
    id: "ops",
    sources: { hookway: repoBlob("docs/reference/configuration.md#metrics"), hookdeck: "https://hookdeck.com/docs/metrics", convoy: "https://www.getconvoy.io/docs/product-manual/metrics" },
    capability: "Operator observability",
    values: {
      hookway: {
        level: "yes",
        detail: "Prometheus metrics and in-repo Grafana dashboard",
      },
      hookdeck: {
        level: "yes",
        detail: "Managed observability in the Event Gateway product",
      },
      convoy: {
        level: "yes",
        detail: "Prometheus metrics; dashboards in the product",
      },
    },
  },
];

export const COMPARE_OUTPOST_NOTE = {
  title: "About Hookdeck Outpost",
  body: "Hookdeck Outpost is a separate outbound delivery service with 11 destination types, including SQS, S3, EventBridge, Kinesis, RabbitMQ, Kafka, and webhooks. It sends your application’s events to customer destinations. Hookway receives third-party webhooks into your infrastructure. This table compares Hookway with Event Gateway; it does not attribute Outpost’s features to Event Gateway.",
  href: "https://hookdeck.com/docs/outpost/overview",
  label: "Hookdeck Outpost overview",
} as const;

export const COMPARE_SOURCES = [
  {
    href: "https://hookdeck.com/docs/destinations",
    label: "Hookdeck Destinations",
  },
  {
    href: "https://hookdeck.com/docs/transformations",
    label: "Hookdeck Transformations",
  },
  { href: "https://hookdeck.com/docs/limits", label: "Hookdeck Limits" },
  { href: "https://hookdeck.com/docs/events", label: "Hookdeck Events" },
  { href: "https://hookdeck.com/docs/retries", label: "Hookdeck Retries" },
  { href: "https://hookdeck.com/docs/requests", label: "Hookdeck Requests" },
  {
    href: "https://hookdeck.com/event-gateway",
    label: "Hookdeck Event Gateway",
  },
  {
    href: "https://hookdeck.com/docs/outpost/overview",
    label: "Hookdeck Outpost",
  },
  {
    href: "https://www.getconvoy.io/docs/home/introduction",
    label: "Convoy introduction",
  },
  {
    href: "https://www.getconvoy.io/docs/product-manual/sources",
    label: "Convoy Sources",
  },
  {
    href: "https://www.getconvoy.io/docs/product-manual/functions",
    label: "Convoy Functions",
  },
  {
    href: "https://www.getconvoy.io/docs/product-manual/metrics",
    label: "Convoy Metrics",
  },
  {
    href: "https://www.getconvoy.io/docs/product-manual/events-and-event-deliveries",
    label: "Convoy events and event deliveries",
  },
  {
    href: "https://www.getconvoy.io/docs/api-reference/event-deliveries/retry-event-delivery",
    label: "Convoy retry event delivery",
  },
  {
    href: "https://www.getconvoy.io/docs/glossary/retry-schedule",
    label: "Convoy retry schedule",
  },
  {
    href: "https://www.getconvoy.io/docs/product-manual/retention-policy",
    label: "Convoy retention policy",
  },
  { href: repoBlob("docs/operations/replay-service.md"), label: "Hookway replay service" },
  { href: GITHUB_REPO, label: "Hookway repository" },
] as const;
