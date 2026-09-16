import { expect, test } from "vitest";
import { isValidConnection } from "./connection";
import { createNode, resetIdCounters, type HookwayNode } from "./model";
import type { Edge } from "@xyflow/react";

function pair(fromType: string, toType: string): { from: HookwayNode; to: HookwayNode; nodes: HookwayNode[] } {
  resetIdCounters();
  const from = createNode(fromType, { x: 0, y: 0 });
  const to = createNode(toType, { x: 100, y: 0 });
  return { from, to, nodes: [from, to] };
}

function connect(from: HookwayNode, to: HookwayNode, sourceHandle = "out") {
  return { source: from.id, target: to.id, sourceHandle, targetHandle: "in" };
}

test("allows RequestHead -> RequestHead", () => {
  const { from, to, nodes } = pair("http_trigger", "rate_limit");
  expect(isValidConnection(connect(from, to), nodes, [])).toBe(true);
});

test("allows RequestHead -> RawRequest (read_body)", () => {
  const { from, to, nodes } = pair("http_trigger", "read_body");
  expect(isValidConnection(connect(from, to), nodes, [])).toBe(true);
});

test("REJECTS RawRequest -> RequestHead (a gate after read_body)", () => {
  const { from, to, nodes } = pair("read_body", "rate_limit");
  expect(isValidConnection(connect(from, to), nodes, [])).toBe(false);
});

test("REJECTS RawRequest -> Envelope (auth bypass)", () => {
  const { from, to, nodes } = pair("read_body", "envelope");
  expect(isValidConnection(connect(from, to), nodes, [])).toBe(false);
});

test("on_error carries the failing node's input, not its output", () => {
  const { from, to, nodes } = pair("auth", "no_auth");
  expect(isValidConnection(connect(from, to, "on_error"), nodes, [])).toBe(true);
  const gate = pair("auth", "rate_limit");
  expect(isValidConnection(connect(gate.from, gate.to, "on_error"), gate.nodes, [])).toBe(false);
});

test("self-loop is invalid", () => {
  resetIdCounters();
  const n = createNode("filter", { x: 0, y: 0 });
  expect(isValidConnection({ source: n.id, target: n.id, sourceHandle: "out", targetHandle: "in" }, [n], [])).toBe(false);
});

test("duplicate legal edges remain allowed", () => {
  const { from, to, nodes } = pair("envelope", "filter");
  const edges: Edge[] = [{ id: "e1", source: from.id, target: to.id }];
  expect(isValidConnection(connect(from, to), nodes, edges)).toBe(true);
});
