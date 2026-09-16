import { beforeEach, expect, test } from "vitest";
import { layout, layoutAllFlows, maybeLayout, nodesStacked } from "./layout";
import { createNode, emptyDocument, resetIdCounters } from "./model";

beforeEach(() => {
  resetIdCounters();
});

test("layout spreads stacked nodes onto distinct positions", () => {
  const a = createNode("http_trigger", { x: 0, y: 0 });
  const b = createNode("publish", { x: 0, y: 0 });
  const laid = layout(
    [a, b],
    [{ id: "e1", source: a.id, target: b.id, sourceHandle: "out", targetHandle: "in" }],
  );
  expect(nodesStacked(laid)).toBe(false);
});

test("maybeLayout leaves an already-spread graph alone", () => {
  const a = createNode("http_trigger", { x: 10, y: 20 });
  const b = createNode("publish", { x: 200, y: 20 });
  const nodes = [a, b];
  expect(maybeLayout(nodes, [])).toBe(nodes);
});

test("layoutAllFlows positions every flow, not just the active one", () => {
  const a = createNode("http_trigger", { x: 0, y: 0 });
  const b = createNode("publish", { x: 0, y: 0 });
  const c = createNode("http_trigger", { x: 0, y: 0 });
  const d = createNode("publish", { x: 0, y: 0 });
  const doc = layoutAllFlows({
    ...emptyDocument(),
    nodes: [a, b],
    edges: [{ id: "e1", source: a.id, target: b.id }],
    otherFlows: {
      billing: {
        nodes: [c, d],
        edges: [{ id: "e2", source: c.id, target: d.id }],
      },
    },
  });
  expect(nodesStacked(doc.nodes)).toBe(false);
  expect(nodesStacked(doc.otherFlows.billing.nodes)).toBe(false);
});
