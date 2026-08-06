import type { Edge } from "@xyflow/react";
import type { HookwayNode, EndpointData, DestinationData, FilterData, AuthData } from "./model";
import { DEFAULT_SERVER } from "./model";
import { DEST_TYPE_SPECS } from "./destTypes";
import type { FieldSpec } from "./destTypes";

export class ExportError extends Error {}

interface Route {
  filters: FilterData[];
  dest: string;
  target: string;
  create: boolean;
  rawExtra?: Record<string, unknown>;
}

function mapAuth(a: AuthData): Record<string, unknown> | undefined {
  if (a.type === "none") return { type: "no_auth", scheme: "no_auth" };
  const out: Record<string, unknown> = { type: a.type };
  if (a.type === "hmac_sha256" || a.type === "jwt") out.scheme = a.scheme;
  if (a.signatureHeader) out.signature_header = a.signatureHeader;
  if (a.secret) out.secret = a.secretMode === "env" ? `\${${a.secret}}` : a.secret;
  return out;
}

function mapFilter(f: FilterData): Record<string, unknown> {
  const out: Record<string, unknown> = { source: f.source, path: f.path, operator: f.operator };
  if (f.value !== undefined && f.value !== "") out.value = f.value;
  return out;
}

function mapFields(fields: FieldSpec[], values: Record<string, unknown>): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  for (const field of fields) {
    const v = values[field.key];
    switch (field.kind) {
      case "bool":
        // Emit only when true. Go's zero value is false, so an omitted bool
        // decodes identically — and this keeps untouched optional groups
        // (dead_letter, tls) from emitting spurious `enabled: false` blocks.
        if (v === true) out[field.key] = true;
        break;
      case "number":
        if (v !== undefined && v !== null && v !== "") out[field.key] = Number(v);
        break;
      case "list": {
        const arr = String(v ?? "").split(",").map((s) => s.trim()).filter(Boolean);
        if (arr.length > 0) out[field.key] = arr;
        break;
      }
      case "group": {
        const nested = mapFields(field.fields ?? [], (v as Record<string, unknown>) ?? {});
        if (Object.keys(nested).length > 0) out[field.key] = nested;
        break;
      }
      default:
        if (v !== undefined && v !== "") out[field.key] = v;
    }
  }
  return out;
}

function mapSettings(destType: string, settings: Record<string, unknown>): Record<string, unknown> {
  const spec = DEST_TYPE_SPECS[destType];
  if (!spec) return {};
  return mapFields(spec.fields, settings);
}

function longestCommonPrefix(lists: string[][]): number {
  if (lists.length === 0) return 0;
  const first = lists[0];
  let i = 0;
  for (; i < first.length; i++) {
    if (!lists.every((l) => l[i] === first[i])) break;
  }
  return i;
}

export function toConfig(
  nodes: HookwayNode[],
  edges: Edge[],
  serverConfig?: Record<string, unknown>,
  configExtra?: Record<string, unknown>,
): { config: Record<string, unknown>; notes: { endpoints: Record<string, string>; destinations: Record<string, string> } } {
  const byId = new Map(nodes.map((n) => [n.id, n]));
  const out = (id: string) => edges.filter((e) => e.source === id);

  function routesFrom(startId: string): Route[] {
    const routes: Route[] = [];
    const walk = (nodeId: string, acc: FilterData[], seen: Set<string>) => {
      if (seen.has(nodeId)) throw new ExportError(`filter cycle detected at node "${nodeId}"`);
      for (const e of out(nodeId)) {
        const tgt = byId.get(e.target);
        if (!tgt) continue;
        const ed = (e.data ?? {}) as { target?: string; create?: boolean; rawExtra?: Record<string, unknown> };
        if (tgt.data.kind === "destination") {
          routes.push({
            filters: [...acc],
            dest: String(tgt.data.name),
            target: String(ed.target ?? tgt.data.name),
            create: ed.create ?? true,
            rawExtra: ed.rawExtra,
          });
        } else if (tgt.data.kind === "filter") {
          const next = new Set(seen);
          next.add(nodeId);
          walk(e.target, [...acc, tgt.data as FilterData], next);
        }
      }
    };
    walk(startId, [], new Set());
    return routes;
  }

  const destinations: Record<string, unknown> = {};
  const destNotes: Record<string, string> = {};
  for (const n of nodes) {
    if (n.data.kind !== "destination") continue;
    const d = n.data as DestinationData;
    const block = DEST_TYPE_SPECS[d.destType]?.block ?? d.destType;
    const mapped = mapSettings(d.destType, d.settings ?? {});
    // Start from preserved extras (entry-level + block-level), then layer the
    // UI-managed values over the block. Managed keys the user cleared stay
    // cleared (they were stripped from rawExtra on import).
    const rawExtra = (d.rawExtra ?? {}) as Record<string, unknown>;
    const blockExtra = (rawExtra[block] ?? {}) as Record<string, unknown>;
    const entry: Record<string, unknown> = { ...rawExtra, type: d.destType };
    const blockOut = { ...blockExtra, ...mapped };
    if (Object.keys(blockOut).length > 0) entry[block] = blockOut;
    else delete entry[block];
    destinations[d.name] = entry;
    if (d.notes) destNotes[d.name] = d.notes;
  }

  const endpoints: Record<string, unknown> = {};
  const endpointNotes: Record<string, string> = {};
  for (const n of nodes) {
    if (n.data.kind !== "endpoint") continue;
    const ep = n.data as EndpointData;
    const routes = routesFrom(n.id);
    const prefixLen = longestCommonPrefix(routes.map((r) => r.filters.map((f) => f.name)));
    // Preserved endpoint-level extras first, then managed keys override them.
    const entry: Record<string, unknown> = {
      ...((ep.rawExtra ?? {}) as Record<string, unknown>),
      enabled: ep.enabled ?? true,
      path: ep.path,
    };
    const auth = mapAuth(ep.auth);
    if (auth) entry.auth = auth;
    if (prefixLen > 0 && routes.length > 0) {
      entry.filters = routes[0].filters.slice(0, prefixLen).map(mapFilter);
    }
    entry.destinations = routes.map((r) => {
      // Preserved route extras first, then managed keys override.
      const dest: Record<string, unknown> = {
        ...((r.rawExtra ?? {}) as Record<string, unknown>),
        destination: r.dest,
        target: r.target,
        create: r.create,
      };
      const perRoute = r.filters.slice(prefixLen);
      if (perRoute.length > 0) dest.filters = perRoute.map(mapFilter);
      return dest;
    });
    endpoints[ep.name] = entry;
    if (ep.notes) endpointNotes[ep.name] = ep.notes;
  }

  const server = serverConfig && Object.keys(serverConfig).length > 0 ? serverConfig : { ...DEFAULT_SERVER };
  // server first (readability), then any preserved top-level blocks
  // (security/rate_limit/metrics/logging/replay), then the graph-derived maps.
  const config: Record<string, unknown> = {
    server,
    ...(configExtra ?? {}),
    destinations,
    endpoints,
  };
  return { config, notes: { endpoints: endpointNotes, destinations: destNotes } };
}
