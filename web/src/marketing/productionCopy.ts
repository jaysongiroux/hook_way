import { DARK } from "../tokens";

// Copy for the production bento. Each tile headlines one verifiable number and
// one line; the long form stays in docs/ so there is a single source of truth.
//
// Nothing here is aspirational. Items listed as gaps in
// docs/production-readiness.md are deliberately absent: no readiness drain, no
// durable ingest spool, no automatic re-drive, no config hot reload, no scoped
// admin tokens, no OpenTelemetry export, and no throughput claim.

export interface ProductionCard {
  id: string;
  title: string;
  /** The large figure on the tile. */
  value: string;
  /** What the figure counts. */
  valueLabel: string;
  /** One supporting sentence. */
  line: string;
  accent: string;
}

export const PRODUCTION_CARDS: ProductionCard[] = [
  {
    id: "load",
    title: "Handles load predictably",
    value: "256",
    valueLabel: "default in-flight ceiling",
    line: "Excess requests get a 503 and Retry-After before the body is ever buffered.",
    accent: DARK.accent,
  },
  {
    id: "retries",
    title: "Retries, then dead-letters",
    value: "3",
    valueLabel: "webhook attempts, 500ms to 30s",
    line: "Webhook, Kafka and RabbitMQ back off, honour a 429, then dead-letter.",
    accent: "#7070FF",
  },
  {
    id: "metrics",
    title: "Metrics on day one",
    value: "42",
    valueLabel: "Prometheus metrics exported",
    line: "With a Grafana dashboard in the repo that a test keeps matching the metric names.",
    accent: "#25BE8A",
  },
  {
    id: "controls",
    title: "Controls per endpoint",
    value: "11",
    valueLabel: "signature schemes",
    line: "Verification fails closed, and rate limits and replay protection are set per endpoint.",
    accent: DARK.highlight,
  },
  {
    id: "replay",
    title: "Replay what happened",
    value: "4",
    valueLabel: "replay API routes",
    line: "Accepted events land in Postgres and re-deliver one at a time or in bulk.",
    accent: "#35B2AD",
  },
  {
    id: "packaging",
    title: "Ships as one binary",
    value: "1",
    valueLabel: "static binary, non-root",
    line: "A scratch image with a TLS 1.2 floor wherever a destination speaks TLS.",
    accent: "#A3B4D3",
  },
];
