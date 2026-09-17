import { beforeEach, expect, test } from "vitest";
import { useGraphStore } from "./store";
import { createNode, emptyDocument, resetIdCounters } from "./model";
import { templateSubgraph } from "./templates";

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

test("addFlow returns false for an empty or duplicate name", () => {
  expect(useGraphStore.getState().addFlow("")).toBe(false);
  expect(useGraphStore.getState().addFlow("main")).toBe(false);
  expect(useGraphStore.getState().addFlow("billing")).toBe(true);
  expect(useGraphStore.getState().activeFlow).toBe("billing");
});

test("renameFlow ignores empty, whitespace, and colliding names", () => {
  useGraphStore.getState().addFlow("billing");
  useGraphStore.getState().renameFlow("");
  useGraphStore.getState().renameFlow("   ");
  expect(useGraphStore.getState().activeFlow).toBe("billing");
  useGraphStore.getState().renameFlow("main");
  expect(useGraphStore.getState().activeFlow).toBe("billing");
  useGraphStore.getState().renameFlow("payments");
  expect(useGraphStore.getState().activeFlow).toBe("payments");
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

test("arrangeNodes spreads overlapping nodes", () => {
  const a = createNode("http_trigger", { x: 0, y: 0 });
  const b = createNode("publish", { x: 0, y: 0 });
  useGraphStore.setState({
    nodes: [a, b],
    edges: [{ id: "e1", source: a.id, target: b.id, sourceHandle: "out", targetHandle: "in" }],
  });
  useGraphStore.getState().arrangeNodes();
  const [left, right] = useGraphStore.getState().nodes;
  expect(left.position).not.toEqual(right.position);
});

test("setGraph lays out stacked nodes on inactive flows", () => {
  const a = createNode("http_trigger", { x: 0, y: 0 });
  const b = createNode("publish", { x: 0, y: 0 });
  useGraphStore.getState().setGraph({
    ...emptyDocument(),
    nodes: [createNode("filter", { x: 40, y: 40 })],
    otherFlows: {
      billing: {
        nodes: [a, b],
        edges: [{ id: "e1", source: a.id, target: b.id, sourceHandle: "out", targetHandle: "in" }],
      },
    },
  });
  const billing = useGraphStore.getState().otherFlows.billing.nodes;
  expect(billing[0].position).not.toEqual(billing[1].position);
});

test("setActiveFlow lays out a stacked snapshot", () => {
  const a = createNode("http_trigger", { x: 0, y: 0 });
  const b = createNode("publish", { x: 0, y: 0 });
  useGraphStore.setState({
    ...emptyDocument(),
    nodes: [createNode("filter", { x: 40, y: 40 })],
    otherFlows: {
      billing: {
        nodes: [a, b],
        edges: [{ id: "e1", source: a.id, target: b.id, sourceHandle: "out", targetHandle: "in" }],
      },
    },
    selectedId: null,
    selectedEdgeId: null,
  });
  useGraphStore.getState().setActiveFlow("billing");
  const nodes = useGraphStore.getState().nodes;
  expect(nodes).toHaveLength(2);
  expect(nodes[0].position).not.toEqual(nodes[1].position);
});

test("insertSubgraph adds a template's nodes and edges at the drop point", () => {
  useGraphStore.getState().clear();
  const sub = templateSubgraph("slack-notify")!;
  useGraphStore.getState().insertSubgraph(sub, { x: 400, y: 120 });
  const s = useGraphStore.getState();
  expect(s.nodes.map((n) => n.id).sort()).toEqual(["fmt", "post"]);
  expect(s.edges).toHaveLength(1);
  expect(Math.min(...s.nodes.map((n) => n.position.x))).toBe(400);
  expect(Math.min(...s.nodes.map((n) => n.position.y))).toBe(120);
});

test("inserting the same template twice produces unique node and edge ids", () => {
  useGraphStore.getState().clear();
  const sub = templateSubgraph("slack-notify")!;
  useGraphStore.getState().insertSubgraph(sub, { x: 0, y: 0 });
  useGraphStore.getState().insertSubgraph(sub, { x: 0, y: 200 });
  const s = useGraphStore.getState();
  expect(s.nodes).toHaveLength(4);
  const nodeIds = s.nodes.map((n) => n.id);
  expect(new Set(nodeIds).size).toBe(4);
  const yamlIds = s.nodes.map((n) => n.data.yamlId);
  expect(new Set(yamlIds).size).toBe(4);
  const edgeIds = s.edges.map((e) => e.id);
  expect(new Set(edgeIds).size).toBe(edgeIds.length);
  expect(s.edges).toHaveLength(2);
});

test("insertSubgraph does not collide with a hand-added node of the same id", () => {
  useGraphStore.getState().clear();
  // A node the operator added by hand that happens to use a template id. This
  // is the case that silently exports a duplicate `id:` if renaming is skipped.
  useGraphStore.getState().addNode({
    id: "fmt",
    type: "flow",
    position: { x: 0, y: 0 },
    data: { nodeType: "code", yamlId: "fmt", options: { script: "return {};" } },
  });
  const sub = templateSubgraph("slack-notify")!;
  useGraphStore.getState().insertSubgraph(sub, { x: 0, y: 0 });
  const s = useGraphStore.getState();
  const ids = s.nodes.map((n) => n.id);
  expect(ids).toEqual(["fmt", "fmt_2", "post"]);
  expect(new Set(s.nodes.map((n) => n.data.yamlId)).size).toBe(3);
  expect(s.edges[0].source).toBe("fmt_2");
});
