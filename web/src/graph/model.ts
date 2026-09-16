import type { Edge, Node } from "@xyflow/react";
import { defaultNodeOptions, isNodeType, NODE_TYPES } from "./nodeSchema";

export const DEFAULT_SERVER: Record<string, unknown> = { addr: ":8080", max_body_bytes: 1048576 };
export const DEFAULT_FLOW = "main";

export interface FlowNodeData extends Record<string, unknown> {
  nodeType: string;
  options: Record<string, unknown>;
  yamlId: string;
  notes?: string;
}

export type HookwayNode = Node<FlowNodeData>;
export type HookwayEdge = Edge;

export interface FlowSnapshot {
  nodes: HookwayNode[];
  edges: Edge[];
  deliveryFailurePolicy?: string;
}

export interface GraphDocument {
  nodes: HookwayNode[];
  edges: Edge[];
  serverConfig: Record<string, unknown>;
  configExtra: Record<string, unknown>;
  destinations: Record<string, unknown>;
  activeFlow: string;
  otherFlows: Record<string, FlowSnapshot>;
  deliveryFailurePolicy?: string;
}

const counters: Record<string, number> = {};

export function resetIdCounters(): void {
  for (const key of Object.keys(counters)) delete counters[key];
}

export function nextName(type: string): string {
  counters[type] = (counters[type] ?? 0) + 1;
  return `${type}_${counters[type]}`;
}

export function seedCounters(nodes: HookwayNode[]): void {
  resetIdCounters();
  const maxByType: Record<string, number> = {};
  for (const n of nodes) {
    const type = n.data.nodeType;
    const m = new RegExp(`^${type}_(\\d+)$`).exec(n.id);
    if (m) maxByType[type] = Math.max(maxByType[type] ?? 0, Number(m[1]));
  }
  for (const [type, max] of Object.entries(maxByType)) {
    counters[type] = max;
  }
}

export function isNodeKind(s: string): boolean {
  return isNodeType(s);
}

export function createNode(type: string, position: { x: number; y: number }): HookwayNode {
  const kind = isNodeType(type) ? type : NODE_TYPES[0];
  const name = nextName(kind);
  return {
    id: name,
    type: "flow",
    position,
    data: {
      nodeType: kind,
      yamlId: name,
      options: defaultNodeOptions(kind, name),
    },
  };
}

export function emptyDocument(): GraphDocument {
  return {
    nodes: [],
    edges: [],
    serverConfig: { ...DEFAULT_SERVER },
    configExtra: {},
    destinations: {},
    activeFlow: DEFAULT_FLOW,
    otherFlows: {},
  };
}

export function yamlIdOf(node: HookwayNode): string {
  return String(node.data.yamlId || node.id);
}
