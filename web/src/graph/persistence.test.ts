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

test("loadGraph promotes a legacy replay block without changing the record key", async () => {
  await saveGraph({
    ...emptyDocument(),
    coreReplayConfig: null,
    configExtra: { replay: { url: "https://replay.internal", capture_token: "${REPLAY_CAPTURE_TOKEN}" }, logging: { level: "info" } },
  });
  const loaded = await loadGraph();
  expect(loaded?.coreReplayConfig).toEqual({ url: "https://replay.internal", capture_token: "${REPLAY_CAPTURE_TOKEN}" });
  expect(loaded?.configExtra).toEqual({ logging: { level: "info" } });
});
