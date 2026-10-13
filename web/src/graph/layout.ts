import dagre from "@dagrejs/dagre";
import type { Edge } from "@xyflow/react";
import { nodeMeta } from "./nodeSchema";
import type { FlowSnapshot, GraphDocument, HookwayNode } from "./model";
import { NODE_SIZE } from "../tokens";

// Gaps between dagre ranks (horizontal) and within a rank (vertical). ranksep
// has to leave room for an arrowhead in open canvas, not just avoid a collision.
const RANK_SEP = 140;
const NODE_SEP = 56;

const PORT_RANK: Record<string, number> = {
  None: 0,
  RequestHead: 1,
  RawRequest: 2,
  Verified: 3,
  Envelope: 4,
  Terminal: 5,
};

/**
 * sizeOf prefers the size React Flow measured after render and falls back to
 * the shared card geometry. Import runs before anything is on screen, so the
 * fallback is the common path; Arrange runs after, so a card that ever deviates
 * from NODE_SIZE (a long switch with many ports, say) still gets real room.
 */
function sizeOf(n: HookwayNode): { width: number; height: number } {
  return {
    width: n.measured?.width ?? NODE_SIZE.width,
    height: n.measured?.height ?? NODE_SIZE.height,
  };
}

export function layout(nodes: HookwayNode[], edges: Edge[]): HookwayNode[] {
  const g = new dagre.graphlib.Graph();
  g.setGraph({ rankdir: "LR", nodesep: NODE_SEP, ranksep: RANK_SEP });
  g.setDefaultEdgeLabel(() => ({}));
  for (const n of nodes) {
    const meta = nodeMeta(n.data.nodeType, n.data.options);
    const rank = PORT_RANK[meta?.output ?? "Envelope"] ?? 3;
    g.setNode(n.id, { ...sizeOf(n), rank });
  }
  for (const e of edges) g.setEdge(e.source, e.target);
  dagre.layout(g);
  return nodes.map((n) => {
    const pos = g.node(n.id);
    if (!pos) return n;
    // dagre reports centres; React Flow positions the top-left corner. Use this
    // node's own size, not a single constant, or a wide card lands off-centre.
    const { width, height } = sizeOf(n);
    return { ...n, position: { x: pos.x - width / 2, y: pos.y - height / 2 } };
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
