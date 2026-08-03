import type { Connection, Edge } from "@xyflow/react";
import type { HookwayNode, NodeKind } from "./model";

// Which target kinds each source kind may connect to. An endpoint may route
// directly to a destination or through one or more filters; filters may chain
// and then reach a destination; a destination is a sink.
const ALLOWED_TARGETS: Record<NodeKind, NodeKind[]> = {
  endpoint: ["filter", "destination"],
  filter: ["filter", "destination"],
  destination: [],
};

// Note: duplicate source->target pairs are intentionally allowed — parallel
// routes (same endpoint to the same destination via different target queues)
// are legal config. `_edges` is unused but kept for API stability.
export function isValidConnection(
  c: Connection | Edge,
  nodes: HookwayNode[],
  _edges: Edge[],
): boolean {
  if (!c.source || !c.target || c.source === c.target) return false;
  const src = nodes.find((n) => n.id === c.source);
  const tgt = nodes.find((n) => n.id === c.target);
  if (!src || !tgt) return false;
  return ALLOWED_TARGETS[src.data.kind].includes(tgt.data.kind);
}
