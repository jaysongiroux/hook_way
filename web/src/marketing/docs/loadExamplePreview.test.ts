import { afterEach, expect, test, vi } from "vitest";
import { loadExamplePreview, type PublishedYAML } from "./loadExamplePreview";
import { useGraphStore } from "../../graph/store";

afterEach(() => vi.unstubAllGlobals());

function fixture(files: Record<string, string>) {
  const pages = Object.keys(files).map((source) => ({ source, rawUrl: "/docs/raw/" + source }));
  const fetcher = vi.fn(async (url: string, options?: RequestInit) => {
    if (options?.signal?.aborted) throw new DOMException("Aborted", "AbortError");
    const text = files[url.replace("/docs/raw/", "")];
    return new Response(text ?? "Missing", { status: text === undefined ? 404 : 200 });
  });
  vi.stubGlobal("fetch", fetcher);
  const load = (page: PublishedYAML = pages[0], published = pages) =>
    loadExamplePreview(page, published, new AbortController().signal);
  return { pages, fetcher, load };
}

test("loads all inline flows and destinations without touching the builder", async () => {
  const before = useGraphStore.getState();
  const { load } = fixture({ "examples/core.yaml": `
destinations:
  queue: {type: sqs, sqs: {region: us-east-1}}
flows:
  first:
    nodes:
      - {id: in, type: http_trigger, path: /one}
      - {id: out, type: publish, destination: queue}
    edges: [in>out]
  second:
    nodes: [{id: in, type: http_trigger, path: /two}]
` });
  const document = await load();
  expect(document?.activeFlow).toBe("first");
  expect(document?.nodes).toHaveLength(2);
  expect(document?.edges).toHaveLength(1);
  expect(document?.nodes[1].position.x).toBeGreaterThan(document!.nodes[0].position.x);
  expect(document?.otherFlows.second.nodes[0].data.options.path).toBe("/two");
  expect(document?.destinations.queue).toMatchObject({ type: "sqs" });
  expect(useGraphStore.getState()).toBe(before);
});

test("resolves split flow files through the published inventory", async () => {
  const { load, fetcher } = fixture({
    "examples/multi/core.yaml": "flows:\n  orders: {file: flows/orders.yaml}\n  billing: {file: flows/billing.yaml}\n",
    "examples/multi/flows/orders.yaml": "nodes: [{id: in, type: http_trigger, path: /orders}]",
    "examples/multi/flows/billing.yaml": "nodes: [{id: in, type: http_trigger, path: /billing}]",
  });
  const document = await load();
  expect(document?.nodes[0].data.options.path).toBe("/orders");
  expect(document?.otherFlows.billing.nodes[0].data.options.path).toBe("/billing");
  expect(fetcher).toHaveBeenCalledTimes(3);
});

test("standalone flow bodies retain descriptions and nested options", async () => {
  const { load } = fixture({ "examples/flows/process.yaml": `
nodes:
  - id: request
    type: http_request
    description: Preserve the full note.
    url: \${ENDPOINT}
    retry: {max_attempts: 3}
    headers: {X-Source: example}
edges: []
` });
  const document = await load();
  expect(document?.activeFlow).toBe("process");
  expect(document?.nodes[0].data).toMatchObject({ description: "Preserve the full note.", options: { url: "${ENDPOINT}", retry: { max_attempts: 3 }, headers: { "X-Source": "example" } } });
});

test.each([
  "services: {web: {image: nginx}}",
  "database: {url: postgres://example}\ncore: {url: http://core}",
  "connections: {queue: {type: sqs}}",
  "flows: {empty: {nodes: [], edges: []}}",
])("omits a graph for non-flow or empty YAML: %s", async (yaml) => {
  const { load } = fixture({ "examples/config.yaml": yaml });
  expect(await load()).toBeNull();
});

test.each(["../outside.yaml", "https://example.test/flow.yaml", "/absolute.yaml", "unpublished.yaml"])("refuses unpublished or unsafe references: %s", async (reference) => {
  const { load, fetcher } = fixture({ "examples/core.yaml": "flows: {main: {file: '" + reference + "'}}" });
  await expect(load()).rejects.toThrow();
  expect(fetcher).toHaveBeenCalledTimes(1);
});

test("reports invalid YAML and failed downloads", async () => {
  const { load, pages } = fixture({ "examples/invalid.yaml": "flows: [" });
  await expect(load()).rejects.toThrow();
  await expect(load({ source: "examples/missing.yaml", rawUrl: "/docs/raw/examples/missing.yaml" }, pages)).rejects.toThrow("unavailable");
});

test("propagates cancellation to the fetch", async () => {
  const { pages, fetcher } = fixture({ "examples/core.yaml": "flows: {}" });
  const controller = new AbortController();
  controller.abort();
  await expect(loadExamplePreview(pages[0], pages, controller.signal)).rejects.toMatchObject({ name: "AbortError" });
  expect(fetcher).toHaveBeenCalledWith(pages[0].rawUrl, { signal: controller.signal });
});
