import { editorAPIFallbackEnabled } from "../shared/wasm/validationMode";
import { createEditorClient } from "../shared/contracts/editorClient";
import type { components } from "../shared/contracts/generated/editor";
import { hasWasmCapability, invokeWasm, loadWasm } from "../shared/wasm/loader";

export type ValidationIssue = components["schemas"]["Issue"];
const editor = createEditorClient();

// Kept as the graph's public entry point while callers migrate to the shared
// bridge. Load errors remain observable by callers and getWasmState().
export async function loadValidator(): Promise<void> {
  await loadWasm();
}

async function fromWasm(
  capability: "core_yaml_validate" | "replay_yaml_validate",
  yaml: string,
): Promise<ValidationIssue[] | null> {
  try {
    await loadWasm();
    if (hasWasmCapability(capability))
      return parseIssues(invokeWasm(capability, yaml));
  } catch {
    // The editor API is the authoritative fallback; the loader retains the
    // concrete startup or contract error in its public state.
  }
  return null;
}

export async function validateYaml(
  yaml: string,
): Promise<ValidationIssue[] | null> {
  const issues = await fromWasm("core_yaml_validate", yaml);
  if (issues) return issues;
  if (!editorAPIFallbackEnabled()) return null;
  try {
    const { data } = await editor.POST("/validate", {
      body: yaml,
      bodySerializer: (body) => body,
      headers: { "Content-Type": "application/yaml" },
    });
    return data ? (data.issues ?? []) : null;
  } catch {
    return null;
  }
}

export async function validateReplayYaml(
  yaml: string,
): Promise<ValidationIssue[] | null> {
  const issues = await fromWasm("replay_yaml_validate", yaml);
  if (issues) return issues;
  if (!editorAPIFallbackEnabled()) return null;
  try {
    const { data } = await editor.POST("/validate-replay", {
      body: yaml,
      bodySerializer: (body) => body,
      headers: { "Content-Type": "application/yaml" },
    });
    return data ? (data.issues ?? []) : null;
  } catch {
    return null;
  }
}

export async function loadReplaySchema(): Promise<Record<
  string,
  unknown
> | null> {
  try {
    await loadWasm();
    if (hasWasmCapability("replay_schema")) {
      return JSON.parse(invokeWasm("replay_schema")) as Record<string, unknown>;
    }
  } catch {
    // A stale bridge can fail parsing; the editor API remains available.
  }
  if (!editorAPIFallbackEnabled()) return null;
  try {
    const { data } = await editor.GET("/replay-schema");
    return data ?? null;
  } catch {
    return null;
  }
}

function parseIssues(raw: string): ValidationIssue[] {
  const parsed = JSON.parse(raw) as {
    error?: string;
    issues?: ValidationIssue[];
  };
  if (parsed.error)
    return [{ field: "config", message: parsed.error, severity: "error" }];
  return parsed.issues ?? [];
}
