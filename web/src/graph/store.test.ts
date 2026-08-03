import { beforeEach, expect, test } from "vitest";
import { useGraphStore } from "./store";
import { createNode, resetIdCounters } from "./model";

beforeEach(() => {
  resetIdCounters();
  useGraphStore.setState({ nodes: [], edges: [], selectedId: null, selectedEdgeId: null });
});

test("addNode appends a node", () => {
  useGraphStore.getState().addNode(createNode("endpoint", { x: 0, y: 0 }));
  expect(useGraphStore.getState().nodes).toHaveLength(1);
});

test("updateNodeData patches the node's data immutably", () => {
  const n = createNode("filter", { x: 0, y: 0 });
  useGraphStore.getState().addNode(n);
  useGraphStore.getState().updateNodeData(n.id, { path: "action", value: "opened" });
  const updated = useGraphStore.getState().nodes[0];
  expect(updated.data).toMatchObject({ path: "action", value: "opened", kind: "filter" });
});

test("onConnect adds a valid endpoint->destination edge and rejects invalid", () => {
  const ep = createNode("endpoint", { x: 0, y: 0 });
  const d = createNode("destination", { x: 100, y: 0 });
  const s = useGraphStore.getState();
  s.addNode(ep);
  s.addNode(d);
  s.onConnect({ source: ep.id, target: d.id, sourceHandle: null, targetHandle: null });
  expect(useGraphStore.getState().edges).toHaveLength(1);
  // invalid: destination -> endpoint
  s.onConnect({ source: d.id, target: ep.id, sourceHandle: null, targetHandle: null });
  expect(useGraphStore.getState().edges).toHaveLength(1);
});

test("setSelected tracks selection", () => {
  useGraphStore.getState().setSelected("endpoint_1");
  expect(useGraphStore.getState().selectedId).toBe("endpoint_1");
  useGraphStore.getState().setSelected(null);
  expect(useGraphStore.getState().selectedId).toBeNull();
});

test("onConnect defaults edge target to the destination name and create=true", () => {
  const ep = createNode("endpoint", { x: 0, y: 0 });
  const d = createNode("destination", { x: 100, y: 0 });
  const s = useGraphStore.getState();
  s.addNode(ep); s.addNode(d);
  s.onConnect({ source: ep.id, target: d.id, sourceHandle: null, targetHandle: null });
  const edge = useGraphStore.getState().edges[0];
  expect(edge.data).toMatchObject({ target: d.data.name, create: true });
});

test("onConnect creates parallel routes with distinct default targets and ids", () => {
  const ep = createNode("endpoint", { x: 0, y: 0 });
  const d = createNode("destination", { x: 100, y: 0 });
  const s = useGraphStore.getState();
  s.addNode(ep);
  s.addNode(d);
  s.onConnect({ source: ep.id, target: d.id, sourceHandle: null, targetHandle: null });
  s.onConnect({ source: ep.id, target: d.id, sourceHandle: null, targetHandle: null });
  const edges = useGraphStore.getState().edges;
  expect(edges).toHaveLength(2);
  const targets = edges.map((e) => (e.data as { target: string }).target);
  expect(targets[0]).toBe(d.data.name); // first defaults to the destination name
  expect(targets[1]).toBe(`${d.data.name}-2`); // second avoids a duplicate route
  expect(edges[0].id).not.toBe(edges[1].id); // unique ids
});

test("updateEdgeData patches edge data immutably", () => {
  const ep = createNode("endpoint", { x: 0, y: 0 });
  const d = createNode("destination", { x: 100, y: 0 });
  const s = useGraphStore.getState();
  s.addNode(ep); s.addNode(d);
  s.onConnect({ source: ep.id, target: d.id, sourceHandle: null, targetHandle: null });
  const id = useGraphStore.getState().edges[0].id;
  useGraphStore.getState().updateEdgeData(id, { target: "github-webhooks" });
  expect((useGraphStore.getState().edges[0].data as { target: string }).target).toBe("github-webhooks");
});

test("selecting a node clears edge selection and vice versa", () => {
  const s = useGraphStore.getState();
  s.setSelected("endpoint_1");
  expect(useGraphStore.getState().selectedEdgeId).toBeNull();
  s.setSelectedEdge("e1");
  expect(useGraphStore.getState().selectedId).toBeNull();
  expect(useGraphStore.getState().selectedEdgeId).toBe("e1");
});

test("setGraph replaces the graph, clears selection, and seeds counters", () => {
  const ep = createNode("endpoint", { x: 0, y: 0 });
  useGraphStore.getState().setSelected("x");
  useGraphStore.getState().setGraph([ep], []);
  expect(useGraphStore.getState().nodes).toHaveLength(1);
  expect(useGraphStore.getState().selectedId).toBeNull();
});

test("clear empties the graph and selection", () => {
  const ep = createNode("endpoint", { x: 0, y: 0 });
  useGraphStore.setState({ nodes: [ep], edges: [], selectedId: ep.id, selectedEdgeId: null });
  useGraphStore.getState().clear();
  expect(useGraphStore.getState().nodes).toHaveLength(0);
  expect(useGraphStore.getState().selectedId).toBeNull();
});
