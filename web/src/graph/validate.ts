export interface ValidationIssue {
  field: string;
  message: string;
  suggestion?: string;
  severity: string;
}

declare global {
  interface Window {
    hookwayValidate?: (yaml: string) => string;
    hookwayNodeSchemas?: () => string;
    Go?: new () => { importObject: WebAssembly.Imports; run(instance: WebAssembly.Instance): Promise<void> };
  }
}

let loading: Promise<void> | null = null;

export function loadValidator(): Promise<void> {
  if (typeof window === "undefined") return Promise.resolve();
  if (window.hookwayValidate) return Promise.resolve();
  if (!loading) loading = instantiateWasm().catch(() => undefined);
  return loading;
}

async function instantiateWasm(): Promise<void> {
  if (!window.Go) {
    await new Promise<void>((resolve, reject) => {
      const script = document.createElement("script");
      script.src = "/wasm_exec.js";
      script.onload = () => resolve();
      script.onerror = () => reject(new Error("wasm_exec.js failed to load"));
      document.head.appendChild(script);
    });
  }
  if (!window.Go) throw new Error("Go WASM runtime missing");
  const go = new window.Go();
  const response = await fetch("/hookway.wasm");
  if (!response.ok) throw new Error("hookway.wasm not found");
  const result = await WebAssembly.instantiateStreaming(response, go.importObject);
  void go.run(result.instance);
}

export async function validateYaml(yaml: string): Promise<ValidationIssue[] | null> {
  await loadValidator();
  if (typeof window !== "undefined" && window.hookwayValidate) {
    try {
      return parseIssues(window.hookwayValidate(yaml));
    } catch {
      return null;
    }
  }
  try {
    const resp = await fetch("/validate", { method: "POST", headers: { "Content-Type": "application/yaml" }, body: yaml });
    if (!resp.ok) return null;
    return parseIssues(await resp.text());
  } catch {
    return null;
  }
}

function parseIssues(raw: string): ValidationIssue[] {
  const parsed = JSON.parse(raw) as { error?: string; issues?: ValidationIssue[] };
  if (parsed.error) return [{ field: "config", message: parsed.error, severity: "error" }];
  return parsed.issues ?? [];
}
