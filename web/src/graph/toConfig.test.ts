import { beforeEach, expect, test } from "vitest";
import { toConfig } from "./toConfig";
import { createNode, emptyDocument, resetIdCounters } from "./model";

beforeEach(() => resetIdCounters());

test("exports schema_version 2 flows, not endpoints", () => {
  const trigger = createNode("http_trigger", { x: 0, y: 0 });
  const pub = createNode("publish", { x: 0, y: 0 });
  pub.data.options = { destination: "primary", target: "gh-webhooks" };
  const { config } = toConfig({
    ...emptyDocument(),
    nodes: [trigger, pub],
    edges: [{ id: "e1", source: trigger.id, target: pub.id, sourceHandle: "out", targetHandle: "in" }],
    destinations: { primary: { type: "webhook", webhook: { url: "https://x" } } },
  });
  expect(config.schema_version).toBe(2);
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
