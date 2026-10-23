// This runtime lives exclusively in a worker, independent from the canvas WASM instance.
interface ImportWorkerScope {
  Go: new () => {
    importObject: WebAssembly.Imports;
    run(instance: WebAssembly.Instance): Promise<void>;
  };
  hookwayWasmReady?: () => void;
  hookwayContractInfo?: () => string;
  hookwayOpenAPIInspect?: (input: string) => string;
  hookwayOpenAPIGenerate?: (input: string) => string;
  postMessage: (value: unknown) => void;
  onmessage: ((event: MessageEvent) => void) | null;
}
const scope = globalThis as unknown as ImportWorkerScope;
let ready: Promise<void> | undefined;
function boot(runtimeURL: string, wasmURL: string) {
  ready ??= (async () => {
    await import(/* @vite-ignore */ runtimeURL);
    const go = new scope.Go();
    const response = await fetch(wasmURL);
    if (!response.ok)
      throw new Error("Matching importer WASM assets are unavailable.");
    const bytes = await response.arrayBuffer();
    const { instance } = await WebAssembly.instantiate(bytes, go.importObject);
    await new Promise<void>((resolve, reject) => {
      scope.hookwayWasmReady = resolve;
      void go
        .run(instance)
        .then(() => reject(new Error("Importer stopped")), reject);
    });
    const info = JSON.parse(scope.hookwayContractInfo?.() || "{}");
    if (
      info.contract_version !== 1 ||
      !["openapi_inspect", "openapi_generate"].every((c) =>
        info.capabilities?.includes(c),
      ) ||
      !scope.hookwayOpenAPIInspect ||
      !scope.hookwayOpenAPIGenerate
    )
      throw new Error(
        "Matching OpenAPI importer capabilities are missing. Reload the matching builder assets.",
      );
  })();
  return ready;
}
scope.onmessage = async ({ data }) => {
  try {
    await boot(data.runtimeURL, data.wasmURL);
    const fn =
      data.operation === "openapi_inspect"
        ? scope.hookwayOpenAPIInspect
        : data.operation === "openapi_generate"
          ? scope.hookwayOpenAPIGenerate
          : undefined;
    if (!fn) throw new Error("Unsupported import operation");
    const result = JSON.parse(fn(JSON.stringify(data.input)));
    if (result.error) throw new Error(result.error);
    scope.postMessage({ id: data.id, result });
  } catch (error) {
    scope.postMessage({
      id: data.id,
      error: error instanceof Error ? error.message : "Local import failed",
    });
  }
};
export {};
