import type { Edge } from "@xyflow/react";
import type { FlowSnapshot, GraphDocument, HookwayNode } from "./model";
import { DEFAULT_FLOW, DEFAULT_SERVER } from "./model";
import { HANDLE_IN, HANDLE_OUT, edgePort } from "./connection";
import { NODE_IDENTITY_KEYS } from "./nodeIdentity";

const MANAGED_TOP = new Set(["server", "replay", "connections", "destinations", "flows"]);
const MANAGED_FLOW = new Set(["nodes", "edges", "delivery_failure_policy"]);

export function fromConfig(config: Record<string, unknown>): GraphDocument {
  const flowsRaw = isObject(config.flows) ? config.flows : {};
  const names = Object.keys(flowsRaw);
  const activeFlow = names[0] ?? DEFAULT_FLOW;
  const otherFlows: Record<string, FlowSnapshot> = {};
  let nodes: HookwayNode[] = [];
  let edges: Edge[] = [];
  let deliveryFailurePolicy: string | undefined;
  let flowExtra: Record<string, unknown> | undefined;
  for (const name of names) {
    const snap = parseFlow(isObject(flowsRaw[name]) ? flowsRaw[name] : {});
    if (name === activeFlow) {
      nodes = snap.nodes;
      edges = snap.edges;
      deliveryFailurePolicy = snap.deliveryFailurePolicy;
      flowExtra = snap.flowExtra;
    } else {
      otherFlows[name] = snap;
    }
  }
  const configExtra: Record<string, unknown> = {};
  for (const [k, v] of Object.entries(config)) {
    if (!MANAGED_TOP.has(k)) configExtra[k] = v;
  }
  // A malformed replay block is still part of the imported document. Keep it
  // visible in export until the operator replaces it with structured settings.
  if (Object.prototype.hasOwnProperty.call(config, "replay") && !isObject(config.replay)) configExtra.replay = config.replay;
  const server = isObject(config.server) ? { ...config.server } : { ...DEFAULT_SERVER };
  const coreReplayConfig = isObject(config.replay) ? { ...config.replay } : null;
  const connections = isObject(config.connections) ? { ...config.connections } : {};
  const destinations = isObject(config.destinations) ? { ...config.destinations } : {};
  return {
    nodes,
    edges,
    serverConfig: Object.keys(server).length > 0 ? server : { ...DEFAULT_SERVER },
    coreReplayConfig,
    replayServiceConfig: null,
    configExtra,
    connections,
    destinations,
    activeFlow,
    otherFlows,
    deliveryFailurePolicy,
    flowExtra,
  };
}

function parseFlow(raw: Record<string, unknown>): FlowSnapshot {
  const used = new Set<string>();
  const nodes: HookwayNode[] = [];
  const yamlToRf = new Map<string, string>();
  const list = Array.isArray(raw.nodes) ? raw.nodes : [];
  list.forEach((item, index) => {
    const rec = isObject(item) ? item : {};
    const yamlId = String(rec.id ?? `node_${index + 1}`);
    const nodeType = String(rec.type ?? "http_trigger");
    const rfId = uniqueId(yamlId, used);
    if (!yamlToRf.has(yamlId)) yamlToRf.set(yamlId, rfId);
    const options: Record<string, unknown> = {};
    for (const [k, v] of Object.entries(rec)) {
      if (!NODE_IDENTITY_KEYS.has(k)) options[k] = v;
    }
    const description = rec.description === undefined ? undefined : String(rec.description);
    nodes.push({
      id: rfId,
      type: "flow",
      position: { x: 0, y: 0 },
      data: { nodeType, yamlId, options, description },
    });
  });
  const edges: Edge[] = [];
  const edgeList = Array.isArray(raw.edges) ? raw.edges.map(String) : [];
  edgeList.forEach((spec, i) => {
    const parsed = parseEdge(spec);
    if (!parsed) return;
    const source = yamlToRf.get(parsed.from);
    const target = yamlToRf.get(parsed.to);
    if (!source || !target) return;
    edges.push({
      id: spec || `e${i}`,
      source,
      target,
      sourceHandle: parsed.port || HANDLE_OUT,
      targetHandle: HANDLE_IN,
    });
  });
  const policy = String(raw.delivery_failure_policy ?? "").trim();
  const extra: Record<string, unknown> = {};
  for (const [k, v] of Object.entries(raw)) {
    if (!MANAGED_FLOW.has(k)) extra[k] = v;
  }
  return {
    nodes,
    edges,
    deliveryFailurePolicy: policy || undefined,
    flowExtra: Object.keys(extra).length > 0 ? extra : undefined,
  };
}

function parseEdge(spec: string): { from: string; port: string; to: string } | null {
  const cut = spec.split(">");
  if (cut.length !== 2) return null;
  const to = cut[1].trim();
  const left = cut[0].trim();
  const colon = left.indexOf(":");
  if (colon >= 0) return { from: left.slice(0, colon).trim(), port: left.slice(colon + 1).trim(), to };
  return { from: left, port: "", to };
}

function uniqueId(id: string, used: Set<string>): string {
  if (!used.has(id)) {
    used.add(id);
    return id;
  }
  for (let i = 2; ; i++) {
    const candidate = `${id}__${i}`;
    if (!used.has(candidate)) {
      used.add(candidate);
      return candidate;
    }
  }
}

function isObject(v: unknown): v is Record<string, unknown> {
  return Boolean(v) && typeof v === "object" && !Array.isArray(v);
}

export function formatEdge(from: string, port: string, to: string): string {
  return edgePort(port) ? `${from}:${port}>${to}` : `${from}>${to}`;
}
