import { beforeEach, expect, test } from "vitest";
import { saveGraph, loadGraph, clearGraph } from "./persistence";
import { createNode, resetIdCounters } from "./model";

beforeEach(async () => {
  resetIdCounters();
  await clearGraph();
});

test("loadGraph returns null when nothing saved", async () => {
  expect(await loadGraph()).toBeNull();
});

test("saveGraph then loadGraph round-trips nodes and edges", async () => {
  const ep = createNode("endpoint", { x: 1, y: 2 });
  const d = createNode("destination", { x: 3, y: 4 });
  const edges = [{ id: "e1", source: ep.id, target: d.id, data: { target: "q", create: true } }];
  await saveGraph([ep, d], edges as any);
  const loaded = await loadGraph();
  expect(loaded!.nodes).toHaveLength(2);
  expect(loaded!.edges[0]).toMatchObject({ id: "e1", data: { target: "q", create: true } });
});

test("clearGraph removes the saved record", async () => {
  const ep = createNode("endpoint", { x: 0, y: 0 });
  await saveGraph([ep], []);
  await clearGraph();
  expect(await loadGraph()).toBeNull();
});
