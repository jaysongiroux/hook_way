import type { Edge } from "@xyflow/react";
import type { AuthData, HookwayNode } from "./model";
import { DEST_TYPE_SPECS } from "./destTypes";

function importSettings(destType: string, block: Record<string, unknown> | undefined): Record<string, unknown> {
  const spec = DEST_TYPE_SPECS[destType];
  const out: Record<string, unknown> = {};
  if (!spec || !block) return out;
  for (const f of spec.fields) {
    const v = block[f.key];
    if (v === undefined) continue;
    out[f.key] = f.kind === "list" ? (Array.isArray(v) ? v.join(",") : String(v)) : v;
  }
  return out;
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

export function fromConfig(config: Record<string, unknown>): { nodes: HookwayNode[]; edges: Edge[] } {
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
    nodes.push({
      id: `destination:${name}`,
      type: "destination",
      position: { x: 0, y: 0 },
      data: { kind: "destination", name, destType, settings: importSettings(destType, block) },
    });
  }

  const eps = (config.endpoints ?? {}) as Record<string, Record<string, unknown>>;
  for (const [name, ep] of Object.entries(eps)) {
    const endpointId = `endpoint:${name}`;
    nodes.push({
      id: endpointId,
      type: "endpoint",
      position: { x: 0, y: 0 },
      data: { kind: "endpoint", name, path: String(ep.path ?? ""), auth: importAuth(ep.auth as Record<string, unknown>) },
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
      edges.push({
        id: nextEdgeId(),
        source: branch,
        target: `destination:${dest}`,
        data: { target: String(route.target ?? dest), create: route.create ?? true },
      });
    }
  }

  return { nodes, edges };
}
