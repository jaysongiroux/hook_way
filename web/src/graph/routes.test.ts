import { beforeEach, expect, test } from "vitest";
import { endpointRoutes, owningEndpointId, parallelGroups } from "./routes";
import { createNode, resetIdCounters } from "./model";
import type { Edge } from "@xyflow/react";

beforeEach(() => resetIdCounters());
const e = (id: string, s: string, t: string, data = { target: "q", create: true }): Edge => ({ id, source: s, target: t, data });

test("endpointRoutes lists direct routes with target/create", () => {
  const ep = createNode("endpoint", { x: 0, y: 0 });
  const d = createNode("destination", { x: 0, y: 0 });
  const rows = endpointRoutes(ep.id, [ep, d], [e("e1", ep.id, d.id, { target: "gh", create: false })]);
  expect(rows).toEqual([{ edgeId: "e1", dest: d.data.name, target: "gh", create: false }]);
});

test("endpointRoutes follows a filter chain to the destination", () => {
  const ep = createNode("endpoint", { x: 0, y: 0 });
  const f = createNode("filter", { x: 0, y: 0 });
  const d = createNode("destination", { x: 0, y: 0 });
  const rows = endpointRoutes(ep.id, [ep, f, d], [e("e1", ep.id, f.id), e("e2", f.id, d.id, { target: "t", create: true })]);
  expect(rows).toEqual([{ edgeId: "e2", dest: d.data.name, target: "t", create: true }]);
});

test("owningEndpointId walks back through a filter to the endpoint", () => {
  const ep = createNode("endpoint", { x: 0, y: 0 });
  const f = createNode("filter", { x: 0, y: 0 });
  const d = createNode("destination", { x: 0, y: 0 });
  const edges = [e("e1", ep.id, f.id), e("e2", f.id, d.id)];
  expect(owningEndpointId("e2", [ep, f, d], edges)).toBe(ep.id);
});

test("owningEndpointId returns the endpoint for a direct edge", () => {
  const ep = createNode("endpoint", { x: 0, y: 0 });
  const d = createNode("destination", { x: 0, y: 0 });
  expect(owningEndpointId("e1", [ep, d], [e("e1", ep.id, d.id)])).toBe(ep.id);
});

test("parallelGroups indexes edges sharing a source→target pair", () => {
  const edges = [
    { id: "a", source: "ep", target: "d" },
    { id: "b", source: "ep", target: "d" },
    { id: "c", source: "ep", target: "d2" },
  ] as any;
  const g = parallelGroups(edges);
  expect(g.get("a")).toEqual({ index: 0, count: 2 });
  expect(g.get("b")).toEqual({ index: 1, count: 2 });
  expect(g.get("c")).toEqual({ index: 0, count: 1 });
});
