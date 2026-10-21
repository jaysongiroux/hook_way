import { expect, test } from "vitest";
import { readFileSync } from "node:fs";
import { parse } from "yaml";
import { fromYaml } from "./fromYaml";
import { toBundle, fromBundle } from "./bundle";
import { useGraphStore } from "./store";
import { saveGraph, loadGraph } from "./persistence";
import { createNode } from "./model";
const yaml = readFileSync("../examples/openapi/core.yaml", "utf8");
test("imported dollars survive edits, node/flow rename, persistence and split exports", async () => {
  const doc = fromYaml(yaml);
  const trigger = doc.nodes.find((n) => n.data.nodeType === "mcp_trigger")!;
  expect(trigger.data.literalDollars).toBe(true);
  const tools = trigger.data.options.tools as Record<string, unknown>[];
  tools[0].description = "Literal ${HOST_SECRET} and $5";
  tools[0].input_schema = {
    type: "object",
    properties: { body: { type: "string", enum: ["${HOST_SECRET}", "$5"] } },
  };
  trigger.data.yamlId = "renamed_mcp";
  const ordinary = createNode("mcp_result", { x: 0, y: 0 });
  ordinary.data.options = { source: "constant", value: "${INTENTIONAL_ENV}" };
  doc.nodes.push(ordinary);
  useGraphStore.getState().setGraph(doc);
  useGraphStore.getState().renameFlow("renamed_flow");
  await saveGraph(useGraphStore.getState().document());
  const restored = (await loadGraph())!;
  expect(
    restored.nodes.find((n) => n.data.nodeType === "mcp_trigger")?.data
      .literalDollars,
  ).toBe(true);
  for (const yamlMode of ["single", "split"] as const) {
    restored.yamlMode = yamlMode;
    const bundle = toBundle(restored);
    const text = Object.values(bundle.files).join("\n");
    expect(text).toContain("\\u0024{HOST_SECRET}");
    expect(text).not.toContain("${HOST_SECRET}");
    expect(text).toContain("${INTENTIONAL_ENV}");
    const reopened = fromBundle(bundle);
    expect(
      reopened.nodes.find((n) => n.data.nodeType === "mcp_trigger")?.data
        .literalDollars,
    ).toBe(true);
    expect(toBundle(reopened)).toEqual(bundle);
  }
  expect(
    parse(toBundle({ ...restored, yamlMode: "single" }).files["core.yaml"])
      .flows.renamed_flow.nodes[0].tools[0].description,
  ).toBe("Literal ${HOST_SECRET} and $5");
});
test("ordinary authored environment references retain their export behavior", () => {
  const doc = fromYaml(
    'server: {addr: "${LISTEN_ADDR}"}\nflows: {main: {nodes: [], edges: []}}\n',
  );
  expect(toBundle(doc).files["core.yaml"]).toContain("${LISTEN_ADDR}");
  expect(toBundle(doc).files["core.yaml"]).not.toContain("\\u0024");
});

test("MCP schema numbers that lose precision cannot replace a builder workspace", () => {
  const text =
    "flows: {main: {nodes: [{id: mcp, type: mcp_trigger, tools: [{input_schema: {type: integer, maximum: 9007199254740993}}]}], edges: []}}";
  expect(() => fromYaml(text)).toThrow(/lose precision/);
});

test("split files retain escaped literals with YAML document markers", () => {
  const doc = fromYaml(yaml);
  doc.yamlMode = "split";
  const bundle = toBundle(doc);
  for (const filename of Object.keys(bundle.files))
    bundle.files[filename] = "---\n" + bundle.files[filename];
  const imported = fromBundle(bundle);
  expect(
    imported.nodes.find((n) => n.data.nodeType === "mcp_trigger")?.data
      .literalDollars,
  ).toBe(true);
  expect(Object.values(toBundle(imported).files).join("\n")).toContain(
    "\\u0024ref",
  );
});
