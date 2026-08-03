import { expect, test } from "vitest";
import { isValidConnection } from "./connection";
import { createNode, resetIdCounters, type HookwayNode } from "./model";
import type { Edge } from "@xyflow/react";

function graph(): { ep1: HookwayNode; ep2: HookwayNode; d1: HookwayNode } {
  resetIdCounters();
  const ep1 = createNode("endpoint", { x: 0, y: 0 });
  const ep2 = createNode("endpoint", { x: 0, y: 100 });
  const d1 = createNode("destination", { x: 200, y: 0 });
  return { ep1, ep2, d1 };
}

test("endpoint -> destination is valid", () => {
  const { ep1, d1 } = graph();
  expect(isValidConnection({ source: ep1.id, target: d1.id, sourceHandle: null, targetHandle: null }, [ep1, d1], [])).toBe(true);
});

test("two endpoints can share one destination", () => {
  const { ep1, ep2, d1 } = graph();
  const edges: Edge[] = [{ id: "e1", source: ep1.id, target: d1.id }];
  expect(isValidConnection({ source: ep2.id, target: d1.id, sourceHandle: null, targetHandle: null }, [ep1, ep2, d1], edges)).toBe(true);
});

test("endpoint -> endpoint is invalid", () => {
  const { ep1, ep2 } = graph();
  expect(isValidConnection({ source: ep1.id, target: ep2.id, sourceHandle: null, targetHandle: null }, [ep1, ep2], [])).toBe(false);
});

test("destination -> endpoint is invalid", () => {
  const { ep1, d1 } = graph();
  expect(isValidConnection({ source: d1.id, target: ep1.id, sourceHandle: null, targetHandle: null }, [ep1, d1], [])).toBe(false);
});

test("self-loop is invalid", () => {
  const { ep1 } = graph();
  expect(isValidConnection({ source: ep1.id, target: ep1.id, sourceHandle: null, targetHandle: null }, [ep1], [])).toBe(false);
});

test("duplicate endpoint->destination is now allowed (parallel routes)", () => {
  const { ep1, d1 } = graph();
  const edges: Edge[] = [{ id: "e1", source: ep1.id, target: d1.id }];
  expect(isValidConnection({ source: ep1.id, target: d1.id, sourceHandle: null, targetHandle: null }, [ep1, d1], edges)).toBe(true);
});

test("endpoint -> filter is valid", () => {
  resetIdCounters();
  const ep = createNode("endpoint", { x: 0, y: 0 });
  const f = createNode("filter", { x: 100, y: 0 });
  expect(isValidConnection({ source: ep.id, target: f.id, sourceHandle: null, targetHandle: null }, [ep, f], [])).toBe(true);
});

test("filter -> destination is valid", () => {
  resetIdCounters();
  const f = createNode("filter", { x: 0, y: 0 });
  const d = createNode("destination", { x: 100, y: 0 });
  expect(isValidConnection({ source: f.id, target: d.id, sourceHandle: null, targetHandle: null }, [f, d], [])).toBe(true);
});

test("filter -> filter is valid (chaining)", () => {
  resetIdCounters();
  const f1 = createNode("filter", { x: 0, y: 0 });
  const f2 = createNode("filter", { x: 100, y: 0 });
  expect(isValidConnection({ source: f1.id, target: f2.id, sourceHandle: null, targetHandle: null }, [f1, f2], [])).toBe(true);
});

test("filter -> endpoint is invalid", () => {
  resetIdCounters();
  const f = createNode("filter", { x: 0, y: 0 });
  const ep = createNode("endpoint", { x: 100, y: 0 });
  expect(isValidConnection({ source: f.id, target: ep.id, sourceHandle: null, targetHandle: null }, [f, ep], [])).toBe(false);
});

test("destination -> filter is invalid", () => {
  resetIdCounters();
  const d = createNode("destination", { x: 0, y: 0 });
  const f = createNode("filter", { x: 100, y: 0 });
  expect(isValidConnection({ source: d.id, target: f.id, sourceHandle: null, targetHandle: null }, [d, f], [])).toBe(false);
});
