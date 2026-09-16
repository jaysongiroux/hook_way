import { beforeEach, expect, test } from "vitest";
import { useGraphStore } from "./store";
import { createNode, resetIdCounters } from "./model";

beforeEach(() => {
  resetIdCounters();
  useGraphStore.getState().clear();
});

test("addNode appends a node", () => {
  useGraphStore.getState().addNode(createNode("http_trigger", { x: 0, y: 0 }));
  expect(useGraphStore.getState().nodes).toHaveLength(1);
});

test("updateNodeData patches options", () => {
  const n = createNode("http_trigger", { x: 0, y: 0 });
  useGraphStore.getState().addNode(n);
  useGraphStore.getState().updateNodeData(n.id, { options: { ...n.data.options, path: "/webhooks/github" } });
  expect(useGraphStore.getState().nodes[0].data.options.path).toBe("/webhooks/github");
});

test("onConnect adds a valid trigger->read_body edge and rejects a gate after read_body", () => {
  const trigger = createNode("http_trigger", { x: 0, y: 0 });
  const body = createNode("read_body", { x: 100, y: 0 });
  const rl = createNode("rate_limit", { x: 200, y: 0 });
  const s = useGraphStore.getState();
  s.addNode(trigger);
  s.addNode(body);
  s.addNode(rl);
  s.onConnect({ source: trigger.id, target: body.id, sourceHandle: "out", targetHandle: "in" });
  expect(useGraphStore.getState().edges).toHaveLength(1);
  s.onConnect({ source: body.id, target: rl.id, sourceHandle: "out", targetHandle: "in" });
  expect(useGraphStore.getState().edges).toHaveLength(1);
});

test("setGraph replaces the document and clears selection", () => {
  const n = createNode("filter", { x: 0, y: 0 });
  useGraphStore.getState().setSelected("x");
  useGraphStore.getState().setGraph({
    nodes: [n],
    edges: [],
    serverConfig: { addr: ":8080" },
    configExtra: {},
    destinations: {},
    activeFlow: "github",
    otherFlows: {},
  });
  expect(useGraphStore.getState().nodes).toHaveLength(1);
  expect(useGraphStore.getState().activeFlow).toBe("github");
  expect(useGraphStore.getState().selectedId).toBeNull();
});
