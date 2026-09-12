// The fan-out diagram shown on the overview, kept as data so the SVG and the
// narrow-screen card list render from one source of truth.
//
// The shape mirrors what Hookway actually supports: route-level filters gate
// individual branches, so a route whose chain fails is dropped while the other
// routes still deliver. See examples/filters.yaml.

export type FlowKind = "endpoint" | "filter" | "destination";

export interface FlowNode {
  id: string;
  kind: FlowKind;
  /** Primary line inside the card. */
  label: string;
  /** Detail line; unique across nodes so tests and readers can tell them apart. */
  sub: string;
  /** Hometap accent used for the card's top bar. */
  accent: string;
  column: number;
  row: number;
}

export interface FlowEdge {
  from: string;
  to: string;
}

export const LAYOUT = {
  width: 900,
  height: 280,
  nodeW: 200,
  nodeH: 60,
  colX: [8, 350, 692],
  rowY: [10, 110, 210],
} as const;

export const FLOW_NODES: FlowNode[] = [
  {
    id: "ep",
    kind: "endpoint",
    label: "POST /webhooks/github",
    sub: "verified with hmac_sha256",
    accent: "#366CED",
    column: 0,
    row: 1,
  },
  { id: "f1", kind: "filter", label: "Filter", sub: "X-GitHub-Event = push", accent: "#7070FF", column: 1, row: 0 },
  {
    id: "f2",
    kind: "filter",
    label: "Filter",
    sub: "repository.private = false",
    accent: "#7070FF",
    column: 1,
    row: 2,
  },
  { id: "sqs", kind: "destination", label: "Amazon SQS", sub: "orders", accent: "#25BE8A", column: 2, row: 0 },
  {
    id: "rabbit",
    kind: "destination",
    label: "RabbitMQ",
    sub: "github-events",
    accent: "#434C5E",
    column: 2,
    row: 1,
  },
  { id: "s3", kind: "destination", label: "Amazon S3", sub: "raw/github", accent: "#70DAFF", column: 2, row: 2 },
];

export const FLOW_EDGES: FlowEdge[] = [
  { from: "ep", to: "f1" },
  { from: "ep", to: "rabbit" },
  { from: "ep", to: "f2" },
  { from: "f1", to: "sqs" },
  { from: "f2", to: "s3" },
];

export interface Box {
  x: number;
  y: number;
  w: number;
  h: number;
}

/** nodeBox resolves a node's column and row to its rectangle in the viewBox. */
export function nodeBox(n: FlowNode): Box {
  return { x: LAYOUT.colX[n.column], y: LAYOUT.rowY[n.row], w: LAYOUT.nodeW, h: LAYOUT.nodeH };
}

/**
 * edgePath draws a horizontal bezier from the source's right edge to the
 * target's left edge, the same S-curve the builder's canvas uses.
 */
export function edgePath(from: FlowNode, to: FlowNode): string {
  const a = nodeBox(from);
  const b = nodeBox(to);
  const x1 = a.x + a.w;
  const y1 = a.y + a.h / 2;
  const x2 = b.x;
  const y2 = b.y + b.h / 2;
  const mx = (x1 + x2) / 2;
  return `M ${x1} ${y1} C ${mx} ${y1} ${mx} ${y2} ${x2} ${y2}`;
}

/** A sentence describing the flow, used as the diagram's accessible name. */
export function flowDescription(): string {
  const dests = FLOW_NODES.filter((n) => n.kind === "destination").map((n) => n.label);
  return `One endpoint fanning out to ${dests.join(", ")}, with route-level filters on two of the three branches.`;
}
