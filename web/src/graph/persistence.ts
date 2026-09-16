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
const CURRENT = "current";

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
      nodes: rec.nodes ?? [],
      edges: rec.edges ?? [],
      serverConfig: rec.serverConfig ?? base.serverConfig,
      configExtra: rec.configExtra ?? {},
      destinations: rec.destinations ?? {},
      activeFlow: rec.activeFlow ?? base.activeFlow,
      otherFlows: rec.otherFlows ?? {},
      deliveryFailurePolicy: rec.deliveryFailurePolicy,
    };
  } catch {
    return null;
  }
}

export async function clearGraph(): Promise<void> {
  try {
    await db.graphs.delete(CURRENT);
  } catch {
    /* best effort */
  }
}
