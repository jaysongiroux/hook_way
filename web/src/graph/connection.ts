import type { Connection, Edge } from "@xyflow/react";
import { labelledPorts, nodeMeta, portAccepts } from "./nodeSchema";
import type { HookwayNode } from "./model";

export const HANDLE_IN = "in";
export const HANDLE_OUT = "out";
export const HANDLE_ON_ERROR = "on_error";

export function isValidConnection(c: Connection | Edge, nodes: HookwayNode[], edges: Edge[]): boolean {
  if (!c.source || !c.target || c.source === c.target) return false;
  const src = nodes.find((n) => n.id === c.source);
  const tgt = nodes.find((n) => n.id === c.target);
  if (!src || !tgt) return false;
  const from = nodeMeta(src.data.nodeType, src.data.options);
  const to = nodeMeta(tgt.data.nodeType, tgt.data.options);
  if (!from || !to) return false;
  if (c.targetHandle && c.targetHandle !== HANDLE_IN) return false;
  // The compiler rejects reconvergence, duplicate edges and cycles.
  const existing = edges.filter((e) => !("id" in c && e.id === c.id));
  if (existing.some((e) => e.target === c.target)) return false;
  const pending = [c.target];
  const seen = new Set<string>();
  while (pending.length) {
    const id = pending.pop()!;
    if (id === c.source) return false;
    if (seen.has(id)) continue;
    seen.add(id);
    pending.push(...existing.filter((e) => e.source === id).map((e) => e.target));
  }
  const sourceHandle = c.sourceHandle ?? HANDLE_OUT;
  if (sourceHandle === HANDLE_ON_ERROR) {
    if (!from.on_error) return false;
    return portAccepts(to.input, from.input);
  }
  const ports = labelledPorts(src.data.nodeType, src.data.options);
  if (ports.length > 0) {
    if (!ports.includes(sourceHandle)) return false;
    if (existing.some((e) => e.source === c.source && e.sourceHandle === sourceHandle)) return false;
  } else if (sourceHandle !== HANDLE_OUT) return false;
  // Detached destinations (publish) allow at most one success edge; respond
  // writes the caller connection and cannot follow a publish continuation.
  if (from.detached && sourceHandle === HANDLE_OUT) {
    if (existing.some((e) => e.source === c.source && (e.sourceHandle ?? HANDLE_OUT) === HANDLE_OUT)) {
      return false;
    }
    if (tgt.data.nodeType === "respond") return false;
  }
  return from.output !== "Terminal" && portAccepts(to.input, from.output);
}

export function edgePort(sourceHandle: string | null | undefined): string {
  if (!sourceHandle || sourceHandle === HANDLE_OUT) return "";
  return sourceHandle;
}
