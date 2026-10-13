import { expect, test } from "vitest";
import { FLOW_EDGES, FLOW_NODES, LAYOUT, edgePath, nodeBox, nodeChrome } from "./flowGraph";

const byId = (id: string) => FLOW_NODES.find((n) => n.id === id)!;

test("the diagram shows one flow fanning out to three destinations", () => {
  expect(FLOW_NODES.filter((n) => n.kind === "endpoint")).toHaveLength(1);
  const dests = FLOW_NODES.filter((n) => n.kind === "destination");
  expect(dests).toHaveLength(3);
  expect(dests.every((d) => d.nodeType === "publish")).toBe(true);
  expect(new Set(dests.map((d) => d.summary)).size).toBe(3);
});

test("two branches are gated by a filter and one is not", () => {
  expect(FLOW_NODES.filter((n) => n.kind === "filter")).toHaveLength(2);
  const endpoint = FLOW_NODES.find((n) => n.kind === "endpoint")!;
  const direct = FLOW_EDGES.filter((e) => e.from === endpoint.id && byId(e.to).kind === "destination");
  // A route whose filter chain fails is dropped while the others still
  // deliver, so an unfiltered branch alongside filtered ones is the honest
  // picture of route-level filtering.
  expect(direct).toHaveLength(1);
  expect(byId(direct[0].to).yamlId).toBe("stream");
});

test("node chrome matches the builder catalog", () => {
  expect(nodeChrome(byId("ep"))).toMatchObject({ showIn: false, showOut: true, showError: false });
  expect(nodeChrome(byId("f1"))).toMatchObject({ showIn: true, showOut: true, showError: true });
  // publish declares Envelope so a success continuation can attach an out handle.
  expect(nodeChrome(byId("sqs"))).toMatchObject({ showIn: true, showOut: true, showError: true });
});

test("every edge connects nodes that exist and moves left to right", () => {
  for (const e of FLOW_EDGES) {
    const from = byId(e.from);
    const to = byId(e.to);
    expect(from, `edge source ${e.from} missing`).toBeDefined();
    expect(to, `edge target ${e.to} missing`).toBeDefined();
    expect(to.column).toBeGreaterThan(from.column);
  }
});

test("every node is reachable from the endpoint", () => {
  const endpoint = FLOW_NODES.find((n) => n.kind === "endpoint")!;
  const seen = new Set([endpoint.id]);
  // Columns run left to right, so one pass per column settles reachability.
  for (let i = 0; i < LAYOUT.colX.length; i++) {
    for (const e of FLOW_EDGES) if (seen.has(e.from)) seen.add(e.to);
  }
  expect(seen.size).toBe(FLOW_NODES.length);
});

test("node boxes stay inside the viewBox and never overlap", () => {
  const boxes = FLOW_NODES.map(nodeBox);
  for (const b of boxes) {
    expect(b.x).toBeGreaterThanOrEqual(0);
    expect(b.y).toBeGreaterThanOrEqual(0);
    expect(b.x + b.w).toBeLessThanOrEqual(LAYOUT.width);
    expect(b.y + b.h).toBeLessThanOrEqual(LAYOUT.height);
  }
  for (let i = 0; i < boxes.length; i++) {
    for (let j = i + 1; j < boxes.length; j++) {
      const a = boxes[i];
      const b = boxes[j];
      const overlaps = a.x < b.x + b.w && b.x < a.x + a.w && a.y < b.y + b.h && b.y < a.y + a.h;
      expect(overlaps, `${FLOW_NODES[i].id} overlaps ${FLOW_NODES[j].id}`).toBe(false);
    }
  }
});

test("an edge path spans the gap between the two nodes it joins", () => {
  const from = nodeBox(byId("ep"));
  const to = nodeBox(byId("rabbit"));
  const d = edgePath(byId("ep"), byId("rabbit"));
  // Starts at the source's right edge, ends at the target's left edge.
  expect(d.startsWith(`M ${from.x + from.w} ${from.y + from.h / 2}`)).toBe(true);
  expect(d.trimEnd().endsWith(`${to.x} ${to.y + to.h / 2}`)).toBe(true);
});
