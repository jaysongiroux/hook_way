import { afterEach, expect, test, vi } from "vitest";
import { loadReplaySchema, validateReplayYaml, validateYaml } from "./validate";

afterEach(() => {
  delete window.hookwayValidate;
  delete window.hookwayReplayValidate;
  delete window.hookwayReplaySchema;
  delete window.Go;
  vi.unstubAllGlobals();
});

test("replay schema and validation use the editor API when the WASM bridge fails", async () => {
  window.hookwayValidate = () => JSON.stringify({ issues: [] });
  window.hookwayReplaySchema = () => "invalid JSON";
  window.hookwayReplayValidate = () => "invalid JSON";
  const fetch = vi.fn(async (url: string) => ({
    ok: true,
    json: async () => ({ type: "object", properties: { server: { type: "object" } } }),
    text: async () => JSON.stringify({ issues: [{ field: "keys", message: "required", severity: "error" }] }),
    url,
  }));
  vi.stubGlobal("fetch", fetch);

  expect(await loadReplaySchema()).toMatchObject({ type: "object" });
  expect(await validateReplayYaml("keys: []")).toEqual([{ field: "keys", message: "required", severity: "error" }]);
  expect(fetch.mock.calls.map(([url]) => url)).toEqual(["/replay-schema", "/validate-replay"]);
});

test("core validation uses the editor API when the WASM bridge throws", async () => {
  window.hookwayValidate = () => { throw new Error("bridge failed"); };
  const fetch = vi.fn(async () => ({
    ok: true,
    text: async () => JSON.stringify({ issues: [{ field: "flows", message: "invalid", severity: "error" }] }),
  }));
  vi.stubGlobal("fetch", fetch);

  expect(await validateYaml("flows: {}"))
    .toEqual([{ field: "flows", message: "invalid", severity: "error" }]);
  expect(fetch).toHaveBeenCalledWith("/validate", expect.objectContaining({ method: "POST" }));
});

test("a failed WASM load can be retried", async () => {
  window.Go = class {
    importObject = {};
    async run() {}
  };
  const fetch = vi.fn()
    .mockRejectedValueOnce(new Error("temporary network failure"))
    .mockResolvedValueOnce({ ok: true });
  vi.stubGlobal("fetch", fetch);
  vi.stubGlobal("WebAssembly", { instantiateStreaming: vi.fn(async () => ({ instance: {} })) });
  vi.resetModules();
  const { loadValidator } = await import("./validate");

  await loadValidator();
  await loadValidator();
  expect(fetch).toHaveBeenCalledTimes(2);
});
