import { create } from "zustand";
import {
  applyEdgeChanges,
  applyNodeChanges,
  type Connection,
  type Edge,
  type EdgeChange,
  type NodeChange,
} from "@xyflow/react";
import { isValidConnection } from "./connection";
import { seedCounters, DEFAULT_SERVER, type HookwayNode } from "./model";

let edgeSeq = 0;

// Finds a target queue name that doesn't already collide with another edge
// pointing at the same destination node, so parallel routes get distinct
// default target names (e.g. "dest", "dest-2", "dest-3", ...).
function uniqueTarget(base: string, destNodeId: string, edges: Edge[]): string {
  const taken = new Set(
    edges
      .filter((e) => e.target === destNodeId)
      .map((e) => String((e.data as { target?: string } | undefined)?.target ?? "")),
  );
  if (!taken.has(base)) return base;
  for (let i = 2; ; i++) {
    const candidate = `${base}-${i}`;
    if (!taken.has(candidate)) return candidate;
  }
}

interface GraphState {
  nodes: HookwayNode[];
  edges: Edge[];
  // Whole `server:` block (managed keys the Server panel edits + any unknown
  // keys ride along untouched) and the other top-level config blocks the graph
  // doesn't model (security, rate_limit, metrics, logging, replay, ...). Both
  // are preserved verbatim across import→export.
  serverConfig: Record<string, unknown>;
  configExtra: Record<string, unknown>;
  selectedId: string | null;
  selectedEdgeId: string | null;
  onNodesChange: (changes: NodeChange[]) => void;
  onEdgesChange: (changes: EdgeChange[]) => void;
  onConnect: (c: Connection) => void;
  addNode: (n: HookwayNode) => void;
  updateNodeData: (id: string, patch: Record<string, unknown>) => void;
  updateEdgeData: (id: string, patch: Record<string, unknown>) => void;
  setServerConfig: (patch: Record<string, unknown>) => void;
  setSelected: (id: string | null) => void;
  setSelectedEdge: (id: string | null) => void;
  setGraph: (
    nodes: HookwayNode[],
    edges: Edge[],
    serverConfig?: Record<string, unknown>,
    configExtra?: Record<string, unknown>,
  ) => void;
  clear: () => void;
}

export const useGraphStore = create<GraphState>((set, get) => ({
  nodes: [],
  edges: [],
  serverConfig: { ...DEFAULT_SERVER },
  configExtra: {},
  selectedId: null,
  selectedEdgeId: null,
  onNodesChange: (changes) =>
    set({ nodes: applyNodeChanges(changes, get().nodes) as HookwayNode[] }),
  onEdgesChange: (changes) => set({ edges: applyEdgeChanges(changes, get().edges) }),
  onConnect: (c) => {
    const { nodes, edges } = get();
    if (!isValidConnection(c, nodes, edges)) return;
    const dest = nodes.find((n) => n.id === c.target);
    const base = dest ? String(dest.data.name) : "";
    const edge: Edge = {
      id: `e${(edgeSeq += 1)}`,
      source: c.source as string,
      target: c.target as string,
      sourceHandle: c.sourceHandle ?? null,
      targetHandle: c.targetHandle ?? null,
      data: { target: uniqueTarget(base, c.target as string, edges), create: true },
    };
    set({ edges: [...edges, edge] });
  },
  addNode: (n) => set({ nodes: [...get().nodes, n] }),
  updateNodeData: (id, patch) =>
    set({
      nodes: get().nodes.map((n) =>
        n.id === id ? ({ ...n, data: { ...n.data, ...patch } } as HookwayNode) : n,
      ),
    }),
  updateEdgeData: (id, patch) =>
    set({
      edges: get().edges.map((e) =>
        e.id === id ? { ...e, data: { ...(e.data ?? {}), ...patch } } : e,
      ),
    }),
  setServerConfig: (patch) => {
    // Merge then drop cleared fields so the exported server block never carries
    // empty values (a blank duration/number would export as "" and misvalidate).
    const merged = { ...get().serverConfig, ...patch };
    const pruned: Record<string, unknown> = {};
    for (const [k, v] of Object.entries(merged)) if (v !== "" && v !== null && v !== undefined) pruned[k] = v;
    set({ serverConfig: pruned });
  },
  setSelected: (id) => set({ selectedId: id, selectedEdgeId: null }),
  setSelectedEdge: (id) => set({ selectedEdgeId: id, selectedId: null }),
  setGraph: (nodes, edges, serverConfig, configExtra) => {
    seedCounters(nodes);
    set({
      nodes,
      edges,
      serverConfig: serverConfig ?? { ...DEFAULT_SERVER },
      configExtra: configExtra ?? {},
      selectedId: null,
      selectedEdgeId: null,
    });
  },
  clear: () =>
    set({
      nodes: [],
      edges: [],
      serverConfig: { ...DEFAULT_SERVER },
      configExtra: {},
      selectedId: null,
      selectedEdgeId: null,
    }),
}));
