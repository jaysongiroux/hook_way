import type { Edge } from "@xyflow/react";
import type { HookwayNode } from "./model";
import type { TemplateSubgraph } from "./templates";

let insertSeq = 0;

/**
 * Suffixes a colliding id with _2, _3, … skipping any suffix already taken.
 * The scheme matches model.ts's nextName so inserted ids read like builder
 * ids, and — unlike fromConfig's internal `id__2` — stays comfortable as a
 * YAML `id:` key.
 */
function freeId(base: string, taken: Set<string>): string {
  if (!taken.has(base)) return base;
  for (let i = 2; ; i++) {
    const candidate = `${base}_${i}`;
    if (!taken.has(candidate)) return candidate;
  }
}

/**
 * Renames a subgraph's nodes so none collide with `taken`, rewriting its edges
 * to the new ids. Both the React Flow id and the exported yamlId are renamed
 * together: they are equal for a freshly inserted node, and toConfig writes
 * yamlId as the config's `id:`, so letting them diverge would export a
 * duplicate id.
 *
 * Edge ids get a per-insert sequence number. Template edge ids come from the
 * source YAML ("fmt>post"), so two inserts of the same template would
 * otherwise produce duplicate edge ids and React Flow would silently render
 * only one of them.
 */
export function renameSubgraph(sub: TemplateSubgraph, taken: Set<string>): TemplateSubgraph {
  const claimed = new Set(taken);
  const rename = new Map<string, string>();
  const nodes: HookwayNode[] = sub.nodes.map((n) => {
    const id = freeId(n.id, claimed);
    claimed.add(id);
    rename.set(n.id, id);
    return { ...n, id, data: { ...n.data, yamlId: id } };
  });
  const seq = (insertSeq += 1);
  const edges: Edge[] = sub.edges.map((e, i) => ({
    ...e,
    id: `t${seq}_${i}_${rename.get(e.source) ?? e.source}>${rename.get(e.target) ?? e.target}`,
    source: rename.get(e.source) ?? e.source,
    target: rename.get(e.target) ?? e.target,
  }));
  return { nodes, edges };
}

/**
 * Translates a subgraph so its top-left node sits at `position`, preserving the
 * relative layout the source file's dagre pass produced.
 */
export function offsetSubgraph(sub: TemplateSubgraph, position: { x: number; y: number }): TemplateSubgraph {
  if (sub.nodes.length === 0) return { nodes: [], edges: [...sub.edges] };
  const minX = Math.min(...sub.nodes.map((n) => n.position.x));
  const minY = Math.min(...sub.nodes.map((n) => n.position.y));
  return {
    nodes: sub.nodes.map((n) => ({
      ...n,
      position: { x: n.position.x - minX + position.x, y: n.position.y - minY + position.y },
    })),
    edges: [...sub.edges],
  };
}
