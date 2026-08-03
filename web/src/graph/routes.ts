import type { Edge } from "@xyflow/react";
import type { HookwayNode } from "./model";

export interface RouteRow {
  edgeId: string;
  dest: string;
  target: string;
  create: boolean;
}

export function endpointRoutes(
  endpointId: string,
  nodes: HookwayNode[],
  edges: Edge[],
): RouteRow[] {
  const byId = new Map(nodes.map((n) => [n.id, n]));
  const rows: RouteRow[] = [];
  const visited = new Set<string>();
  const walk = (id: string) => {
    if (visited.has(id)) return;
    visited.add(id);
    for (const edge of edges.filter((x) => x.source === id)) {
      const tgt = byId.get(edge.target);
      if (!tgt) continue;
      if (tgt.data.kind === "destination") {
        const d = (edge.data ?? {}) as { target?: string; create?: boolean };
        rows.push({
          edgeId: edge.id,
          dest: String(tgt.data.name),
          target: String(d.target ?? ""),
          create: d.create ?? true,
        });
      } else if (tgt.data.kind === "filter") {
        walk(edge.target);
      }
    }
  };
  walk(endpointId);
  return rows;
}

export function parallelGroups(edges: Edge[]): Map<string, { index: number; count: number }> {
  const byKey = new Map<string, string[]>();
  for (const e of edges) {
    const key = `${e.source}->${e.target}`;
    const ids = byKey.get(key);
    if (ids) ids.push(e.id);
    else byKey.set(key, [e.id]);
  }
  const out = new Map<string, { index: number; count: number }>();
  for (const ids of byKey.values()) {
    ids.forEach((id, index) => out.set(id, { index, count: ids.length }));
  }
  return out;
}

export function owningEndpointId(
  edgeId: string,
  nodes: HookwayNode[],
  edges: Edge[],
): string | null {
  const byId = new Map(nodes.map((n) => [n.id, n]));
  const start = edges.find((e) => e.id === edgeId);
  if (!start) return null;
  let cur: string | undefined = start.source;
  const seen = new Set<string>();
  while (cur && !seen.has(cur)) {
    seen.add(cur);
    const n = byId.get(cur);
    if (!n) return null;
    if (n.data.kind === "endpoint") return cur;
    cur = edges.find((e) => e.target === cur)?.source;
  }
  return null;
}
