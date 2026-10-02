// Copy for the production bento. Each tile headlines one verifiable number and
// one line; the long form stays in docs/ so there is a single source of truth.
//
// Nothing here is aspirational. Items listed as gaps in
// docs/production-readiness.md are deliberately absent: no readiness withdrawal, no
// durable ingest spool, no automatic re-drive, no config hot reload, no scoped
// core admin tokens, or OpenTelemetry export. throughput measurements
// carry their workload and synthetic-destination qualification separately.
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

export const PERFORMANCE_SUMMARY =
  "Measured: 5,000 requests/s in three 20-second bursts on two replicas, each with 2 vCPU and 4 GiB, against a synthetic destination.";

export const PRODUCTION_CARDS: ProductionCard[] = [
  {
    id: "load",
    title: "Limit simultaneous requests",
    value: "256",
    valueLabel: "active requests per replica by default",
    line: "At capacity, new requests get 503 and Retry-After before their bodies are read. You can enable a short wait for capacity.",
  },
  {
    id: "retries",
    title: "Retry failed deliveries",
    value: "3",
    valueLabel: "webhook attempts; waits from 500ms to 30s",
    line: "Webhook, Kafka and RabbitMQ retry with increasing waits. Only webhook handles 429 Retry-After or sends failures to an optional dead-letter destination.",
  },
  {
    id: "metrics",
    title: "Exports Prometheus metrics",
    value: "55",
    valueLabel: "Prometheus metrics exported",
    line: "Enable Prometheus metrics to track requests, failures, and delivery latency. The repository includes a Grafana dashboard.",
  },
  {
    id: "controls",
    title: "Set access rules for each flow",
    value: "13",
    valueLabel: "authentication formats",
    line: "Reject invalid credentials, limit request rates, and block duplicate requests. Rate limits and duplicate caches are local to each replica.",
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
