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
import { layout, maybeLayout } from "./layout";
import { emptyDocument, seedCounters, type GraphDocument, type HookwayNode, type FlowSnapshot } from "./model";
import { offsetSubgraph, renameSubgraph } from "./subgraph";
import type { TemplateSubgraph } from "./templates";

let edgeSeq = 0;

interface GraphState extends GraphDocument {
  selectedId: string | null;
  selectedEdgeId: string | null;
  onNodesChange: (changes: NodeChange[]) => void;
  onEdgesChange: (changes: EdgeChange[]) => void;
  onConnect: (c: Connection) => void;
  addNode: (n: HookwayNode) => void;
  insertSubgraph: (sub: TemplateSubgraph, position: { x: number; y: number }) => void;
  updateNodeData: (id: string, patch: Record<string, unknown>) => void;
  updateEdgeData: (id: string, patch: Record<string, unknown>) => void;
  setServerConfig: (patch: Record<string, unknown>) => void;
  setConnections: (connections: Record<string, unknown>) => void;
  setDestinations: (destinations: Record<string, unknown>) => void;
  setDeliveryFailurePolicy: (value: string) => void;
  setSelected: (id: string | null) => void;
  setSelectedEdge: (id: string | null) => void;
  setGraph: (doc: GraphDocument) => void;
  setActiveFlow: (name: string) => void;
  addFlow: (name: string) => boolean;
  renameFlow: (name: string) => void;
  arrangeNodes: () => void;
  arrangeSeq: number;
  document: () => GraphDocument;
  clear: () => void;
}

function snapshotOf(s: Pick<GraphState, "nodes" | "edges" | "deliveryFailurePolicy" | "flowExtra">): FlowSnapshot {
  return { nodes: s.nodes, edges: s.edges, deliveryFailurePolicy: s.deliveryFailurePolicy, flowExtra: s.flowExtra };
}

export const useGraphStore = create<GraphState>((set, get) => ({
  ...emptyDocument(),
  selectedId: null,
  selectedEdgeId: null,
  arrangeSeq: 0,
  onNodesChange: (changes) => set({ nodes: applyNodeChanges(changes, get().nodes) as HookwayNode[] }),
  onEdgesChange: (changes) => set({ edges: applyEdgeChanges(changes, get().edges) }),
  onConnect: (c) => {
    const { nodes, edges } = get();
    if (!isValidConnection(c, nodes, edges)) return;
    const edge: Edge = {
      id: `e${(edgeSeq += 1)}`,
      source: c.source as string,
      target: c.target as string,
      sourceHandle: c.sourceHandle ?? null,
      targetHandle: c.targetHandle ?? null,
    };
    set({ edges: [...edges, edge] });
  },
  addNode: (n) => set({ nodes: [...get().nodes, n] }),
  // insertSubgraph drops a palette template onto the canvas. Ids are renamed
  // against everything already in this flow (a second insert of the same
  // template would otherwise export duplicate `id:` keys), and positions are
  // translated so the template lands where it was dropped instead of on top
  // of the existing graph.
  insertSubgraph: (sub, position) => {
    const { nodes, edges } = get();
    const taken = new Set(nodes.flatMap((n) => [n.id, String(n.data.yamlId ?? n.id)]));
    const placed = offsetSubgraph(renameSubgraph(sub, taken), position);
    set({ nodes: [...nodes, ...placed.nodes], edges: [...edges, ...placed.edges] });
  },
  updateNodeData: (id, patch) =>
    set({
      nodes: get().nodes.map((n) =>
        n.id === id
          ? ({
              ...n,
              data: {
                ...n.data,
                ...patch,
                options:
                  patch.options && typeof patch.options === "object"
                    ? (patch.options as Record<string, unknown>)
                    : n.data.options,
              },
            } as HookwayNode)
          : n,
      ),
    }),
  updateEdgeData: (id, patch) =>
    set({
      edges: get().edges.map((e) => (e.id === id ? { ...e, data: { ...(e.data ?? {}), ...patch } } : e)),
    }),
  setServerConfig: (patch) => {
    const merged = { ...get().serverConfig, ...patch };
    const pruned: Record<string, unknown> = {};
    for (const [k, v] of Object.entries(merged)) if (v !== "" && v !== null && v !== undefined) pruned[k] = v;
    set({ serverConfig: pruned });
  },
  setConnections: (connections) => set({ connections }),
  setDestinations: (destinations) => set({ destinations }),
  setDeliveryFailurePolicy: (value) => set({ deliveryFailurePolicy: value || undefined }),
  setSelected: (id) => set({ selectedId: id, selectedEdgeId: null }),
  setSelectedEdge: (id) => set({ selectedEdgeId: id, selectedId: null }),
  setGraph: (doc) => {
    seedCounters(doc.nodes);
    const otherFlows: Record<string, FlowSnapshot> = {};
    for (const [name, snap] of Object.entries(doc.otherFlows ?? {})) {
      otherFlows[name] = { ...snap, nodes: maybeLayout(snap.nodes, snap.edges) };
    }
    set({
      ...emptyDocument(),
      ...doc,
      nodes: maybeLayout(doc.nodes, doc.edges),
      otherFlows,
      selectedId: null,
      selectedEdgeId: null,
    });
  },
  setActiveFlow: (name) => {
    const s = get();
    if (name === s.activeFlow) return;
    const current = snapshotOf(s);
    const other = { ...s.otherFlows, [s.activeFlow]: current };
    const next = other[name] ?? { nodes: [], edges: [] };
    const rest = { ...other };
    delete rest[name];
    const nodes = maybeLayout(next.nodes, next.edges);
    seedCounters(nodes);
    set({
      activeFlow: name,
      otherFlows: rest,
      nodes,
      edges: next.edges,
      deliveryFailurePolicy: next.deliveryFailurePolicy,
      flowExtra: next.flowExtra,
      selectedId: null,
      selectedEdgeId: null,
    });
  },
  arrangeNodes: () => {
    const { nodes, edges, arrangeSeq } = get();
    if (nodes.length === 0) return;
    set({ nodes: layout(nodes, edges), arrangeSeq: arrangeSeq + 1 });
  },
  addFlow: (name) => {
    const trimmed = name.trim();
    const s = get();
    if (!trimmed || trimmed === s.activeFlow || s.otherFlows[trimmed]) return false;
    set({
      otherFlows: { ...s.otherFlows, [s.activeFlow]: snapshotOf(s) },
      activeFlow: trimmed,
      nodes: [],
      edges: [],
      deliveryFailurePolicy: undefined,
      flowExtra: undefined,
      selectedId: null,
      selectedEdgeId: null,
    });
    return true;
  },
  renameFlow: (name) => {
    const trimmed = name.trim();
    const s = get();
    if (!trimmed || trimmed === s.activeFlow || s.otherFlows[trimmed]) return;
    set({ activeFlow: trimmed });
  },
  document: () => {
    const s = get();
    return {
      nodes: s.nodes,
      edges: s.edges,
      serverConfig: s.serverConfig,
      configExtra: s.configExtra,
      connections: s.connections,
      destinations: s.destinations,
      activeFlow: s.activeFlow,
      otherFlows: s.otherFlows,
      deliveryFailurePolicy: s.deliveryFailurePolicy,
      flowExtra: s.flowExtra,
    };
  },
  clear: () => set({ ...emptyDocument(), selectedId: null, selectedEdgeId: null }),
}));
