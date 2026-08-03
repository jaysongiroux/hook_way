import { parse } from "yaml";
import type { Edge } from "@xyflow/react";
import type { HookwayNode } from "./model";
import { fromConfig } from "./fromConfig";
import { layout } from "./layout";

export function fromYaml(text: string): { nodes: HookwayNode[]; edges: Edge[] } {
  const config = (parse(text) ?? {}) as Record<string, unknown>;
  const { nodes, edges } = fromConfig(config);
  return { nodes: layout(nodes, edges), edges };
}
