import { expect, test } from "vitest";
import type { Edge } from "@xyflow/react";
import { renameSubgraph, offsetSubgraph } from "./subgraph";
import type { TemplateSubgraph } from "./templates";
import type { HookwayNode } from "./model";

function node(id: string, x = 0, y = 0): HookwayNode {
  return {
    id,
    type: "flow",
    position: { x, y },
    data: { nodeType: "code", yamlId: id, options: { script: "return {};" } },
  };
}

function sub(): TemplateSubgraph {
  const nodes = [node("fmt", 100, 40), node("post", 300, 40)];
  const edges: Edge[] = [{ id: "fmt>post", source: "fmt", target: "post", sourceHandle: "out", targetHandle: "in" }];
  return { nodes, edges };
}

test("an insert with no collisions keeps the template ids", () => {
  const out = renameSubgraph(sub(), new Set<string>());
  expect(out.nodes.map((n) => n.id)).toEqual(["fmt", "post"]);
  expect(out.nodes.map((n) => n.data.yamlId)).toEqual(["fmt", "post"]);
  expect(out.edges[0].source).toBe("fmt");
  expect(out.edges[0].target).toBe("post");
});

test("colliding ids are suffixed and edges are rewritten to match", () => {
  const out = renameSubgraph(sub(), new Set(["fmt", "post"]));
  expect(out.nodes.map((n) => n.id)).toEqual(["fmt_2", "post_2"]);
  expect(out.nodes.map((n) => n.data.yamlId)).toEqual(["fmt_2", "post_2"]);
  expect(out.edges[0].source).toBe("fmt_2");
  expect(out.edges[0].target).toBe("post_2");
});

test("renaming skips suffixes that are themselves taken", () => {
  const out = renameSubgraph(sub(), new Set(["fmt", "fmt_2", "fmt_3"]));
  expect(out.nodes[0].id).toBe("fmt_4");
  expect(out.nodes[1].id).toBe("post");
});

test("renaming a second copy does not collide with the first", () => {
  const taken = new Set<string>();
  const first = renameSubgraph(sub(), taken);
  for (const n of first.nodes) taken.add(n.id);
  const second = renameSubgraph(sub(), taken);
  expect(second.nodes.map((n) => n.id)).toEqual(["fmt_2", "post_2"]);
  const all = [...first.nodes, ...second.nodes].map((n) => n.id);
  expect(new Set(all).size).toBe(all.length);
});

test("edge ids are unique per insert so React Flow does not drop one", () => {
  const taken = new Set<string>();
  const first = renameSubgraph(sub(), taken);
  for (const n of first.nodes) taken.add(n.id);
  const second = renameSubgraph(sub(), taken);
  expect(first.edges[0].id).not.toBe(second.edges[0].id);
});

test("offsetting moves the top-left node to the drop point and keeps relative layout", () => {
  const out = offsetSubgraph(sub(), { x: 500, y: 200 });
  expect(out.nodes[0].position).toEqual({ x: 500, y: 200 });
  expect(out.nodes[1].position).toEqual({ x: 700, y: 200 });
});

test("the helpers do not mutate their input", () => {
  const original = sub();
  renameSubgraph(original, new Set(["fmt", "post"]));
  offsetSubgraph(original, { x: 9, y: 9 });
  expect(original.nodes[0].id).toBe("fmt");
  expect(original.nodes[0].position).toEqual({ x: 100, y: 40 });
  expect(original.edges[0].source).toBe("fmt");
});
