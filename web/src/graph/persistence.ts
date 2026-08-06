import Dexie, { type Table } from "dexie";
import type { Edge } from "@xyflow/react";
import type { HookwayNode } from "./model";

interface GraphRecord {
  id: string;
  nodes: HookwayNode[];
  edges: Edge[];
  serverConfig?: Record<string, unknown>;
  configExtra?: Record<string, unknown>;
}

class HookwayDB extends Dexie {
  graphs!: Table<GraphRecord, string>;
  constructor() {
    super("hookway-config-ui");
    // v2 adds serverConfig/configExtra to the record. They aren't indexed, so
    // the schema string is unchanged; the bump just documents the shape change.
    // v1 records load with the fields undefined and default on read.
    this.version(2).stores({ graphs: "id" });
  }
}

const db = new HookwayDB();
const CURRENT = "current";

// All three functions are best-effort: IndexedDB can legitimately fail (storage
// quota, private-browsing restrictions, disabled storage), and persistence must
// never break the editor. Errors are swallowed; a failed load restores nothing.
export async function saveGraph(
  nodes: HookwayNode[],
  edges: Edge[],
  serverConfig?: Record<string, unknown>,
  configExtra?: Record<string, unknown>,
): Promise<void> {
  try {
    await db.graphs.put({ id: CURRENT, nodes, edges, serverConfig, configExtra });
  } catch {
    /* best effort — ignore storage failures */
  }
}

export async function loadGraph(): Promise<{
  nodes: HookwayNode[];
  edges: Edge[];
  serverConfig?: Record<string, unknown>;
  configExtra?: Record<string, unknown>;
} | null> {
  try {
    const rec = await db.graphs.get(CURRENT);
    return rec
      ? { nodes: rec.nodes, edges: rec.edges, serverConfig: rec.serverConfig, configExtra: rec.configExtra }
      : null;
  } catch {
    return null;
  }
}

export async function clearGraph(): Promise<void> {
  try {
    await db.graphs.delete(CURRENT);
  } catch {
    /* best effort — ignore storage failures */
  }
}
