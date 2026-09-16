import { beforeEach, expect, test } from "vitest";
import { toConfig } from "./toConfig";
import { createNode, emptyDocument, resetIdCounters } from "./model";

beforeEach(() => resetIdCounters());

test("exports flows with no version field and no endpoints key", () => {
  const trigger = createNode("http_trigger", { x: 0, y: 0 });
  const pub = createNode("publish", { x: 0, y: 0 });
  pub.data.options = { destination: "primary", target: "gh-webhooks" };
  const { config } = toConfig({
    ...emptyDocument(),
    nodes: [trigger, pub],
    edges: [{ id: "e1", source: trigger.id, target: pub.id, sourceHandle: "out", targetHandle: "in" }],
    destinations: { primary: { type: "webhook", webhook: { url: "https://x" } } },
  });
  // No config version field exists: the export must not invent one, and
  // there is no legacy endpoints key to fall back to.
  expect(config.schema_version).toBeUndefined();
  expect(config.endpoints).toBeUndefined();
  const flow = (config.flows as Record<string, { nodes: unknown[]; edges: string[] }>).main;
  expect(flow.edges).toEqual([`${trigger.data.yamlId}>${pub.data.yamlId}`]);
  expect(flow.nodes[0]).toMatchObject({ type: "http_trigger" });
});

test("labelled switch ports export as from:port>to", () => {
  const sw = createNode("switch", { x: 0, y: 0 });
  const pub = createNode("publish", { x: 0, y: 0 });
  const { config } = toConfig({
    ...emptyDocument(),
    nodes: [sw, pub],
    edges: [{ id: "e1", source: sw.id, target: pub.id, sourceHandle: "matched", targetHandle: "in" }],
  });
  const flow = (config.flows as Record<string, { edges: string[] }>).main;
  expect(flow.edges[0]).toBe(`${sw.data.yamlId}:matched>${pub.data.yamlId}`);
});

test("preserves explicit false, empty and null values in authored config", () => {
  const node = createNode("filter", { x: 0, y: 0 });
  node.data.options = { conditions: [{ source: "body", path: "active", operator: "eq", value: false }, { source: "body", path: "value", operator: "eq", value: null }], empty: "", list: [] };
  const doc = { ...emptyDocument(), nodes: [node], configExtra: { metrics: { enabled: false } } };
  const { config } = toConfig(doc);
  expect(config.metrics).toEqual({ enabled: false });
  expect((config.flows as any).main.nodes[0]).toMatchObject(node.data.options);
});
