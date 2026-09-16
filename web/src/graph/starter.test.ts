import { expect, test } from "vitest";
import { isValidConnection } from "./connection";
import { nodesStacked } from "./layout";
import { NODE_TYPES } from "./nodeSchema";
import { starterDocument, PRODUCTION_YAML } from "./starter";
import type { Edge } from "@xyflow/react";
import type { FlowSnapshot, HookwayNode } from "./model";

function snapshots(doc: ReturnType<typeof starterDocument>): FlowSnapshot[] {
  return [
    { nodes: doc.nodes, edges: doc.edges, deliveryFailurePolicy: doc.deliveryFailurePolicy },
    ...Object.values(doc.otherFlows),
  ];
}

function allNodes(doc: ReturnType<typeof starterDocument>): HookwayNode[] {
  return snapshots(doc).flatMap((s) => s.nodes);
}

test("the production YAML is the builder starter", () => {
  expect(PRODUCTION_YAML).not.toContain("schema_version");
  expect(PRODUCTION_YAML).toContain("delivery_failure_policy: skip_failed");
  expect(PRODUCTION_YAML).toContain("ack:on_error>dlq");
});

test("every catalog node type except lookup appears in the starter", () => {
  const types = new Set(allNodes(starterDocument()).map((n) => n.data.nodeType));
  for (const type of NODE_TYPES.filter((t) => t !== "lookup")) {
    expect(types.has(type), type).toBe(true);
  }
  expect(types.has("lookup")).toBe(false);
});

test("the github flow fans out, switches, and has an on_error path", () => {
  const doc = starterDocument();
  expect(doc.activeFlow).toBe("github");
  expect(doc.deliveryFailurePolicy).toBe("skip_failed");
  expect(doc.edges.some((e) => e.sourceHandle === "on_error")).toBe(true);
  expect(doc.edges.some((e) => e.sourceHandle === "main")).toBe(true);
  expect(doc.edges.some((e) => e.sourceHandle === "other")).toBe(true);
  expect(Object.keys(doc.otherFlows).sort()).toEqual(["proxy", "trusted"]);
});

test("every starter flow is laid out instead of stacked at the origin", () => {
  for (const snap of snapshots(starterDocument())) {
    expect(nodesStacked(snap.nodes), "flow nodes should have distinct positions").toBe(false);
  }
});

test("every starter edge is a legal connection in its flow", () => {
  for (const snap of snapshots(starterDocument())) {
    snap.edges.forEach((edge, i) => {
      const prior: Edge[] = snap.edges.slice(0, i);
      const ok = isValidConnection(
        {
          source: edge.source,
          target: edge.target,
          sourceHandle: edge.sourceHandle ?? "out",
          targetHandle: edge.targetHandle ?? "in",
        },
        snap.nodes,
        prior,
      );
      expect(ok, `${edge.source}:${edge.sourceHandle ?? "out"}>${edge.target}`).toBe(true);
    });
  }
});
