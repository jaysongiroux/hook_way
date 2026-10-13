import { beforeEach, expect, test } from "vitest";
import { layout, layoutAllFlows, maybeLayout, nodesStacked } from "./layout";
import { createNode, emptyDocument, resetIdCounters } from "./model";
import { NODE_SIZE } from "../tokens";

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

test("layout leaves a visible gap between connected cards", () => {
  const chain = ["http_trigger", "read_body", "auth", "envelope", "publish"].map((t) =>
    createNode(t, { x: 0, y: 0 }),
  );
  const edges = chain.slice(0, -1).map((n, i) => ({
    id: `e${i}`,
    source: n.id,
    target: chain[i + 1].id,
    sourceHandle: "out",
    targetHandle: "in",
  }));
  const laid = layout(chain, edges);
  const byId = new Map(laid.map((n) => [n.id, n]));
  for (const edge of edges) {
    const source = byId.get(edge.source)!;
    const target = byId.get(edge.target)!;
    const gap = target.position.x - (source.position.x + NODE_SIZE.width);
    // Cards must not touch, and the gap has to be wide enough to draw an
    // arrowhead in. Anything under ~100px reads as bunched on the canvas.
    expect(gap, `${edge.source} -> ${edge.target}`).toBeGreaterThanOrEqual(100);
  }
});

test("layout prefers a node's measured size over the default estimate", () => {
  const a = { ...createNode("http_trigger", { x: 0, y: 0 }), measured: { width: 400, height: 96 } };
  const b = createNode("publish", { x: 0, y: 0 });
  const laid = layout([a, b], [{ id: "e1", source: a.id, target: b.id, sourceHandle: "out", targetHandle: "in" }]);
  const gap = laid[1].position.x - (laid[0].position.x + 400);
  expect(gap).toBeGreaterThanOrEqual(100);
});
