import Dexie, { type Table } from "dexie";
import type { Edge } from "@xyflow/react";
import type { HookwayNode } from "./model";

interface GraphRecord {
  id: string;
  nodes: HookwayNode[];
  edges: Edge[];
}

class HookwayDB extends Dexie {
  graphs!: Table<GraphRecord, string>;
  constructor() {
    super("hookway-config-ui");
    this.version(1).stores({ graphs: "id" });
  }
}

const db = new HookwayDB();
const CURRENT = "current";

// All three functions are best-effort: IndexedDB can legitimately fail (storage
// quota, private-browsing restrictions, disabled storage), and persistence must
// never break the editor. Errors are swallowed; a failed load restores nothing.
export async function saveGraph(nodes: HookwayNode[], edges: Edge[]): Promise<void> {
  try {
    await db.graphs.put({ id: CURRENT, nodes, edges });
  } catch {
    /* best effort — ignore storage failures */
  }
}

export async function loadGraph(): Promise<{ nodes: HookwayNode[]; edges: Edge[] } | null> {
  try {
    const rec = await db.graphs.get(CURRENT);
    return rec ? { nodes: rec.nodes, edges: rec.edges } : null;
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
