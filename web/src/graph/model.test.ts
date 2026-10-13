import { beforeEach, expect, test } from "vitest";
import { createNode, isNodeKind, nextName, resetIdCounters } from "./model";

beforeEach(() => resetIdCounters());

test("nextName increments per node type", () => {
  expect(nextName("http_trigger")).toBe("http_trigger_1");
  expect(nextName("http_trigger")).toBe("http_trigger_2");
  expect(nextName("publish")).toBe("publish_1");
});

test("createNode makes an http_trigger with a path", () => {
  const n = createNode("http_trigger", { x: 10, y: 20 });
  expect(n.type).toBe("flow");
  expect(n.data.nodeType).toBe("http_trigger");
  expect(n.id).toBe("http_trigger_1");
  expect(n.data.options.path).toBe("/webhooks/http_trigger_1");
  expect(n.position).toEqual({ x: 10, y: 20 });
});

test("createNode seeds publish destination and target as required", () => {
  const n = createNode("publish", { x: 0, y: 0 });
  expect(n.data.options).toMatchObject({ destination: "", target: "" });
});

test("isNodeKind only accepts catalog types", () => {
  expect(isNodeKind("http_trigger")).toBe(true);
  expect(isNodeKind("endpoint")).toBe(false);
});
