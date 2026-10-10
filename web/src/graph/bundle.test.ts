import { beforeEach, expect, test } from "vitest";
import { parse, stringify } from "yaml";
import { strToU8, zipSync, Zip, ZipPassThrough } from "fflate";
import { fromYaml } from "./fromYaml";
import { toConfig } from "./toConfig";
import { emptyDocument } from "./model";
import { useGraphStore } from "./store";
import { saveGraph, loadGraph } from "./persistence";
import {
  checkFiles,
  entryCandidates,
  flowFilenames,
  fromBundle,
  missingFlowFiles,
  needsZip,
  toBundle,
  unzipBundle,
  yamlPath,
  zipBundle,
  MAX_DOCUMENT_BYTES,
} from "./bundle";

const inline = `server: {addr: ':8080'}
logging: {level: info}
flows:
  orders:
    capture_failures: false
    nodes:
      - {id: in, type: http_trigger, path: /orders}
    edges: []
  billing:
    responses: {success: {status: 202}}
    nodes:
      - {id: in, type: http_trigger, path: /billing}
    edges: []
`;

beforeEach(() => useGraphStore.getState().setGraph(emptyDocument()));

test("split/inline conversion and ZIP round trips retain semantic configuration and replay", async () => {
  const doc = fromYaml(inline);
  doc.yamlMode = "split";
  doc.replayServiceConfig = {
    server: { addr: ":8081" },
    database: { url: "${DATABASE}" },
    core: { control_url: "http://core" },
    retention: { ttl: "168h" },
  };
  const bundle = toBundle(doc);
  expect(Object.keys(bundle.files).sort()).toEqual([
    "core.yaml",
    "flows/billing.yaml",
    "flows/orders.yaml",
    "replay.yaml",
  ]);
  expect(parse(bundle.files["core.yaml"]).flows).toEqual({
    orders: { file: "flows/orders.yaml" },
    billing: { file: "flows/billing.yaml" },
  });
  expect(parse(bundle.files["flows/orders.yaml"]).capture_failures).toBe(false);
  const files = unzipBundle(await zipBundle(bundle));
  const loaded = fromBundle({ entry: "core.yaml", files });
  expect(loaded.yamlMode).toBe("split");
  expect(toConfig(loaded).config).toEqual(toConfig(doc).config);
  expect(loaded.replayServiceConfig).toEqual(doc.replayServiceConfig);
  expect(toBundle(loaded)).toEqual(bundle);
  loaded.yamlMode = "single";
  expect(parse(toBundle(loaded).files["core.yaml"])).toEqual(
    toConfig(doc).config,
  );
});

test("ZIP thresholds preserve the chosen YAML layout", () => {
  const one = emptyDocument();
  expect(needsZip(one, toBundle(one))).toBe(false);
  one.replayServiceConfig = {};
  expect(needsZip(one, toBundle(one))).toBe(true);
  one.replayServiceConfig = null;
  one.yamlMode = "split";
  expect(needsZip(one, toBundle(one))).toBe(true);
  const multiple = fromYaml(inline);
  expect(needsZip(multiple, toBundle(multiple))).toBe(true);
  expect(Object.keys(toBundle(multiple).files)).toEqual(["core.yaml"]);
});

test("custom relative entry/file paths survive import", () => {
  const doc = fromYaml(inline);
  doc.yamlMode = "split";
  doc.entryFilename = "deployment/core.yml";
  doc.flowFiles = { orders: "hooks/orders.yml", billing: "hooks/billing.yml" };
  const bundle = toBundle(doc);
  const imported = fromBundle(bundle);
  expect(imported.entryFilename).toBe(doc.entryFilename);
  expect(imported.flowFiles).toEqual(doc.flowFiles);
  expect(missingFlowFiles(bundle.entry, bundle.files)).toEqual([]);
  expect(entryCandidates(bundle.files)).toEqual(["deployment/core.yml"]);
});

test("filename assignment survives rename, mode switches, deletion and persistence", async () => {
  const state = useGraphStore.getState();
  state.setGraph(fromYaml(inline));
  state.setYAMLMode("split");
  state.setFlowFilename("orders", "custom/orders.yaml");
  state.renameFlow("orders_v2");
  expect(useGraphStore.getState().flowFiles?.orders_v2).toBe(
    "custom/orders.yaml",
  );
  state.setYAMLMode("single");
  state.setYAMLMode("split");
  expect(useGraphStore.getState().flowFiles?.orders_v2).toBe(
    "custom/orders.yaml",
  );
  state.removeFlow("billing");
  expect(
    Object.keys(toBundle(useGraphStore.getState().document()).files).sort(),
  ).toEqual(["core.yaml", "custom/orders.yaml"]);
  await saveGraph(useGraphStore.getState().document());
  expect((await loadGraph())?.flowFiles).toEqual({
    orders_v2: "custom/orders.yaml",
  });
  await saveGraph(fromYaml(inline));
  expect((await loadGraph())?.yamlMode).toBe("single");
  expect((await loadGraph())?.entryFilename).toBe("core.yaml");
  const names = flowFilenames({
    ...emptyDocument(),
    activeFlow: "a/b",
    otherFlows: { "a.b": { nodes: [], edges: [] } },
  });
  expect(new Set(Object.values(names)).size).toBe(2);
});

test("invalid bundle structures are rejected before import", () => {
  const make = (entry: string, body = "nodes: []") => ({
    entry: "core.yaml",
    files: { "core.yaml": entry, "a.yaml": body },
  });
  expect(() =>
    fromBundle(make("flows: {a: {file: a.yaml}, b: {nodes: []}}")),
  ).toThrow(/mixed/);
  expect(() =>
    fromBundle(make("flows: {a: {file: a.yaml, nodes: []}}")),
  ).toThrow(/inline fields/);
  expect(() =>
    fromBundle(make("flows: {a: {file: a.yaml}, b: {file: a.yaml}}")),
  ).toThrow(/Duplicate/);
  expect(() => fromBundle(make("flows: {a: {file: missing.yaml}}"))).toThrow(
    /Missing/,
  );
  for (const body of [
    "file: next.yaml",
    "flows: {}",
    "server: {}",
    "nodes: [",
    "nodes: []\n---\nnodes: []",
  ]) {
    expect(() =>
      fromBundle(make("flows: {a: {file: a.yaml}}", body)),
    ).toThrow();
  }
  expect(() => fromYaml("flows: {a: {file: a.yaml}}")).toThrow(
    /references flow files/,
  );
  for (const name of [
    "../a.yaml",
    "/a.yaml",
    "${FILE}.yaml",
    "https://x/a.yaml",
    "a/*.yaml",
    "a.txt",
    "x/../../a.yml",
  ])
    expect(() => yamlPath(name)).toThrow();
  expect(() =>
    checkFiles({ "a.yaml": "nodes: []", "./a.yaml": "nodes: []" }),
  ).toThrow(/Duplicate/);
  expect(() =>
    checkFiles({ "a.yaml": "x".repeat(MAX_DOCUMENT_BYTES + 1) }),
  ).toThrow(/5 MiB/);
  const oversized = zipSync({
    "a.yaml": strToU8("x".repeat(MAX_DOCUMENT_BYTES + 1)),
  });
  expect(() => unzipBundle(oversized)).toThrow(/5 MiB/);
  expect(() =>
    unzipBundle(zipSync({ "../escape.yaml": strToU8("nodes: []") })),
  ).toThrow(/escape/);
});

test("ZIP duplicate entries are rejected without overwriting", () => {
  const chunks: Uint8Array[] = [];
  const archive = new Zip((error, chunk) => {
    if (error) throw error;
    chunks.push(chunk);
  });
  for (let i = 0; i < 2; i++) {
    const file = new ZipPassThrough("core.yaml");
    archive.add(file);
    file.push(strToU8("flows: {}"), true);
  }
  archive.end();
  const data = new Uint8Array(
    chunks.reduce((sum, chunk) => sum + chunk.length, 0),
  );
  let offset = 0;
  for (const chunk of chunks) {
    data.set(chunk, offset);
    offset += chunk.length;
  }
  expect(() => unzipBundle(data)).toThrow(/Duplicate ZIP path/);
});

test("unsafe schema numbers in a flow file keep the existing import precision guard", () => {
  expect(() =>
    fromBundle({
      entry: "core.yaml",
      files: {
        "core.yaml": "flows: {a: {file: a.yaml}}",
        "a.yaml": stringify({
          nodes: [
            {
              id: "check",
              type: "json_validate",
              schema: { minimum: 9007199254740992 },
            },
          ],
          edges: [],
        }).replace("9007199254740992", "9007199254740993"),
      },
    }),
  ).toThrow(/precision/);
});

test("clearing core preserves the replay document's assigned filename", () => {
  const store = useGraphStore.getState();
  store.setReplayFilename("services/replay.yml");
  store.setReplayServiceConfig({ server: { addr: ":8081" } });
  store.clearCore();
  expect(useGraphStore.getState().document().replayFilename).toBe(
    "services/replay.yml",
  );
});

test("manual directory ZIPs ignore non-YAML files while rejecting unsafe paths", () => {
  const archive = zipSync({
    "README.md": strToU8("Instructions"),
    "core.yaml": strToU8("flows: {}"),
  });
  expect(Object.keys(unzipBundle(archive))).toEqual(["core.yaml"]);
  expect(() =>
    unzipBundle(
      zipSync({
        "../README.md": strToU8("Instructions"),
        "core.yaml": strToU8("flows: {}"),
      }),
    ),
  ).toThrow(/escape/);
});
