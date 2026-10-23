import { beforeEach, expect, test } from "vitest";
import { useGraphStore } from "./store";
import { fromYaml } from "./fromYaml";
import { toBundle } from "./bundle";
import { templateSubgraph } from "./templates";
import { createNode } from "./model";

beforeEach(() => useGraphStore.getState().clear());

test("subsequent Add actions allocate names after the imported node IDs", () => {
  const source = fromYaml("flows: {main: {nodes: [{id: http_trigger_1, type: http_trigger}], edges: []}}");
  useGraphStore.getState().appendExampleGraph(source);
  useGraphStore.getState().addNode(createNode("http_trigger", { x: 0, y: 0 }));
  expect(useGraphStore.getState().nodes.map((node) => node.id)).toEqual(["http_trigger_1", "http_trigger_2"]);
});

test("repeated appends rename IDs and edges, preserve handles/options, and land below existing content", () => {
  const source = fromYaml(`flows:
  source:
    nodes:
      - {id: in, type: http_trigger, path: /w, description: Original}
      - {id: ack, type: respond, status: 200}
    edges: [in:out>ack]
`);
  const original = structuredClone(source);
  useGraphStore.getState().setGraph(source);
  const bottom = Math.max(...source.nodes.map((node) => node.position.y + 160));
  useGraphStore.getState().appendExampleGraph(source);
  useGraphStore.getState().appendExampleGraph(source);
  const state = useGraphStore.getState();
  expect(state.nodes.map((node) => node.id)).toEqual(["in", "ack", "in_2", "ack_2", "in_3", "ack_3"]);
  expect(state.edges.map((edge) => [edge.source, edge.target, edge.sourceHandle])).toEqual([
    ["in", "ack", "out"], ["in_2", "ack_2", "out"], ["in_3", "ack_3", "out"],
  ]);
  expect(new Set(state.edges.map((edge) => edge.id)).size).toBe(3);
  expect(Math.min(...state.nodes.slice(2, 4).map((node) => node.position.y))).toBeGreaterThan(bottom);
  expect(state.nodes[2].data.description).toBe("Original");
  expect(source).toEqual(original);
});

test("MCP append stays in the active flow and imports no settings or resources", () => {
  const source = templateSubgraph("mcp-gateway")!;
  const existing = fromYaml("server: {addr: ':9999'}\nflows: {current: {nodes: [{id: in, type: http_trigger, path: /mcp}], edges: [], delivery_failure_policy: skip_failed, capture_failures: true}}\n");
  existing.configExtra = { credentials: { existing: {} }, state_stores: { local: {} } };
  existing.connections = { existing: {} };
  existing.destinations = { existing: {} };
  existing.coreReplayConfig = { enabled: true };
  existing.replayServiceConfig = { database: {} };
  useGraphStore.getState().setGraph(existing);
  const before = useGraphStore.getState().document();
  let updates = 0;
  const unsubscribe = useGraphStore.subscribe(() => updates++);
  useGraphStore.getState().appendExampleGraph(source);
  unsubscribe();
  const after = useGraphStore.getState().document();
  expect(updates).toBe(1);
  expect({ ...after, nodes: before.nodes, edges: before.edges }).toEqual(before);
  const mcp = after.nodes.find((node) => node.data.nodeType === "mcp_trigger")!;
  expect(mcp.data.options).toEqual(source.nodes.find((node) => node.data.nodeType === "mcp_trigger")!.data.options);
});

test("append preserves literal provenance and ordinary resource/environment references on export", () => {
  const source = fromYaml('flows: {main: {nodes: [{id: send, type: http_request, description: "Literal \\u0024{TOKEN}", url: "${API_URL}", credential: {profile: upstream}}], edges: []}}');
  useGraphStore.getState().appendExampleGraph(source);
  const node = useGraphStore.getState().nodes[0];
  expect(node.data.literalDollarPaths).toEqual(source.nodes[0].data.literalDollarPaths);
  expect(node.data.options.credential).toEqual({ profile: "upstream" });
  const text = toBundle(useGraphStore.getState().document()).files["core.yaml"];
  expect(text).toContain('\\u0024{TOKEN}');
  expect(text).toContain('${API_URL}');
});
