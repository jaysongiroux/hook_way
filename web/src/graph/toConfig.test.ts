import { beforeEach, expect, test } from "vitest";
import { toConfig, ExportError } from "./toConfig";
import { createNode, resetIdCounters } from "./model";
import type { Edge } from "@xyflow/react";

beforeEach(() => resetIdCounters());

function edge(source: string, target: string, data = { target: "q", create: true }): Edge {
  return { id: `${source}-${target}`, source, target, data };
}

test("endpoint -> destination yields a route with target and default server", () => {
  const ep = createNode("endpoint", { x: 0, y: 0 });
  const d = createNode("destination", { x: 0, y: 0 });
  const { config } = toConfig([ep, d], [edge(ep.id, d.id, { target: "gh-webhooks", create: true })]);
  expect((config.server as any).max_body_bytes).toBe(1048576);
  const e = (config.endpoints as any)[ep.data.name as string];
  expect(e.path).toBe("/webhooks/endpoint_1");
  expect(e.destinations).toEqual([{ destination: d.data.name, target: "gh-webhooks", create: true }]);
  expect((config.destinations as any)[d.data.name as string].type).toBe("rabbitmq");
});

test("endpoint -> filter -> two destinations puts the filter at endpoint level", () => {
  const ep = createNode("endpoint", { x: 0, y: 0 });
  const f = createNode("filter", { x: 0, y: 0 });
  const d1 = createNode("destination", { x: 0, y: 0 });
  const d2 = createNode("destination", { x: 0, y: 0 });
  const edges = [
    edge(ep.id, f.id),
    edge(f.id, d1.id, { target: "t1", create: true }),
    edge(f.id, d2.id, { target: "t2", create: true }),
  ];
  const { config } = toConfig([ep, f, d1, d2], edges);
  const e = (config.endpoints as any)[ep.data.name as string];
  expect(e.filters).toHaveLength(1); // shared prefix => endpoint-level
  expect(e.destinations).toHaveLength(2);
  expect(e.destinations[0].filters ?? []).toHaveLength(0); // no per-route filters
});

test("hmac secret in env mode is wrapped", () => {
  const ep = createNode("endpoint", { x: 0, y: 0 });
  (ep.data as any).auth.secret = "GH_SECRET";
  const d = createNode("destination", { x: 0, y: 0 });
  const { config } = toConfig([ep, d], [edge(ep.id, d.id)]);
  expect((config.endpoints as any)[ep.data.name as string].auth.secret).toBe("${GH_SECRET}");
});

test("default env-mode empty secret is omitted (no invalid ${})", () => {
  const ep = createNode("endpoint", { x: 0, y: 0 });
  const d = createNode("destination", { x: 0, y: 0 });
  const { config } = toConfig([ep, d], [edge(ep.id, d.id)]);
  const auth = (config.endpoints as any)[ep.data.name as string].auth;
  expect(auth.secret).toBeUndefined();
});

test("literal secret is exported raw, unwrapped", () => {
  const ep = createNode("endpoint", { x: 0, y: 0 });
  (ep.data as any).auth.secretMode = "literal";
  (ep.data as any).auth.secret = "rawval";
  const d = createNode("destination", { x: 0, y: 0 });
  const { config } = toConfig([ep, d], [edge(ep.id, d.id)]);
  const auth = (config.endpoints as any)[ep.data.name as string].auth;
  expect(auth.secret).toBe("rawval");
});

test("auth type 'none' exports a valid no_auth block, not an omitted auth", () => {
  const ep = createNode("endpoint", { x: 0, y: 0 });
  (ep.data as any).auth.type = "none";
  const d = createNode("destination", { x: 0, y: 0 });
  const { config } = toConfig([ep, d], [edge(ep.id, d.id)]);
  const auth = (config.endpoints as any)[ep.data.name as string].auth;
  expect(auth).toEqual({ type: "no_auth", scheme: "no_auth" });
});

test("divergent first filter: no shared prefix, each destination gets its own per-route filter", () => {
  const ep = createNode("endpoint", { x: 0, y: 0 });
  const fA = createNode("filter", { x: 0, y: 0 });
  const fB = createNode("filter", { x: 0, y: 0 });
  const d1 = createNode("destination", { x: 0, y: 0 });
  const d2 = createNode("destination", { x: 0, y: 0 });
  const edges = [
    edge(ep.id, fA.id),
    edge(ep.id, fB.id),
    edge(fA.id, d1.id, { target: "t1", create: true }),
    edge(fB.id, d2.id, { target: "t2", create: true }),
  ];
  const { config } = toConfig([ep, fA, fB, d1, d2], edges);
  const e = (config.endpoints as any)[ep.data.name as string];
  expect(e.filters).toBeUndefined();
  expect(e.destinations).toHaveLength(2);
  for (const dest of e.destinations) {
    expect(dest.filters).toHaveLength(1);
  }
});

test("nested prefix: shared filterA hoisted to endpoint, filterB stays per-route", () => {
  const ep = createNode("endpoint", { x: 0, y: 0 });
  const fA = createNode("filter", { x: 0, y: 0 });
  const fB = createNode("filter", { x: 0, y: 0 });
  const d1 = createNode("destination", { x: 0, y: 0 });
  const d2 = createNode("destination", { x: 0, y: 0 });
  const edges = [
    edge(ep.id, fA.id),
    edge(fA.id, fB.id),
    edge(fB.id, d1.id, { target: "t1", create: true }),
    edge(fA.id, d2.id, { target: "t2", create: true }),
  ];
  const { config } = toConfig([ep, fA, fB, d1, d2], edges);
  const e = (config.endpoints as any)[ep.data.name as string];
  expect(e.filters).toHaveLength(1);
  const d1Route = e.destinations.find((d: any) => d.destination === d1.data.name);
  const d2Route = e.destinations.find((d: any) => d.destination === d2.data.name);
  expect(d1Route.filters).toHaveLength(1);
  expect(d2Route.filters ?? []).toHaveLength(0);
});

test("a filter cycle throws ExportError", () => {
  const ep = createNode("endpoint", { x: 0, y: 0 });
  const f1 = createNode("filter", { x: 0, y: 0 });
  const f2 = createNode("filter", { x: 0, y: 0 });
  const edges = [edge(ep.id, f1.id), edge(f1.id, f2.id), edge(f2.id, f1.id)];
  expect(() => toConfig([ep, f1, f2], edges)).toThrow(ExportError);
});

test("a self-loop filter (filterA -> filterA) throws ExportError", () => {
  const ep = createNode("endpoint", { x: 0, y: 0 });
  const f1 = createNode("filter", { x: 0, y: 0 });
  const edges = [edge(ep.id, f1.id), edge(f1.id, f1.id)];
  expect(() => toConfig([ep, f1], edges)).toThrow(ExportError);
});

test("kafka destination exports brokers as a trimmed array", () => {
  const d = createNode("destination", { x: 0, y: 0 });
  (d.data as any).destType = "kafka";
  (d.data as any).settings = { brokers: "b1:9092, b2:9092" };
  const { config } = toConfig([d], []);
  const entry = (config.destinations as any)[d.data.name as string];
  expect(entry.type).toBe("kafka");
  expect(entry.kafka.brokers).toEqual(["b1:9092", "b2:9092"]);
});

test("destination bool/number/select settings map to typed YAML values", () => {
  const ep = createNode("endpoint", { x: 0, y: 0 });
  const d = createNode("destination", { x: 0, y: 0 });
  (d.data as any).settings = { url: "amqp://h", exchange: "x", exchange_type: "topic", durable: false, declare_queues: true, publish_timeout: "" };
  const { config } = toConfig([ep, d], [{ id: "e", source: ep.id, target: d.id, data: { target: "t", create: true } } as any]);
  const rmq = (config.destinations as any)[d.data.name as string].rabbitmq;
  expect(rmq.exchange_type).toBe("topic");
  expect(rmq.durable).toBe(false);       // bool always emitted
  expect(rmq.declare_queues).toBe(true);
  expect(rmq.publish_timeout).toBeUndefined(); // empty text omitted
});
