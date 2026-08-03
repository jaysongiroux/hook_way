import type { Edge, Node } from "@xyflow/react";
import { defaultSettings } from "./destTypes";

export type NodeKind = "endpoint" | "destination" | "filter";

export interface AuthData {
  type: string;
  scheme: string;
  secret: string;
  secretMode: "literal" | "env";
  signatureHeader: string;
}

export interface EndpointData extends Record<string, unknown> {
  kind: "endpoint";
  name: string;
  path: string;
  auth: AuthData;
  notes?: string;
}

export interface DestinationData extends Record<string, unknown> {
  kind: "destination";
  name: string;
  destType: string;
  settings: Record<string, unknown>;
  notes?: string;
}

export interface FilterData extends Record<string, unknown> {
  kind: "filter";
  name: string;
  source: string;
  path: string;
  operator: string;
  value: unknown;
  notes?: string;
}

export type HookwayNodeData = EndpointData | DestinationData | FilterData;
export type HookwayNode = Node<HookwayNodeData>;
export type HookwayEdge = Edge;

export interface EdgeData extends Record<string, unknown> {
  target: string;
  create: boolean;
}

export function isNodeKind(s: string): s is NodeKind {
  return s === "endpoint" || s === "destination" || s === "filter";
}

// Node names double as config keys (endpoints.<name>, destinations.<name>) and
// React Flow node ids, so they are unique per kind and human-readable.
const counters: Record<NodeKind, number> = { endpoint: 0, destination: 0, filter: 0 };

export function resetIdCounters(): void {
  counters.endpoint = 0;
  counters.destination = 0;
  counters.filter = 0;
}

export function nextName(kind: NodeKind): string {
  counters[kind] += 1;
  return `${kind}_${counters[kind]}`;
}

// seedCounters advances the per-kind name counters past any imported names of
// the form "<kind>_<n>", so nextName() won't collide after an import.
export function seedCounters(nodes: HookwayNode[]): void {
  resetIdCounters();
  const kinds: NodeKind[] = ["endpoint", "destination", "filter"];
  for (const kind of kinds) {
    let max = 0;
    const re = new RegExp(`^${kind}_(\\d+)$`);
    for (const n of nodes) {
      const m = re.exec(n.id);
      if (m) max = Math.max(max, Number(m[1]));
    }
    for (let i = 0; i < max; i++) nextName(kind);
  }
}

export function createNode(
  kind: NodeKind,
  position: { x: number; y: number },
): HookwayNode {
  const name = nextName(kind);
  if (kind === "endpoint") {
    return {
      id: name,
      type: "endpoint",
      position,
      data: {
        kind: "endpoint",
        name,
        path: `/webhooks/${name}`,
        auth: {
          type: "hmac_sha256",
          scheme: "hmac_sha256_hex",
          secret: "",
          secretMode: "env",
          signatureHeader: "X-Signature-256",
        },
      },
    };
  }
  if (kind === "filter") {
    return {
      id: name,
      type: "filter",
      position,
      data: { kind: "filter", name, source: "body", path: "", operator: "eq", value: "" },
    };
  }
  return {
    id: name,
    type: "destination",
    position,
    data: { kind: "destination", name, destType: "rabbitmq", settings: defaultSettings("rabbitmq") },
  };
}
