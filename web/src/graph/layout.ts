import dagre from "@dagrejs/dagre";
import type { Edge } from "@xyflow/react";
import type { HookwayNode } from "./model";

const NODE_W = 170;
const NODE_H = 70;

export function layout(nodes: HookwayNode[], edges: Edge[]): HookwayNode[] {
  const g = new dagre.graphlib.Graph();
  g.setGraph({ rankdir: "LR", nodesep: 40, ranksep: 90 });
  g.setDefaultEdgeLabel(() => ({}));
  for (const n of nodes) g.setNode(n.id, { width: NODE_W, height: NODE_H });
  for (const e of edges) g.setEdge(e.source, e.target);
  dagre.layout(g);
  return nodes.map((n) => {
    const pos = g.node(n.id);
    return pos ? { ...n, position: { x: pos.x - NODE_W / 2, y: pos.y - NODE_H / 2 } } : n;
  });
}
