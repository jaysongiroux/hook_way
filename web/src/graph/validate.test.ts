import { afterEach, expect, test, vi } from "vitest";
import { loadReplaySchema, validateReplayYaml } from "./validate";

afterEach(() => {
  delete window.hookwayValidate;
  delete window.hookwayReplayValidate;
  delete window.hookwayReplaySchema;
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
