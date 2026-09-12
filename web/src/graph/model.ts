import type { Edge, Node } from "@xyflow/react";
import { defaultSettings } from "./destTypes";

export type NodeKind = "endpoint" | "destination" | "filter";

// Minimum valid server block: `addr` and `max_body_bytes` are both REQUIRED by
// the Go validator. A fresh graph (and any export without a customized server)
// falls back to these so the output always validates.
export const DEFAULT_SERVER: Record<string, unknown> = { addr: ":8080", max_body_bytes: 1048576 };

export interface AuthData {
  type: string;
  scheme: string;
  secret: string;
  secretMode: "literal" | "env";
  signatureHeader: string;
}

// VariableData is one per-request routing variable (endpoints.<n>.variables.<name>).
// `values` is edited as a comma-separated list; `mode` picks values vs pattern.
export interface VariableData {
  name: string;
  source: "header" | "body" | "query" | "path" | "lookup";
  path: string;
  mode: "values" | "pattern" | "any";
  values: string;
  pattern: string;
  default: string;
  // Lookup-source settings (only when source === "lookup"). The UI edits the
  // key, provider, and each provider's required fields; any other keys under
  // `lookup` round-trip verbatim through `extra`.
  lookup?: LookupData;
}

export interface LookupData {
  keySource: "header" | "body" | "query" | "path";
  keyPath: string;
  provider: "dynamodb" | "http" | "postgres";
  dynamodb: { table: string; index: string; value_attribute: string; filter_attribute: string; filter_value: string };
  http: { url: string; method: string; value_path: string };
  postgres: { url: string; query: string };
  extra?: Record<string, unknown>;
}

export const LOOKUP_PROVIDERS: LookupData["provider"][] = ["dynamodb", "http", "postgres"];

export function emptyLookup(): LookupData {
  return {
    keySource: "body",
    keyPath: "",
    provider: "dynamodb",
    dynamodb: { table: "", index: "", value_attribute: "", filter_attribute: "", filter_value: "" },
    http: { url: "", method: "GET", value_path: "" },
    postgres: { url: "", query: "" },
  };
}

// FallbackData is endpoints.<n>.on_unresolved_variable: the static route that
// receives events whose variables could not be resolved.
export interface FallbackData {
  destination: string;
  target: string;
}

export const VARIABLE_SOURCES: VariableData["source"][] = ["header", "body", "query", "path", "lookup"];
export const VARIABLE_KEY_SOURCES: LookupData["keySource"][] = ["header", "body", "query", "path"];

export interface EndpointData extends Record<string, unknown> {
  kind: "endpoint";
  name: string;
  path: string;
  enabled: boolean;
  auth: AuthData;
  // Routing variables and their fallback route. Absent/empty means none.
  variables?: VariableData[];
  fallback?: FallbackData;
  // Verbatim endpoint-entry config the UI doesn't model (rate_limit,
  // replay_protection, max_body_bytes, delivery_failure_policy, ...), minus the
  // managed keys (enabled/path/auth/destinations/filters/variables/
  // on_unresolved_variable). Merged on export.
  rawExtra?: Record<string, unknown>;
  notes?: string;
}

export interface DestinationData extends Record<string, unknown> {
  kind: "destination";
  name: string;
  destType: string;
  settings: Record<string, unknown>;
  // Verbatim config the UI doesn't (yet) model: the destination entry minus
  // `type`, with the type's managed field keys stripped from the block. Merged
  // back under the block on export so nothing is dropped. Structure preserved
  // (block extras stay nested under the block key).
  rawExtra?: Record<string, unknown>;
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
  // Verbatim route config the UI doesn't model (queue_options, sqs_options,
  // fifo, ...), minus managed keys (destination/target/create/filters).
  rawExtra?: Record<string, unknown>;
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
        enabled: true,
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
