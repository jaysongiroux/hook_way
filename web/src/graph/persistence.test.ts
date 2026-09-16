import { beforeEach, expect, test } from "vitest";
import { saveGraph, loadGraph, clearGraph } from "./persistence";
import { createNode, emptyDocument, resetIdCounters } from "./model";

beforeEach(async () => {
  resetIdCounters();
  await clearGraph();
});

test("loadGraph returns null when nothing saved", async () => {
  expect(await loadGraph()).toBeNull();
});

test("saveGraph then loadGraph round-trips nodes and edges", async () => {
  const n = createNode("http_trigger", { x: 1, y: 2 });
  const edges = [{ id: "e1", source: n.id, target: n.id }];
  await saveGraph({ ...emptyDocument(), nodes: [n], edges });
  const loaded = await loadGraph();
  expect(loaded!.nodes).toHaveLength(1);
  expect(loaded!.edges[0]).toMatchObject({ id: "e1" });
});

test("clearGraph removes the saved record", async () => {
  const n = createNode("http_trigger", { x: 0, y: 0 });
  await saveGraph({ ...emptyDocument(), nodes: [n], edges: [] });
  await clearGraph();
  expect(await loadGraph()).toBeNull();
});
