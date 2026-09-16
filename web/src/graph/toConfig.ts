import type { Edge } from "@xyflow/react";
import type { FlowSnapshot, GraphDocument, HookwayNode } from "./model";
import { DEFAULT_FLOW, DEFAULT_SERVER, yamlIdOf } from "./model";
import { formatEdge } from "./fromConfig";
import { edgePort } from "./connection";

export class ExportError extends Error {}

export function toConfig(doc: GraphDocument): { config: Record<string, unknown>; notes: Notes } {
  const notes: Notes = { flows: {}, destinations: {} };
  const flows: Record<string, unknown> = {};
  for (const [name, snap] of Object.entries(doc.otherFlows)) {
    flows[name] = snapshotToFlow(snap, notes, name);
  }
  const active = doc.activeFlow || DEFAULT_FLOW;
  flows[active] = snapshotToFlow(
    { nodes: doc.nodes, edges: doc.edges, deliveryFailurePolicy: doc.deliveryFailurePolicy },
    notes,
    active,
  );
  const destinations = pruneEmpty(doc.destinations, true);
  const server = Object.keys(doc.serverConfig).length > 0 ? pruneEmpty(doc.serverConfig, false) : { ...DEFAULT_SERVER };
  const config: Record<string, unknown> = {
    schema_version: 2,
    server,
    destinations: destinations ?? {},
    ...pruneEmpty(doc.configExtra, false),
    flows,
  };
  return { config, notes };
}

function snapshotToFlow(snap: FlowSnapshot, notes: Notes, flowName: string): Record<string, unknown> {
  const seen = new Set<string>();
  const nodes = snap.nodes.map((n) => nodeToYaml(n, seen));
  const byRf = new Map(snap.nodes.map((n) => [n.id, n]));
  const edges = snap.edges.map((e) => edgeToYaml(e, byRf));
  const out: Record<string, unknown> = { nodes, edges };
  if (snap.deliveryFailurePolicy) out.delivery_failure_policy = snap.deliveryFailurePolicy;
  const flowNotes = snap.nodes.map((n) => n.data.notes).filter((n): n is string => Boolean(n && n.trim()));
  if (flowNotes.length === 1) notes.flows[flowName] = flowNotes[0];
  return out;
}

function nodeToYaml(n: HookwayNode, seen: Set<string>): Record<string, unknown> {
  const id = yamlIdOf(n);
  if (seen.has(id)) {
    // Duplicate YAML ids are preserved so invalid examples round-trip.
  }
  seen.add(id);
  const out: Record<string, unknown> = { id, type: n.data.nodeType };
  for (const [k, v] of Object.entries(n.data.options ?? {})) {
    const pruned = pruneValue(v);
    if (pruned !== undefined) out[k] = pruned;
  }
  if (n.data.notes && n.data.notes.trim()) {
    // Notes are YAML comments on the flow, not node fields.
  }
  return out;
}

function edgeToYaml(e: Edge, byRf: Map<string, HookwayNode>): string {
  const from = byRf.get(e.source);
  const to = byRf.get(e.target);
  if (!from || !to) throw new ExportError(`edge ${e.id} is missing a node`);
  return formatEdge(yamlIdOf(from), edgePort(e.sourceHandle), yamlIdOf(to));
}

export interface Notes {
  flows: Record<string, string>;
  destinations: Record<string, string>;
}

function pruneEmpty(obj: Record<string, unknown>, keepEmptyObject: boolean): Record<string, unknown> | undefined {
  const out: Record<string, unknown> = {};
  for (const [k, v] of Object.entries(obj)) {
    const pruned = pruneValue(v);
    if (pruned !== undefined) out[k] = pruned;
  }
  if (Object.keys(out).length === 0) return keepEmptyObject ? {} : undefined;
  return out;
}

function pruneValue(v: unknown): unknown {
  if (v === undefined || v === null || v === "") return undefined;
  if (typeof v === "boolean") return v ? true : undefined;
  if (Array.isArray(v)) {
    const items = v.map(pruneValue).filter((x) => x !== undefined);
    return items.length > 0 ? items : undefined;
  }
  if (typeof v === "object") {
    const nested: Record<string, unknown> = {};
    for (const [k, val] of Object.entries(v as Record<string, unknown>)) {
      const pruned = pruneValue(val);
      if (pruned !== undefined) nested[k] = pruned;
    }
    return Object.keys(nested).length > 0 ? nested : undefined;
  }
  return v;
}
