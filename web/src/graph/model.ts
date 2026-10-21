import type { Edge, Node } from "@xyflow/react";
import { defaultNodeOptions, isNodeType, NODE_TYPES } from "./nodeSchema";

export const DEFAULT_SERVER: Record<string, unknown> = {
  addr: ":8080",
  max_body_bytes: 1048576,
};
export const DEFAULT_FLOW = "main";

export type ConfigPath = (string | number)[];

interface FlowNodeData extends Record<string, unknown> {
  /** Imported strings are literals at deployment; never written as runtime options. */
  literalDollarPaths?: ConfigPath[];
  nodeType: string;
  options: Record<string, unknown>;
  yamlId: string;
  /** Operator note exported as the node's `description:` field. */
  description?: string;
}

export type HookwayNode = Node<FlowNodeData>;

export interface FlowSnapshot {
  nodes: HookwayNode[];
  edges: Edge[];
  deliveryFailurePolicy?: string;
  /** Flow keys the builder does not model (e.g. capture_failures), written back unchanged. */
  flowExtra?: Record<string, unknown>;
}

export interface EditorDraft {
  text?: string;
  rows?: { id: string; key: string; value: string }[];
  rowIds?: string[];
  error: string;
}

export interface GraphDocument {
  literalConfigPaths?: ConfigPath[];
  yamlMode?: "single" | "split";
  entryFilename?: string;
  flowFiles?: Record<string, string>;
  replayFilename?: string;
  /** Editor-only drafts; persisted locally, never serialized into YAML. */
  formDrafts?: Record<string, EditorDraft>;
  nodes: HookwayNode[];
  edges: Edge[];
  serverConfig: Record<string, unknown>;
  /** Core's optional top-level replay: connection/control block. */
  coreReplayConfig: Record<string, unknown> | null;
  /** The separate hookway-replay document; null keeps replay opt-in. */
  replayServiceConfig: Record<string, unknown> | null;
  configExtra: Record<string, unknown>;
  connections: Record<string, unknown>;
  destinations: Record<string, unknown>;
  activeFlow: string;
  otherFlows: Record<string, FlowSnapshot>;
  deliveryFailurePolicy?: string;
  /** Flow keys the builder does not model (e.g. capture_failures), written back unchanged. */
  flowExtra?: Record<string, unknown>;
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

export function createNode(
  type: string,
  position: { x: number; y: number },
): HookwayNode {
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
    literalConfigPaths: [],
    formDrafts: {},
    yamlMode: "single",
    entryFilename: "core.yaml",
    flowFiles: {},
    replayFilename: "replay.yaml",
    nodes: [],
    edges: [],
    serverConfig: { ...DEFAULT_SERVER },
    coreReplayConfig: null,
    replayServiceConfig: null,
    configExtra: {},
    connections: {},
    destinations: {},
    activeFlow: DEFAULT_FLOW,
    otherFlows: {},
    // Explicit, so loading or clearing a document resets the previous
    // flow's values in the store (zustand merges state shallowly).
    deliveryFailurePolicy: undefined,
    flowExtra: undefined,
  };
}

export function yamlIdOf(node: HookwayNode): string {
  return String(node.data.yamlId || node.id);
}
