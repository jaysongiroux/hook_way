import { afterEach, beforeEach, expect, test, vi } from "vitest";

beforeEach(() => { vi.resetModules(); });

afterEach(() => {
  delete window.hookwayContractInfo;
  delete window.hookwayValidate;
  delete window.hookwayReplayValidate;
  delete window.hookwayReplaySchema;
  delete window.Go;
  vi.unstubAllGlobals();
});

function mockContract(capabilities: string[]) {
  window.hookwayContractInfo = () => JSON.stringify({ contract_version: 1, capabilities });
}

test("replay schema and validation use the editor API when the WASM bridge fails", async () => {
  mockContract(["replay_schema", "replay_yaml_validate"]);
  window.hookwayReplaySchema = () => "invalid JSON";
  window.hookwayReplayValidate = () => "invalid JSON";
  const fetch = vi.fn(async (request: Request) => new Response(JSON.stringify(
    request.url.endsWith("/replay-schema")
      ? { type: "object", properties: { server: { type: "object" } } }
      : { issues: [{ field: "keys", message: "required", severity: "error" }] },
  ), { headers: { "Content-Type": "application/json" } }));
  vi.stubGlobal("fetch", fetch);
  const { loadReplaySchema, validateReplayYaml } = await import("./validate");

  expect(await loadReplaySchema()).toMatchObject({ type: "object" });
  expect(await validateReplayYaml("keys: []")).toEqual([{ field: "keys", message: "required", severity: "error" }]);
  expect(fetch.mock.calls.map(([request]) => new URL(request.url).pathname)).toEqual(["/replay-schema", "/validate-replay"]);
});

test("core validation uses the editor API when the WASM bridge throws", async () => {
  mockContract(["core_yaml_validate"]);
  window.hookwayValidate = () => { throw new Error("bridge failed"); };
  const fetch = vi.fn(async (_request: Request) => new Response(JSON.stringify({ issues: [{ field: "flows", message: "invalid", severity: "error" }] }), {
    headers: { "Content-Type": "application/json" },
  }));
  vi.stubGlobal("fetch", fetch);
  const { validateYaml } = await import("./validate");

  expect(await validateYaml("flows: {}"))
    .toEqual([{ field: "flows", message: "invalid", severity: "error" }]);
  expect(fetch.mock.calls[0][0]).toMatchObject({ method: "POST" });
  expect(new URL(fetch.mock.calls[0][0].url).pathname).toBe("/validate");
});

test("legacy globals without contract metadata use the editor API", async () => {
  window.hookwayValidate = () => JSON.stringify({ issues: [] });
  const fetch = vi.fn(async () => new Response(JSON.stringify({ issues: [{ field: "flows", message: "API", severity: "error" }] }), {
    headers: { "Content-Type": "application/json" },
  }));
  vi.stubGlobal("fetch", fetch);
  const { validateYaml } = await import("./validate");
  setTimeout(() => document.querySelector('script[src$="wasm_exec.js"]')?.dispatchEvent(new Event("error")), 0);
  // WASM startup cannot complete in this test host, so the editor API wins.
  expect(await validateYaml("flows: {}"))
    .toEqual([{ field: "flows", message: "API", severity: "error" }]);
});


test("deployed product never sends builder validation to an editor API", async () => {
  mockContract(["core_yaml_validate", "replay_yaml_validate", "replay_schema"]);
  window.hookwayValidate = () => { throw new Error("bridge failed"); };
  window.hookwayReplayValidate = () => { throw new Error("bridge failed"); };
  window.hookwayReplaySchema = () => "invalid JSON";
  const fetch = vi.fn();
  vi.stubGlobal("fetch", fetch);
  const { configureEditorValidation } = await import("../shared/wasm/validationMode");
  configureEditorValidation("local");
  const { validateYaml, validateReplayYaml, loadReplaySchema } = await import("./validate");
  expect(await validateYaml("flows: {}")).toBeNull();
  expect(await validateReplayYaml("keys: []")).toBeNull();
  expect(await loadReplaySchema()).toBeNull();
  expect(fetch).not.toHaveBeenCalled();
});
