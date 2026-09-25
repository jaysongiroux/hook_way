import type { Edge } from "@xyflow/react";
import type { FlowSnapshot, GraphDocument, HookwayNode } from "./model";
import { DEFAULT_FLOW, DEFAULT_SERVER, yamlIdOf } from "./model";
import { formatEdge } from "./fromConfig";
import { edgePort } from "./connection";

export class ExportError extends Error {}

export function toConfig(doc: GraphDocument): { config: Record<string, unknown> } {
  const flows: Record<string, unknown> = {};
  for (const [name, snap] of Object.entries(doc.otherFlows)) {
    flows[name] = snapshotToFlow(snap);
  }
  const active = doc.activeFlow || DEFAULT_FLOW;
  flows[active] = snapshotToFlow({
    nodes: doc.nodes,
    edges: doc.edges,
    deliveryFailurePolicy: doc.deliveryFailurePolicy,
    flowExtra: doc.flowExtra,
  });
  const connections = pruneEmpty(doc.connections);
  const destinations = pruneEmpty(doc.destinations);
  const server = Object.keys(doc.serverConfig).length > 0 ? pruneEmpty(doc.serverConfig) : { ...DEFAULT_SERVER };
  const config: Record<string, unknown> = { server };
  if (doc.coreReplayConfig !== null) config.replay = pruneEmpty(doc.coreReplayConfig) ?? {};
  // Connections come before destinations, so the file reads in the order the
  // references resolve. Omitted entirely when empty, for the same
  // import/export fidelity reason as destinations below.
  if (connections) config.connections = connections;
  // A proxy-style flow (http_request, no publish) declares no destinations at
  // all, and examples/xml.yaml is one. Emitting `destinations: {}` there adds
  // a key the operator never wrote and breaks import/export fidelity, so the
  // block is omitted rather than emitted empty. fromConfig already treats a
  // missing key as {}.
  if (destinations) config.destinations = destinations;
  const extra = pruneEmpty(doc.configExtra);
  if (extra && doc.coreReplayConfig !== null) delete extra.replay;
  Object.assign(config, extra, { flows });
  return { config };
}

function snapshotToFlow(snap: FlowSnapshot): Record<string, unknown> {
  const nodes = snap.nodes.map(nodeToYaml);
  const byRf = new Map(snap.nodes.map((n) => [n.id, n]));
  const edges = snap.edges.map((e) => edgeToYaml(e, byRf));
  const out: Record<string, unknown> = { ...(snap.flowExtra ?? {}), nodes, edges };
  if (snap.deliveryFailurePolicy) out.delivery_failure_policy = snap.deliveryFailurePolicy;
  return out;
}

function nodeToYaml(n: HookwayNode): Record<string, unknown> {
  const out: Record<string, unknown> = { id: yamlIdOf(n), type: n.data.nodeType };
  // description follows type so the node reads as identity-then-options,
  // matching the field order the Go loader strips.
  const description = n.data.description?.trim();
  if (description) out.description = description;
  for (const [k, v] of Object.entries(n.data.options ?? {})) {
    const pruned = pruneValue(v);
    if (pruned !== undefined) out[k] = pruned;
  }
  return out;
}

function edgeToYaml(e: Edge, byRf: Map<string, HookwayNode>): string {
  const from = byRf.get(e.source);
  const to = byRf.get(e.target);
  if (!from || !to) throw new ExportError(`edge ${e.id} is missing a node`);
  return formatEdge(yamlIdOf(from), edgePort(e.sourceHandle), yamlIdOf(to));
}

function pruneEmpty(obj: Record<string, unknown>): Record<string, unknown> | undefined {
  const out: Record<string, unknown> = {};
  for (const [k, v] of Object.entries(obj)) {
    const pruned = pruneValue(v);
    if (pruned !== undefined) out[k] = pruned;
  }
  if (Object.keys(out).length === 0) return undefined;
  return out;
}

function pruneValue(v: unknown): unknown {
  // Only absent editor values may be omitted. False/null/empty values have
  // meaning in destination settings, filter predicates and arbitrary JSON.
  if (v === undefined) return undefined;
  if (Array.isArray(v)) return v.map(pruneValue);
  if (v !== null && typeof v === "object") {
    return Object.fromEntries(Object.entries(v).filter(([, value]) => value !== undefined).map(([key, value]) => [key, pruneValue(value)]));
  }
  return v;
}
