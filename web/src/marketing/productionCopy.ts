// Copy for the production bento. Each tile headlines one verifiable number and
// one line; the long form stays in docs/ so there is a single source of truth.
//
// Nothing here is aspirational. Items listed as gaps in
// docs/production-readiness.md are deliberately absent: no readiness drain, no
// durable ingest spool, no automatic re-drive, no config hot reload, no scoped
// admin tokens, no OpenTelemetry export, and no throughput claim.
//
// Write for the reader, not the code: "the sender already got its 2xx" says
// what "an acknowledged failure" means to someone who runs webhooks.

export interface ProductionCard {
  id: string;
  title: string;
  /** The large figure on the tile. */
  value: string;
  /** What the figure counts. */
  valueLabel: string;
  /** One supporting sentence. */
  line: string;
}

export const PRODUCTION_CARDS: ProductionCard[] = [
  {
    id: "load",
    title: "Bounds in-flight work",
    value: "256",
    valueLabel: "default in-flight ceiling",
    line: "Excess requests get a 503 and Retry-After before the body is buffered.",
  },
  {
    id: "retries",
    title: "Retries, then optional dead-letter",
    value: "3",
    valueLabel: "webhook attempts, 500ms to 30s",
    line: "Webhook, Kafka and RabbitMQ retry with backoff; only webhook honours a 429 or dead-letters a failed publish.",
  },
  {
    id: "metrics",
    title: "Exports Prometheus metrics",
    value: "45",
    valueLabel: "Prometheus metrics exported",
    line: "With a Grafana dashboard in the repo whose key panels are pinned to the metric names by a test.",
  },
  {
    id: "controls",
    title: "Scopes controls per flow",
    value: "11",
    valueLabel: "verification schemes",
    line: "Verification fails closed; rate limits and replay protection are set per flow.",
  },
  {
    id: "packaging",
    title: "Ships as one binary",
    value: "1",
    valueLabel: "static binary, non-root",
    line: "A scratch image; outbound TLS is 1.2 or newer. Nothing else to run unless you add replay.",
  },
  {
    id: "replay",
    title: "Keeps what a destination missed",
    value: "7",
    valueLabel: "days a missed delivery is kept",
    line: "The sender already got its 2xx, so the optional replay service holds the event for you to retry.",
  },
];
