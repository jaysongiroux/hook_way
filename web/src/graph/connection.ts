import type { Connection, Edge } from "@xyflow/react";
import { NODE_SCHEMAS, portAccepts } from "./nodeSchema";
import type { HookwayNode } from "./model";

export const HANDLE_IN = "in";
export const HANDLE_OUT = "out";
export const HANDLE_ON_ERROR = "on_error";

export function isValidConnection(c: Connection | Edge, nodes: HookwayNode[], _edges: Edge[]): boolean {
  if (!c.source || !c.target || c.source === c.target) return false;
  const src = nodes.find((n) => n.id === c.source);
  const tgt = nodes.find((n) => n.id === c.target);
  if (!src || !tgt) return false;
  const from = NODE_SCHEMAS[src.data.nodeType];
  const to = NODE_SCHEMAS[tgt.data.nodeType];
  if (!from || !to) return false;
  const sourceHandle = c.sourceHandle ?? HANDLE_OUT;
  if (sourceHandle === HANDLE_ON_ERROR) {
    if (from.input === "None") return false;
    return portAccepts(to.input, from.input);
  }
  return portAccepts(to.input, from.output);
}

export function edgePort(sourceHandle: string | null | undefined): string {
  if (!sourceHandle || sourceHandle === HANDLE_OUT) return "";
  return sourceHandle;
}
