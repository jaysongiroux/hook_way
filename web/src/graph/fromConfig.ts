import type { Edge } from "@xyflow/react";
import type { AuthData, HookwayNode, VariableData, FallbackData, LookupData } from "./model";
import { VARIABLE_SOURCES, VARIABLE_KEY_SOURCES, LOOKUP_PROVIDERS, emptyLookup } from "./model";
import { DEFAULT_SERVER } from "./model";
import { DEST_TYPE_SPECS, managedKeys } from "./destTypes";
import type { FieldSpec } from "./destTypes";

// destRawExtra captures the parts of a destination entry the UI doesn't model:
// entry-level keys other than `type` and the block, plus block-level keys not
// in the type's managed set (kept nested under the block key). Returns
// undefined when nothing extra needs preserving.
function destRawExtra(destType: string, entry: Record<string, unknown>): Record<string, unknown> | undefined {
  const blockKey = DEST_TYPE_SPECS[destType]?.block ?? destType;
  const managed = managedKeys(destType);
  const extra: Record<string, unknown> = {};
  for (const [k, v] of Object.entries(entry)) {
    if (k === "type") continue;
    if (k === blockKey) {
      const blockExtra: Record<string, unknown> = {};
      if (isPlainObject(v)) {
        for (const [bk, bv] of Object.entries(v)) if (!managed.has(bk)) blockExtra[bk] = bv;
      }
      if (Object.keys(blockExtra).length > 0) extra[blockKey] = blockExtra;
    } else {
      extra[k] = v;
    }
  }
  return Object.keys(extra).length > 0 ? extra : undefined;
}

function importFields(fields: FieldSpec[], block: Record<string, unknown>): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  for (const f of fields) {
    const v = block[f.key];
    if (f.kind === "group") {
      if (isPlainObject(v)) out[f.key] = importFields(f.fields ?? [], v);
      continue;
    }
    if (v === undefined) continue;
    out[f.key] = f.kind === "list" ? (Array.isArray(v) ? v.join(",") : String(v)) : v;
  }
  return out;
}

function importSettings(destType: string, block: Record<string, unknown> | undefined): Record<string, unknown> {
  const spec = DEST_TYPE_SPECS[destType];
  if (!spec || !block) return {};
  return importFields(spec.fields, block);
}

// omitKeys returns a shallow copy of obj without the given keys, or undefined
// if nothing remains — used to capture the "everything else" passthrough.
function omitKeys(obj: Record<string, unknown>, keys: string[]): Record<string, unknown> | undefined {
  const drop = new Set(keys);
  const out: Record<string, unknown> = {};
  for (const [k, v] of Object.entries(obj)) if (!drop.has(k)) out[k] = v;
  return Object.keys(out).length > 0 ? out : undefined;
}

const ENDPOINT_MANAGED = ["enabled", "path", "auth", "destinations", "filters", "variables", "on_unresolved_variable"];
const ROUTE_MANAGED = ["destination", "target", "create", "filters"];

function importVariables(raw: Record<string, Record<string, unknown>> | undefined): { variables?: VariableData[] } {
  if (!raw || Object.keys(raw).length === 0) return {};
  const variables: VariableData[] = Object.entries(raw).map(([name, v]) => {
    const values = Array.isArray(v.values) ? (v.values as unknown[]).map(String) : [];
    const source = VARIABLE_SOURCES.includes(v.source as VariableData["source"]) ? (v.source as VariableData["source"]) : "header";
    const mode: VariableData["mode"] =
      v.pattern !== undefined && values.length === 0 ? "pattern" : values.length === 0 && source === "lookup" ? "any" : "values";
    const out: VariableData = {
      name,
      source,
      path: String(v.path ?? ""),
      mode,
      values: values.join(", "),
      pattern: String(v.pattern ?? ""),
      default: String(v.default ?? ""),
    };
    if (source === "lookup" && v.lookup && typeof v.lookup === "object") {
      out.lookup = importLookup(v.lookup as Record<string, unknown>);
    }
    return out;
  });
  return { variables };
}

const LOOKUP_MANAGED = ["key", "provider", "dynamodb", "http", "postgres"];

function importLookup(raw: Record<string, unknown>): LookupData {
  const l = emptyLookup();
  const key = (raw.key ?? {}) as Record<string, unknown>;
  if (VARIABLE_KEY_SOURCES.includes(key.source as LookupData["keySource"])) l.keySource = key.source as LookupData["keySource"];
  l.keyPath = String(key.path ?? "");
  if (LOOKUP_PROVIDERS.includes(raw.provider as LookupData["provider"])) l.provider = raw.provider as LookupData["provider"];
  const d = (raw.dynamodb ?? {}) as Record<string, unknown>;
  const f = (d.filter ?? {}) as Record<string, unknown>;
  l.dynamodb = {
    table: String(d.table ?? ""),
    index: String(d.index ?? ""),
    value_attribute: String(d.value_attribute ?? ""),
    filter_attribute: String(f.attribute ?? ""),
    filter_value: String(f.value ?? ""),
  };
  const h = (raw.http ?? {}) as Record<string, unknown>;
  l.http = { url: String(h.url ?? ""), method: String(h.method ?? "GET"), value_path: String(h.value_path ?? "") };
  const p = (raw.postgres ?? {}) as Record<string, unknown>;
  l.postgres = { url: String(p.url ?? ""), query: String(p.query ?? "") };
  // Preserve unmodelled lookup keys (timeout, cache, region, headers, ...) and
  // unmodelled keys inside each provider block.
  const extra = omitKeys(raw, LOOKUP_MANAGED) ?? {};
  const dExtra = omitKeys(d, ["table", "index", "value_attribute", "filter"]);
  const hExtra = omitKeys(h, ["url", "method", "value_path"]);
  const pExtra = omitKeys(p, ["url", "query"]);
  if (dExtra) extra.dynamodb = dExtra;
  if (hExtra) extra.http = hExtra;
  if (pExtra) extra.postgres = pExtra;
  if (Object.keys(extra).length > 0) l.extra = extra;
  return l;
}

function importFallback(raw: Record<string, unknown> | undefined): { fallback?: FallbackData } {
  if (!raw) return {};
  return { fallback: { destination: String(raw.destination ?? ""), target: String(raw.target ?? "") } };
}

function importAuth(a: Record<string, unknown> | undefined): AuthData {
  const raw = (a ?? {}) as Record<string, unknown>;
  const type = raw.type === "no_auth" ? "none" : String(raw.type ?? "none");
  const secretRaw = String(raw.secret ?? "");
  const m = /^\$\{([A-Za-z_][A-Za-z0-9_]*)\}$/.exec(secretRaw);
  return {
    type,
    scheme: String(raw.scheme ?? ""),
    secret: m ? m[1] : secretRaw,
    // A blank/absent secret keeps the createNode default (env mode), so a
    // round-tripped unfilled endpoint doesn't surprise the user with "literal".
    secretMode: m ? "env" : secretRaw === "" ? "env" : "literal",
    signatureHeader: String(raw.signature_header ?? ""),
  };
}

function filterNode(name: string, f: Record<string, unknown>): HookwayNode {
  return {
    id: name,
    type: "filter",
    position: { x: 0, y: 0 },
    data: {
      kind: "filter",
      name,
      source: String(f.source ?? "body"),
      path: String(f.path ?? ""),
      operator: String(f.operator ?? "eq"),
      value: f.value ?? "",
    },
  };
}

function isPlainObject(v: unknown): v is Record<string, unknown> {
  return typeof v === "object" && v !== null && !Array.isArray(v);
}

export function fromConfig(config: Record<string, unknown>): {
  nodes: HookwayNode[];
  edges: Edge[];
  serverConfig: Record<string, unknown>;
  configExtra: Record<string, unknown>;
} {
  const nodes: HookwayNode[] = [];
  const edges: Edge[] = [];
  let filterCounter = 0;
  let edgeCounter = 0;
  const nextFilter = () => `filter_${++filterCounter}`;
  const nextEdgeId = () => `e_${++edgeCounter}`;

  const plainEdge = (source: string, target: string): Edge => ({
    id: nextEdgeId(),
    source,
    target,
    data: { target: "", create: true },
  });

  if (config.destinations !== undefined && !isPlainObject(config.destinations)) {
    throw new Error("destinations must be a mapping");
  }
  if (config.endpoints !== undefined && !isPlainObject(config.endpoints)) {
    throw new Error("endpoints must be a mapping");
  }

  const dests = (config.destinations ?? {}) as Record<string, Record<string, unknown>>;
  for (const [name, d] of Object.entries(dests)) {
    const destType = String(d.type ?? "rabbitmq");
    const block = d[DEST_TYPE_SPECS[destType]?.block ?? destType] as Record<string, unknown> | undefined;
    const rawExtra = destRawExtra(destType, d);
    nodes.push({
      id: `destination:${name}`,
      type: "destination",
      position: { x: 0, y: 0 },
      data: { kind: "destination", name, destType, settings: importSettings(destType, block), ...(rawExtra ? { rawExtra } : {}) },
    });
  }

  const eps = (config.endpoints ?? {}) as Record<string, Record<string, unknown>>;
  for (const [name, ep] of Object.entries(eps)) {
    const endpointId = `endpoint:${name}`;
    const epRawExtra = omitKeys(ep, ENDPOINT_MANAGED);
    nodes.push({
      id: endpointId,
      type: "endpoint",
      position: { x: 0, y: 0 },
      data: {
        kind: "endpoint",
        name,
        path: String(ep.path ?? ""),
        enabled: ep.enabled === undefined ? true : Boolean(ep.enabled),
        auth: importAuth(ep.auth as Record<string, unknown>),
        ...importVariables(ep.variables as Record<string, Record<string, unknown>> | undefined),
        ...importFallback(ep.on_unresolved_variable as Record<string, unknown> | undefined),
        ...(epRawExtra ? { rawExtra: epRawExtra } : {}),
      },
    });

    let prev = endpointId;
    for (const f of (ep.filters ?? []) as Record<string, unknown>[]) {
      const fn = nextFilter();
      nodes.push(filterNode(fn, f));
      edges.push(plainEdge(prev, fn));
      prev = fn;
    }
    const fanout = prev;

    for (const route of (ep.destinations ?? []) as Record<string, unknown>[]) {
      let branch = fanout;
      for (const f of (route.filters ?? []) as Record<string, unknown>[]) {
        const fn = nextFilter();
        nodes.push(filterNode(fn, f));
        edges.push(plainEdge(branch, fn));
        branch = fn;
      }
      const dest = String(route.destination ?? "");
      if (!Object.prototype.hasOwnProperty.call(dests, dest)) continue;
      const routeRawExtra = omitKeys(route, ROUTE_MANAGED);
      edges.push({
        id: nextEdgeId(),
        source: branch,
        target: `destination:${dest}`,
        data: { target: String(route.target ?? dest), create: route.create ?? true, ...(routeRawExtra ? { rawExtra: routeRawExtra } : {}) },
      });
    }
  }

  // Whole server block rides along (Server panel edits known keys, unknown keys
  // preserved); every other top-level block is preserved verbatim.
  const serverConfig = isPlainObject(config.server) ? config.server : { ...DEFAULT_SERVER };
  const configExtra = omitKeys(config, ["server", "destinations", "endpoints"]) ?? {};

  return { nodes, edges, serverConfig, configExtra };
}
