const WASM_CONTRACT_VERSION = 1;

export type WasmCapability =
  | "core_yaml_validate"
  | "core_yaml_bundle_validate"
  | "core_schema"
  | "node_schemas"
  | "replay_yaml_validate"
  | "replay_schema"
  | "replay_action_input"
  | "replay_resolution_input"
  | "json_sample_validate"
 | "openapi_inspect"
 | "openapi_generate";

interface WasmContractInfo {
  contract_version: number;
  capabilities: WasmCapability[];
}

export interface WasmBridge {
  info: WasmContractInfo;
  validateCore(yaml: string): string;
  coreSchema(): string;
  nodeSchemas(): string;
  validateReplay(yaml: string): string;
  replaySchema(): string;
  validateReplayAction(json: string): string;
  jsonSampleValidate(json: string): string;
}

export type WasmState =
  | { status: "idle" | "loading" | "ready"; error?: undefined }
  | { status: "unavailable" | "mismatch"; error: Error };

declare global {
  interface Window {
    Go?: new () => { importObject: WebAssembly.Imports; run(instance: WebAssembly.Instance): Promise<void> };
    hookwayWasmReady?: () => void;
    hookwayContractInfo?: () => string;
    hookwayValidateBundle?: (json: string) => string;
    hookwayValidate?: (yaml: string) => string;
    hookwaySchema?: () => string;
    hookwayNodeSchemas?: () => string;
    hookwayReplayValidate?: (yaml: string) => string;
    hookwayReplaySchema?: () => string;
    hookwayReplayActionValidate?: (json: string) => string;
    hookwayOpenAPIInspect?: (json: string) => string;
    hookwayOpenAPIGenerate?: (json: string) => string;
    hookwayJSONSampleValidate?: (json: string) => string;
  }
}

const required: Record<WasmCapability, keyof Window> = {
  core_yaml_bundle_validate: "hookwayValidateBundle",
  core_yaml_validate: "hookwayValidate",
  core_schema: "hookwaySchema",
  node_schemas: "hookwayNodeSchemas",
  replay_yaml_validate: "hookwayReplayValidate",
  replay_schema: "hookwayReplaySchema",
  replay_action_input: "hookwayReplayActionValidate",
  replay_resolution_input: "hookwayReplayActionValidate",
  json_sample_validate: "hookwayJSONSampleValidate",
  openapi_inspect: "hookwayOpenAPIInspect",
  openapi_generate: "hookwayOpenAPIGenerate",
};

let state: WasmState = { status: "idle" };
let pending: Promise<WasmBridge> | null = null;
let bridge: WasmBridge | null = null;
let selectedBase: string | null = null;
let runGeneration = 0;

export function getWasmState(): WasmState { return state; }

export function hasWasmCapability(capability: WasmCapability): boolean {
  return state.status === "ready" && bridge?.info.capabilities.includes(capability) === true;
}

function inspectBridge(): WasmBridge {
  if (typeof window.hookwayContractInfo !== "function") {
    throw new Error("WASM contract metadata is missing");
  }
  const info = JSON.parse(window.hookwayContractInfo()) as WasmContractInfo;
  if (info.contract_version !== WASM_CONTRACT_VERSION || !Array.isArray(info.capabilities)) {
    throw new Error(`WASM contract mismatch: expected version ${WASM_CONTRACT_VERSION}`);
  }
  for (const [capability, globalName] of Object.entries(required) as [WasmCapability, keyof Window][]) {
    if (info.capabilities.includes(capability) && typeof window[globalName] !== "function") {
      throw new Error(`WASM contract mismatch: ${capability} has no implementation`);
    }
  }
  return {
    info,
    validateCore: (yaml) => call("core_yaml_validate", yaml),
    coreSchema: () => call("core_schema"),
    nodeSchemas: () => call("node_schemas"),
    validateReplay: (yaml) => call("replay_yaml_validate", yaml),
    replaySchema: () => call("replay_schema"),
    validateReplayAction: (json) => call("replay_action_input", json),
    jsonSampleValidate: (json) => call("json_sample_validate", json),
  };
}

function call(capability: WasmCapability, arg?: string): string {
  if (!hasWasmCapability(capability)) throw new Error(`WASM capability unavailable: ${capability}`);
  const fn = window[required[capability]] as ((value?: string) => string) | undefined;
  if (!fn) throw new Error(`WASM bridge missing: ${capability}`);
  const result = fn(arg);
  if (typeof result !== "string") throw new Error(`WASM bridge returned non-string: ${capability}`);
  return result;
}

export function invokeWasm(capability: WasmCapability, arg?: string): string { return call(capability, arg); }

function assetUrl(base: string, file: string): string {
  return new URL(file, new URL(base.endsWith("/") ? base : `${base}/`, window.location.href)).toString();
}

async function loadGoRuntime(base: string): Promise<void> {
  if (window.Go) return;
  await new Promise<void>((resolve, reject) => {
    const script = document.createElement("script");
    const timer = window.setTimeout(() => fail(new Error("wasm_exec.js timed out")), 15000);
    function cleanup() { window.clearTimeout(timer); }
    function fail(error: Error) { cleanup(); script.remove(); reject(error); }
    script.src = assetUrl(base, "wasm_exec.js");
    script.onload = () => { cleanup(); resolve(); };
    script.onerror = () => fail(new Error("wasm_exec.js failed to load"));
    document.head.appendChild(script);
  });
  if (!window.Go) throw new Error("Go WASM runtime missing");
}

async function instantiate(base: string): Promise<WasmBridge> {
  const generation = ++runGeneration;
  await loadGoRuntime(base);
  const go = new window.Go!();
  const response = await fetch(assetUrl(base, "hookway.wasm"));
  if (!response.ok) throw new Error(`hookway.wasm failed to load (${response.status})`);
  const fallback = response.clone();
  let instance: WebAssembly.Instance;
  try {
    ({ instance } = await WebAssembly.instantiateStreaming(response, go.importObject));
  } catch {
    ({ instance } = await WebAssembly.instantiate(await fallback.arrayBuffer(), go.importObject));
  }
  return new Promise<WasmBridge>((resolve, reject) => {
    let finished = false;
    const timer = window.setTimeout(() => fail(new Error("WASM bridge did not become ready")), 15000);
    const previousReady = window.hookwayWasmReady;
    function cleanup() {
      window.clearTimeout(timer);
      if (window.hookwayWasmReady === ready) window.hookwayWasmReady = previousReady;
    }
    function fail(error: Error) {
      if (finished) return;
      finished = true;
      cleanup();
      reject(error);
    }
    function ready() {
      if (finished) return;
      try {
        const found = inspectBridge();
        finished = true;
        cleanup();
        resolve(found);
      } catch (error) {
        fail(error instanceof Error ? error : new Error(String(error)));
      }
    }
    window.hookwayWasmReady = ready;
    function exited(error: Error) {
      if (!finished) { fail(error); return; }
      if (generation === runGeneration) {
        bridge = null;
        state = { status: "unavailable", error };
      }
    }
    try {
      void go.run(instance).then(
        () => exited(new Error("WASM runtime exited")),
        (error: unknown) => exited(error instanceof Error ? error : new Error(String(error))),
      );
    } catch (error) {
      fail(error instanceof Error ? error : new Error(String(error)));
    }
  });
}

export function loadWasm(options: { assetBase?: string } = {}): Promise<WasmBridge> {
  if (typeof window === "undefined") return Promise.reject(new Error("WASM requires a browser"));
  const base = options.assetBase ?? import.meta.env.BASE_URL ?? "/";
  if (bridge && state.status === "ready") return Promise.resolve(bridge);
  if (pending) return pending;
  selectedBase = base;
  state = { status: "loading" };
  pending = (async () => {
    // A test host or previously booted bridge may already be present. Metadata
    // is required; legacy validator globals alone never imply compatibility.
    if (window.hookwayContractInfo && !window.Go) return inspectBridge();
    return instantiate(base);
  })().then((found) => {
    bridge = found;
    state = { status: "ready" };
    return found;
  }).catch((error: unknown) => {
    const err = error instanceof Error ? error : new Error(String(error));
    state = { status: /contract|capability|metadata/i.test(err.message)
      ? "mismatch" : "unavailable", error: err };
    throw err;
  }).finally(() => { pending = null; });
  return pending;
}

export function retryWasm(options: { assetBase?: string } = {}): Promise<WasmBridge> {
  if (pending) return pending;
  bridge = null;
  state = { status: "idle" };
  return loadWasm({ assetBase: options.assetBase ?? selectedBase ?? undefined });
}
