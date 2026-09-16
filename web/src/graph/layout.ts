import dagre from "@dagrejs/dagre";
import type { Edge } from "@xyflow/react";
import { NODE_SCHEMAS } from "./nodeSchema";
import type { FlowSnapshot, GraphDocument, HookwayNode } from "./model";

const NODE_W = 180;
const NODE_H = 78;

const PORT_RANK: Record<string, number> = {
  None: 0,
  RequestHead: 1,
  RawRequest: 2,
  Verified: 3,
  Envelope: 4,
  Terminal: 5,
};

export function layout(nodes: HookwayNode[], edges: Edge[]): HookwayNode[] {
  const g = new dagre.graphlib.Graph();
  g.setGraph({ rankdir: "LR", nodesep: 48, ranksep: 110 });
  g.setDefaultEdgeLabel(() => ({}));
  for (const n of nodes) {
    const meta = NODE_SCHEMAS[n.data.nodeType];
    const rank = PORT_RANK[meta?.output ?? "Envelope"] ?? 3;
    g.setNode(n.id, { width: NODE_W, height: NODE_H, rank });
  }
  for (const e of edges) g.setEdge(e.source, e.target);
  dagre.layout(g);
  return nodes.map((n) => {
    const pos = g.node(n.id);
    return pos ? { ...n, position: { x: pos.x - NODE_W / 2, y: pos.y - NODE_H / 2 } } : n;
  });
}

/** True when every node shares one position — the YAML import default of (0,0). */
export function nodesStacked(nodes: HookwayNode[]): boolean {
  if (nodes.length < 2) return false;
  const { x, y } = nodes[0].position;
  return nodes.every((n) => n.position.x === x && n.position.y === y);
}

export function maybeLayout(nodes: HookwayNode[], edges: Edge[]): HookwayNode[] {
  return nodesStacked(nodes) ? layout(nodes, edges) : nodes;
}

function layoutSnapshot(snap: FlowSnapshot): FlowSnapshot {
  return { ...snap, nodes: layout(snap.nodes, snap.edges) };
}

export function layoutAllFlows(doc: GraphDocument): GraphDocument {
  const otherFlows: Record<string, FlowSnapshot> = {};
  for (const [name, snap] of Object.entries(doc.otherFlows)) {
    otherFlows[name] = layoutSnapshot(snap);
  }
  return { ...doc, nodes: layout(doc.nodes, doc.edges), otherFlows };
}
