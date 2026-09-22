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

test("read_body and no_auth cannot start an on_error edge", () => {
  const body = pair("read_body", "rate_limit");
  expect(isValidConnection(connect(body.from, body.to, "on_error"), body.nodes, [])).toBe(false);
  const na = pair("no_auth", "envelope");
  expect(isValidConnection(connect(na.from, na.to, "on_error"), na.nodes, [])).toBe(false);
});

test("self-loop is invalid", () => {
  resetIdCounters();
  const n = createNode("filter", { x: 0, y: 0 });
  expect(isValidConnection({ source: n.id, target: n.id, sourceHandle: "out", targetHandle: "in" }, [n], [])).toBe(false);
});

test("duplicate edges are refused", () => {
  const { from, to, nodes } = pair("envelope", "filter");
  const edges: Edge[] = [{ id: "e1", source: from.id, target: to.id }];
  expect(isValidConnection(connect(from, to), nodes, edges)).toBe(false);
});

 test("refuses cycles and undeclared switch ports", () => {
 const {from,to,nodes}=pair("filter","filter");
 expect(isValidConnection(connect(from,to),nodes,[{id:"back",source:to.id,target:from.id}])).toBe(false);
 const sw=pair("switch","publish");
 expect(isValidConnection(connect(sw.from,sw.to,"ghost"),sw.nodes,[])).toBe(false);
 expect(isValidConnection(connect(sw.from,sw.to,"matched"),sw.nodes,[])).toBe(true);
 });

test("if_else accepts only its true and false ports", () => {
  const { from, to, nodes } = pair("if_else", "publish");
  expect(isValidConnection(connect(from, to, "true"), nodes, [])).toBe(true);
  expect(isValidConnection(connect(from, to, "false"), nodes, [])).toBe(true);
  expect(isValidConnection(connect(from, to, "maybe"), nodes, [])).toBe(false);
  // A labelled node has no unlabelled default output to fall back on.
  expect(isValidConnection(connect(from, to, "out"), nodes, [])).toBe(false);
});

test("a labelled port takes exactly one edge", () => {
  resetIdCounters();
  const from = createNode("if_else", { x: 0, y: 0 });
  const a = createNode("publish", { x: 100, y: 0 });
  const b = createNode("publish", { x: 100, y: 80 });
  const taken: Edge[] = [{ id: "e1", source: from.id, target: a.id, sourceHandle: "true" }];
  expect(isValidConnection(connect(from, b, "true"), [from, a, b], taken)).toBe(false);
  expect(isValidConnection(connect(from, b, "false"), [from, a, b], taken)).toBe(true);
});

test("publish may connect to another publish once", () => {
  const { from, to, nodes } = pair("publish", "publish");
  expect(isValidConnection(connect(from, to), nodes, [])).toBe(true);
  const third = createNode("publish", { x: 200, y: 0 });
  const taken: Edge[] = [{ id: "e1", source: from.id, target: to.id, sourceHandle: "out" }];
  expect(isValidConnection(connect(from, third), [...nodes, third], taken)).toBe(false);
});

test("publish cannot connect to respond", () => {
  const { from, to, nodes } = pair("publish", "respond");
  expect(isValidConnection(connect(from, to), nodes, [])).toBe(false);
});
