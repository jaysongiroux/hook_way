import Dexie, { type Table } from "dexie";
import type { GraphDocument } from "./model";
import { emptyDocument } from "./model";

interface GraphRecord extends GraphDocument {
  id: string;
}

class HookwayDB extends Dexie {
  graphs!: Table<GraphRecord, string>;
  constructor() {
    super("hookway-config-ui");
    this.version(2).stores({ graphs: "id" });
    this.version(3).stores({ graphs: "id" });
  }
}

const db = new HookwayDB();
const CURRENT = "current.v4";

export async function saveGraph(doc: GraphDocument): Promise<void> {
  try {
    await db.graphs.put({ id: CURRENT, ...doc });
  } catch {
    /* best effort */
  }
}

export async function loadGraph(): Promise<GraphDocument | null> {
  try {
    const rec = await db.graphs.get(CURRENT);
    if (!rec) return null;
    const base = emptyDocument();
    return {
      ...base,
      literalConfigFields: rec.literalConfigFields ?? [],
      yamlMode: rec.yamlMode ?? "single",
      entryFilename: rec.entryFilename ?? "core.yaml",
      flowFiles: rec.flowFiles ?? {},
      replayFilename: rec.replayFilename ?? "replay.yaml",
      formDrafts: rec.formDrafts ?? {},
      nodes: rec.nodes ?? [],
      edges: rec.edges ?? [],
      serverConfig: rec.serverConfig ?? base.serverConfig,
      coreReplayConfig:
        rec.coreReplayConfig ??
        (isRecord(rec.configExtra?.replay)
          ? { ...(rec.configExtra.replay as Record<string, unknown>) }
          : null),
      replayServiceConfig: rec.replayServiceConfig ?? null,
      configExtra: isRecord(rec.configExtra?.replay)
        ? withoutLegacyReplay(rec.configExtra)
        : { ...(rec.configExtra ?? {}) },
      connections: rec.connections ?? {},
      destinations: rec.destinations ?? {},
      activeFlow: rec.activeFlow ?? base.activeFlow,
      otherFlows: rec.otherFlows ?? {},
      deliveryFailurePolicy: rec.deliveryFailurePolicy,
      flowExtra: rec.flowExtra,
    };
  } catch {
    return null;
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === "object" && !Array.isArray(value);
}

function withoutLegacyReplay(
  extra: Record<string, unknown> | undefined,
): Record<string, unknown> {
  const next = { ...(extra ?? {}) };
  delete next.replay;
  return next;
}

export async function clearGraph(): Promise<void> {
  try {
    await db.graphs.delete(CURRENT);
  } catch {
    /* best effort */
  }
}
