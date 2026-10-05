import { afterEach, beforeEach, expect, test, vi } from "vitest";

beforeEach(() => { vi.resetModules(); });
afterEach(() => {
  delete window.Go;
  delete window.hookwayWasmReady;
  delete window.hookwayContractInfo;
  delete window.hookwayValidate;
  delete window.hookwaySchema;
  delete window.hookwayNodeSchemas;
  delete window.hookwayReplayValidate;
  delete window.hookwayReplaySchema;
  delete window.hookwayReplayActionValidate;
  delete window.hookwayJSONSampleValidate;
  vi.unstubAllGlobals();
});

function installGo(version = 1) {
  let starts = 0;
  window.Go = class {
    importObject = {};
    run() {
      starts++;
      queueMicrotask(() => {
        window.hookwayValidate = () => JSON.stringify({ issues: [] });
        window.hookwayContractInfo = () => JSON.stringify({ contract_version: version, capabilities: ["core_yaml_validate"] });
        window.hookwayWasmReady?.();
      });
      return new Promise<void>(() => {});
    }
  };
  return () => starts;
}

function installInstantiate() {
  vi.stubGlobal("WebAssembly", {
    instantiateStreaming: vi.fn(async () => ({ instance: {} })),
    instantiate: vi.fn(async () => ({ instance: {} })),
  });
}

test("loads a nested asset base once and waits for the Go readiness callback", async () => {
  const starts = installGo();
  installInstantiate();
  const fetch = vi.fn(async () => ({ ok: true, status: 200, clone: () => ({ arrayBuffer: async () => new ArrayBuffer(0) }) }));
  vi.stubGlobal("fetch", fetch);
  const { loadWasm, getWasmState, invokeWasm } = await import("./loader");
  const [first, second] = await Promise.all([loadWasm({ assetBase: "/console/" }), loadWasm({ assetBase: "/console/" })]);
  expect(first).toBe(second);
  expect(starts()).toBe(1);
  expect(fetch).toHaveBeenCalledWith(new URL("/console/hookway.wasm", window.location.href).toString());
  expect(getWasmState().status).toBe("ready");
  expect(invokeWasm("core_yaml_validate", "flows: {}")).toBe('{"issues":[]}');
});

test("a contract mismatch is explicit and a later retry can recover", async () => {
  let version = 2;
  window.Go = class {
    importObject = {};
    run() {
      queueMicrotask(() => {
        window.hookwayValidate = () => "{}";
        window.hookwayContractInfo = () => JSON.stringify({ contract_version: version, capabilities: ["core_yaml_validate"] });
        window.hookwayWasmReady?.();
      });
      return new Promise<void>(() => {});
    }
  };
  installInstantiate();
  vi.stubGlobal("fetch", vi.fn(async () => ({ ok: true, status: 200, clone: () => ({ arrayBuffer: async () => new ArrayBuffer(0) }) })));
  const { loadWasm, retryWasm, getWasmState } = await import("./loader");
  await expect(loadWasm()).rejects.toThrow(/contract mismatch/);
  expect(getWasmState().status).toBe("mismatch");
  version = 1;
  await expect(retryWasm()).resolves.toMatchObject({ info: { contract_version: 1 } });
  expect(getWasmState().status).toBe("ready");
});

test("legacy globals without metadata never establish readiness", async () => {
  window.hookwayValidate = () => "{}";
  window.hookwayContractInfo = () => "{}";
  const { loadWasm, getWasmState } = await import("./loader");
  await expect(loadWasm()).rejects.toThrow(/contract mismatch/);
  expect(getWasmState().status).toBe("mismatch");
});

test("JSON sample capability preserves the bridge's exact JSON text", async () => {
  const result = JSON.stringify({ valid: true, issues: [], output: "{\"id\":9007199254740993}" });
  const validate = vi.fn(() => result);
  window.hookwayJSONSampleValidate = validate;
  window.hookwayContractInfo = () => JSON.stringify({ contract_version: 1, capabilities: ["json_sample_validate"] });
  const { loadWasm, hasWasmCapability } = await import("./loader");
  const bridge = await loadWasm();
  expect(hasWasmCapability("json_sample_validate")).toBe(true);
  expect(bridge.jsonSampleValidate("raw request JSON")).toBe(result);
  expect(validate).toHaveBeenCalledWith("raw request JSON");
});

test("an advertised JSON sample capability without implementation is a mismatch", async () => {
  window.hookwayContractInfo = () => JSON.stringify({ contract_version: 1, capabilities: ["json_sample_validate"] });
  const { loadWasm, getWasmState } = await import("./loader");
  await expect(loadWasm()).rejects.toThrow(/json_sample_validate has no implementation/);
  expect(getWasmState().status).toBe("mismatch");
});

test("an older version 1 bridge never reports a missing JSON sample capability as success", async () => {
  window.hookwayValidate = () => "{}";
  window.hookwayContractInfo = () => JSON.stringify({ contract_version: 1, capabilities: ["core_yaml_validate"] });
  const { loadWasm, hasWasmCapability } = await import("./loader");
  const bridge = await loadWasm();
  expect(hasWasmCapability("json_sample_validate")).toBe(false);
  expect(() => bridge.jsonSampleValidate("{}")).toThrow(/capability unavailable/);
});

test("a Go runtime exit after readiness marks the bridge unavailable", async () => {
  let finish!: () => void;
  window.Go = class {
    importObject = {};
    run() {
      queueMicrotask(() => {
        window.hookwayValidate = () => "{}";
        window.hookwayContractInfo = () => JSON.stringify({ contract_version: 1, capabilities: ["core_yaml_validate"] });
        window.hookwayWasmReady?.();
      });
      return new Promise<void>((resolve) => { finish = resolve; });
    }
  };
  installInstantiate();
  vi.stubGlobal("fetch", vi.fn(async () => ({ ok: true, status: 200, clone: () => ({ arrayBuffer: async () => new ArrayBuffer(0) }) })));
  const { loadWasm, getWasmState, hasWasmCapability } = await import("./loader");
  await loadWasm();
  finish();
  await vi.waitFor(() => expect(getWasmState().status).toBe("unavailable"));
  expect(hasWasmCapability("core_yaml_validate")).toBe(false);
});
