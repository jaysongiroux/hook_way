// Operational defaults; Go tests check the quoted counts against their sources.
// Performance measurements describe a dated workload, not a capacity guarantee.

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
  "Measured: 5,000 requests/s in three 20-second bursts on two replicas, each with 2 vCPU and 4 GiB, against a synthetic destination (2026-10-01 ingress benchmark; shared protection and replay require separate sizing).";

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
    value: "68",
    valueLabel: "core Prometheus metrics exported",
    line: "Enable Prometheus metrics to track requests, failures, and delivery latency. Grafana dashboards are included.",
  },
  {
    id: "controls",
    title: "Set access rules for each flow",
    value: "14",
    valueLabel: "authentication formats",
    line: "Reject invalid senders and duplicates. Use memory for one replica, or Redis/PostgreSQL for shared protection.",
  },
  {
    id: "packaging",
    title: "Core runs as one binary",
    value: "1",
    valueLabel: "static binary, non-root",
    line: "A scratch image with outbound TLS 1.2 or newer. Add Redis or PostgreSQL for shared protection. No replay service is required unless you add replay.",
  },
  {
    id: "replay",
    title: "Recover failures and inspect webhooks",
    value: "7",
    valueLabel: "days of replay retention by default",
    line: "Use the optional replay console to retry failed deliveries after a sender’s 2xx, or spot-check webhooks with view-only inspections.",
  },
];
