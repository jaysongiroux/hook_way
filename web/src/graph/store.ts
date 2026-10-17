import { create } from "zustand";
import {
  applyEdgeChanges,
  applyNodeChanges,
  type Connection,
  type Edge,
  type EdgeChange,
  type NodeChange,
} from "@xyflow/react";
import { isValidConnection } from "./connection";
import { layout, maybeLayout } from "./layout";
import {
  emptyDocument,
  seedCounters,
  type EditorDraft,
  type GraphDocument,
  type HookwayNode,
  type FlowSnapshot,
} from "./model";
import { offsetSubgraph, renameSubgraph } from "./subgraph";
import type { TemplateSubgraph } from "./templates";
import {
  defaultCoreReplayConfig,
  defaultReplayServiceConfig,
} from "./replayDefaults";

import { flowFilenames } from "./bundle";

let edgeSeq = 0;

interface GraphState extends GraphDocument {
  setYAMLMode: (mode: "single" | "split") => void;
  setEntryFilename: (filename: string) => void;
  setFlowFilename: (name: string, filename: string) => void;
  setReplayFilename: (filename: string) => void;
  removeFlow: (name: string) => void;
  selectedId: string | null;
  selectedEdgeId: string | null;
  onNodesChange: (changes: NodeChange[]) => void;
  onEdgesChange: (changes: EdgeChange[]) => void;
  onConnect: (c: Connection) => void;
  addNode: (n: HookwayNode) => void;
  insertSubgraph: (
    sub: TemplateSubgraph,
    position: { x: number; y: number },
  ) => void;
  updateNodeData: (id: string, patch: Record<string, unknown>) => void;
  updateEdgeData: (id: string, patch: Record<string, unknown>) => void;
  setFormDraft: (key: string, draft: EditorDraft | undefined) => void;
  clearFormDrafts: (prefix: string) => void;
  setConfigExtra: (patch: Record<string, unknown>) => void;
  setServerConfig: (patch: Record<string, unknown>) => void;
  setCoreReplayConfig: (config: Record<string, unknown> | null) => void;
  setReplayServiceConfig: (config: Record<string, unknown> | null) => void;
  addReplayConfiguration: () => void;
  removeReplayConfiguration: () => void;
  setConnections: (connections: Record<string, unknown>) => void;
  setDestinations: (destinations: Record<string, unknown>) => void;
  setDeliveryFailurePolicy: (value: string) => void;
  setFlowResponses: (value: unknown) => void;
  setSelected: (id: string | null) => void;
  setSelectedEdge: (id: string | null) => void;
  setGraph: (doc: GraphDocument) => void;
  setActiveFlow: (name: string) => void;
  addFlow: (name: string) => boolean;
  renameFlow: (name: string) => void;
  arrangeNodes: () => void;
  arrangeSeq: number;
  document: () => GraphDocument;
  clear: () => void;
  clearCore: () => void;
}

function snapshotOf(
  s: Pick<
    GraphState,
    "nodes" | "edges" | "deliveryFailurePolicy" | "flowExtra"
  >,
): FlowSnapshot {
  return {
    nodes: s.nodes,
    edges: s.edges,
    deliveryFailurePolicy: s.deliveryFailurePolicy,
    flowExtra: s.flowExtra,
  };
}

export const useGraphStore = create<GraphState>((set, get) => ({
  ...emptyDocument(),
  setYAMLMode: (yamlMode) => set({ yamlMode, flowFiles: flowFilenames(get()) }),
  setEntryFilename: (entryFilename) => set({ entryFilename }),
  setFlowFilename: (name, filename) =>
    set({ flowFiles: { ...flowFilenames(get()), [name]: filename } }),
  setReplayFilename: (replayFilename) => set({ replayFilename }),
  removeFlow: (name) => {
    const s = get();
    const names = [s.activeFlow, ...Object.keys(s.otherFlows)].filter(
      (n) => n !== name,
    );
    if (!names.length) return;
    if (name === s.activeFlow) s.setActiveFlow(names[0]);
    const current = get();
    const otherFlows = { ...current.otherFlows };
    delete otherFlows[name];
    const flowFiles = { ...current.flowFiles };
    delete flowFiles[name];
    const prefix = `node/${encodeURIComponent(name)}/`;
    set({
      otherFlows,
      flowFiles,
      formDrafts: Object.fromEntries(
        Object.entries(current.formDrafts ?? {}).filter(
          ([key]) =>
            !key.startsWith(prefix) &&
            !draftOwnedBy(key, `flow/${encodeURIComponent(name)}`),
        ),
      ),
    });
  },
  selectedId: null,
  selectedEdgeId: null,
  arrangeSeq: 0,
  onNodesChange: (changes) => {
    const s = get();
    const removed = changes.filter((c) => c.type === "remove").map((c) => c.id);
    const prefixes = removed.map(
      (id) =>
        `node/${encodeURIComponent(s.activeFlow)}/${encodeURIComponent(id)}`,
    );
    const formDrafts = Object.fromEntries(
      Object.entries(s.formDrafts ?? {}).filter(
        ([key]) =>
          !prefixes.some(
            (p) => key.startsWith(p + "|") || key.startsWith(p + "/"),
          ),
      ),
    );
    set({
      nodes: applyNodeChanges(changes, s.nodes) as HookwayNode[],
      formDrafts,
    });
  },
  onEdgesChange: (changes) =>
    set({ edges: applyEdgeChanges(changes, get().edges) }),
  onConnect: (c) => {
    const { nodes, edges } = get();
    if (!isValidConnection(c, nodes, edges)) return;
    const edge: Edge = {
      id: `e${(edgeSeq += 1)}`,
      source: c.source as string,
      target: c.target as string,
      sourceHandle: c.sourceHandle ?? null,
      targetHandle: c.targetHandle ?? null,
    };
    set({ edges: [...edges, edge] });
  },
  addNode: (n) => {
    if (n.data.nodeType === "mcp_trigger" && get().nodes.length > 0)
      get().addFlow(nextMCPFlowName(get()));
    set({ nodes: [...get().nodes, availableMCPEndpoint(n, get())] });
  },
  // insertSubgraph drops a palette template onto the canvas. Ids are renamed
  // against everything already in this flow (a second insert of the same
  // template would otherwise export duplicate `id:` keys), and positions are
  // translated so the template lands where it was dropped instead of on top
  // of the existing graph.
  insertSubgraph: (sub, position) => {
    if (
      sub.nodes.some((n) => n.data.nodeType === "mcp_trigger") &&
      get().nodes.length > 0
    ) {
      get().addFlow(nextMCPFlowName(get()));
    }
    sub = {
      ...sub,
      nodes: sub.nodes.map((node) => availableMCPEndpoint(node, get())),
    };
    if (sub.configExtra) {
      const extra = { ...get().configExtra };
      const names = new Map<string, string>();
      const credentials = { ...objectRecord(extra.credentials) };
      for (const [original, entry] of Object.entries(
        objectRecord(sub.configExtra.credentials),
      )) {
        let name = original;
        let suffix = 2;
        while (name in credentials) name = `${original}_${suffix++}`;
        names.set(original, name);
        credentials[name] = entry;
      }
      extra.credentials = credentials;
      sub = {
        ...sub,
        nodes: sub.nodes.map((n) => {
          const credential = objectRecord(n.data.options.credential);
          return credential.profile
            ? {
                ...n,
                data: {
                  ...n.data,
                  options: {
                    ...n.data.options,
                    credential: {
                      ...credential,
                      profile:
                        names.get(String(credential.profile)) ??
                        credential.profile,
                    },
                  },
                },
              }
            : n;
        }),
      };
      set({ configExtra: extra, flowExtra: sub.flowExtra });
    }
    const { nodes, edges } = get();
    const taken = new Set(
      nodes.flatMap((n) => [n.id, String(n.data.yamlId ?? n.id)]),
    );
    const placed = offsetSubgraph(renameSubgraph(sub, taken), position);
    set({
      nodes: [...nodes, ...placed.nodes],
      edges: [...edges, ...placed.edges],
    });
  },
  updateNodeData: (id, patch) =>
    set({
      nodes: get().nodes.map((n) =>
        n.id === id
          ? ({
              ...n,
              data: {
                ...n.data,
                ...patch,
                options:
                  patch.options && typeof patch.options === "object"
                    ? (patch.options as Record<string, unknown>)
                    : n.data.options,
              },
            } as HookwayNode)
          : n,
      ),
    }),
  updateEdgeData: (id, patch) =>
    set({
      edges: get().edges.map((e) =>
        e.id === id ? { ...e, data: { ...(e.data ?? {}), ...patch } } : e,
      ),
    }),
  setFormDraft: (key, draft) =>
    set((s) => {
      const formDrafts = { ...s.formDrafts };
      if (draft === undefined) delete formDrafts[key];
      else formDrafts[key] = draft;
      return { formDrafts };
    }),
  clearFormDrafts: (prefix) =>
    set((s) => ({
      formDrafts: Object.fromEntries(
        Object.entries(s.formDrafts ?? {}).filter(
          ([key]) => !key.startsWith(prefix),
        ),
      ),
    })),
  setConfigExtra: (patch) =>
    set((s) => {
      const configExtra = { ...s.configExtra, ...patch };
      for (const [key, value] of Object.entries(configExtra))
        if (value === undefined) delete configExtra[key];
      return {
        configExtra,
        formDrafts: reconcileConfigExtraDrafts(
          s.formDrafts ?? {},
          s.configExtra,
          configExtra,
        ),
      };
    }),
  setServerConfig: (patch) => {
    const merged = { ...get().serverConfig, ...patch };
    const pruned: Record<string, unknown> = {};
    for (const [k, v] of Object.entries(merged))
      if (v !== "" && v !== null && v !== undefined) pruned[k] = v;
    set({ serverConfig: pruned });
  },
  setCoreReplayConfig: (coreReplayConfig) =>
    set({ coreReplayConfig, configExtra: withoutReplay(get().configExtra) }),
  setReplayServiceConfig: (replayServiceConfig) => set({ replayServiceConfig }),
  addReplayConfiguration: () =>
    set({
      coreReplayConfig: get().coreReplayConfig ?? defaultCoreReplayConfig(),
      replayServiceConfig:
        get().replayServiceConfig ?? defaultReplayServiceConfig(),
      configExtra: withoutReplay(get().configExtra),
    }),
  removeReplayConfiguration: () =>
    set((s) => ({
      coreReplayConfig: null,
      replayServiceConfig: null,
      configExtra: withoutReplay(s.configExtra),
      formDrafts: Object.fromEntries(
        Object.entries(s.formDrafts ?? {}).filter(
          ([key]) =>
            !draftOwnedBy(key, "replay") && !draftOwnedBy(key, "core/replay"),
        ),
      ),
    })),
  setConnections: (connections) =>
    set((s) => ({
      connections,
      formDrafts: reconcileNamedDrafts(
        s.formDrafts ?? {},
        s.connections,
        connections,
        "connection",
      ),
    })),
  setDestinations: (destinations) =>
    set((s) => ({
      destinations,
      formDrafts: reconcileNamedDrafts(
        s.formDrafts ?? {},
        s.destinations,
        destinations,
        "destination",
      ),
    })),
  setDeliveryFailurePolicy: (value) =>
    set({ deliveryFailurePolicy: value || undefined }),
  setFlowResponses: (value) =>
    set((s) => {
      const flowExtra = { ...s.flowExtra };
      if (value === undefined) delete flowExtra.responses;
      else flowExtra.responses = value;
      return {
        flowExtra: Object.keys(flowExtra).length ? flowExtra : undefined,
      };
    }),
  setSelected: (id) => set({ selectedId: id, selectedEdgeId: null }),
  setSelectedEdge: (id) => set({ selectedEdgeId: id, selectedId: null }),
  setGraph: (doc) => {
    const configExtra = { ...(doc.configExtra ?? {}) };
    const legacyReplay = isRecord(configExtra.replay)
      ? { ...configExtra.replay }
      : null;
    if (legacyReplay || doc.coreReplayConfig !== null)
      delete configExtra.replay;
    seedCounters(doc.nodes);
    const otherFlows: Record<string, FlowSnapshot> = {};
    for (const [name, snap] of Object.entries(doc.otherFlows ?? {})) {
      otherFlows[name] = {
        ...snap,
        nodes: maybeLayout(snap.nodes, snap.edges),
      };
    }
    set({
      ...emptyDocument(),
      ...doc,
      configExtra,
      coreReplayConfig: doc.coreReplayConfig ?? legacyReplay,
      replayServiceConfig: doc.replayServiceConfig ?? null,
      nodes: maybeLayout(doc.nodes, doc.edges),
      otherFlows,
      selectedId: null,
      selectedEdgeId: null,
    });
  },
  setActiveFlow: (name) => {
    const s = get();
    if (name === s.activeFlow) return;
    const current = snapshotOf(s);
    const other = { ...s.otherFlows, [s.activeFlow]: current };
    const next = other[name] ?? { nodes: [], edges: [] };
    const rest = { ...other };
    delete rest[name];
    const nodes = maybeLayout(next.nodes, next.edges);
    seedCounters(nodes);
    set({
      activeFlow: name,
      otherFlows: rest,
      nodes,
      edges: next.edges,
      deliveryFailurePolicy: next.deliveryFailurePolicy,
      flowExtra: next.flowExtra,
      selectedId: null,
      selectedEdgeId: null,
    });
  },
  arrangeNodes: () => {
    const { nodes, edges, arrangeSeq } = get();
    if (nodes.length === 0) return;
    set({ nodes: layout(nodes, edges), arrangeSeq: arrangeSeq + 1 });
  },
  addFlow: (name) => {
    const trimmed = name.trim();
    const s = get();
    if (!trimmed || trimmed === s.activeFlow || s.otherFlows[trimmed])
      return false;
    const nextFlows = { ...s.otherFlows, [s.activeFlow]: snapshotOf(s) };
    const flowFiles = flowFilenames({
      ...s,
      otherFlows: nextFlows,
      activeFlow: trimmed,
    });
    set({
      flowFiles,
      otherFlows: nextFlows,
      activeFlow: trimmed,
      nodes: [],
      edges: [],
      deliveryFailurePolicy: undefined,
      flowExtra: undefined,
      selectedId: null,
      selectedEdgeId: null,
    });
    return true;
  },
  renameFlow: (name) => {
    const trimmed = name.trim();
    const s = get();
    if (!trimmed || trimmed === s.activeFlow || s.otherFlows[trimmed]) return;
    const previous = `node/${encodeURIComponent(s.activeFlow)}/`;
    const next = `node/${encodeURIComponent(trimmed)}/`;
    const formDrafts = Object.fromEntries(
      Object.entries(s.formDrafts ?? {}).map(([key, draft]) => [
        key.startsWith(previous) ? next + key.slice(previous.length) : key,
        draft,
      ]),
    );
    const flowFiles = {
      ...flowFilenames(s),
      [trimmed]: flowFilenames(s)[s.activeFlow],
    };
    delete flowFiles[s.activeFlow];
    set({ activeFlow: trimmed, formDrafts, flowFiles });
  },
  document: () => {
    const s = get();
    return {
      yamlMode: s.yamlMode,
      entryFilename: s.entryFilename,
      flowFiles: s.flowFiles,
      replayFilename: s.replayFilename,
      formDrafts: s.formDrafts,
      nodes: s.nodes,
      edges: s.edges,
      serverConfig: s.serverConfig,
      coreReplayConfig: s.coreReplayConfig,
      replayServiceConfig: s.replayServiceConfig,
      configExtra: s.configExtra,
      connections: s.connections,
      destinations: s.destinations,
      activeFlow: s.activeFlow,
      otherFlows: s.otherFlows,
      deliveryFailurePolicy: s.deliveryFailurePolicy,
      flowExtra: s.flowExtra,
    };
  },
  clear: () =>
    set({ ...emptyDocument(), selectedId: null, selectedEdgeId: null }),
  clearCore: () => {
    const { replayServiceConfig, replayFilename, formDrafts } = get();
    const replayDrafts =
      replayServiceConfig === null
        ? {}
        : Object.fromEntries(
            Object.entries(formDrafts ?? {}).filter(([key]) =>
              draftOwnedBy(key, "replay"),
            ),
          );
    set({
      ...emptyDocument(),
      replayServiceConfig,
      replayFilename,
      formDrafts: replayDrafts,
      selectedId: null,
      selectedEdgeId: null,
    });
  },
}));

function isRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === "object" && !Array.isArray(value);
}

function withoutReplay(
  extra: Record<string, unknown>,
): Record<string, unknown> {
  const next = { ...extra };
  delete next.replay;
  return next;
}

function draftOwnedBy(key: string, scope: string): boolean {
  return key.startsWith(scope + "|") || key.startsWith(scope + "/");
}

function namedEntryPairs(
  previous: Record<string, unknown>,
  next: Record<string, unknown>,
): Array<[string, string]> {
  const added = Object.keys(next).filter(
    (name) => !Object.prototype.hasOwnProperty.call(previous, name),
  );
  return Object.keys(previous).flatMap((name) => {
    if (Object.prototype.hasOwnProperty.call(next, name))
      return [[name, name] as [string, string]];
    const renamed = added.find(
      (candidate) => next[candidate] === previous[name],
    );
    return renamed ? [[name, renamed] as [string, string]] : [];
  });
}

function reconcileNamedDrafts(
  drafts: Record<string, EditorDraft>,
  previous: Record<string, unknown>,
  next: Record<string, unknown>,
  namespace: string,
  options: { encodeNames?: boolean; pathPrefix?: string } = {},
) {
  const pairs = new Map(namedEntryPairs(previous, next));
  const segment = (name: string) =>
    options.encodeNames ? encodeURIComponent(name) : name;
  return Object.fromEntries(
    Object.entries(drafts).flatMap(([key, draft]) => {
      for (const name of Object.keys(previous)) {
        const prefix = `${namespace}/${segment(name)}`;
        if (!key.startsWith(prefix + "|") && !key.startsWith(prefix + "/"))
          continue;
        const renamed = pairs.get(name);
        if (renamed === undefined) return [];
        const separator = key.indexOf("|");
        let scope = key.slice(0, separator);
        let path = key.slice(separator + 1);
        scope = `${namespace}/${segment(renamed)}${scope.slice(prefix.length)}`;
        if (options.pathPrefix) {
          const oldPath = `${options.pathPrefix}.${name}`;
          if (path === oldPath || path.startsWith(oldPath + "."))
            path = `${options.pathPrefix}.${renamed}${path.slice(oldPath.length)}`;
        }
        return [[`${scope}|${path}`, draft] as const];
      }
      return [[key, draft] as const];
    }),
  );
}

/** Remove drafts only when the corresponding authored field was explicitly removed. */
function pruneRemovedFieldDrafts(
  drafts: Record<string, EditorDraft>,
  previous: Record<string, unknown>,
  next: Record<string, unknown>,
  scope: string,
  path: string,
  ancestors: Array<{ scope: string; path: string }> = [],
): Record<string, EditorDraft> {
  const contexts = [...ancestors, { scope, path }];
  let result = drafts;
  for (const key of Object.keys(previous)) {
    const fieldPath = path ? `${path}.${key}` : key;
    const fieldScope = `${scope}/${encodeURIComponent(key)}`;
    if (
      !Object.prototype.hasOwnProperty.call(next, key) ||
      next[key] === undefined
    ) {
      result = Object.fromEntries(
        Object.entries(result).filter(([draftKey]) => {
          const separator = draftKey.indexOf("|");
          const draftScope = draftKey.slice(0, separator);
          const draftPath = draftKey.slice(separator + 1);
          if (
            draftScope === fieldScope ||
            draftScope.startsWith(fieldScope + "/")
          )
            return false;
          return !contexts.some((context) => {
            if (
              draftScope !== context.scope &&
              !draftScope.startsWith(context.scope + "/")
            )
              return false;
            const relativePath = context.path
              ? fieldPath.slice(context.path.length + 1)
              : fieldPath;
            return [fieldPath, relativePath].some(
              (candidate) =>
                draftPath === candidate ||
                draftPath.startsWith(candidate + "."),
            );
          });
        }),
      );
    } else if (isRecord(previous[key]) && isRecord(next[key])) {
      result = pruneRemovedFieldDrafts(
        result,
        previous[key] as Record<string, unknown>,
        next[key] as Record<string, unknown>,
        fieldScope,
        fieldPath,
        contexts,
      );
    }
  }
  return result;
}

function reconcileConfigExtraDrafts(
  drafts: Record<string, EditorDraft>,
  previous: Record<string, unknown>,
  next: Record<string, unknown>,
) {
  const asRecord = (value: unknown) => (isRecord(value) ? value : {});
  const oldCredentials = asRecord(previous.credentials),
    credentials = asRecord(next.credentials);
  let result = reconcileNamedDrafts(
    drafts,
    oldCredentials,
    credentials,
    "credentials",
    { encodeNames: true, pathPrefix: "credentials" },
  );
  for (const [oldName, name] of namedEntryPairs(oldCredentials, credentials)) {
    const oldEntry = asRecord(oldCredentials[oldName]),
      entry = asRecord(credentials[name]);
    const scope = `credentials/${encodeURIComponent(name)}`,
      path = `credentials.${name}`;
    for (const category of ["headers", "secret_parameters"]) {
      result = reconcileNamedDrafts(
        result,
        asRecord(oldEntry[category]),
        asRecord(entry[category]),
        `${scope}/${category}`,
        { encodeNames: true, pathPrefix: `${path}.${category}` },
      );
    }
    result = pruneRemovedFieldDrafts(result, oldEntry, entry, scope, path);
  }
  for (const category of ["lookups", "state_stores"]) {
    const oldEntries = asRecord(previous[category]),
      entries = asRecord(next[category]);
    result = reconcileNamedDrafts(result, oldEntries, entries, category, {
      encodeNames: true,
      pathPrefix: category,
    });
    for (const [oldName, name] of namedEntryPairs(oldEntries, entries)) {
      result = pruneRemovedFieldDrafts(
        result,
        asRecord(oldEntries[oldName]),
        asRecord(entries[name]),
        `${category}/${encodeURIComponent(name)}`,
        `${category}.${name}`,
      );
    }
  }
  const oldRouting = asRecord(previous.routing),
    routing = asRecord(next.routing);
  for (const category of ["profiles", "environments", "stores"]) {
    const oldEntries = asRecord(oldRouting[category]),
      entries = asRecord(routing[category]);
    const namespace = `routing/${category}`,
      pathPrefix = `routing.${category}`;
    result = reconcileNamedDrafts(result, oldEntries, entries, namespace, {
      encodeNames: true,
      pathPrefix,
    });
    for (const [oldName, name] of namedEntryPairs(oldEntries, entries)) {
      result = pruneRemovedFieldDrafts(
        result,
        asRecord(oldEntries[oldName]),
        asRecord(entries[name]),
        `${namespace}/${encodeURIComponent(name)}`,
        `${pathPrefix}.${name}`,
      );
    }
  }
  return result;
}

function objectRecord(value: unknown): Record<string, unknown> {
  return value && typeof value === "object" && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : {};
}

function nextMCPFlowName(state: Pick<GraphState, "activeFlow" | "otherFlows">) {
  let name = "mcp_gateway";
  let suffix = 2;
  while (name === state.activeFlow || state.otherFlows[name])
    name = `mcp_gateway_${suffix++}`;
  return name;
}

function availableMCPEndpoint(
  node: HookwayNode,
  state: Pick<GraphState, "nodes" | "otherFlows">,
): HookwayNode {
  if (node.data.nodeType !== "mcp_trigger") return node;
  const paths = new Set(
    [state, ...Object.values(state.otherFlows)].flatMap((flow) =>
      flow.nodes
        .filter((node) =>
          ["mcp_trigger", "http_trigger"].includes(node.data.nodeType),
        )
        .map((node) => node.data.options.path),
    ),
  );
  const original = String(node.data.options.path ?? "/mcp");
  let path = original;
  let suffix = 2;
  while (paths.has(path)) path = `${original}-${suffix++}`;
  if (path === original) return node;
  let publicURL = node.data.options.public_url;
  try {
    const url = new URL(String(publicURL));
    url.pathname = path;
    publicURL = url.toString();
  } catch {
    /* Preserve invalid drafts for validation instead of guessing an origin. */
  }
  return {
    ...node,
    data: {
      ...node.data,
      options: { ...node.data.options, path, public_url: publicURL },
    },
  };
}
