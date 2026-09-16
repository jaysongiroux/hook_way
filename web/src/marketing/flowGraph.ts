// The fan-out diagram shown on the overview, kept as data so the SVG and the
// narrow-screen card list render from one source of truth.
//
// Node anatomy matches the builder: yaml id, type, summary, port handles.
// Topology is still the marketing story: route-level filters gate two of three
// publish branches. See examples/filters.yaml.

import { NODE_SCHEMAS, nodeHasOnError } from "../graph/nodeSchema";

export type FlowKind = "endpoint" | "filter" | "destination";

export interface FlowNode {
  id: string;
  kind: FlowKind;
  yamlId: string;
  nodeType: string;
  /** Detail line; unique across nodes so tests and readers can tell them apart. */
  summary: string;
  column: number;
  row: number;
}

export interface FlowEdge {
  from: string;
  to: string;
}

export const LAYOUT = {
  width: 900,
  height: 340,
  nodeW: 200,
  nodeH: 90,
  nodeHShort: 68,
  handle: 5,
  colX: [12, 350, 688],
  rowY: [16, 126, 236],
} as const;

export const FLOW_NODES: FlowNode[] = [
  {
    id: "ep",
    kind: "endpoint",
    yamlId: "in",
    nodeType: "http_trigger",
    summary: "/webhooks/github",
    column: 0,
    row: 1,
  },
  {
    id: "f1",
    kind: "filter",
    yamlId: "pushes",
    nodeType: "filter",
    summary: "X-GitHub-Event = push",
    column: 1,
    row: 0,
  },
  {
    id: "f2",
    kind: "filter",
    yamlId: "public",
    nodeType: "filter",
    summary: "repository.private = false",
    column: 1,
    row: 2,
  },
  {
    id: "sqs",
    kind: "destination",
    yamlId: "orders",
    nodeType: "publish",
    summary: "orders → github-events",
    column: 2,
    row: 0,
  },
  {
    id: "rabbit",
    kind: "destination",
    yamlId: "stream",
    nodeType: "publish",
    summary: "events → github-pushes",
    column: 2,
    row: 1,
  },
  {
    id: "s3",
    kind: "destination",
    yamlId: "archive",
    nodeType: "publish",
    summary: "archive → raw/github",
    column: 2,
    row: 2,
  },
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

export interface NodeChrome {
  input: string;
  output: string;
  showIn: boolean;
  showOut: boolean;
  showError: boolean;
}

/** nodeChrome mirrors builder FlowNode handle rules from the catalog. */
export function nodeChrome(n: FlowNode): NodeChrome {
  const meta = NODE_SCHEMAS[n.nodeType];
  const input = meta?.input ?? "Envelope";
  const output = meta?.output ?? "Envelope";
  return {
    input,
    output,
    showIn: input !== "None",
    showOut: output !== "Terminal",
    showError: nodeHasOnError(n.nodeType),
  };
}

/** nodeBox resolves a node's column and row to its rectangle in the viewBox. */
export function nodeBox(n: FlowNode): Box {
  const h = nodeChrome(n).showError ? LAYOUT.nodeH : LAYOUT.nodeHShort;
  const y = LAYOUT.rowY[n.row] + (LAYOUT.nodeH - h) / 2;
  return { x: LAYOUT.colX[n.column], y, w: LAYOUT.nodeW, h };
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
  const dests = FLOW_NODES.filter((n) => n.kind === "destination").map((n) => n.yamlId);
  return `One flow fanning out to ${dests.join(", ")}, with route-level filters on two of the three branches.`;
}
