import { beforeEach, expect, test } from "vitest";
import { createNode, isNodeKind, nextName, resetIdCounters } from "./model";

beforeEach(() => resetIdCounters());

test("nextName increments per kind independently", () => {
  expect(nextName("endpoint")).toBe("endpoint_1");
  expect(nextName("endpoint")).toBe("endpoint_2");
  expect(nextName("destination")).toBe("destination_1");
});

test("createNode makes an endpoint node with source-shaped data", () => {
  const n = createNode("endpoint", { x: 10, y: 20 });
  expect(n.type).toBe("endpoint");
  expect(n.id).toBe("endpoint_1");
  expect(n.position).toEqual({ x: 10, y: 20 });
  expect(n.data).toMatchObject({ kind: "endpoint", name: "endpoint_1" });
  expect((n.data as { path: string }).path).toBe("/webhooks/endpoint_1");
});

test("createNode makes a destination node defaulting to rabbitmq", () => {
  const n = createNode("destination", { x: 0, y: 0 });
  expect(n.type).toBe("destination");
  expect(n.id).toBe("destination_1");
  expect(n.data).toMatchObject({ kind: "destination", name: "destination_1", destType: "rabbitmq" });
});

test("createNode makes a filter node with condition defaults and both-handle kind", () => {
  const n = createNode("filter", { x: 5, y: 6 });
  expect(n.type).toBe("filter");
  expect(n.id).toBe("filter_1");
  expect(n.data).toMatchObject({
    kind: "filter",
    name: "filter_1",
    source: "body",
    path: "",
    operator: "eq",
    value: "",
  });
});

test("filter has its own name counter", () => {
  expect(nextName("filter")).toBe("filter_1");
  expect(nextName("filter")).toBe("filter_2");
  expect(nextName("endpoint")).toBe("endpoint_1");
});

test("isNodeKind guards the three kinds", () => {
  expect(isNodeKind("endpoint")).toBe(true);
  expect(isNodeKind("destination")).toBe(true);
  expect(isNodeKind("filter")).toBe(true);
  expect(isNodeKind("bogus")).toBe(false);
});

test("endpoint node has default hmac auth in env mode", () => {
  const n = createNode("endpoint", { x: 0, y: 0 });
  expect(n.data).toMatchObject({
    auth: { type: "hmac_sha256", scheme: "hmac_sha256_hex", secret: "", secretMode: "env", signatureHeader: "X-Signature-256" },
  });
});

test("destination node seeds rabbitmq default settings", () => {
  const n = createNode("destination", { x: 0, y: 0 });
  expect(n.data).toMatchObject({
    destType: "rabbitmq",
    settings: { url: "", exchange: "", exchange_type: "direct" },
  });
});
