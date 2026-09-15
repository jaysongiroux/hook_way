import { DEST_TYPES } from "../graph/destTypes";

export interface Stat {
  /** Sentence case, no trailing colon. */
  label: string;
  value: number;
  /** What was counted, so a reader can go and check it. */
  detail: string;
}

// Countable facts only. This project has never been load-tested, so there is
// no honest latency, uptime, or events-per-second number to headline with; a
// count of what ships is verifiable and does not expire.
export const STATS: Stat[] = [
  { label: "Destination types", value: DEST_TYPES.length, detail: "queues, streams, stores, and services" },
  { label: "Prometheus metrics", value: 45, detail: "exported by default, no exporter to add" },
  { label: "Signature schemes", value: 11, detail: "across hmac, jwt, and header auth" },
  { label: "Filter operators", value: 12, detail: "on any header or body field" },
];
