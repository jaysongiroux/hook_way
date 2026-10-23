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
  const doc = fromYaml(
    yaml.replace(
      "description: Get a customer.",
      'description: "Literal \\u0024{ORIGINAL}"',
    ),
  );
  const trigger = doc.nodes.find((n) => n.data.nodeType === "mcp_trigger")!;
  expect(trigger.data.literalDollarPaths).toBeDefined();
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
      .literalDollarPaths,
  ).toBeDefined();
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
        .literalDollarPaths,
    ).toBeDefined();
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
      .literalDollarPaths,
  ).toBeDefined();
  expect(Object.values(toBundle(imported).files).join("\n")).toContain(
    "\\u0024ref",
  );
});

test("new authored references stay expandable alongside imported literal fields", () => {
  const doc = fromYaml(yaml);
  useGraphStore.getState().setGraph(doc);
  const request = doc.nodes.find(
    (node) => node.data.nodeType === "http_request",
  )!;
  useGraphStore.getState().updateNodeData(request.id, {
    options: {
      ...request.data.options,
      url: "https://${UPSTREAM_HOST}/customers/{path_1}",
    },
  });
  useGraphStore.getState().setServerConfig({ addr: "${LISTEN_ADDR}" });
  const text = toBundle(useGraphStore.getState().document()).files["core.yaml"];
  expect(text).toContain("${UPSTREAM_HOST}");
  expect(text).toContain("${LISTEN_ADDR}");
  expect(text).toContain("\\u0024ref");
});

test("literal schema and description provenance follows tool rename and removal", () => {
  const doc = fromYaml(
    yaml.replace(
      "description: Get a customer.",
      'description: "Literal \\u0024{ORIGINAL}"',
    ),
  );
  useGraphStore.getState().setGraph(doc);
  const trigger = useGraphStore
    .getState()
    .nodes.find((n) => n.data.nodeType === "mcp_trigger")!;
  const tools = trigger.data.options.tools as Record<string, unknown>[];
  const renamed = tools.map((tool, i) =>
    i === 0
      ? { ...tool, name: "renamed", description: "Literal ${EDITED}" }
      : tool,
  );
  useGraphStore.getState().updateNodeData(trigger.id, {
    options: { ...trigger.data.options, tools: renamed },
  });
  let text = toBundle(useGraphStore.getState().document()).files["core.yaml"];
  expect(text).toContain("\\u0024{EDITED}");
  const schema = {
    type: "object",
    properties: { text: { type: "string", enum: ["${SCHEMA_LITERAL}"] } },
  };
  useGraphStore.getState().updateNodeData(trigger.id, {
    options: {
      ...trigger.data.options,
      tools: [{ ...renamed[1], input_schema: schema }],
    },
  });
  text = toBundle(useGraphStore.getState().document()).files["core.yaml"];
  expect(text).toContain("\\u0024{SCHEMA_LITERAL}");
  expect(text).not.toContain("EDITED");
});

test("credential rename preserves only imported literal references", () => {
  const doc = fromYaml(
    'credentials: {"\\u0024original": {kind: static, allowed_origins: [https://api.example.com], headers: {Authorization: {secret_ref: {provider: file, name: "/literal/\\u0024TOKEN"}}}}}\nflows: {main: {nodes: [], edges: []}}',
  );
  useGraphStore.getState().setGraph(doc);
  const profile = (
    doc.configExtra.credentials as Record<string, Record<string, unknown>>
  )["$original"];
  useGraphStore
    .getState()
    .setConfigExtra({ credentials: { renamed: profile } });
  const header = (profile.headers as Record<string, Record<string, unknown>>)
    .Authorization;
  useGraphStore.getState().setConfigExtra({
    credentials: {
      renamed: {
        ...profile,
        headers: {
          Authorization: { ...header, prefix: "Bearer ${AUTH_PREFIX}" },
        },
      },
    },
  });
  const text = toBundle(useGraphStore.getState().document()).files["core.yaml"];
  expect(text).toContain("/literal/\\u0024TOKEN");
  expect(text).toContain("${AUTH_PREFIX}");
  expect(text).not.toContain("original:");
});
