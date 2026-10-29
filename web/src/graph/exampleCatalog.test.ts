import { expect, test } from "vitest";
import { createExampleCatalog } from "./exampleCatalog";
import { repositoryExamples } from "./repositoryExamples";
import { fromBundle, toBundle } from "./bundle";
import { parse } from "yaml";

const flow = "nodes: [{id: in, type: http_trigger, path: /w}]\nedges: []\n";
const yaml = `server: {addr: ':8080'}\nflows:\n  main:\n    nodes: [{id: in, type: http_trigger, path: /w}]\n    edges: []\n`;

test("discovers nested YAML/YML, distinguishes duplicate basenames, and picks up new files", () => {
  const files = { "one/core.yaml": yaml, "two/core.yaml": yaml, "new/deep/new-example.yml": yaml };
  expect(createExampleCatalog(files).map((item) => [item.id, item.label])).toEqual([
    ["new/deep/new-example.yml", "New example"], ["one/core.yaml", "Core"], ["two/core.yaml", "Core"],
  ]);
});

test("excludes non-core YAML, invalid fixtures, empty configs, and fragments", () => {
  const files = {
    "invalid-many.yaml": "broken: [", "docker-compose.yaml": "broken: [",
    "monitoring/prometheus.yaml": "broken: [", "replay.yaml": "database: {}\ncore: {}",
    "api.yaml": "openapi: 3.1.0\npaths: {}", "settings.yaml": "endpoint: {}",
    "flow.yaml": flow, "empty.yaml": "flows: {}", "valid.yaml": yaml,
    "mcp-events/demo/runtime/core.yaml": yaml, "mcp-events/demo/evidence/core.yaml": yaml,
  };
  expect(createExampleCatalog(files).map((item) => item.id)).toEqual(["valid.yaml"]);
});

test("resolves only a core entry and its flow files into a portable bundle", () => {
  const [example] = createExampleCatalog({
    "nested/core.yaml": "flows: {orders: {file: flows/orders.yml}}",
    "nested/flows/orders.yml": flow,
    "nested/replay.yaml": "database: {}\ncore: {}",
  });
  expect(example.flows).toEqual([{ name: "orders", nodeCount: 1, edgeCount: 0 }]);
  expect(Object.keys(example.files)).toEqual(["core.yaml", "flows/orders.yml"]);
  const doc = fromBundle(example, null);
  expect(doc.yamlMode).toBe("split");
  expect(doc.flowFiles).toEqual({ orders: "flows/orders.yml" });
  expect(doc.replayServiceConfig).toBeNull();
});

test.each([
  ["flows: {orders: {file: missing.yaml}}", /Missing flow file/],
  ["flows: {orders: {file: ../escape.yaml}}", /path/i],
  ["flows: []", /flows must be a mapping/],
  ["flows: {orders: {nodes: [], edges: wrong}}", /nodes and edges/],
  ["flows: [", /core.yaml/],
])("rejects malformed candidates atomically: %s", (text, message) => {
  expect(() => createExampleCatalog({ "core.yaml": text })).toThrow(message);
});

test("repository discovery includes every eligible directory and all bundles round trip", () => {
  expect(repositoryExamples.length).toBeGreaterThan(60);
  for (const id of ["config.yaml", "multi-yaml/core.yaml", "openapi/core.yaml", "mcp-events.yaml", "production-local/core.yaml", "templates/mcp-gateway.yaml", "sentry-discord.yaml", "templates/sentry-ingest.yaml"]) {
    expect(repositoryExamples.some((item) => item.id === id), id).toBe(true);
  }
  for (const example of repositoryExamples) {
    const doc = fromBundle(example, null);
    expect(doc.nodes.length, example.id).toBeGreaterThan(0);
    const exported = toBundle(doc);
    const reopened = toBundle(fromBundle(exported, null));
    expect(reopened.entry).toBe(exported.entry);
    const parsed = (files: Record<string, string>) => Object.fromEntries(Object.entries(files).map(([name, text]) => [name, parse(text)]));
    expect(parsed(reopened.files), example.id).toEqual(parsed(exported.files));
  }
});
