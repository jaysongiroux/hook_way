import type { components } from "../contracts/generated/authoring";
export type OpenAPIReport = components["schemas"]["OpenAPIReport"];
export type OpenAPIOperation = components["schemas"]["Operation"];
export type ImportDiagnostic = components["schemas"]["Diagnostic"];
export type ImportResult = components["schemas"]["OpenAPIGenerateResult"];
export const MAX_OPENAPI_BYTES = 8 * 1024 * 1024;
export function checkDocument(text: string) {
  if (!text.trim() || new TextEncoder().encode(text).length > MAX_OPENAPI_BYTES)
    throw new Error("Provide one JSON or YAML document of at most 8 MiB.");
}

/** A private worker/runtime per wizard. Cancel never interrupts the canvas validator. */
export class OpenAPIImporter {
  private worker: Worker | null = null;
  private sequence = 0;
  private pending = new Map<
    number,
    { resolve: (v: unknown) => void; reject: (e: Error) => void }
  >();
  constructor(
    private factory = () =>
      new Worker(new URL("./openapi.worker.ts", import.meta.url), {
        type: "module",
      }),
  ) {}
  private call(
    operation: "openapi_inspect" | "openapi_generate",
    input: unknown,
  ): Promise<unknown> {
    if (!this.worker) {
      this.worker = this.factory();
      this.worker.onmessage = ({ data }) => {
        const pending = this.pending.get(data.id);
        if (!pending) return;
        this.pending.delete(data.id);
        if (data.error) pending.reject(new Error(data.error));
        else pending.resolve(data.result);
      };
      this.worker.onerror = () =>
        this.cancel(
          new Error(
            "Local importer failed to load or stopped. Retry, or use hookway import-openapi.",
          ),
        );
    }
    const id = ++this.sequence;
    return new Promise((resolve, reject) => {
      this.pending.set(id, { resolve, reject });
      const base = new URL(
        import.meta.env.BASE_URL || "/",
        window.location.href,
      );
      this.worker!.postMessage({
        id,
        operation,
        input,
        runtimeURL: new URL("wasm_exec.js", base).href,
        wasmURL: new URL("hookway.wasm", base).href,
      });
    });
  }
  async inspect(document: string): Promise<OpenAPIReport> {
    checkDocument(document);
    return (await this.call("openapi_inspect", { document })) as OpenAPIReport;
  }
  async generate(
    document: string,
    settings_yaml: string,
  ): Promise<ImportResult> {
    checkDocument(document);
    checkDocument(settings_yaml);
    return (await this.call("openapi_generate", {
      document,
      settings_yaml,
    })) as ImportResult;
  }
  cancel(error: Error = new DOMException("Import canceled", "AbortError")) {
    this.worker?.terminate();
    this.worker = null;
    for (const p of this.pending.values()) p.reject(error);
    this.pending.clear();
  }
}
